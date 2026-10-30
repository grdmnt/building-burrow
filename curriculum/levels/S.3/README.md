# S.3 · Handshake

Mutual TLS between cluster peers: every node presents a certificate and checks the other side's, so a process that can reach the RPC port is not a peer until a CA says so. Then wire the whole chapter into `devcluster -secure`: one CA minted at start, a certificate per node, tokens for clients, and the same three-node cluster answering only over TLS.

## Build

```go
// pki additions
// PeerServerConfig requires and verifies a client certificate against ca, then checks the leaf
// with allow (nil error means accepted). TLS 1.3 only.
func (ca *CA) PeerServerConfig(cert tls.Certificate, allow func(leaf *x509.Certificate) error) *tls.Config
// PeerClientConfig presents cert and verifies the server against ca, expecting serverName.
func (ca *CA) PeerClientConfig(cert tls.Certificate, serverName string) *tls.Config
// AllowNames returns an allow func accepting leaves whose CN is one of names.
func AllowNames(names ...string) func(*x509.Certificate) error

// rpc additions (9.2)
type Server struct {
	Handle Handler
	Logger *slog.Logger
	TLS    *tls.Config // nil: plaintext, exactly as in 9.2; otherwise ln is wrapped with tls.NewListener
}
func DialTLS(ctx context.Context, addr string, cfg *tls.Config) (*Conn, error) // tls.Dialer{Config: cfg}.DialContext

// burrowd serve gains -peer-cert, -peer-key, -peer-ca (all three or none; otherwise exit 2).
// With them, the RPC server requires peer certificates signed by -peer-ca whose CN is one of the
// configured peer IDs, and every outgoing RPC presents -peer-cert and expects the peer's ID as ServerName.

// devcluster gains -secure:
//   mints a CA into its work dir, issues one certificate per node (CN = node id, hosts localhost and
//   127.0.0.1, usages server and client), an API server certificate, and a tokens file with one write
//   token; starts every node with -tls-cert/-tls-key, -auth-tokens, -peer-cert/-peer-key/-peer-ca;
//   prints `ca <path>` and `token <token>` before `cluster ready`, so a shell can drive burrowctl.
```

- `PeerServerConfig`, `PeerClientConfig`, `AllowNames`: `ClientAuth: tls.RequireAndVerifyClientCert`, `ClientCAs`, and `VerifyConnection` (or `VerifyPeerCertificate`) for the allow check
- `rpc.Server.TLS` and `rpc.DialTLS`; the 9.2 code path untouched when `TLS` is nil
- `-peer-*` flags in `burrowd serve`, threaded into Raft's transport
- `devcluster -secure`

Where the check goes: `tls.Config.ClientCAs` plus `RequireAndVerifyClientCert` makes the handshake fail for a client with no certificate or one from another CA, before any of your code runs. Membership (is this CA-signed certificate actually one of my peers?) is yours: `VerifyConnection func(tls.ConnectionState) error` runs after the chain is verified and sees `PeerCertificates[0]`, the leaf; return an error and the handshake fails with it. `VerifyPeerCertificate` is the older hook with raw DER and the verified chains. On the dialing side `ServerName` does the same job for free: Go checks the server's SANs against it, so issuing each node a certificate for its own ID and dialing with `ServerName: peerID` means a node cannot impersonate another even with a valid certificate. `tls.NewListener` turns any `net.Listener` into a TLS one, which is why `rpc.Server` needs one new field and no new accept loop; `tls.Dialer` is the client-side twin of `net.Dialer`.

Decisions the tests rely on:

- Peer certificates carry the node ID as CN and as a DNS SAN, with both `ServerAuth` and `ClientAuth` usages: one certificate per node serves both directions.
- A failed allow check closes the connection during the handshake; the RPC `Handle` function is never called for it, and the server logs the rejection at `Warn` with the presented CN.
- `rpc.Dial` (plaintext) to a TLS-enabled server fails within the dial context's deadline with an error, never a hang; `DialTLS` without a client certificate fails with a handshake error that mentions a certificate.
- `-peer-ca` with a missing `-peer-cert` or `-peer-key` (or any other incomplete combination) exits 2 naming the missing flag. A certificate whose CN is not the node's own `-id` exits 1 at startup: a node refuses to run with someone else's identity.
- `devcluster -secure` writes the CA, certificates, keys (0600) and tokens into its work directory, prints `ca <path>` and `token <token>` lines before `cluster ready`, and removes nothing on exit so the files can be inspected. Without `-secure` nothing changes from 9.1.
- Raft's transport uses `DialTLS` with `PeerClientConfig(cert, peerID)` for each peer; the `Peers` map still maps IDs to addresses.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S.3/files/`, matching the earlier chapters.

## Pass when

Three nodes elect a leader and replicate over mutual TLS, a client with a certificate from another CA or with a CN that is not a peer is refused during the handshake, plaintext dials fail fast, and `devcluster -secure` brings the whole thing up with burrowctl talking to it over https with a token.

The suite will cover:

- pki: a server built from PeerServerConfig accepts a client presenting a leaf from the same CA whose CN is in the allow list and the handler runs; a client with no certificate fails with an error mentioning a certificate; a leaf from a second CA fails; a same-CA leaf with CN `n9` against AllowNames("n1","n2","n3") fails and the handler never ran; PeerClientConfig with ServerName `n2` refuses a server presenting `n3`'s certificate
- rpc over TLS: the 9.2 suite's shape again (1,000 concurrent calls over one connection, server close fails every pending call within 1 s, ctx deadline on a slow handler, no leaks over 100 DialTLS/Close cycles) with Server.TLS set and DialTLS; Server with TLS nil still passes the plaintext suite
- Mixed: plain rpc.Dial against a TLS server fails within 1 s; DialTLS against a plaintext server fails (not hangs)
- burrowd: three real processes with -peer-* flags elect a leader and a value written through the leader's https API with a token reads back from a follower; a fourth process started with a certificate for `n9` cannot join (its RPCs are rejected, the three-node cluster keeps serving); a node started with -peer-cert for a different ID exits 1; an incomplete -peer-* set exits 2
- devcluster -secure: `ca <path>` and `token <token>` lines precede `cluster ready`; burrowctl -http https://127.0.0.1:<port> -cacert <ca> -token <token> set/get succeed; the same burrowctl without -token is 401; a plain-TCP connection to any node's RPC port is dropped; kill n1 and the survivors still answer over TLS; after Ctrl-C no burrowd remains and the certificate files are still on disk

## Try it

```bash
go run ./cmd/devcluster -secure
# in another terminal, with the ca and token lines it printed:
go run ./cmd/burrowctl -http https://127.0.0.1:8081 -cacert <ca> -token <token> set who n1
openssl s_client -connect 127.0.0.1:9001 </dev/null   # peer port: handshake fails, no client cert
```

## Uses

- TLS API (built in S.1 · Shielded Cable)
- Bearer auth (built in S.2 · Keycard)
- RPC (built in 9.2 · Interconnect)
- devcluster (built in 9.1 · Multi-core)

## In the wild

etcd's --peer-client-cert-auth and --peer-trusted-ca-file; Consul and Nomad's auto-encrypt; SPIFFE/SPIRE issue exactly this kind of per-node identity, with the ID in a URI SAN instead of the CN; Istio does it per pod

## New in Go

tls.Config.ClientAuth and ClientCAs · VerifyConnection and tls.ConnectionState · ServerName as identity · tls.NewListener and tls.Dialer · threading configuration through layers · generating and placing secrets at startup

Standard library: `crypto/tls`, `crypto/x509`, `net`, `os`, `path/filepath`
