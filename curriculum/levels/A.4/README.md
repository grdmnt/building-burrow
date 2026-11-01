# A.4 · Composition Root

> The only package that knows every concrete type is main. Everything else knows an interface and is handed the real thing.

## Your goal

Finish `accessd`. Write `access/httpapi`, the HTTP edge: seven routes whose handlers decode the request, call one `Service` method and turn the result (or the error) into a response, with one `writeError` deciding every status code. Then write `cmd/accessd`, whose `main` is the *composition root*: the one place that builds the concrete pieces (a `burrow.Client`, a `kvrepo.Repo`, an `access.Service`, the handler, an `http.Server`) and plugs each into the next.

This level also turns the architecture into tests. A.4's suite parses the imports of every package in the chapter and fails, with the file and line, if a dependency points the wrong way: the domain importing HTTP, a handler importing the repository, anything but `cmd/` importing `kvrepo`. That is the part of Packwerk that Go needs, done with the standard library's own parser.

## What you're using from before

**Access service (A.2).** `httpapi.New` takes a `*access.Service` and nothing else from the domain. Each handler is one call:

```go
token, t, err := h.svc.IssueKey(r.Context(), r.PathValue("id"),
	version, req.Name, req.Role)
```

**KV repository (A.3).** `main` builds it from the Burrow client. Nothing else in the module may import it.

**JSON API (7.2).** Errors are `api.ErrorResponse{Error, Code}`, the same shape Burrow uses, and requests are decoded the same strict way: `DisallowUnknownFields`, a 1 MiB limit, exactly one JSON value.

**Middleware chain (7.3) and Deadline guard (7.4).** The handler is wrapped exactly as Burrow's is, so every response gets `X-Request-ID`, every request gets an access log line, a panic becomes a JSON 500, and every request has a 1 s budget that reaches the store through the context:

```go
return api.Chain(mux, api.Recover(), api.RequestID(),
	api.AccessLog(l), api.Timeout(time.Second))
```

`cmd/accessd` sets the four `http.Server` timeouts and shuts down with `Shutdown` under a 10 s budget, as `burrowd` does since 7.4.

## What's new here

**1. Handlers have no rules.** A handler does four things: decode the request, call the service, map the error, encode the result. If you find an `if` about teams or keys in a handler, it belongs in `Team` or `Service`. This is a Rails controller with strong parameters (the request struct plus `DisallowUnknownFields`) and nothing else, and it is why the handlers here are short enough to read at a glance.

```go
func (h *handler) getTeam(w http.ResponseWriter,
	r *http.Request) {
	t, err := h.svc.GetTeam(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(t.Version))
	writeJSON(w, http.StatusOK, teamOut(t))
}
```

`teamOut` builds a response struct of its own, a second DTO, and leaves out the hashes: the response format is the HTTP layer's decision, just as the stored format was `kvrepo`'s.

**2. One place maps errors to statuses.** `writeError` is a `switch` on `errors.Is`, in one function:

| Error | Status | Code |
|---|---|---|
| bad JSON, bad `If-Match` | 400 | `invalid_request` |
| `If-Match` missing | 428 | `precondition_required` |
| `ErrConflict` | 412 | `conflict` |
| `ErrTeamNotFound`, `ErrKeyNotFound` | 404 | `not_found` |
| `ErrUnauthenticated` | 401 | `unauthenticated` |
| `ErrDuplicateKey` | 422 | `duplicate_key` |
| `ErrTooManyKeys` | 422 | `too_many_keys` |
| `ErrRevoked` | 422 | `revoked` |
| `ErrInvalid` | 422 | `invalid` |
| context deadline or cancel | 503 | `unavailable` |
| anything else | 500 | `internal` |

422 Unprocessable Content means "I understood the request, and the rules say no". The last row matters most: an unknown error's text may hold a host name, a file path or a query, so the client gets only `internal error (request <id>)`, and the real error goes to the log under the same request ID (`api.RequestIDFrom(r.Context())`).

**3. Optimistic concurrency over HTTP.** `GET` sends the team's version as an `ETag: "3"`. Changing a team's keys requires `If-Match: "3"`: without it, `428 Precondition Required`; with an old one, `412 Precondition Failed`. Every success sends the new `ETag`. This is A.2's version argument carried by HTTP's own headers, the way a Rails form carries `lock_version` in a hidden field, except that here the server refuses to guess when the client didn't say.

**4. Reusing Burrow's middleware.** `httpapi` imports `burrow/api` for `Chain`, `RequestID`, `AccessLog`, `Recover`, `Timeout` and `ErrorResponse`. That couples the access service's edge to Burrow's API package, which is a fair trade inside one repository. In a real codebase you would lift those into a shared package (say `internal/httpx`) that both services import, and the import rules below would not change.

**5. The composition root.** `main` is the only code that names every concrete type, and it reads like the architecture diagram:

```go
client, err := burrow.New(*burrowURL, burrow.Options{})
repo := kvrepo.New(client)
svc := access.NewService(repo, access.Options{
	Events: logSink{logger},
})
srv := &http.Server{
	Handler:           httpapi.New(svc, logger),
	ReadHeaderTimeout: 5 * time.Second,
	// ReadTimeout, WriteTimeout, IdleTimeout as in 7.4
}
```

Swap `kvrepo.New(client)` for `access.NewMemStore()` and nothing else changes: that is the payoff of A.2's port. `logSink` is a tiny type in `main` with a `Publish` method that writes each event as a log line; it is the production `EventSink`.

**6. Import rules as tests.** `go/parser.ParseFile` with the `parser.ImportsOnly` mode reads a file just far enough to list its imports, each with a position you can turn into `file:line`. A test that walks the chapter's directories and checks each import against a deny-list is Packwerk's dependency check in 60 lines:

```go
fset := token.NewFileSet()
f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
for _, imp := range f.Imports {
	p, _ := strconv.Unquote(imp.Path.Value)
	if p == "burrow/access/kvrepo" {
		pos := fset.Position(imp.Pos())
		t.Errorf("%s:%d imports %s", pos.Filename, pos.Line, p)
	}
}
```

The rules are deny-lists (what may not be imported), never allow-lists, so adding a sub-package or a standard library import never breaks them. They look only at *direct* imports of non-test files, so tests may import whatever helps, and a file marked `//go:build ignore` is skipped.

**7. How it all maps.** Go has no Rails conventions; this chapter is one sound layout, not the only one. Here is where each familiar piece ended up:

| Rails / DDD | Here |
|---|---|
| Controller | `httpapi` handler: decode, call, map the error; no rules |
| Service object, interactor | an `access.Service` method, one per use case |
| Model (minus ActiveRecord) | `access.Team`, an aggregate that knows nothing of storage |
| `lock_version`, `StaleObjectError` | `Team.Version`, CAS, `ErrConflict`, 412 |
| Repository, ActiveRecord | the `TeamStore` port plus the `kvrepo` adapter |
| dry-system container | `cmd/accessd/main.go` and constructor arguments |
| Packwerk `package.yml` | the import rules in `TestLA_4_ImportRules` |
| Rails Event Store | `access.Event` values and the `EventSink` port |
| Bounded context, engine | the `access/` tree |
| dry-monads `Result` | `(T, error)` and `errors.Is` on sentinels |

One more connection: S.2 gives `burrowd` a file of bearer tokens. In a real deployment that file would be generated from `accessd`, which is where teams and their keys are managed.

## What to do

1. Create `access/httpapi/` with `New(svc *access.Service, l *slog.Logger) http.Handler`. A nil logger means discard.
2. Register the seven routes in the Reference on an `http.ServeMux` and wrap it in the chain above.
3. Write a strict `decode` (7.2's rules), an `ifMatch` that parses `"<n>"`, and `writeError` with the table above. On a 500, log the error with the request ID and send only `internal error (request <id>)`.
4. Write the handlers. Send `ETag` with every team response and `Location` on create.
5. Create `cmd/accessd/` with the `run(args, stdout, stderr) int` pattern from 0.1 and a `serve` command taking `-burrow <url>` (required) and `-http <addr>` (default `127.0.0.1:8090`).
6. In `serve`, build the pieces in order, log with `slog.NewJSONHandler(stderr, nil)`, listen, print `http on <addr>` to stdout, and on `SIGINT` or `SIGTERM` call `Shutdown` with a 10 s budget and exit 0.
7. Keep the import rules (Reference) and run `go test ./...` to see them checked.

## Example

```bash
$ go run ./cmd/burrowd serve -http 127.0.0.1:8080 &
$ go run ./cmd/accessd serve -burrow http://127.0.0.1:8080 &
http on 127.0.0.1:8090
$ curl -si localhost:8090/v1/teams -d '{"name":"Platform"}'
HTTP/1.1 201 Created
Etag: "1"
Location: /v1/teams/5f0c...e9
X-Request-Id: 3b9e...

{"id":"5f0c...e9","name":"Platform","version":1,"keys":[]}
$ curl -s localhost:8090/v1/teams/5f0c...e9/keys -H 'If-Match: "1"' \
    -d '{"name":"ci","role":"write"}'
{"token":"brw_9a1d...","version":2,"key":{"name":"ci",
 "role":"write","created":"2026-11-01T09:00:00Z"}}
$ curl -s localhost:8090/v1/teams/5f0c...e9/keys -H 'If-Match: "1"' \
    -d '{"name":"deploy","role":"read"}'
{"error":"access: version conflict: ...","code":"conflict"}
$ curl -s localhost:8090/v1/authenticate -d '{"token":"brw_9a1d..."}'
{"team_id":"5f0c...e9","team":"Platform","key":"ci","role":"write"}
```

And the rule test, when a handler reaches for the repository:

```text
--- FAIL: TestLA_4_ImportRules
    access/httpapi/teams.go:9 imports burrow/access/kvrepo. Handlers
    call the Service through its port; only cmd/accessd decides
    which TeamStore is real. Build it in main and pass it in.
```

## Done when

- [ ] Every route answers as in the Reference, with `ETag` on team responses and `If-Match` required for key changes
- [ ] Every error is an `api.ErrorResponse` with the status and code from the table; a 500 never shows the internal error but names the request ID, and the log has the error
- [ ] Two clients sending the same `If-Match` get one 201 and one 412
- [ ] A panic is a 500 and the next request works; a store that hangs gives 503 after about 1 s, and the store saw `context.DeadlineExceeded`
- [ ] `accessd serve -burrow <url>` stores teams in that Burrow, logs events as JSON lines, and exits 0 on `SIGTERM`
- [ ] The import rules pass, and break with the file and line when you violate one

## Reference

```go
// package httpapi (directory access/httpapi/)
func New(svc *access.Service, l *slog.Logger) http.Handler

// Routes (JSON in and out):
//   POST   /v1/teams                  {"name"} -> 201 team,
//                                     ETag, Location /v1/teams/{id}
//   GET    /v1/teams                  -> 200 [team, ...] ([] if none)
//   GET    /v1/teams/{id}             -> 200 team, ETag
//   DELETE /v1/teams/{id}             -> 204
//   POST   /v1/teams/{id}/keys        If-Match, {"name","role"}
//          -> 201 {"token","version","key"}, ETag
//   DELETE /v1/teams/{id}/keys/{name} If-Match -> 200 team, ETag
//   POST   /v1/authenticate           {"token"}
//          -> 200 {"team_id","team","key","role"}
//
// team = {"id","name","version","keys":[key...]}
// key  = {"name","role","created","revoked"?}; never the hash
// ETag and If-Match: the team's version in quotes, "3".
// Wrapped in api.Chain(mux, api.Recover(), api.RequestID(),
//   api.AccessLog(l), api.Timeout(time.Second)).

// cmd/accessd
//   accessd serve -burrow <url> [-http 127.0.0.1:8090]
// prints "http on <addr>" to stdout once listening, logs JSON to
// stderr, one {"msg":"event","type":"key.issued",...} line per
// event, exits 0 after SIGINT or SIGTERM. No command, an unknown
// command or flag, or serve without -burrow: usage on stderr, exit 2.
```

Import rules (direct imports of non-test files; test files and `//go:build ignore` files are skipped):

| Package | May not import |
|---|---|
| `access` | anything under `burrow/`, `net/http` and below, `database/sql` |
| `access/kvrepo` | `net/http` and below, `burrow/api`, `burrow/access/httpapi` |
| `access/httpapi` | `burrow/access/kvrepo`, `burrow/burrow`, `burrow/store` |
| everything outside `cmd/` | `burrow/access/kvrepo` |

Decisions the tests rely on:

- A request body is one JSON object with only the listed fields; anything else is 400 `invalid_request`. `If-Match` that isn't a quoted number is 400.
- Revoking returns 200 with the updated team (so the client has the new `ETag`); deleting a team returns 204.
- `POST /v1/authenticate` answers 401 for every bad token, unknown or revoked alike.
- Tokens and hashes never appear in a team response or in any log line.
- `access/`, `access/kvrepo/`, `access/httpapi/` and `cmd/accessd/` each contain at least one non-test Go file. Extra sub-packages are fine.

## The tests check

- `POST /v1/teams`: 201, JSON body with `"keys": []`, `ETag` equal to the version, `Location`, a 32-hex `X-Request-ID`; `GET` returns the same team and `ETag`; `GET /v1/teams` lists it, and is `[]` for an empty service
- Key lifecycle: missing `If-Match` is 428, an unquoted one 400, a future one 412; issuing returns token, version and key with a matching `ETag`; the team response never contains the token or its hex hash; authenticate returns team ID, team name, key and role; revoke needs `If-Match`, rejects a stale one, returns the team with `revoked` set and a new `ETag`; the revoked token is 401; revoking again is 422 `revoked`
- Error mapping for a missing team, empty name, unknown field, non-JSON, two JSON values, duplicate key, eleventh key, bad role, keys of a missing team, unknown key, unknown token and deleting a missing team; `DELETE /v1/teams/{id}` is 204 and the team is gone
- A store whose errors contain a host name: 500 `internal` on three routes, the body names the request ID but not the error, the log has the error and no token
- A store that panics: 500 `internal`, repeatedly, with the server still answering
- A store that blocks until its context ends: 503 `unavailable` in 0.5 to 3 s, and the store saw `context.DeadlineExceeded`
- The access log has a JSON line with `msg` `request`, the response's request ID, the path and status 201
- Six clients sending the same `If-Match`: one 201, five 412
- `run` usage errors: no command, unknown command, `serve` without `-burrow`, unknown flag, all exit 2 naming the problem
- The built `accessd` against an `httptest` Burrow: `http on` line, a team created through it is in Burrow at `access/team/<id>` with the same version, issuing and authenticating work, `X-Request-ID` is set, events are logged as JSON lines with `msg` `event` and both event types, no token is logged, and `SIGTERM` exits 0
- TestLA_4_ImportRules: the four directories exist, and the deny-lists above hold, failing with `file:line` and an explanation
- TestLA_4_OnlyMainKnowsKvrepo: a walk over the whole module (skipping nested modules, dot directories and `testdata`) finds `burrow/access/kvrepo` imported only under `cmd/`

## Try it

```bash
./burrow watch
go run ./cmd/burrowd serve -http 127.0.0.1:8080 &
go run ./cmd/accessd serve -burrow http://127.0.0.1:8080
curl -s localhost:8090/v1/teams -d '{"name":"Platform"}'
go run ./cmd/burrowctl -http http://127.0.0.1:8080 keys access/
```

To see a rule fire, add `import _ "burrow/access/kvrepo"` to a file in `access/httpapi/` and run `go test -run TestLA_4_Import ./cmd/accessd`. Take it out again.

## In the wild

Shopify's Packwerk and the Go tool `go-arch-lint` check the same kind of rules; Kubernetes runs `import-boss` over its own tree. Ben Johnson's WTF Dial app wires its `main` exactly like this, and kube-apiserver is the large version of `accessd`: an HTTP edge over aggregates stored one per key in etcd, with `resourceVersion` as the ETag.

## New in Go

handlers that only translate · one error-to-status function · 422, 428 and 412 · ETag and If-Match · a response DTO · reusing another package's middleware · the composition root in main · an EventSink in main · go/parser with ImportsOnly · token.FileSet positions · architecture rules as tests · deny-lists over allow-lists

Standard library: `net/http`, `encoding/json`, `log/slog`, `go/parser`, `go/token`, `strconv`, `os/signal`
