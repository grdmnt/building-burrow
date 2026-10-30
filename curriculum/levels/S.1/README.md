# S.1 · Shielded Cable

Put the HTTP API behind TLS with certificates you mint in code. A tiny `pki` package (a CA and leaf certificates from `crypto/x509` and `ecdsa`, no openssl) serves the tests, the devcluster and your own laptop; `burrowd serve` gets `-tls-cert`/`-tls-key`; the client library learns to trust a CA; and HTTP/2 arrives for free because ALPN negotiates it during the handshake.

## Build

```go
// pki/pki.go
package pki

// CA is a self-signed ECDSA P-256 certificate and its key, held in memory.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

func NewCA(name string, validFor time.Duration) (*CA, error)

// Issue signs a leaf certificate for hosts (DNS names and IP addresses, each going into the
// right SAN list; the CN is name) with the given extended key usage, valid for validFor.
func (ca *CA) Issue(name string, hosts []string, usage []x509.ExtKeyUsage, validFor time.Duration) (tls.Certificate, error)

func (ca *CA) Pool() *x509.CertPool // a pool holding only this CA, for RootCAs / ClientCAs
func (ca *CA) CertPEM() []byte      // the CA certificate as PEM, for -cacert files

// WriteFiles writes <dir>/<name>.pem (the leaf, PEM "CERTIFICATE") and <dir>/<name>-key.pem
// (PKCS #8, PEM "PRIVATE KEY", mode 0600) and returns both paths.
func WriteFiles(dir, name string, cert tls.Certificate) (certPath, keyPath string, err error)

// ServerConfig is TLS 1.3 only, with cert as the server certificate.
func ServerConfig(cert tls.Certificate) *tls.Config

// cmd/burrowd
//   burrowd serve ... [-http addr] [-tls-cert cert.pem -tls-key key.pem]
//   With both flags the HTTP listener is served with http.Server.ServeTLS and the startup line
//   is `https on <addr>` instead of `http on <addr>`. One flag without the other is a usage error (exit 2).
//   The frame port stays plaintext in this chapter.

// burrow (client library, 7.5) Options gains:
//   RootCAs *x509.CertPool // nil: the system roots
//   The Transport sets TLSClientConfig{RootCAs, MinVersion: TLS 1.3} and ForceAttemptHTTP2: true.
// cmd/burrowctl gains -cacert ca.pem for -http https://... URLs.
```

- `pki.NewCA`, `Issue`, `Pool`, `CertPEM`, `WriteFiles`, `ServerConfig`: a CA and leaves from `crypto/x509`, `crypto/ecdsa`, `crypto/rand`, `encoding/pem`
- `burrowd serve -tls-cert -tls-key` → `http.Server.ServeTLS`, `https on <addr>`
- `burrow.Options.RootCAs` and `burrowctl -cacert`; HTTP/2 negotiated, which the tests check through `resp.Proto`
- Your own test that issues a cert from a fresh CA and verifies it with `x509.Certificate.Verify`

The cryptography is all library calls; the lesson is which fields matter. A CA template needs `IsCA: true`, `BasicConstraintsValid: true` and `KeyUsageCertSign`; a leaf needs `KeyUsageDigitalSignature`, an `ExtKeyUsage` list (`ExtKeyUsageServerAuth`, `ExtKeyUsageClientAuth` or both) and, above all, SANs: since Go 1.15 a certificate whose only name is the CN is rejected, so hosts go into `DNSNames` and `IPAddresses` (use `net.ParseIP` to decide which). Serial numbers are random 128-bit values from `crypto/rand`, not a counter. `x509.CreateCertificate(rand.Reader, &leafTemplate, ca.Cert, &leafKey.PublicKey, ca.Key)` signs; the result is DER, which `pem.Encode` wraps in `-----BEGIN CERTIFICATE-----`. A `tls.Certificate` is the DER chain plus the private key, and `tls.X509KeyPair` reconstructs it from the two PEM files you wrote. `x509.MarshalPKCS8PrivateKey` is the modern key encoding; `MarshalECPrivateKey` ("EC PRIVATE KEY") is the older one you will see in files that already exist.

On the client side, the trap: a custom `http.Transport` with a `TLSClientConfig` does not negotiate HTTP/2 unless `ForceAttemptHTTP2` is true (the default transport does, your 7.5 one doesn't). Set it and check `resp.Proto`.

Decisions the tests rely on:

- Keys are ECDSA P-256; signatures are ECDSA with SHA-256. Leaf validity is clamped to the CA's `NotAfter`.
- `Issue` puts each host into `DNSNames` or `IPAddresses` by whether `net.ParseIP` accepts it; the CN is `name` and nothing is trusted on the CN alone.
- `WriteFiles` writes the key file with mode 0600 and the certificate with 0644. The key is PKCS #8 (`PRIVATE KEY`).
- `ServerConfig` sets `MinVersion: tls.VersionTLS13` and leaves `NextProtos` alone, so `ServeTLS` adds `h2` and `http/1.1` itself.
- `-tls-cert` without `-tls-key`, or the reverse, is a usage error: exit 2, message on stderr naming the missing flag. A file that fails to load exits 1.
- With TLS on, `burrowd` prints `https on <addr>` as its second line; without, `http on <addr>` as before.
- `Options.RootCAs == nil` means the system roots, so an `https://` base URL to a public host still works. Every TLS error from the client surfaces as the `*url.Error` from `http.Client`, so `errors.As` with `x509.UnknownAuthorityError` and `x509.HostnameError` works on it.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S.1/files/`, matching the earlier chapters.

## Pass when

burrowd serves the API over https with a certificate minted by pki, curl needs --cacert and gets HTTP/2, a client without the CA is refused with an x509 error, and the frame port behaves exactly as before.

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
go run ./cmd/burrowd serve -http 127.0.0.1:8443 -tls-cert certs/server.pem -tls-key certs/server-key.pem &
curl --cacert certs/ca.pem -i https://127.0.0.1:8443/v1/keys      # look for HTTP/2 200
curl -i https://127.0.0.1:8443/v1/keys                            # unknown CA
go run ./cmd/burrowctl -http https://127.0.0.1:8443 -cacert certs/ca.pem set greeting hello
```

## Uses

- HTTP client lib (built in 7.5 · Client Library)
- Deadline guard (built in 7.4 · Watchdog)

## In the wild

mkcert and step-ca do what pki does; cert-manager and Let's Encrypt's autocert (golang.org/x/crypto/acme/autocert) issue the real ones; every Go HTTPS server is http.Server.ServeTLS or a tls.Listener

## New in Go

crypto/x509 templates and CreateCertificate · crypto/ecdsa and elliptic.P256 · encoding/pem · tls.Certificate and X509KeyPair · tls.Config: MinVersion, RootCAs, NextProtos · http.Server.ServeTLS · http.Transport.TLSClientConfig and ForceAttemptHTTP2 · x509 error types with errors.As · file modes with os.WriteFile

Standard library: `crypto/x509`, `crypto/ecdsa`, `crypto/elliptic`, `crypto/rand`, `crypto/tls`, `encoding/pem`, `math/big`, `net`, `net/http`
