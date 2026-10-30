# S.2 · Keycard

Bearer tokens on the API: a token file, a middleware that checks `Authorization: Bearer` in constant time, read and write roles, and a client that sends the token. Tokens only travel over TLS, so this level sits on S.1.

## Build

```go
// api/auth.go
type Role int

const (
	RoleRead  Role = iota + 1 // GET and HEAD
	RoleWrite                 // everything
)

type Principal struct {
	Name string
	Role Role
}

// TokenSet holds the SHA-256 of each token, never the token itself.
type TokenSet struct { /* yours */ }

// ParseTokens reads one `name role token` per line (role is read or write; # starts a comment;
// blank lines are skipped). Names are unique, tokens are at least 16 bytes.
func ParseTokens(r io.Reader) (*TokenSet, error)

// Lookup hashes token and compares it with every stored hash using crypto/subtle.ConstantTimeCompare,
// visiting all of them before answering, so the time taken does not depend on which token (if any) matched.
func (ts *TokenSet) Lookup(token string) (Principal, bool)

type AuthOptions struct {
	Tokens        *TokenSet
	AllowInsecure bool // accept tokens on plaintext connections (r.TLS == nil); tests and local only
}

// Auth is a Middleware. Missing, malformed or unknown credentials: 401 ErrorResponse{code: unauthorized}
// with `WWW-Authenticate: Bearer realm="burrow"`. A token presented over plaintext without AllowInsecure:
// 401 whose Error mentions TLS. A read principal on a non-GET/HEAD request: 403 {code: forbidden}.
// On success the Principal is stored in the request context and the access log line (7.3) gains
// `principal: <name>`. The token itself is never logged.
func Auth(o AuthOptions) Middleware
func PrincipalFrom(ctx context.Context) (Principal, bool)

// Server.Handler(): Chain(mux, Recover(), RequestID(), AccessLog(l), Auth(o)) when o.Tokens != nil.
// burrowd serve gains -auth-tokens <file> and -auth-insecure. -auth-tokens without -tls-cert exits 2
// unless -auth-insecure is also given.
// burrow.Options gains Token string, sent as `Authorization: Bearer <token>` on every request, retries included.
// burrowctl gains -token and reads BURROW_TOKEN when the flag is absent.
```

- `ParseTokens`, `TokenSet.Lookup` with `sha256` + `subtle.ConstantTimeCompare` over every entry
- `Auth` middleware: 401 with `WWW-Authenticate`, 403 for read tokens on writes, principal in context and in the access log
- `-auth-tokens`/`-auth-insecure` in `burrowd serve`; `Options.Token`; `burrowctl -token` / `BURROW_TOKEN`

Why hash first and compare all: `subtle.ConstantTimeCompare` returns 0 immediately when the lengths differ, which leaks the length of the secret, and a `map[string]Principal` keyed by the raw token uses the fast, variable-time string comparison. Hashing the presented token with SHA-256 gives every comparison the same 32-byte length, and comparing against every stored hash (accumulating the result with `|`, never breaking early) makes the time independent of which entry matched. It is a few microseconds for a few hundred tokens and the shape generalises to API keys, webhook signatures (`hmac.Equal` is the same function) and password-reset codes. Hashing with plain SHA-256 is fine here because tokens are long random strings, not passwords; passwords need a slow hash (`golang.org/x/crypto/argon2` or `bcrypt`), which is outside the standard library and outside this curriculum.

Decisions the tests rely on:

- `Authorization` must be exactly `Bearer <token>` (scheme case-insensitive, one space, no trailing data). `Basic`, a bare token, and a token in the query string are all 401. A `?token=` query parameter is ignored, never accepted.
- `GET` and `HEAD` are reads; every other method is a write. `POST /v1/batch` is a write even when every op in it is a `get`.
- `Auth` runs inside `AccessLog` (so the log line has the principal) and the admin port (7.6) has no auth: `/healthz` and `/readyz` stay open.
- `ParseTokens` rejects a bad role, a duplicate name, a token shorter than 16 bytes and a line with the wrong number of fields, naming the line number in the error.
- Without `-auth-tokens` (or with `AuthOptions.Tokens == nil`) the API is open, as it was before; every earlier suite still passes unchanged.
- `burrow.Client` never retries a 401 or 403 (they are not transient); both surface as `*APIError` with the status and code.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S.2/files/`, matching the earlier chapters.

## Pass when

A request without a token is 401 with WWW-Authenticate, a read token can GET but gets 403 on PUT, a write token does everything, the access log names the principal and never the token, and burrowd refuses to combine tokens with plaintext unless told to.

The suite will cover:

- ParseTokens: comments, blank lines, read and write roles, a duplicate name, an unknown role, a 10-byte token and a 2-field line are errors naming the line
- Lookup over a 3-token set: the right token finds its principal; the same token with its last byte changed, the empty string, and a token of a different length all miss; `fmt.Sprintf("%+v", ts)` does not contain any token string, because only hashes are stored
- Auth over httptest.NewTLSServer wrapping the full Handler(): no header → 401, JSON code `unauthorized`, `WWW-Authenticate: Bearer realm="burrow"`; `Basic xyz` and `Bearer` with no token → 401; unknown token → 401; read token: GET 200, HEAD 200, PUT 403 `forbidden`, DELETE 403, POST /v1/batch of gets 403; write token: PUT 204, DELETE 204, batch 200; `?token=` with a valid token and no header → 401
- Over plain httptest.NewServer: a valid token is 401 with an error mentioning TLS unless AllowInsecure, with AllowInsecure it is accepted
- The access log line for an authenticated request has `principal` equal to the token's name and does not contain the token string anywhere; a 401's line has no principal
- burrowd: -auth-tokens without -tls-cert exits 2 with a message naming both flags; with -auth-insecure it starts; with -tls-cert/-tls-key and -auth-tokens, a burrow.Client with RootCAs and Token round-trips, the same client with a read token gets *APIError{Status: 403, Code: "forbidden"} on Set with no retries (the server saw one request), and burrowctl -http https://… -cacert … -token … set/get work, as does BURROW_TOKEN in the environment
- The admin port's /healthz is 200 without any token while the API port requires one

## Try it

```bash
printf 'ops write %s\nreader read %s\n' "$(openssl rand -hex 24)" "$(openssl rand -hex 24)" > tokens.txt
go run ./cmd/burrowd serve -http 127.0.0.1:8443 -tls-cert certs/server.pem -tls-key certs/server-key.pem -auth-tokens tokens.txt &
curl --cacert certs/ca.pem -i https://127.0.0.1:8443/v1/keys                       # 401
BURROW_TOKEN=$(awk '/reader/{print $3}' tokens.txt) go run ./cmd/burrowctl -http https://127.0.0.1:8443 -cacert certs/ca.pem set a 1   # forbidden
```

## Uses

- TLS API (built in S.1 · Shielded Cable)
- Middleware chain (built in 7.3 · Signal Chain)
- HTTP client lib (built in 7.5 · Client Library)

## In the wild

Every API-key scheme (Stripe, GitHub PATs) is this middleware; OAuth2 bearer tokens (RFC 6750) use the same header and the same 401/403 split; hmac.Equal and subtle.ConstantTimeCompare are what webhook verification is built on

## New in Go

crypto/subtle.ConstantTimeCompare · crypto/sha256 · timing side channels · roles and a principal in context · WWW-Authenticate and 401 vs 403 · r.TLS as a connection property · environment variables as defaults for flags

Standard library: `crypto/subtle`, `crypto/sha256`, `bufio`, `net/http`, `context`, `os`
