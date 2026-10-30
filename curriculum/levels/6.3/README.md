# 6.3 · Router

> One handler per route, not one giant switch. Then the first real Burrow API: keys in URLs, values in bodies, versions in ETags.

## Your goal

Write a `Router` for `httpraw`: you register patterns like `GET /v1/kv/{key}` with a handler each, and the router picks the right one for every request, fills in `{key}` so the handler can ask for it with `r.PathValue("key")`, answers `404` for unknown paths and `405` (with an `Allow` header) for known paths with the wrong method. Then mount the raw key-value API on it with `KVRoutes`: `GET`, `PUT` and `DELETE` on `/v1/kv/{key}` and a key listing on `/v1/keys`. The entry's version travels in the `ETag` header, the HTTP name for "a tag identifying this version of the resource".

## What you're using from before

**Response writer (6.2 · Status and Body).** The router is itself a `Handler`: it has a `ServeHTTP(w, req)` method, so you plug it into the 6.2 server and it dispatches from there. Handlers registered on it get the same `*ResponseWriter` and `*Request`, and `Header().Set`, `WriteHeader` and `Write` do what they did in 6.2. The server's 1 MiB body limit still applies before any route runs, which is where the 413 for a 2 MiB `PUT` comes from.

```go
r := httpraw.NewRouter()
r.HandleFunc("GET /health",
    func(w *httpraw.ResponseWriter, _ *httpraw.Request) {
        w.Write([]byte("ok\n"))
    })
srv := &httpraw.Server{Handler: r}
```

**Memory store (1.1 · Register File).** `KVRoutes(r, st)` takes any `store.Store` and uses `Get`, `Set`, `Delete` and `Keys`. `store.ErrNotFound` becomes a `404`, `store.ErrInvalidKey` a `400`, and `Entry.Version` from 1.3 becomes the `ETag`:

```go
e, err := st.Get(context.Background(), key)
if errors.Is(err, store.ErrNotFound) {
    w.WriteHeader(404)
    return
}
w.Header().Set("ETag", `"`+strconv.FormatUint(e.Version, 10)+`"`)
```

## What's new here

**Parse patterns once, at registration.** A pattern is `[METHOD ]/seg/seg/...`. Split it into an optional method and a list of segments, where each segment is one of three kinds: a literal (`kv`), a wildcard `{name}` that matches exactly one non-empty path segment, or a rest wildcard `{name...}` that must be last and matches everything that remains, slashes included, possibly nothing. Do all the checking here and `panic` on a bad pattern. A panic is the right tool because a bad pattern is a programming error in a line of code that runs at startup, and failing loudly at startup beats a route that never matches:

```go
type segment struct {
    literal  string // "kv"; empty for a wildcard
    name     string // "key" for {key} or {key...}
    rest     bool   // {key...}
}
type route struct {
    method string    // "" means any
    segs   []segment
    h      Handler
}
```

**Matching and specificity.** To match a request, split its path on `/` and walk the segments. Several patterns can match one path (`/kv/{key}` and `/kv/special` both match `/kv/special`), and the more specific one must win no matter which was registered first. Give each matched segment a score (literal 2, wildcard 1, rest 0) and compare the score slices. `slices.Compare(a, b)` compares two slices element by element, like comparing strings, and returns -1, 0 or 1:

```go
slices.Compare([]int{2, 2}, []int{2, 1})  // 1: the literal wins
slices.Compare([]int{2, 1}, []int{2, 1})  // 0: same shape
```

A 0 at registration time between two patterns with the same method means they are duplicates, and that is a panic too.

**404 versus 405.** Walk every route. If the path matched but the method did not, remember that route's method. If nothing matched at all it is `404`. If something matched by path only, it is `405` with `Allow` listing the remembered methods, sorted, with `HEAD` added whenever `GET` is there, because a `GET` route also serves `HEAD`.

**Percent decoding per segment with `net/url`.** A client that wants the key `a/b` sends `/v1/kv/a%2Fb`. If you decoded the whole path first, the `/` would appear and split the key in two. So split on `/` first, then `url.PathUnescape` each segment. A bad escape like `%zz` returns an error, and that is a `400`:

```go
url.PathUnescape("a%2Fb%20c")  // "a/b c", nil
url.PathUnescape("bad%zz")     // "", error
```

**Closures capturing a store.** `KVRoutes` registers four handlers, and each one needs the store. A function literal that mentions `st` keeps a reference to it for as long as the function lives. That is a closure, and it is how you hand a dependency to a handler without a global:

```go
func KVRoutes(r *Router, st store.Store) {
    r.HandleFunc("GET /v1/kv/{key}",
        func(w *ResponseWriter, req *Request) {
            key := req.PathValue("key")
            e, err := st.Get(context.Background(), key)
            // ...
        })
}
```

## What to do

1. Create `httpraw/router.go` with `Router`, `NewRouter`, `Handle`, `HandleFunc` and `ServeHTTP`. Give `Request` an unexported field for matched path values and the `PathValue(name)` method, returning `""` for a name that is absent.
2. In `Handle`, parse the pattern into a method and segments. Panic on: an empty pattern, no path, two spaces, a path without a leading `/`, `{}`, an unterminated `{`, two wildcards in one segment, a repeated name, a `{rest...}` that is not last, and a pattern with the same method and same shape as one already registered.
3. In `ServeHTTP`, split the request path on `/`, decode each segment (a bad escape is `400`), and try every route. Keep the best match by score. No path match: `404`. Path match but wrong method: `405` with `Allow`. A `GET` route matches `HEAD` too.
4. Fill the path values from the winning route, then call its handler.
5. Write `KVRoutes(r, st)`:
   - `GET /v1/kv/{key}`: `200`, `Content-Type: application/octet-stream`, `ETag: "<version>"`, the raw bytes. Missing: `404`.
   - `PUT /v1/kv/{key}`: read the whole body (empty is a valid value), `Set`, then `204` with the new `ETag`. An invalid key: `400` with the error text in the body.
   - `DELETE /v1/kv/{key}`: `204`, or `404` when missing.
   - `GET /v1/keys`: `prefix` from the query string, `text/plain; charset=utf-8`, one key per line each followed by `\n`, sorted. No keys: an empty body.

## Example

```bash
$ curl -i -X PUT --data hello localhost:8080/v1/kv/greeting
HTTP/1.1 204 No Content
ETag: "1"
Date: Sat, 31 Oct 2026 09:20:11 GMT

$ curl -i localhost:8080/v1/kv/greeting
HTTP/1.1 200 OK
Content-Type: application/octet-stream
Content-Length: 5
ETag: "1"
Date: Sat, 31 Oct 2026 09:20:12 GMT

hello
$ curl -X PUT --data bye localhost:8080/v1/kv/a%2Fb
$ curl localhost:8080/v1/keys
a/b
greeting
$ curl -i -X POST localhost:8080/v1/kv/greeting
HTTP/1.1 405 Method Not Allowed
Allow: DELETE, GET, HEAD, PUT
```

In code, with wildcards:

```go
r.HandleFunc("GET /users/{id}/posts/{post}",
    func(w *ResponseWriter, req *Request) {
        id, post := req.PathValue("id"), req.PathValue("post")
        w.Write([]byte(id + "|" + post))
    })
// GET /users/42/posts/hello   -> "42|hello"
// GET /users/42/posts         -> 404 (too few segments)
// GET /users//posts/hello     -> 404 ({id} needs a non-empty segment)
```

## Done when

- [ ] `GET`, `POST` and `DELETE` on the same path reach three different handlers; a pattern with no method matches every method; a `GET` route answers `HEAD` with the headers and no body
- [ ] An unknown path is `404`; a known path with the wrong method is `405` with `Allow: DELETE, GET, HEAD, PUT`
- [ ] `{name}` fills `PathValue` and needs one non-empty segment; `{path...}` takes the rest, slashes included, and may be empty; too few or too many segments is `404`
- [ ] `/kv/a%2Fb%20c` gives the key `a/b c`; `%zz` is `400`
- [ ] `/kv/special` beats `/kv/{key}` whichever was registered first
- [ ] The ten bad patterns panic at registration, and so does registering the same shape twice
- [ ] The KV API works through `net/http`'s client: `PUT` is `204` with `ETag "1"`, `GET` returns the bytes (NUL included) with the `ETag`, a second `PUT` gives `"2"`, `DELETE` is `204` then `404`, an invalid key is `400` mentioning `invalid key`, a 2 MiB `PUT` is `413`, `POST` is `405`
- [ ] `/v1/keys` lists sorted keys one per line with a trailing newline, `?prefix=` filters, and `/v1/kv/a%2Fb` stores the key `a/b`

## Reference

The exact contract the tests hold you to.

```go
// httpraw/router.go
type Router struct { /* yours */ }
func NewRouter() *Router
func (r *Router) Handle(pattern string, h Handler)
func (r *Router) HandleFunc(pattern string,
	f func(*ResponseWriter, *Request))
func (r *Router) ServeHTTP(w *ResponseWriter, req *Request)

func (r *Request) PathValue(name string) string // "" if absent

// KVRoutes mounts the raw KV API backed by st:
//   GET    /v1/kv/{key}  200 application/octet-stream
//                        + ETag: "<version>" | 404
//   PUT    /v1/kv/{key}  body is the value (empty ok); 204 + ETag
//                        | 400 invalid key | 413
//   DELETE /v1/kv/{key}  204 | 404
//   GET    /v1/keys?prefix=...  text/plain, one key per line, sorted
func KVRoutes(r *Router, st store.Store)

// Patterns: "[METHOD ]/path/with/{wildcards}". No method means any; GET
// also matches HEAD. {name} matches one non-empty segment; {name...}
// (last only) matches the rest, possibly empty. Split on /, then
// percent-decode each segment, so %2F stays inside a key; a bad
// escape is 400. Most specific pattern wins regardless of registration
// order. Path matches but method doesn't: 405 with Allow listing the
// methods sorted, HEAD included when GET is. Invalid or duplicate
// patterns panic at registration.
```

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
./burrow watch
curl -X PUT --data hello localhost:8080/v1/kv/greeting
curl -i localhost:8080/v1/kv/greeting
```

## In the wild

net/http.ServeMux since Go 1.22 (method patterns, wildcards, precedence); chi and gorilla/mux before that.

## New in Go

pattern parsing at registration · panics for programmer errors · slices.Compare for ranking · net/url.PathUnescape · closures capturing a Store

Standard library: `strings`, `slices`, `net/url`, `strconv`
