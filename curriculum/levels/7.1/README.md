# 7.1 · Front Panel

Rebuild the chapter 6 API on net/http. Turing Complete rule: once you have built a component yourself, the game unlocks the built-in version. Chapter 6's parser, writer and router are consumed here only as the comparison; from this level on, net/http is the part you use. `If-Match` and `?ttl=` map onto the store's CAS and TTL operations.

## Build

```go
// api/api.go
package api

type Server struct {
	Store  store.Store
	Logger *slog.Logger
}

// Handler returns the routes on an *http.ServeMux:
//   GET    /v1/kv/{key}            200 application/octet-stream, ETag: "<version>", Expires when a TTL is set | 404
//   PUT    /v1/kv/{key}[?ttl=10s]  body is the value; If-Match: "<version>" makes it a CAS (If-Match: "0" = create only)
//                                  204 + ETag | 412 Precondition Failed when the precondition fails, including a missing key | 400 invalid key or ttl | 413 over 1 MiB
//   DELETE /v1/kv/{key}            If-Match optional; 204 | 404 (unconditional, missing) | 412 (conditional, mismatch or missing)
//   POST   /v1/kv/{key}/touch?ttl= 204 + ETag | 404 | 400 (ttl missing or invalid)
//   GET    /v1/keys?prefix=        text/plain, one key per line
// Wrong method: 405 with Allow (ServeMux does this). Errors are text/plain for now (JSON in 7.2).
// Precondition rule: any conditional write whose If-Match fails is 412, even when the key is missing.
// store.ErrNotFound maps to 404 only for unconditional operations.
func (s *Server) Handler() http.Handler

// cmd/burrowd
//   burrowd serve ... [-http 127.0.0.1:8080]   serves the HTTP API from the same store as the frame port
// Prints `http on <addr>` as its second stdout line.
```

- `api.Server.Handler()` on `http.ServeMux` with method patterns and `r.PathValue`
- `http.MaxBytesReader` for 413; If-Match mapped to CompareAndSwap, If-Match + ?ttl= to CompareAndSwapWithTTL, ?ttl= alone to SetWithTTL; touch
- `-http` flag in `burrowd serve`; both listeners shut down together

Decisions the tests rely on:

- Any conditional write whose precondition fails is 412, including when the key is missing: `PUT` with `If-Match: "5"` on a missing key and `DELETE` with `If-Match` on a missing key are both 412. `store.ErrNotFound` maps to 404 only for unconditional operations. This is HTTP's `If-Match` semantics.
- `POST .../touch` requires `?ttl=`; a missing ttl is 400.
- `If-Match` must be a quoted decimal such as `"3"`. `*`, weak tags and anything else are 400.
- `Expires` is `ExpiresAt` in `http.TimeFormat`. `?ttl=` is parsed with `time.ParseDuration` and must be positive.
- The HTTP suites use keys without `/`; sending a key with `/` through `net/http.ServeMux` is not part of the contract.
- Startup lines in order: `listening on <addr>`, then `http on <addr>`.

## The tests check

- Every 6.3 KV test passes against api.Server through httptest.NewServer
- ETag equals the version on GET and after PUT; If-Match with the right version succeeds and bumps the ETag; wrong version is 412 and the value is unchanged; If-Match on a missing key is 412, not 404; If-Match: "0" creates only; a malformed If-Match is 400
- PUT ?ttl=50ms then GET after 100 ms is 404; PUT with If-Match: "0" and ?ttl= creates with an expiry in one call (CompareAndSwapWithTTL); Expires header is present and in the future; ttl=0 or junk is 400; POST touch extends; POST touch without ?ttl= is 400
- DELETE with If-Match: 204 on match, 412 on mismatch and 412 on a missing key; unconditional DELETE of a missing key is 404
- 405 carries Allow; a 2 MiB PUT is 413 and the handler saw MaxBytesError
- The real binary with -addr and -http: a value set over frames with client.Client is readable over HTTP and vice versa; SIGTERM exits 0 and both ports stop accepting

## Try it

```bash
curl -i -X PUT --data hello 'localhost:8080/v1/kv/greeting?ttl=30s'
curl -i -X PUT --data hello2 -H 'If-Match: "1"' localhost:8080/v1/kv/greeting
git diff --stat   # how much of chapter 6 did net/http replace?
```

## Uses

- Router (built in 6.3 · Router)
- Durable daemon (built in 5.1 · Non-volatile Daemon)
- CAS (built in 1.3 · Versions)
- Expiry (built in 1.4 · Decay)

## In the wild

net/http is the production server; ETag/If-Match is how S3, Azure Blob and etcd's gateway expose CAS over HTTP

## New in Go

http.Handler and HandlerFunc · ServeMux method and path patterns (Go 1.22+) · r.PathValue · http.MaxBytesReader · errors.As with *http.MaxBytesError · conditional requests

Standard library: `net/http`, `net/http/httptest`, `strconv`, `io`, `time`
