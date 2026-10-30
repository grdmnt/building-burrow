# S.1 · Shielded Cable

> Same signal, same pins, but now nobody can listen in or splice the wire.

## Your goal

Put the HTTP API behind TLS, the encryption layer under every `https://` URL. TLS needs a *certificate*: a public key tied to a name (`localhost`, `127.0.0.1`), signed by a *certificate authority* (CA) that the client trusts. Instead of installing openssl and copying commands, you write a small `pki` package (*public key infrastructure*, the usual name for "the code that makes and checks certificates") that mints a CA and signs server certificates in Go. Then `burrowd serve` gets `-tls-cert` and `-tls-key`, the client library learns to trust your CA, and HTTP/2 arrives for free, because TLS has a step where client and server agree on the protocol to speak next.

## What you're using from before

**HTTP client lib (7.5 · Client Library).** `burrow.Options` gains `RootCAs`, and the `http.Transport` you build in `burrow.New` gets two more fields. `RootCAs: nil` keeps the system's trust store, so public `https://` hosts keep working:

```go
tr := &http.Transport{
	// ...the timeouts and keep-alive settings from 7.5...
	TLSClientConfig: &tls.Config{
		RootCAs:    o.RootCAs,
		MinVersion: tls.VersionTLS13,
	},
	ForceAttemptHTTP2: true,
}
```

**Deadline guard (7.4 · Watchdog).** The `http.Server` with its four timeouts is unchanged; it just starts differently. With the flags set, `burrowd` loads the pair, hands the server a `TLSConfig`, and calls `ServeTLS` instead of `Serve`:

```go
cert, err := tls.LoadX509KeyPair(certFile, keyFile)
srv.TLSConfig = pki.ServerConfig(cert)
err = srv.ServeTLS(ln, "", "") // files already loaded into TLSConfig
```

## What's new here

**Certificates in one paragraph.** A key pair is a private key (kept secret) and a public key (given out). A certificate is a public key plus the names it is valid for plus a validity window, all *signed* by some other private key. The signer is the CA; its own certificate is signed by itself (*self-signed*). A client that holds the CA's certificate can check any certificate the CA signed: the signature verifies, the name matches the host it dialled, the dates are fine. A certificate signed by a CA is called a *leaf*. That is the whole trust model; everything below is the Go API for it.

**`crypto/x509` templates.** You describe a certificate by filling in an `x509.Certificate` struct and calling `x509.CreateCertificate(rand.Reader, template, parent, publicKey, parentPrivateKey)`. For the CA, `parent` is the template itself. The result is DER, the binary encoding, which `x509.ParseCertificate` turns back into a `*Certificate`. The lesson is which fields matter:

```go
tmpl := &x509.Certificate{
	SerialNumber: serial, // random 128-bit *big.Int, not a counter
	Subject:      pkix.Name{CommonName: name},
	NotBefore:    now, NotAfter: now.Add(validFor),
	// CA only:
	IsCA: true, BasicConstraintsValid: true,
	KeyUsage: x509.KeyUsageCertSign,
	// leaf only:
	// KeyUsage: x509.KeyUsageDigitalSignature,
	// ExtKeyUsage: usage, DNSNames: ..., IPAddresses: ...,
}
```

Since Go 1.15 a certificate whose only name is the `CommonName` is rejected: names must be *SANs* (subject alternative names), in `DNSNames` or `IPAddresses`. `net.ParseIP(host) != nil` decides which list a host goes in. Nothing is trusted on the CN alone.

**`crypto/ecdsa` and `elliptic.P256`.** `ecdsa.GenerateKey(elliptic.P256(), rand.Reader)` makes a key pair in microseconds. ECDSA P-256 with SHA-256 is what browsers and Go pick by default; RSA keys are bigger and slower and there is no reason to use them here.

**`encoding/pem`.** PEM is the text wrapper you have seen in files: `-----BEGIN CERTIFICATE-----`, base64, `-----END-----`. `pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: der})` writes one; `pem.Decode` reads one back. The private key goes in a block of type `PRIVATE KEY` holding `x509.MarshalPKCS8PrivateKey(key)`, the modern encoding. You will also meet `EC PRIVATE KEY` (`MarshalECPrivateKey`) in older files.

**`tls.Certificate`.** The in-memory form Go's TLS code wants: the DER chain plus the private key, `tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: key}`. `tls.X509KeyPair(certPEM, keyPEM)` and `tls.LoadX509KeyPair(certFile, keyFile)` rebuild it from PEM, which is how `burrowd` reads the files `WriteFiles` wrote.

**`tls.Config` on both sides.** The server side needs its certificate and a `MinVersion` (`tls.VersionTLS13`; nothing older is worth supporting on a new system). The client side needs `RootCAs`, an `*x509.CertPool` of CA certificates to trust; `x509.NewCertPool()` and `pool.AddCert(ca.Cert)` build one. `NextProtos` is the ALPN list: during the handshake the client offers protocol names and the server picks one. `http.Server.ServeTLS` adds `h2` and `http/1.1` to the server's list itself, so leave `NextProtos` alone in `ServerConfig` and HTTP/2 is negotiated with no further work. Check with `resp.Proto`: `HTTP/2.0`.

**The client-side trap.** `http.DefaultTransport` speaks HTTP/2. A `Transport` you build yourself with a `TLSClientConfig` does *not*, unless `ForceAttemptHTTP2` is true. Your 7.5 transport is one you built yourself.

**Verifying by hand.** `leaf.Verify(x509.VerifyOptions{Roots: ca.Pool(), DNSName: "localhost"})` does what a client does during the handshake, with the error types to match. `errors.As` picks them out even through the `*url.Error` that `http.Client` wraps everything in:

```go
var unknown x509.UnknownAuthorityError // signed by a CA we don't trust
var host x509.HostnameError            // valid, but not for this name
var invalid x509.CertificateInvalidError
if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
	// the certificate is outside its validity window
}
```

**File modes.** `os.WriteFile(path, data, 0o600)` creates the file readable and writable by the owner only, which is the only acceptable mode for a private key. The certificate is public and gets `0o644`.

## What to do

1. Create `pki/pki.go` with the `CA` struct and `NewCA`: generate a P-256 key, fill a CA template (IsCA, BasicConstraintsValid, KeyUsageCertSign, random serial), self-sign it, parse the DER back into `Cert`.
2. Write `Issue`: a fresh key, a leaf template with `KeyUsageDigitalSignature`, the given `ExtKeyUsage`, the CN, hosts split into `DNSNames` and `IPAddresses`, `NotAfter` clamped to the CA's; sign with the CA; return a `tls.Certificate`.
3. Write `Pool`, `CertPEM`, `WriteFiles` (leaf as `CERTIFICATE`, key as PKCS #8 `PRIVATE KEY`, 0600) and `ServerConfig` (TLS 1.3 minimum, the one certificate, `NextProtos` untouched).
4. In `burrowd serve`, add `-tls-cert` and `-tls-key`. One without the other is a usage error (exit 2, naming the missing flag). A pair that won't load exits 1. With both, `ServeTLS` and print `https on <addr>` instead of `http on <addr>`. The frame port stays plaintext.
5. In `burrow.Options`, add `RootCAs` and build the Transport as shown above. In `burrowctl`, add `-cacert ca.pem`, read into a pool, for `-http https://...` URLs.
6. Write your own test under `pki/`: issue a leaf from a fresh CA and `Verify` it against `ca.Pool()` for `localhost`, then check it fails for `example.com`.

## Example

Minting a CA and a server certificate, as a test or a throwaway `main`:

```go
ca, _ := pki.NewCA("burrow dev CA", 24*time.Hour)
cert, _ := ca.Issue("server", []string{"localhost", "127.0.0.1"},
	[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, 24*time.Hour)
// writes certs/server.pem and certs/server-key.pem
pki.WriteFiles("certs", "server", cert)
os.WriteFile("certs/ca.pem", ca.CertPEM(), 0o644)
```

Then the daemon and curl:

```bash
$ go run ./cmd/burrowd serve -http 127.0.0.1:8443 \
    -tls-cert certs/server.pem -tls-key certs/server-key.pem &
listening on 127.0.0.1:7070
https on 127.0.0.1:8443
$ curl --cacert certs/ca.pem -i https://127.0.0.1:8443/v1/keys
HTTP/2 200
content-type: text/plain; charset=utf-8
...
$ curl -i https://127.0.0.1:8443/v1/keys
curl: (60) SSL certificate problem: unable to get local issuer
certificate
$ go run ./cmd/burrowd serve -tls-cert certs/server.pem; echo "exit: $?"
burrowd: -tls-cert requires -tls-key
exit: 2
```

And from Go, with the wrong CA:

```go
url := "https://127.0.0.1:8443"
c, _ := burrow.New(url, burrow.Options{RootCAs: ca.Pool()})
c.Set(ctx, "k", []byte("v"))                     // works, over HTTP/2

other, _ := pki.NewCA("someone else", time.Hour)
c2, _ := burrow.New(url, burrow.Options{RootCAs: other.Pool()})
_, err := c2.Get(ctx, "k")
var ua x509.UnknownAuthorityError
errors.As(err, &ua)                              // true
```

## Done when

- [ ] A leaf issued for `localhost` and `127.0.0.1` verifies against `ca.Pool()` for both names, fails for `example.com` with `HostnameError`, and a leaf from another CA fails with `UnknownAuthorityError`
- [ ] The CA certificate has `IsCA` and `KeyUsageCertSign`; a leaf has `DigitalSignature` and exactly the extended usages asked for; an expired leaf fails with `CertificateInvalidError` and reason `Expired`
- [ ] `WriteFiles` then `tls.LoadX509KeyPair` gives back the same DER; the key file is mode 0600; the PEM types are `CERTIFICATE` and `PRIVATE KEY`
- [ ] `burrowd serve -tls-cert -tls-key` prints `https on <addr>` as its second line and serves the API over TLS; the frame port is still plaintext
- [ ] A `burrow.Client` with `RootCAs: ca.Pool()` round-trips a value and speaks `HTTP/2.0`; one with another CA's pool gets `x509.UnknownAuthorityError` through `errors.As`; a plain `http://` request to the TLS port fails, never hangs; a client limited to TLS 1.2 fails the handshake
- [ ] `-tls-cert` without `-tls-key` exits 2 naming `-tls-key`; a missing certificate file exits 1
- [ ] `burrowctl -http https://... -cacert ca.pem` works; without `-cacert` it fails mentioning the certificate, exit 1
- [ ] Your own `pki` test exists and passes

## Reference

The exact contract the tests will hold you to.

```go
// pki/pki.go
package pki

// CA is a self-signed ECDSA P-256 certificate and its key, held in
// memory.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

func NewCA(name string, validFor time.Duration) (*CA, error)

// Issue signs a leaf certificate for hosts (DNS names and IP addresses,
// each going into the right SAN list; the CN is name) with the given
// extended key usage, valid for validFor.
func (ca *CA) Issue(name string, hosts []string,
	usage []x509.ExtKeyUsage,
	validFor time.Duration) (tls.Certificate, error)

// a pool holding only this CA, for RootCAs / ClientCAs
func (ca *CA) Pool() *x509.CertPool
// the CA certificate as PEM, for -cacert files
func (ca *CA) CertPEM() []byte

// WriteFiles writes <dir>/<name>.pem (the leaf, PEM "CERTIFICATE") and
// <dir>/<name>-key.pem (PKCS #8, PEM "PRIVATE KEY", mode 0600) and
// returns both paths.
func WriteFiles(dir, name string, cert tls.Certificate) (certPath,
	keyPath string, err error)

// ServerConfig is TLS 1.3 only, with cert as the server certificate.
func ServerConfig(cert tls.Certificate) *tls.Config

// cmd/burrowd
//   burrowd serve ... [-http addr]
//                     [-tls-cert cert.pem -tls-key key.pem]
//   With both flags the HTTP listener is served with
//   http.Server.ServeTLS and the startup line is `https on <addr>`
//   instead of `http on <addr>`. One flag without the other is a usage
//   error (exit 2). The frame port stays plaintext in this chapter.

// burrow (client library, 7.5) Options gains:
//   RootCAs *x509.CertPool // nil: the system roots
//   The Transport sets TLSClientConfig{RootCAs, MinVersion: TLS 1.3}
//   and ForceAttemptHTTP2: true.
// cmd/burrowctl gains -cacert ca.pem for -http https://... URLs.
```

What this level builds:

- `pki.NewCA`, `Issue`, `Pool`, `CertPEM`, `WriteFiles`, `ServerConfig`: a CA and leaves from `crypto/x509`, `crypto/ecdsa`, `crypto/rand`, `encoding/pem`
- `burrowd serve -tls-cert -tls-key` → `http.Server.ServeTLS`, `https on <addr>`
- `burrow.Options.RootCAs` and `burrowctl -cacert`; HTTP/2 negotiated, which the tests check through `resp.Proto`
- Your own test that issues a cert from a fresh CA and verifies it with `x509.Certificate.Verify`

Decisions the tests rely on:

- Keys are ECDSA P-256; signatures are ECDSA with SHA-256. Leaf validity is clamped to the CA's `NotAfter`.
- `Issue` puts each host into `DNSNames` or `IPAddresses` by whether `net.ParseIP` accepts it; the CN is `name` and nothing is trusted on the CN alone.
- `WriteFiles` writes the key file with mode 0600 and the certificate with 0644. The key is PKCS #8 (`PRIVATE KEY`).
- `ServerConfig` sets `MinVersion: tls.VersionTLS13` and leaves `NextProtos` alone, so `ServeTLS` adds `h2` and `http/1.1` itself.
- `-tls-cert` without `-tls-key`, or the reverse, is a usage error: exit 2, message on stderr naming the missing flag. A file that fails to load exits 1.
- With TLS on, `burrowd` prints `https on <addr>` as its second line; without, `http on <addr>` as before.
- `Options.RootCAs == nil` means the system roots, so an `https://` base URL to a public host still works. Every TLS error from the client surfaces as the `*url.Error` from `http.Client`, so `errors.As` with `x509.UnknownAuthorityError` and `x509.HostnameError` works on it.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S.1/files/`, matching the earlier chapters.

Pass when: burrowd serves the API over https with a certificate minted by pki, curl needs --cacert and gets HTTP/2, a client without the CA is refused with an x509 error, and the frame port behaves exactly as before.

The suite will cover:

- pki: a leaf issued for `localhost` and `127.0.0.1` passes `x509.Certificate.Verify` against `ca.Pool()` for both names and fails for `example.com` (HostnameError); a leaf from a second CA fails (UnknownAuthorityError); a leaf issued with a 1 ms validity fails after it expires (CertificateInvalidError, Expired); the CA certificate has IsCA and KeyUsageCertSign, the leaf has DigitalSignature and exactly the ExtKeyUsages asked for
- WriteFiles then tls.LoadX509KeyPair gives back a certificate with the same DER; the key file is 0600; the PEM block types are `CERTIFICATE` and `PRIVATE KEY`
- burrowd started with -tls-cert/-tls-key on a t.TempDir: stdout's second line is `https on <addr>`; a burrow.Client with RootCAs: ca.Pool() round-trips a value; a raw http.Client with the same pool sees resp.Proto `HTTP/2.0`; a client with a different CA's pool gets x509.UnknownAuthorityError through errors.As; a plain `http://` GET to the TLS port fails with a `malformed HTTP response` or connection error, never a hang; a client limited to MaxVersion TLS 1.2 gets a handshake failure
- The client library negotiates HTTP/2 by itself: against an httptest.NewUnstartedServer with EnableHTTP2 and StartTLS, a burrow.Client built with Options{RootCAs} and no HTTPClient reaches a handler that records r.Proto as `HTTP/2.0`
- -tls-cert without -tls-key exits 2 and the stderr names `-tls-key`; a missing certificate file exits 1
- The frame port of the same process is still plaintext: client.Dial and a burrowctl set/get work unchanged
- burrowctl -http https://127.0.0.1:<port> -cacert <ca.pem> set/get work; without -cacert the error mentions the certificate and exits 1
- Your own pki test exists under `pki/` (name not starting with `TestL`) and passes

## Try it

```bash
./burrow watch
go run ./cmd/burrowd serve -http 127.0.0.1:8443 \
  -tls-cert certs/server.pem -tls-key certs/server-key.pem &
# HTTP/2 200:
curl --cacert certs/ca.pem -i https://127.0.0.1:8443/v1/keys
# unknown CA:
curl -i https://127.0.0.1:8443/v1/keys
go run ./cmd/burrowctl -http https://127.0.0.1:8443 \
  -cacert certs/ca.pem set greeting hello
```

## In the wild

mkcert and step-ca do what pki does; cert-manager and Let's Encrypt's autocert (golang.org/x/crypto/acme/autocert) issue the real ones; every Go HTTPS server is http.Server.ServeTLS or a tls.Listener

## New in Go

crypto/x509 templates and CreateCertificate · crypto/ecdsa and elliptic.P256 · encoding/pem · tls.Certificate and X509KeyPair · tls.Config: MinVersion, RootCAs, NextProtos · http.Server.ServeTLS · http.Transport.TLSClientConfig and ForceAttemptHTTP2 · x509 error types with errors.As · file modes with os.WriteFile

Standard library: `crypto/x509`, `crypto/ecdsa`, `crypto/elliptic`, `crypto/rand`, `crypto/tls`, `encoding/pem`, `math/big`, `net`, `net/http`
