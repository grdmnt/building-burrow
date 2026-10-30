# S.2 · Keycard

> The door is locked now. A keycard opens it, some cards open fewer doors, and the reader takes the same time to say no to every wrong card.

## Your goal

Make the API ask who is calling. The scheme is *bearer tokens*: a long random string handed out once, sent on every request as `Authorization: Bearer <token>`, and whoever bears it is trusted as its owner. `burrowd` reads a file of `name role token` lines; a `read` token may only `GET` and `HEAD`, a `write` token may do anything. The check is a middleware that answers `401` (who are you?) or `403` (I know who you are, and no), puts the caller's name, the *principal*, into the request context and the access log, and never logs the token itself. The comparison must take the same time whether the token is right, nearly right, or garbage, because the time it takes can leak the secret. Tokens only travel over TLS, which is why this level sits on S.1.

## What you're using from before

**TLS API (S.1 · Shielded Cable).** A bearer token on a plaintext connection is readable by anyone on the path, so `Auth` refuses tokens unless the connection is TLS or it was told not to care. `r.TLS` is how a handler knows: it is a `*tls.ConnectionState` on a TLS connection and `nil` otherwise. The same rule at startup: `-auth-tokens` without `-tls-cert` is an error unless `-auth-insecure` is given.

```go
if r.TLS == nil && !o.AllowInsecure {
	// 401 whose message mentions TLS
}
```

**Middleware chain (7.3 · Signal Chain).** `Auth` is one more `Middleware`. It goes *inside* `AccessLog`, so that when the log line is written after the handler ran, the principal is already in the context. The context value uses an unexported key type, the same move as `RequestIDFrom`:

```go
func (s *Server) Handler() http.Handler {
	mw := []Middleware{Recover(), RequestID(), AccessLog(s.Logger)}
	if o.Tokens != nil { // o: the AuthOptions the Server was given
		mw = append(mw, Auth(o))
	}
	return Chain(mux, mw...)
}
```

**HTTP client lib (7.5 · Client Library).** `Options` gains `Token`, set as the `Authorization` header on every request the client builds, retries included. `401` and `403` are not transient, so the retry loop from 7.5 must not retry them; both come back as `*APIError` with the status and code.

## What's new here

**401 versus 403, and `WWW-Authenticate`.** `401 Unauthorized` means "I don't know who you are": no credentials, malformed ones, or a token nobody has. By the standard it must carry a `WWW-Authenticate` header telling the client what kind of credentials would do, here `Bearer realm="burrow"`. `403 Forbidden` means "I know exactly who you are, and you may not do this": a valid read token on a `PUT`. Keep the two apart; clients and dashboards treat them differently.

**Reading the header.** `Authorization: Bearer abc123` is a scheme, one space, the credentials. `strings.Cut(h, " ")` splits at the first space; `strings.EqualFold(scheme, "Bearer")` compares the scheme case-insensitively, because `bearer` is allowed. Anything else, `Basic ...`, a bare token, or a token in the query string, is not bearer authentication and is a `401`.

**Timing side channels.** Comparing two strings with `==` stops at the first byte that differs. On a fast network an attacker who sends a guess and measures the response time to the microsecond can tell whether the first byte matched, then the second, and recover the token one byte at a time. `crypto/subtle.ConstantTimeCompare(a, b)` compares every byte and returns `1` or `0` in time that depends only on the length. Two more things, both of which break the guarantee if you forget them: it returns `0` *immediately* when the lengths differ, and a `map[string]Principal` keyed by the raw token uses the fast, variable-time comparison. So hash the presented token with SHA-256 first, which gives every comparison the same 32-byte length, and compare it with *every* stored hash, accumulating the result with `|` and deciding only after the loop:

```go
h := sha256.Sum256([]byte(presented)) // [32]byte, a comparable array
match := 0
for i, stored := range hashes {
	eq := subtle.ConstantTimeCompare(h[:], stored[:])
	if eq == 1 {
		found = i
	}
	match |= eq
}
// only now: if match == 1 ...
```

It costs a few microseconds for a few hundred tokens. The shape generalises to API keys, webhook signatures (`hmac.Equal` is the same function) and password-reset codes. Plain SHA-256 is fine here because tokens are long random strings; passwords need a slow hash (`argon2`, `bcrypt`), which is outside the standard library and this curriculum.

**Storing hashes, not tokens.** `TokenSet` keeps the `[32]byte` digest of each token and the principal. A dump of the struct, a debug log, or a core file then contains nothing a thief can use. The test for this is literally `fmt.Sprintf("%+v", ts)` not containing any token.

**Roles with `iota`.** `type Role int` with constants `RoleRead Role = iota + 1` and `RoleWrite`. The `+ 1` is deliberate: the zero value of `Role` means "no role", so a `Principal{}` you forgot to fill in can never pass a check. Which methods count as reads is a rule, not a lookup: `GET` and `HEAD` are reads, everything else is a write, including `POST /v1/batch` even when every op in it is a `get`.

**A principal in the context.** Same pattern as the request ID: `context.WithValue` under an unexported key type, `PrincipalFrom(ctx)` with a comma-ok assertion to read it back. `AccessLog` calls `PrincipalFrom(r.Context())` after the handler returns. The log line gets `principal: <name>` and never the token.

**Environment variables as flag defaults.** `flag.StringVar(&token, "token", os.Getenv("BURROW_TOKEN"), "...")` means the flag wins when given and the environment fills in when it isn't. That is how every CLI tool handles secrets you don't want in shell history.

**Reading the token file.** `bufio.Scanner` over the file, one line at a time, with a line counter. Skip blank lines and `#` comments; `strings.Fields` splits the rest; anything other than three fields, an unknown role, a duplicate name, or a token under 16 bytes is an error that names the line number.

## What to do

1. Create `api/auth.go` with `Role`, `Principal`, `TokenSet`, `ParseTokens` and `Lookup` as in the Reference. Store `[32]byte` digests; `Lookup` hashes once and compares against every entry in constant time.
2. Write `Auth(o AuthOptions) Middleware`:
   - No header, wrong scheme, no token, or a token `Lookup` doesn't know: set `WWW-Authenticate: Bearer realm="burrow"` on the response, then `401` with code `unauthorized`.
   - A token presented on a connection where `r.TLS == nil`, without `AllowInsecure`: `401` whose message mentions TLS.
   - A read principal on anything but `GET` or `HEAD`: `403` with code `forbidden`.
   - Otherwise put the principal in the context and call `next`.
3. Write `PrincipalFrom(ctx)` and add `principal` to the access log line when one is present.
4. In `Server.Handler()`, append `Auth` inside `AccessLog` when tokens were configured. With no tokens the API is open, as before, and every earlier suite still passes.
5. In `burrowd serve`, add `-auth-tokens <file>` and `-auth-insecure`. `-auth-tokens` without `-tls-cert` exits 2 naming both flags, unless `-auth-insecure`. The admin port stays open: `/healthz` and `/readyz` need no token.
6. In `burrow.Options`, add `Token`; send it on every request. Make sure `401` and `403` are never retried and surface as `*APIError`.
7. In `burrowctl`, add `-token`, defaulting to `BURROW_TOKEN`.

## Example

A token file and the server:

```bash
$ cat tokens.txt
# name  role   token
ops     write  0f3c9a7e2b1d4c8f6a5e9b0d2c4f7a1e
reader  read   7b2e4d9c1a6f8e0b3d5c7a9f2e4b6d8c
$ go run ./cmd/burrowd serve -http 127.0.0.1:8443 \
    -tls-cert certs/server.pem -tls-key certs/server-key.pem \
    -auth-tokens tokens.txt &
```

The three answers:

```bash
$ curl --cacert certs/ca.pem -si https://127.0.0.1:8443/v1/keys |
    head -2
HTTP/2 401
www-authenticate: Bearer realm="burrow"
$ R='Authorization: Bearer 7b2e4d9c1a6f8e0b3d5c7a9f2e4b6d8c'
$ curl --cacert certs/ca.pem -s -H "$R" https://127.0.0.1:8443/v1/keys
greeting
$ curl --cacert certs/ca.pem -s -H "$R" -X PUT -d x \
    https://127.0.0.1:8443/v1/kv/greeting
{"error":"read-only token","code":"forbidden"}
```

The access log for the first two (the token appears nowhere):

```json
{"level":"INFO","msg":"request","method":"GET","path":"/v1/keys",
 "status":401,"bytes":52,"duration":91000}
{"level":"INFO","msg":"request","method":"GET","path":"/v1/keys",
 "status":200,"bytes":9,"duration":143000,"principal":"reader"}
```

From Go, the read token on a write:

```go
opts := burrow.Options{RootCAs: ca.Pool(), Token: readTok}
c, _ := burrow.New(url, opts)
_, err := c.Set(ctx, "k", []byte("v"))
var ae *burrow.APIError
errors.As(err, &ae) // true; ae.Status == 403, ae.Code == "forbidden"
                    // and the server saw exactly one request: no retry
```

## Done when

- [ ] `ParseTokens` accepts comments, blank lines and both roles, and rejects a duplicate name, an unknown role, a short token and a line with the wrong number of fields, naming the line
- [ ] `Lookup` finds the right principal; a token with its last byte changed, the empty string and a token of another length all miss; printing the `TokenSet` shows no token
- [ ] No header, `Basic xyz`, `Bearer` with nothing after it, an unknown token, and `?token=` in the query are all `401` with `WWW-Authenticate: Bearer realm="burrow"` and code `unauthorized`
- [ ] A read token gets `200` on `GET` and `HEAD`, `403 forbidden` on `PUT`, `DELETE` and `POST /v1/batch`; a write token does all of them
- [ ] Over plaintext a valid token is `401` mentioning TLS, unless `AllowInsecure`
- [ ] The access log names the principal on a successful request, has none on a `401`, and never contains the token
- [ ] `burrowd -auth-tokens` without `-tls-cert` exits 2 naming both flags; with `-auth-insecure` it starts; `/healthz` on the admin port needs no token
- [ ] `burrow.Client` with `Token` round-trips; a read token's `Set` is one `403` with no retry; `burrowctl -token` and `BURROW_TOKEN` both work

## Reference

The exact contract the tests will hold you to.

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

// ParseTokens reads one `name role token` per line (role is read or
// write; # starts a comment; blank lines are skipped). Names are
// unique, tokens are at least 16 bytes.
func ParseTokens(r io.Reader) (*TokenSet, error)

// Lookup hashes token and compares it with every stored hash using
// crypto/subtle.ConstantTimeCompare, visiting all of them before
// answering, so the time taken does not depend on which token (if any)
// matched.
func (ts *TokenSet) Lookup(token string) (Principal, bool)

type AuthOptions struct {
	Tokens        *TokenSet
	AllowInsecure bool // accept tokens on plaintext connections
	                   // (r.TLS == nil); tests and local only
}

// Auth is a Middleware. Missing, malformed or unknown credentials: 401
// ErrorResponse{code: unauthorized} with
// `WWW-Authenticate: Bearer realm="burrow"`. A token presented over
// plaintext without AllowInsecure: 401 whose Error mentions TLS. A read
// principal on a non-GET/HEAD request: 403 {code: forbidden}.
// On success the Principal is stored in the request context and the
// access log line (7.3) gains `principal: <name>`. The token itself is
// never logged.
func Auth(o AuthOptions) Middleware
func PrincipalFrom(ctx context.Context) (Principal, bool)

// Server.Handler(): Chain(mux, Recover(), RequestID(), AccessLog(l),
// Auth(o)) when o.Tokens != nil.
// burrowd serve gains -auth-tokens <file> and -auth-insecure.
// -auth-tokens without -tls-cert exits 2 unless -auth-insecure is also
// given.
// burrow.Options gains Token string, sent as
// `Authorization: Bearer <token>` on every request, retries included.
// burrowctl gains -token and reads BURROW_TOKEN when the flag is
// absent.
```

What this level builds:

- `ParseTokens`, `TokenSet.Lookup` with `sha256` + `subtle.ConstantTimeCompare` over every entry
- `Auth` middleware: 401 with `WWW-Authenticate`, 403 for read tokens on writes, principal in context and in the access log
- `-auth-tokens`/`-auth-insecure` in `burrowd serve`; `Options.Token`; `burrowctl -token` / `BURROW_TOKEN`

Decisions the tests rely on:

- `Authorization` must be exactly `Bearer <token>` (scheme case-insensitive, one space, no trailing data). `Basic`, a bare token, and a token in the query string are all 401. A `?token=` query parameter is ignored, never accepted.
- `GET` and `HEAD` are reads; every other method is a write. `POST /v1/batch` is a write even when every op in it is a `get`.
- `Auth` runs inside `AccessLog` (so the log line has the principal) and the admin port (7.6) has no auth: `/healthz` and `/readyz` stay open.
- `ParseTokens` rejects a bad role, a duplicate name, a token shorter than 16 bytes and a line with the wrong number of fields, naming the line number in the error.
- Without `-auth-tokens` (or with `AuthOptions.Tokens == nil`) the API is open, as it was before; every earlier suite still passes unchanged.
- `burrow.Client` never retries a 401 or 403 (they are not transient); both surface as `*APIError` with the status and code.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S.2/files/`, matching the earlier chapters.

Pass when: a request without a token is 401 with WWW-Authenticate, a read token can GET but gets 403 on PUT, a write token does everything, the access log names the principal and never the token, and burrowd refuses to combine tokens with plaintext unless told to.

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
./burrow watch
printf 'ops write %s\nreader read %s\n' \
  "$(openssl rand -hex 24)" "$(openssl rand -hex 24)" > tokens.txt
go run ./cmd/burrowd serve -http 127.0.0.1:8443 \
  -tls-cert certs/server.pem -tls-key certs/server-key.pem \
  -auth-tokens tokens.txt &
curl --cacert certs/ca.pem -i https://127.0.0.1:8443/v1/keys  # 401
# the read token on a write: forbidden
BURROW_TOKEN=$(awk '/reader/{print $3}' tokens.txt) \
  go run ./cmd/burrowctl -http https://127.0.0.1:8443 \
  -cacert certs/ca.pem set a 1
```

## In the wild

Every API-key scheme (Stripe, GitHub PATs) is this middleware; OAuth2 bearer tokens (RFC 6750) use the same header and the same 401/403 split; hmac.Equal and subtle.ConstantTimeCompare are what webhook verification is built on

## New in Go

crypto/subtle.ConstantTimeCompare · crypto/sha256 · timing side channels · roles with iota and a principal in context · WWW-Authenticate and 401 vs 403 · r.TLS as a connection property · environment variables as defaults for flags · bufio.Scanner with line numbers

Standard library: `crypto/subtle`, `crypto/sha256`, `bufio`, `net/http`, `context`, `os`
