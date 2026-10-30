# 7.1 · Front Panel

> You built the parser, the writer and the router. Now the standard library's versions unlock, and you see what they were doing for you.

## Your goal

Rebuild the chapter 6 key-value API as package `api` on `net/http`, Go's standard HTTP server. The handlers keep their logic; the parser, the response writer and the router are replaced by `http.ServeMux`, `http.ResponseWriter` and `*http.Request`. Two things are new in the API itself. `If-Match: "<version>"` on a `PUT` or `DELETE` turns it into a compare-and-swap, answered with `412 Precondition Failed` when the version does not match. `?ttl=10s` sets an expiry, which shows up on `GET` as an `Expires` header. `burrowd serve` gains `-http`, so one daemon serves the same store over frames and over HTTP. And you write your own handler tests, graded the same way as in 0.2 and 4.3: four planted bugs that at least one of your tests must catch.

## What you're using from before

**Router (6.3 · Router).** This is the comparison. Everything `KVRoutes` did, `net/http` does with a different type in each slot. The patterns are the same `METHOD /path/{name}` syntax, `r.PathValue` is the same call, `404`, `405` with `Allow` and percent-decoding come free:

```go
// 6.3
r := httpraw.NewRouter()
r.HandleFunc("GET /v1/kv/{key}", get)
func get(w *httpraw.ResponseWriter, r *httpraw.Request)
srv := &httpraw.Server{Handler: r}

// 7.1
mux := http.NewServeMux()
mux.HandleFunc("GET /v1/kv/{key}", s.get)
func (s *Server) get(w http.ResponseWriter, r *http.Request)
srv := &http.Server{Handler: mux}
```

`r.PathValue("key")`, `w.Header().Set`, `WriteHeader` and `Write` are the same calls in both.

Run `git diff --stat` when you are done and see how much of chapter 6 is gone.

**Durable daemon (5.1 · Non-volatile Daemon).** `burrowd serve` already picks a `store.Store` from `-data`. The same variable now feeds `api.Server{Store: st}`, so the frame port and the HTTP port see one store:

```go
apiSrv := &api.Server{Store: st}
httpLn, err := net.Listen("tcp", *httpAddr)
// print "http on <addr>" after "listening on <addr>"
httpSrv := &http.Server{Handler: apiSrv.Handler()}
go httpSrv.Serve(httpLn)
```

**CAS (1.3 · Versions).** `If-Match: "3"` means "only if the stored version is 3", which is exactly `CompareAndSwap(ctx, key, 3, value)`; `"0"` means "only if the key does not exist". `store.ErrVersionMismatch` becomes `412`. **Expiry (1.4 · Decay).** `?ttl=` maps to `SetWithTTL` or `CompareAndSwapWithTTL`, `POST .../touch?ttl=` to `Touch`, and a non-zero `Entry.ExpiresAt` becomes the `Expires` header.

```go
switch {
case !cond && ttl == 0:
    e, err = st.Set(ctx, key, value)
case !cond:
    e, err = st.SetWithTTL(ctx, key, value, ttl)
case ttl == 0:
    e, err = st.CompareAndSwap(ctx, key, v, value)
default:
    e, err = st.CompareAndSwapWithTTL(ctx, key, v, value, ttl)
}
```

## What's new here

**`http.Handler` and `http.HandlerFunc`.** The same shape as 6.2, now from the standard library: `http.Handler` is the interface with `ServeHTTP(w http.ResponseWriter, r *http.Request)`, and `http.HandlerFunc` adapts a function to it. `http.ResponseWriter` is an interface (not a struct as in 6.2) with the same three methods. `*http.Request` carries `Method`, `URL` (a parsed `*url.URL`, so the query string is `r.URL.Query().Get("ttl")`), `Header` and `Body`.

**`http.ServeMux` with method patterns.** Since Go 1.22, `mux.HandleFunc("PUT /v1/kv/{key}", f)` registers a method and a path with wildcards, and `r.PathValue("key")` reads the wildcard. The mux answers `404` for an unknown path and `405` with an `Allow` header when the path is known but the method is not. It does not validate keys, so run `store.ValidateKey` (or simply let the store do it and map `ErrInvalidKey` to `400`).

**Limiting a body with `http.MaxBytesReader`.** `http.MaxBytesReader(w, r.Body, 1<<20)` wraps the body so that reading past 1 MiB fails. The error it returns is a `*http.MaxBytesError`, which you detect with `errors.As` to answer `413`; it also tells the server to close the connection so the client stops sending:

```go
body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
var tooBig *http.MaxBytesError
if errors.As(err, &tooBig) {
    http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
    return
}
```

**Conditional requests.** HTTP has a standard way to say "only if the resource is still the version I saw": the server sends `ETag: "<tag>"` with every representation, and the client sends it back as `If-Match: "<tag>"` on a write. If the tags differ the server answers `412` and changes nothing. Because Burrow's tag is the version number, `If-Match` is CAS over HTTP. One rule to get right: a conditional request whose precondition fails is `412` *even when the key is missing* (the precondition "version 5" cannot hold for a key that does not exist). `404` is only for unconditional operations.

**Testing handlers: `httptest.NewRecorder` and `httptest.NewServer`.** A recorder is a fake `http.ResponseWriter` that stores the status, headers and body, so you can call a handler directly with no network:

```go
req := httptest.NewRequest("PUT", "/v1/kv/k", strings.NewReader("v"))
req.Header.Set("If-Match", `"7"`)
rec := httptest.NewRecorder()
srv.Handler().ServeHTTP(rec, req)
// rec.Code == 412, rec.Header().Get("ETag"), rec.Body.String()
```

`httptest.NewServer(h)` starts a real server on a random port and gives you `srv.URL` to hit with a real client, which is what the provided tests do. Use the recorder for your own tests: one table of method, path, headers, body, expected status and expected headers covers all four mutants.

## What to do

1. Create `api/api.go` with `Server{Store, Logger}` and `Handler()`, which builds an `http.ServeMux` and registers the five routes from the Reference.
2. `GET /v1/kv/{key}`: `Get`; `404` when missing. Set `Content-Type: application/octet-stream`, `ETag: "<version>"`, and `Expires` in `http.TimeFormat` when `ExpiresAt` is non-zero. Write the value.
3. `PUT /v1/kv/{key}`:
   - Read the body through `http.MaxBytesReader` with a 1 MiB limit; `413` when it is over. Exactly 1 MiB is fine.
   - Parse `If-Match` once into `(present bool, version uint64)`. It must be a quoted decimal such as `"3"`; `*`, `W/"2"`, a bare number and `"two"` are `400`.
   - Parse `?ttl=` once into `(present bool, d time.Duration)` with `time.ParseDuration`; it must be positive, so `0`, `-1s`, `soon` and `10` (no unit) are `400`.
   - Pick one of `Set`, `SetWithTTL`, `CompareAndSwap`, `CompareAndSwapWithTTL` from the two booleans. Answer `204` with the new `ETag` (and `Expires` if there is one).
4. `DELETE /v1/kv/{key}`: `Delete`, or `CompareAndDelete` when `If-Match` is present. `204` on success.
5. `POST /v1/kv/{key}/touch?ttl=`: `?ttl=` is required (`400` without it); `Touch`; `204` with the unchanged `ETag`; `404` when missing.
6. `GET /v1/keys?prefix=`: `text/plain`, one key per line with a trailing newline, as in 6.3.
7. Write one error helper used by every handler: `ErrInvalidKey` and `ErrInvalidTTL` are `400`, `ErrVersionMismatch` is `412`, `ErrNotFound` is `412` when the request was conditional and `404` when it was not. Plain-text errors are fine for now.
8. In `cmd/burrowd`, add `-http` (default `127.0.0.1:8080`) to `serve`. Listen, print `http on <addr>` as the second stdout line, and serve `apiSrv.Handler()` with an `http.Server` in a goroutine. On `SIGINT`/`SIGTERM` stop both: `httpSrv.Close()` next to the frame server's `Shutdown`. Both ports must refuse connections after exit.
9. Write your own tests in `api/` (a file such as `api/api_test.go`, test names not starting with `TestL`). Cover at least: a stale `If-Match` is `412`, `GET` carries `ETag`, `PUT ?ttl=` produces `Expires`, and an unconditional `DELETE` of a missing key is `404`.

## Example

```bash
$ curl -i -X PUT --data hello 'localhost:8080/v1/kv/greeting?ttl=30s'
HTTP/1.1 204 No Content
Etag: "1"
Expires: Sat, 31 Oct 2026 09:30:41 GMT

$ curl -i -X PUT --data hello2 -H 'If-Match: "1"' \
    localhost:8080/v1/kv/greeting
HTTP/1.1 204 No Content
Etag: "2"

$ curl -i -X PUT --data hello3 -H 'If-Match: "1"' \
    localhost:8080/v1/kv/greeting
HTTP/1.1 412 Precondition Failed
Content-Type: text/plain; charset=utf-8

version mismatch
$ curl -i -X DELETE localhost:8080/v1/kv/nothing
HTTP/1.1 404 Not Found

$ curl -i -X DELETE -H 'If-Match: "9"' localhost:8080/v1/kv/nothing
HTTP/1.1 412 Precondition Failed
```

And with both ports up, a value written over frames is readable over HTTP:

```bash
$ ./burrowd serve -data ./data &
listening on 127.0.0.1:7070
http on 127.0.0.1:8080
$ go run ./cmd/burrowctl set colour green
OK 1
$ curl -i localhost:8080/v1/kv/colour
HTTP/1.1 200 OK
Content-Type: application/octet-stream
Etag: "1"

green
```

## Done when

- [ ] The 6.3 round trip works unchanged against `api.Server` through `httptest.NewServer`: `404`, `PUT` `204` with `ETag "1"`, `GET` as octet-stream with the `ETag` and no `Expires`, empty values, `DELETE` `204` then `404`, an invalid key `400` mentioning `invalid key`, `/v1/keys` with and without `?prefix=`
- [ ] `If-Match` with the right version succeeds and bumps the `ETag`; a stale one is `412` and the value is unchanged; `"0"` creates when missing and is `412` when present; `"5"` on a missing key is `412`; `*`, `W/"2"`, `2`, `"two"` and `""` are `400`
- [ ] `DELETE` with a stale `If-Match` is `412`, with the right one `204`, on a missing key `412`; an unconditional `DELETE` of a missing key is `404`
- [ ] `PUT ?ttl=50ms` gives a `GET` with `Expires` and a `404` after 100 ms; `?ttl=` of `0`, `-1s`, `soon` and `10` is `400`; `If-Match "0"` plus `?ttl=1h` creates with an expiry in one call
- [ ] `POST .../touch?ttl=` is `204` with the same `ETag` and extends the expiry; a missing key is `404`; no `?ttl=` is `400`
- [ ] `PATCH` is `405` with `Allow` listing `GET`, `PUT` and `DELETE`; a 2 MiB `PUT` is `413`; exactly 1 MiB is accepted
- [ ] The real binary prints `listening on` then `http on`, serves one store over both ports, and exits 0 on `SIGTERM` with both ports closed
- [ ] Your own `api/` tests pass on your code and at least one fails against each of the four mutants

## Reference

The exact contract the tests hold you to.

```go
// api/api.go
package api

type Server struct {
	Store  store.Store
	Logger *slog.Logger
}

// Handler returns the routes on an *http.ServeMux:
//   GET    /v1/kv/{key}
//     200 application/octet-stream, ETag: "<version>", Expires when a
//     TTL is set | 404
//   PUT    /v1/kv/{key}[?ttl=10s]
//     body is the value; If-Match: "<version>" makes it a CAS
//     (If-Match: "0" = create only)
//     204 + ETag | 412 Precondition Failed when the precondition fails,
//     including a missing key | 400 invalid key or ttl | 413 over 1 MiB
//   DELETE /v1/kv/{key}
//     If-Match optional; 204 | 404 (unconditional, missing)
//     | 412 (conditional, mismatch or missing)
//   POST   /v1/kv/{key}/touch?ttl=
//     204 + ETag | 404 | 400 (ttl missing or invalid)
//   GET    /v1/keys?prefix=
//     text/plain, one key per line
// Wrong method: 405 with Allow (ServeMux does this). Errors are
// text/plain.
// Precondition rule: any conditional write whose If-Match fails is 412,
// even when the key is missing. store.ErrNotFound maps to 404 only for
// unconditional operations.
func (s *Server) Handler() http.Handler

// cmd/burrowd
//   burrowd serve ... [-http 127.0.0.1:8080]
//     serves the HTTP API from the same store as the frame port
// Prints `http on <addr>` as its second stdout line.
```

Decisions the tests rely on:

- Any conditional write whose precondition fails is 412, including when the key is missing: `PUT` with `If-Match: "5"` on a missing key and `DELETE` with `If-Match` on a missing key are both 412. `store.ErrNotFound` maps to 404 only for unconditional operations. This is HTTP's `If-Match` semantics.
- `POST .../touch` requires `?ttl=`; a missing ttl is 400.
- `If-Match` must be a quoted decimal such as `"3"`. `*`, weak tags and anything else are 400.
- `Expires` is `ExpiresAt` in `http.TimeFormat`. `?ttl=` is parsed with `time.ParseDuration` and must be positive.
- The HTTP suites use keys without `/`; sending a key with `/` through `net/http.ServeMux` is not part of the contract.
- Startup lines in order: `listening on <addr>`, then `http on <addr>`.
- Your tests (names not starting with `TestL` plus a digit or capital) pass on your code and at least one fails against each of four mutants: (1) a failed `If-Match` answers 404 instead of 412, (2) `GET` drops the `ETag` header, (3) `?ttl=` on `PUT` is ignored, (4) an unconditional `DELETE` of a missing key answers 204. One table over `httptest.NewRecorder` with method, path, headers, body and the expected status and headers covers all four; checking `Expires` after `PUT ?ttl=` catches the third.

## The tests check

- KV round trip against api.Server through httptest.NewServer: 404 for a missing key; PUT is 204 with ETag `"1"`; GET returns application/octet-stream with the ETag and no Expires; an empty body stores an empty value; DELETE is 204 then 404; an invalid key is 400 mentioning `invalid key`
- GET /v1/keys with and without ?prefix=, one key per line, sorted
- If-Match with the right version succeeds and bumps the ETag; a stale version is 412 and the value is unchanged; If-Match `"0"` is 412 when the key exists and creates (204) when it doesn't; If-Match `"5"` on a missing key is 412; `*`, `W/"2"`, `2`, `"two"` and `""` are 400
- DELETE with a stale If-Match is 412, with the right one 204, on a missing key 412; an unconditional DELETE of a missing key is 404
- PUT ?ttl=50ms then GET carries Expires in http.TimeFormat and, after 100 ms, the key is gone; ?ttl= of `0`, `-1s`, `soon` and `10` is 400; If-Match `"0"` with ?ttl=1h creates with an expiry in one call (CompareAndSwapWithTTL)
- POST touch?ttl= is 204 with the unchanged ETag and extends the expiry; touch of a missing key is 404; touch without ?ttl= is 400
- PATCH is 405 with Allow listing GET, PUT and DELETE; a 2 MiB PUT is 413; exactly 1 MiB is allowed
- The real binary with -addr and -http: `listening on` then `http on`; a value set over frames with client.Client is readable over HTTP with its ETag and vice versa; SIGTERM exits 0 and both ports stop accepting connections
- TestL7_1_YourTestsCatchBugs: your api tests pass on your code and fail against each of the four mutants above (412 becomes 404, ETag dropped on GET, ?ttl= ignored on PUT, unconditional DELETE of a missing key is 204)

## Try it

```bash
./burrow watch
curl -i -X PUT --data hello 'localhost:8080/v1/kv/greeting?ttl=30s'
curl -i -X PUT --data hello2 -H 'If-Match: "1"' \
  localhost:8080/v1/kv/greeting
git diff --stat   # how much of chapter 6 did net/http replace?
```

## In the wild

net/http is the production server; ETag/If-Match is how S3, Azure Blob and etcd's gateway expose CAS over HTTP.

## New in Go

http.Handler and HandlerFunc · ServeMux method and path patterns (Go 1.22+) · r.PathValue · http.MaxBytesReader · errors.As with *http.MaxBytesError · conditional requests · httptest.NewRecorder vs httptest.NewServer

Standard library: `net/http`, `net/http/httptest`, `strconv`, `io`, `time`
