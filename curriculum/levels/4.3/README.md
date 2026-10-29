# 4.3 · Switchboard

A router, so handlers stop being one giant switch, and the KV API on top.

## Build

```go
type Router struct { /* yours */ }
func NewRouter() *Router
func (r *Router) Handle(pattern string, h Handler)
func (r *Router) HandleFunc(pattern string, f func(*ResponseWriter, *Request))
func (r *Router) ServeHTTP(w *ResponseWriter, req *Request)

func (r *Request) PathValue(name string) string // "" if absent

// KVRoutes mounts GET, PUT and DELETE /v1/kv/{key} backed by st.
func KVRoutes(r *Router, st store.Store)
```

Patterns: `"[METHOD ]/path/with/{wildcards}"`.

- No method means any method. `GET` also matches `HEAD`.
- `{name}` matches one non-empty segment; `{name...}` (last only) matches the rest, slashes included, possibly empty.
- Split the path on `/`, then percent-decode each segment, so `%2F` stays inside a key. A bad escape is 400.
- The most specific pattern wins (literal beats wildcard), regardless of registration order.
- Path matches but method doesn't: 405 with `Allow` listing the methods, sorted, `HEAD` included when `GET` is.
- Invalid or duplicate patterns panic at registration.

KV API: `GET` returns 200 with `Content-Type: application/octet-stream` or 404. `PUT` stores the body (empty is fine), returns 204. `DELETE` returns 204 or 404. Invalid key: 400 with the error text. Body over 1 MiB: 413.

## The tests check

Every rule above through a real HTTP client, including 2 MiB uploads, NUL bytes in values, and 7 kinds of bad pattern.

## Try it

```bash
curl -X PUT --data hello localhost:8080/v1/kv/greeting
curl -i localhost:8080/v1/kv/greeting
```

## New in Go

methods on function types · the adapter pattern · `net/url.PathUnescape` · `slices.Compare` for ranking
