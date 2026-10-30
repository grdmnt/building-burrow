# 7.6 · Gauges

See inside the running server on a separate port: liveness and readiness, counters, profiles, a log level you can change at runtime, and a status page shipped inside the binary with `embed`.

## Build

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
//   GET /debug/vars (expvar: requests, errors, keys, open_connections, wal_records, uptime_seconds)
//   GET /debug/pprof/... (net/http/pprof on this mux only, never on the API port)
//   PUT /log/level  body: debug|info|warn|error
//   GET /           an embedded status.html rendered with html/template from the expvars
// burrowd serve gains -admin 127.0.0.1:7071. Counters are incremented from api middleware.
```

- `admin.Server` on its own mux and port
- expvar counters wired from the API middleware
- `/readyz` tied to replay
- `//go:embed status.html` + html/template

Why pprof is a trap and what `init()` has to do with it: `net/http/pprof` registers its handlers on `http.DefaultServeMux` from its `init()` function, which runs when the package is imported, so the usual `import _ "net/http/pprof"` (a blank import: run the package's `init`, use nothing from it) exposes profiling on every server that uses the default mux, API port included. `expvar` does the same for `/debug/vars`, even when imported normally. Two rules follow: never serve `DefaultServeMux` (an `http.ListenAndServe(addr, nil)` anywhere in the binary would expose both), and register `pprof.Index`, `pprof.Profile`, `pprof.Cmdline`, `pprof.Symbol`, `pprof.Trace` and `expvar.Handler()` on the admin mux yourself instead of relying on the blank import. The rule generalises: a package-level `var x = f()` and every `init()` run before `main`, in dependency order, and a blank import exists only to trigger them (`image/png` registering a decoder, a `database/sql` driver registering itself).

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.6/files/`, matching the earlier chapters.

## Pass when

curl :7071/debug/vars shows counters climbing under load, readyz is 503 until a 50,000-record replay finishes and 200 after, pprof profiles open, and the log level changes at runtime.

The suite will cover:

- /healthz 200 always; /readyz 503 while Ready returns false, 200 after
- expvar JSON parses and `requests` equals the number of API calls made through the middleware
- /debug/pprof/ is served on the admin handler and absent from the API handler (404)
- PUT /log/level debug makes a Debug log line appear; junk is 400
- GET / returns HTML containing the uptime and request count (embedded template)
- The real binary: readyz is 503 immediately after start with a big log and 200 once `listening on` prints

## Try it

```bash
curl :7071/debug/vars | jq .requests
go tool pprof http://localhost:7071/debug/pprof/profile?seconds=10
```

## Uses

- Middleware chain (built in 7.3 · Signal Chain)
- Durable daemon (built in 5.1 · Non-volatile Daemon)

## In the wild

Prometheus client_golang instead of expvar; the admin-port pattern is Envoy's and Consul's; embed (Go 1.16) is how real binaries ship their UI

## New in Go

expvar · net/http/pprof · init() and blank imports · slog.LevelVar · embed · html/template · liveness vs readiness

Standard library: `expvar`, `net/http/pprof`, `log/slog`, `embed`, `html/template`
