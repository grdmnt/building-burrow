# 6.3 · Router

A router, so handlers stop being one giant switch, and the raw KV API on top of it with ETags carrying the version.

## Build

```go
// httpraw/router.go
type Router struct { /* yours */ }
func NewRouter() *Router
func (r *Router) Handle(pattern string, h Handler)
func (r *Router) HandleFunc(pattern string, f func(*ResponseWriter, *Request))
func (r *Router) ServeHTTP(w *ResponseWriter, req *Request)

func (r *Request) PathValue(name string) string // "" if absent

// KVRoutes mounts the raw KV API backed by st:
//   GET    /v1/kv/{key}  200 application/octet-stream + ETag: "<version>"  | 404
//   PUT    /v1/kv/{key}  body is the value (empty ok); 204 + ETag          | 400 invalid key | 413
//   DELETE /v1/kv/{key}  204 | 404
//   GET    /v1/keys?prefix=...  text/plain, one key per line, sorted
func KVRoutes(r *Router, st store.Store)

// Patterns: "[METHOD ]/path/with/{wildcards}". No method means any; GET also matches HEAD.
// {name} matches one non-empty segment; {name...} (last only) matches the rest, possibly empty.
// Split on /, then percent-decode each segment, so %2F stays inside a key; a bad escape is 400.
// Most specific pattern wins regardless of registration order. Path matches but method doesn't:
// 405 with Allow listing the methods sorted, HEAD included when GET is. Invalid or duplicate
// patterns panic at registration.
```

- `Router` with pattern parsing at registration, specificity ranking and `PathValue`
- 404, 405 with `Allow`, percent decoding, bad-pattern panics
- `KVRoutes` with ETag on reads and writes

Decisions the tests rely on:

- `GET /v1/keys` writes one key per line with a trailing newline after the last one, as `text/plain; charset=utf-8`.
- The `ETag` is the version in double quotes, on `GET` and after `PUT`.
- A key that fails `store.ValidateKey` is a 400 with the error text.

## The tests check

- Method and path matching for GET/POST/DELETE on one path; no-method patterns match any method; GET matches HEAD (200, empty body, Content-Length 5)
- 404 for an unknown path; 405 with `Allow: DELETE, GET, HEAD, PUT` for a known path and wrong method
- {name} wildcards and PathValue (an absent name is empty); {path...} takes the rest including slashes and may be empty; too few, too many and empty segments are 404
- Percent decoding per segment: /kv/a%2Fb%20c gives key `a/b c`; a bad escape %zz is 400
- Most specific wins: /kv/special beats /kv/{key} regardless of registration order
- Ten bad patterns panic at registration (empty, no path, double space, no leading slash, `{}`, unterminated brace, two wildcards in one segment, a repeated name, `{rest...}` not last) and so does registering the same pattern shape twice
- KV API through net/http's client: 404 for a missing key; PUT is 204 with ETag `"1"`; GET returns the bytes (NUL included) as application/octet-stream with the ETag; a second PUT answers ETag `"2"`; DELETE is 204 then 404; an invalid key is 400 mentioning `invalid key`; an empty body stores an empty value; a 2 MiB PUT is 413; POST is 405
- GET /v1/keys lists keys one per line, sorted, with a trailing newline, as text/plain; charset=utf-8; ?prefix= filters; no matches is an empty body
- /v1/kv/a%2Fb stores the key `a/b`

## Try it

```bash
curl -X PUT --data hello localhost:8080/v1/kv/greeting
curl -i localhost:8080/v1/kv/greeting
```

## Uses

- Response writer (built in 6.2 · Status and Body)
- Memory store (built in 1.1 · Register File)

## In the wild

net/http.ServeMux since Go 1.22 (method patterns, wildcards, precedence); chi and gorilla/mux before that

## New in Go

methods on function types · the adapter pattern · net/url.PathUnescape · slices.Compare for ranking · closures capturing a Store

Standard library: `strings`, `slices`, `net/url`, `strconv`
