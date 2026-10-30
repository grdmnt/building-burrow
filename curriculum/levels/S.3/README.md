# S.3 · Handshake

> Two machines meeting in the dark. Each shows a badge, each checks the other's, and the conversation starts only if both are on the list.

## Your goal

So far TLS proves the server to the client. Between cluster nodes that is not enough: a process that can reach the RPC port must not be able to join the cluster just by speaking the protocol. The answer is *mutual TLS* (mTLS): both sides present a certificate, both verify the other's against the same CA, and the server additionally checks that the name in the client's certificate is one of its configured peers. Every node gets a certificate whose name is its node ID, so `n1` cannot pretend to be `n2` even with a valid certificate. Then `devcluster -secure` wires the whole chapter together: one CA minted at start, a certificate per node, an API certificate, a token file, and three nodes that answer only over TLS.

## What you're using from before

**TLS API (S.1 · Shielded Cable).** `pki` does the minting. A peer certificate is one `Issue` call with the node ID as the name and as a host, and *both* extended usages, because one certificate serves as a server certificate when the node is dialled and as a client certificate when it dials:

```go
both := []x509.ExtKeyUsage{
	x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth,
}
hosts := []string{"n1", "localhost", "127.0.0.1"}
cert, err := ca.Issue("n1", hosts, both, 24*time.Hour)
```

`ca.Pool()` becomes `ClientCAs` on the server side and `RootCAs` on the client side. The API port keeps `-tls-cert`/`-tls-key` from S.1, which `devcluster -secure` now sets for every node.

**Bearer auth (S.2 · Keycard).** `devcluster -secure` writes a tokens file with one write token and starts every node with `-auth-tokens`, then prints `token <token>` so a shell can hand it to `burrowctl -token`.

**RPC (9.2 · Interconnect).** `rpc.Server` gains one field and `Dial` gets a TLS twin. When `TLS` is nil nothing changes; when it is set, the listener is wrapped before the accept loop and every accepted connection is already a TLS connection:

```go
func (s *Server) Serve(ln net.Listener) error {
	if s.TLS != nil {
		ln = tls.NewListener(ln, s.TLS)
	}
	return wire.ServeConns(ln, s.serveConn, s.Logger)
}

func DialTLS(ctx context.Context, addr string,
	cfg *tls.Config) (*Conn, error) {
	d := &tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", addr)
	// ...the rest is Dial from 9.2, with conn in place of the plain one
}
```

**devcluster (9.1 · Multi-core).** The `Node` struct and the argv it builds gain the certificate paths and flags. `-secure` adds a minting step before the nodes start and two extra lines of output before `cluster ready`.

## What's new here

**Requiring a client certificate.** On the server side, `tls.Config.ClientAuth: tls.RequireAndVerifyClientCert` with `ClientCAs: ca.Pool()` makes the handshake fail for a client that presents no certificate or one signed by another CA, before a single byte of your protocol is read. That is the whole "from the right CA" check, and none of your code runs for it.

**Membership in the handshake.** "Signed by our CA" is necessary but not enough: a decommissioned node still has a valid certificate. The membership check is yours, and it runs *inside* the handshake through `VerifyConnection func(tls.ConnectionState) error`. By the time it is called the chain has already been verified; `cs.PeerCertificates[0]` is the client's leaf, and returning an error fails the handshake with it:

```go
cfg.VerifyConnection = func(cs tls.ConnectionState) error {
	return allow(cs.PeerCertificates[0]) // nil: accepted
}
```

`VerifyPeerCertificate` is the older hook, handed raw DER and the verified chains; it does the same job with more unpacking. `AllowNames("n1", "n2", "n3")` builds the `allow` function from the CN.

**`ServerName` as identity.** On the dialling side the same job is done for free. `tls.Config.ServerName` is the name the client expects, and Go checks the server's SANs against it during the handshake. Dial `n2` with `ServerName: "n2"` and a server presenting `n3`'s certificate is refused, even though `n3`'s certificate is perfectly valid. Issue each node a certificate for its own ID, dial with the peer's ID, and no node can impersonate another.

**`tls.NewListener` and `tls.Dialer`.** `tls.NewListener(ln, cfg)` wraps any `net.Listener` so every `Accept` returns a TLS connection; that is why `rpc.Server` needs one new field and no new accept loop. `tls.Dialer{Config: cfg}` is the client-side twin of `net.Dialer`, and its `DialContext` completes the handshake before returning, so a refused certificate comes back as the dial error rather than on the first read.

**Mixing plain and TLS fails fast.** A plaintext `rpc.Dial` against a TLS server sends a record the server cannot parse as a `ClientHello`; the server closes and the client's first read fails, within the dial context's deadline. `DialTLS` against a plaintext server fails in the handshake. Neither hangs, and the tests check both directions.

**Threading configuration through layers.** The `tls.Config`s are built once in `main` from `-peer-cert`, `-peer-key` and `-peer-ca`, handed to `rpc.Server.TLS`, and handed to Raft's transport as a function from peer ID to config, since each outgoing dial needs its own `ServerName`. The `Peers` map still maps IDs to addresses; nothing below `main` reads a flag.

**Generating and placing secrets at startup.** `devcluster -secure` is what a bootstrap script does in production: mint, write with the right modes (keys 0600), pass paths to the processes, and leave the files on disk when done so they can be inspected. Printing `ca <path>` and `token <token>` before `cluster ready` is the contract that lets a shell or a test script find them.

## What to do

1. In `pki`, add `PeerServerConfig` (the certificate, `RequireAndVerifyClientCert`, `ClientCAs`, TLS 1.3, `VerifyConnection` calling `allow` on the leaf), `PeerClientConfig` (the certificate, `RootCAs`, `ServerName`, TLS 1.3) and `AllowNames`.
2. In `rpc`, add `Server.TLS` and the listener wrap, and `DialTLS`. The plaintext path must be byte-for-byte what 9.2 does.
3. Log a rejected peer at `Warn` with the presented CN, from the `allow` function or around the accept. The `Handle` function must never run for a rejected connection.
4. In `burrowd serve`, add `-peer-cert`, `-peer-key`, `-peer-ca`: all three or none, otherwise exit 2 naming the missing flag. Load them, check the certificate's CN equals `-id` (exit 1 if not), build the server config with `AllowNames` over the configured peer IDs, and give Raft's transport a per-peer client config.
5. In `devcluster`, add `-secure`: `pki.NewCA`, one `Issue` per node with hosts `{id, "localhost", "127.0.0.1"}` and both usages, one API server certificate, `WriteFiles` into the work directory, the CA PEM alongside, a tokens file with one write token. Append the `-tls-*`, `-auth-tokens` and `-peer-*` flags to each node's argv. Print `ca <path>` and `token <token>` before `cluster ready`. Delete nothing on exit.
6. Check that without `-secure` nothing changed from 9.1.

## Example

```bash
$ go run ./cmd/devcluster -secure
ca /tmp/devcluster-2931/ca.pem
token 4f1e9c7b2a6d8e0f3b5c7d9a1e2f4b6c8d0a2c4e
n1 | listening on 127.0.0.1:7101
n1 | https on 127.0.0.1:7201
n2 | listening on 127.0.0.1:7102
...
cluster ready
```

In another terminal, with those two lines:

```bash
$ go run ./cmd/burrowctl -http https://127.0.0.1:7201 \
    -cacert /tmp/devcluster-2931/ca.pem -token 4f1e9c...4e set who n1
OK 1
$ go run ./cmd/burrowctl -http https://127.0.0.1:7202 \
    -cacert /tmp/devcluster-2931/ca.pem get who          # no token
error: unauthorized (401)
$ openssl s_client -connect 127.0.0.1:7001 </dev/null 2>&1 | tail -2
ssl3_read_bytes:tlsv13 alert certificate required
```

From Go, a peer with the wrong name is refused during the handshake and the handler never runs:

```go
srv := &rpc.Server{Handle: h,
	TLS: ca.PeerServerConfig(n1Cert, pki.AllowNames("n1", "n2", "n3"))}
go srv.Serve(ln)

ok, _ := rpc.DialTLS(ctx, addr, ca.PeerClientConfig(n2Cert, "n1"))
ok.Call(ctx, 1, nil)                                  // served
_, err := rpc.DialTLS(ctx, addr, ca.PeerClientConfig(n9Cert, "n1"))
fmt.Println(err)   // handshake failed: n9 is not a peer; h never ran
_, err = rpc.Dial(ctx, addr)                          // plaintext
fmt.Println(err)   // fails within ctx's deadline, never hangs
```

## Done when

- [ ] A server from `PeerServerConfig` serves a client whose leaf is from the same CA and whose CN is allowed; no certificate, another CA's certificate, or CN `n9` against `AllowNames("n1","n2","n3")` all fail in the handshake and the handler never runs
- [ ] `PeerClientConfig(cert, "n2")` refuses a server presenting `n3`'s certificate
- [ ] The 9.2 suite's behaviour (1,000 concurrent calls, fail-all on close, ctx deadlines, no leaks) holds with `Server.TLS` set and `DialTLS`; with `TLS` nil the plaintext suite still passes
- [ ] Plain `rpc.Dial` against a TLS server and `DialTLS` against a plaintext server both fail within 1 s
- [ ] Three real `burrowd` processes with `-peer-*` flags elect a leader and a value written through the leader's https API with a token reads back from a follower; a fourth process with a certificate for `n9` cannot join; a node whose certificate CN is not its `-id` exits 1; an incomplete `-peer-*` set exits 2
- [ ] `devcluster -secure` prints `ca` and `token` before `cluster ready`; `burrowctl -cacert -token` works and without `-token` is 401; a plain TCP connection to an RPC port is dropped; killing `n1` leaves the survivors answering over TLS; after Ctrl-C no `burrowd` remains and the certificate files are still there

## Reference

The exact contract the tests will hold you to.

```go
// pki additions
// PeerServerConfig requires and verifies a client certificate against
// ca, then checks the leaf with allow (nil error means accepted).
// TLS 1.3 only.
func (ca *CA) PeerServerConfig(cert tls.Certificate,
	allow func(leaf *x509.Certificate) error) *tls.Config
// PeerClientConfig presents cert and verifies the server against ca,
// expecting serverName.
func (ca *CA) PeerClientConfig(cert tls.Certificate,
	serverName string) *tls.Config
// AllowNames returns an allow func accepting leaves whose CN is one of
// names.
func AllowNames(names ...string) func(*x509.Certificate) error

// rpc additions (9.2)
type Server struct {
	Handle Handler
	Logger *slog.Logger
	TLS    *tls.Config // nil: plaintext, exactly as in 9.2; otherwise
	                   // ln is wrapped with tls.NewListener
}
// DialTLS dials with tls.Dialer{Config: cfg}.DialContext.
func DialTLS(ctx context.Context, addr string,
	cfg *tls.Config) (*Conn, error)

// burrowd serve gains -peer-cert, -peer-key, -peer-ca (all three or
// none; otherwise exit 2). With them, the RPC server requires peer
// certificates signed by -peer-ca whose CN is one of the configured
// peer IDs, and every outgoing RPC presents -peer-cert and expects the
// peer's ID as ServerName.

// devcluster gains -secure:
//   mints a CA into its work dir, issues one certificate per node
//   (CN = node id, hosts localhost and 127.0.0.1, usages server and
//   client), an API server certificate, and a tokens file with one
//   write token; starts every node with -tls-cert/-tls-key,
//   -auth-tokens, -peer-cert/-peer-key/-peer-ca; prints `ca <path>` and
//   `token <token>` before `cluster ready`, so a shell can drive
//   burrowctl.
```

What this level builds:

- `PeerServerConfig`, `PeerClientConfig`, `AllowNames`: `ClientAuth: tls.RequireAndVerifyClientCert`, `ClientCAs`, and `VerifyConnection` (or `VerifyPeerCertificate`) for the allow check
- `rpc.Server.TLS` and `rpc.DialTLS`; the 9.2 code path untouched when `TLS` is nil
- `-peer-*` flags in `burrowd serve`, threaded into Raft's transport
- `devcluster -secure`

Decisions the tests rely on:

- Peer certificates carry the node ID as CN and as a DNS SAN, with both `ServerAuth` and `ClientAuth` usages: one certificate per node serves both directions.
- A failed allow check closes the connection during the handshake; the RPC `Handle` function is never called for it, and the server logs the rejection at `Warn` with the presented CN.
- `rpc.Dial` (plaintext) to a TLS-enabled server fails within the dial context's deadline with an error, never a hang; `DialTLS` without a client certificate fails with a handshake error that mentions a certificate.
- `-peer-ca` with a missing `-peer-cert` or `-peer-key` (or any other incomplete combination) exits 2 naming the missing flag. A certificate whose CN is not the node's own `-id` exits 1 at startup: a node refuses to run with someone else's identity.
- `devcluster -secure` writes the CA, certificates, keys (0600) and tokens into its work directory, prints `ca <path>` and `token <token>` lines before `cluster ready`, and removes nothing on exit so the files can be inspected. Without `-secure` nothing changes from 9.1.
- Raft's transport uses `DialTLS` with `PeerClientConfig(cert, peerID)` for each peer; the `Peers` map still maps IDs to addresses.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S.3/files/`, matching the earlier chapters.

Pass when: three nodes elect a leader and replicate over mutual TLS, a client with a certificate from another CA or with a CN that is not a peer is refused during the handshake, plaintext dials fail fast, and `devcluster -secure` brings the whole thing up with burrowctl talking to it over https with a token.

The suite will cover:

- pki: a server built from PeerServerConfig accepts a client presenting a leaf from the same CA whose CN is in the allow list and the handler runs; a client with no certificate fails with an error mentioning a certificate; a leaf from a second CA fails; a same-CA leaf with CN `n9` against AllowNames("n1","n2","n3") fails and the handler never ran; PeerClientConfig with ServerName `n2` refuses a server presenting `n3`'s certificate
- rpc over TLS: the 9.2 suite's shape again (1,000 concurrent calls over one connection, server close fails every pending call within 1 s, ctx deadline on a slow handler, no leaks over 100 DialTLS/Close cycles) with Server.TLS set and DialTLS; Server with TLS nil still passes the plaintext suite
- Mixed: plain rpc.Dial against a TLS server fails within 1 s; DialTLS against a plaintext server fails (not hangs)
- burrowd: three real processes with -peer-* flags elect a leader and a value written through the leader's https API with a token reads back from a follower; a fourth process started with a certificate for `n9` cannot join (its RPCs are rejected, the three-node cluster keeps serving); a node started with -peer-cert for a different ID exits 1; an incomplete -peer-* set exits 2
- devcluster -secure: `ca <path>` and `token <token>` lines precede `cluster ready`; burrowctl -http https://127.0.0.1:<port> -cacert <ca> -token <token> set/get succeed; the same burrowctl without -token is 401; a plain-TCP connection to any node's RPC port is dropped; kill n1 and the survivors still answer over TLS; after Ctrl-C no burrowd remains and the certificate files are still on disk

## Try it

```bash
./burrow watch
go run ./cmd/devcluster -secure
# in another terminal, with the ca and token lines it printed:
go run ./cmd/burrowctl -http https://127.0.0.1:7201 \
  -cacert <ca> -token <token> set who n1
# n1's RPC port, no client certificate: the handshake fails
openssl s_client -connect 127.0.0.1:7001 </dev/null
```

## In the wild

etcd's --peer-client-cert-auth and --peer-trusted-ca-file; Consul and Nomad's auto-encrypt; SPIFFE/SPIRE issue exactly this kind of per-node identity, with the ID in a URI SAN instead of the CN; Istio does it per pod

## New in Go

tls.Config.ClientAuth and ClientCAs · VerifyConnection and tls.ConnectionState · ServerName as identity · tls.NewListener and tls.Dialer · threading configuration through layers · generating and placing secrets at startup

Standard library: `crypto/tls`, `crypto/x509`, `net`, `os`, `path/filepath`
