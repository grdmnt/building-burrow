# 7.6 · Gauges

> A server you cannot see inside is a server you cannot run. Put the dials on their own port.

## Your goal

Give `burrowd` an admin port (`-admin 127.0.0.1:7071`, separate from the API port) that answers the questions an operator asks. Is the process alive (`/healthz`)? Is it ready to serve, meaning has the log finished replaying (`/readyz`)? How many requests, errors, keys and log records are there (`/debug/vars`)? Where is the CPU going (`/debug/pprof/`)? Can I turn on debug logging without a restart (`PUT /log/level`)? And a status page at `/`, an HTML file compiled into the binary so there is nothing to deploy next to it.

## What you're using from before

**Middleware chain (7.3 · Signal Chain).** The counters are incremented from the API's middleware, which already sees every request and its status. Either extend `AccessLog` or add a small `Metrics` middleware next to it:

```go
var (
    requests  = expvar.NewInt("requests")
    errorsVar = expvar.NewInt("errors") // "errors" is a package name
)

// inside the middleware, after next.ServeHTTP:
requests.Add(1)
if rec.status >= 500 {
    errorsVar.Add(1)
}
```

**Durable daemon (5.1 · Non-volatile Daemon).** `durable.Open` returns only once replay is done, so "ready" is a flag the daemon sets right after `Open` returns. For `/readyz` to be `503` *during* replay, the admin server has to be listening before `Open` is called. `keys` comes from the store's `Len()` and `wal_records` from `LogLen()`.

```go
var ready atomic.Bool // sync/atomic: safe to read from any goroutine
adm := &admin.Server{Ready: ready.Load, Level: &level, Logger: logger}
go http.Serve(adminLn, adm.Handler())  // up first
ds, err := durable.Open(*dataDir)      // replay (may take a while)
ready.Store(true)
// then listen on the frame and HTTP ports and print `listening on`
```

## What's new here

**`expvar`: counters that publish themselves.** `expvar.NewInt("requests")` registers a named integer in a process-wide table and returns a pointer whose `Add` is safe from any goroutine. `expvar.Handler()` serves the whole table as one JSON object. For a value computed on demand, like the number of keys, `expvar.Publish("keys", expvar.Func(func() any { return st.Len() }))` calls your function each time the page is read.

```bash
$ curl -s localhost:7071/debug/vars
{"cmdline":["./burrowd","serve"],"errors":0,"keys":42,
 "memstats":{...},"open_connections":3,"requests":1187,
 "uptime_seconds":912,"wal_records":17}
```

**`net/http/pprof` and the `init()` trap.** Importing `net/http/pprof` registers the profiling handlers on `http.DefaultServeMux` from its `init()` function, which runs when the package is imported. The usual `import _ "net/http/pprof"` is a *blank import*: run the package's `init`, use nothing from it. The result is that every server in the binary that uses the default mux exposes profiling, the API port included. `expvar` does the same for `/debug/vars` even when imported normally. Two rules follow: never serve `DefaultServeMux` (an `http.ListenAndServe(addr, nil)` anywhere in the binary would expose both), and register the handlers on the admin mux yourself:

```go
mux.HandleFunc("GET /debug/pprof/", pprof.Index)
mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
mux.Handle("GET /debug/vars", expvar.Handler())
```

The rule generalises: a package-level `var x = f()` and every `init()` run before `main`, in dependency order, and a blank import exists only to trigger them (`image/png` registering a decoder, a `database/sql` driver registering itself).

**`slog.LevelVar`: a log level you can change later.** A handler's minimum level is normally fixed when you build it. `slog.LevelVar` is a level that can be read and set atomically, and a handler built with it in its options reads it on every log call:

```go
var level slog.LevelVar // starts at Info
opts := &slog.HandlerOptions{Level: &level}
logger := slog.New(slog.NewJSONHandler(os.Stderr, opts))

// from PUT /log/level; takes effect on the next log call
level.Set(slog.LevelDebug)
```

`slog.Level` has an `UnmarshalText` method, so `level.UnmarshalText([]byte("debug"))` parses the body for you and fails on junk.

**`embed`: files inside the binary.** A `//go:embed` directive above a variable tells the compiler to include a file's contents at build time. The file must sit next to the Go source, and the import of `embed` is needed even if only `string` or `[]byte` variables are used (another blank import):

```go
import _ "embed"

//go:embed status.html
var statusHTML string
```

**`html/template`.** Templates put values into text with `{{.Field}}` placeholders. `html/template` has the same API as `text/template` but escapes every value for the context it lands in, so a key named `<script>` renders as text, not as a script. Parse once at startup (`template.Must` panics on a bad template, which is the right behaviour for a file compiled into the binary) and execute per request:

```go
var statusTmpl = template.Must(template.New("status").Parse(statusHTML))

statusTmpl.Execute(w, map[string]any{
    "Uptime":   time.Since(start).Round(time.Second),
    "Requests": requests.Value(),
})
```

**Liveness versus readiness.** `/healthz` answers "is the process running and able to answer HTTP" and should be cheap and almost always `200`; an orchestrator restarts the process when it fails. `/readyz` answers "should traffic be sent here now" and is `503` while the store is still replaying; an orchestrator just routes around it. Mixing them up gets a server restarted for being busy.

## What to do

1. Create `admin/admin.go` with `Server{Ready, Level, Logger}` and `Handler()` building its own `http.ServeMux`.
2. `GET /healthz`: `200` with body `ok`. `GET /readyz`: `200` when `Ready()` is true, `503` otherwise.
3. Register `expvar.Handler()` at `/debug/vars` and publish `requests`, `errors`, `keys`, `open_connections`, `wal_records` and `uptime_seconds`. Counters are `expvar.Int`s bumped from the API middleware; the rest are `expvar.Func`s.
4. Register the five `pprof` handlers under `/debug/pprof/` on this mux only.
5. `PUT /log/level`: parse the body into `s.Level`; `400` for anything that is not `debug`, `info`, `warn` or `error`.
6. Add `admin/status.html`, embed it, parse it with `html/template`, and serve it at `GET /` with the uptime and the counters.
7. In `cmd/burrowd`, add `-admin` (default `127.0.0.1:7071`), build the logger from a `slog.LevelVar`, start the admin listener before opening the store, flip the ready flag after `Open` returns, and shut the admin server down with the others.

## Example

```bash
$ ./burrowd serve -data ./data &            # a big log is replaying
$ curl -i localhost:7071/readyz
HTTP/1.1 503 Service Unavailable
$ curl -i localhost:7071/healthz
HTTP/1.1 200 OK

ok
listening on 127.0.0.1:7070                 # replay finished
$ curl -i localhost:7071/readyz
HTTP/1.1 200 OK
$ curl -s localhost:7071/debug/vars | jq .requests
1187
$ curl -X PUT --data debug localhost:7071/log/level
$ curl -i -X PUT --data loud localhost:7071/log/level
HTTP/1.1 400 Bad Request
$ curl -s localhost:7071/ | head -3
<h1>burrowd</h1>
<p>up 15m12s, 1187 requests, 0 errors</p>
<p>42 keys, 17 log records</p>
$ curl -s localhost:8080/debug/pprof/
{"error":"not found","code":"not_found"}  # never on the API port
```

## Done when

- [ ] `/healthz` is always `200`; `/readyz` is `503` while `Ready()` is false and `200` after
- [ ] `/debug/vars` parses as JSON and `requests` equals the number of API calls made through the middleware
- [ ] `/debug/pprof/` is served on the admin handler and is a `404` on the API handler
- [ ] `PUT /log/level debug` makes a `Debug` log line appear; junk is `400`
- [ ] `GET /` returns HTML containing the uptime and request count, rendered from the embedded template
- [ ] The real binary answers `503` on `/readyz` right after start with a big log and `200` once `listening on` has printed

## Reference

The exact contract the tests will hold you to.

```go
// admin/admin.go
package admin

type Server struct {
	Ready   func() bool        // false until replay finished
	Level   *slog.LevelVar
	Logger  *slog.Logger
}
func (s *Server) Handler() http.Handler
//   GET /healthz -> 200 ok
//   GET /readyz  -> 200 | 503
//   GET /debug/vars (expvar: requests, errors, keys, open_connections,
//                    wal_records, uptime_seconds)
//   GET /debug/pprof/... (net/http/pprof on this mux only, never on the
//                         API port)
//   PUT /log/level  body: debug|info|warn|error
//   GET /           an embedded status.html rendered with html/template
//                   from the expvars
// burrowd serve gains -admin 127.0.0.1:7071. Counters are incremented
// from api middleware.
```

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.6/files/`, matching the earlier chapters.

Pass when: curl :7071/debug/vars shows counters climbing under load, readyz is 503 until a 50,000-record replay finishes and 200 after, pprof profiles open, and the log level changes at runtime.

The suite will cover:

- /healthz 200 always; /readyz 503 while Ready returns false, 200 after
- expvar JSON parses and `requests` equals the number of API calls made through the middleware
- /debug/pprof/ is served on the admin handler and absent from the API handler (404)
- PUT /log/level debug makes a Debug log line appear; junk is 400
- GET / returns HTML containing the uptime and request count (embedded template)
- The real binary: readyz is 503 immediately after start with a big log and 200 once `listening on` prints

## Try it

```bash
./burrow test
curl -s localhost:7071/debug/vars | jq .requests
go tool pprof 'http://localhost:7071/debug/pprof/profile?seconds=10'
```

## In the wild

Prometheus client_golang instead of expvar; the admin-port pattern is Envoy's and Consul's; embed (Go 1.16) is how real binaries ship their UI.

## New in Go

expvar · net/http/pprof · init() and blank imports · slog.LevelVar · embed · html/template · liveness vs readiness

Standard library: `expvar`, `net/http/pprof`, `log/slog`, `embed`, `html/template`
