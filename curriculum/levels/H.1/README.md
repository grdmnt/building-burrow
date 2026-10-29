# H.1 · Keep-Alive

Serve many requests over one connection, and protect the server from clients that hold connections hostage.

## Build

```go
// httpraw/server.go additions
// Server gains:
//   IdleTimeout        time.Duration // between requests on a kept-alive connection; 0 means 30s
//   ReadHeaderTimeout  time.Duration // to receive a full request head; 0 means 10s
//   MaxRequestsPerConn int           // 0 means 100

// Connection rules:
//   - HTTP/1.1 connections stay open by default. HTTP/1.0 ones close unless the client sends
//     Connection: keep-alive (then reply with it too).
//   - Connection: close from the client (tokens are case-insensitive, comma-separated) means:
//     reply with Connection: close, then close.
//   - Before reading the next request, skip whatever body the handler didn't read. If more than
//     256 KiB is left, close instead.
//   - The last request allowed on a connection gets Connection: close.
```

- A request loop per connection, replacing the one-request-then-close behaviour from 6.1
- The Connection header rules above, for HTTP/1.1 and HTTP/1.0
- `SetReadDeadline` per phase: the idle deadline while waiting for the next request, the header deadline once bytes arrive
- Drain unread bodies up to 256 KiB, then close

Decisions the tests rely on:

- Timeouts reset per request: a connection that keeps sending requests inside the idle window stays open.
- The `MaxRequestsPerConn`-th response carries `Connection: close` and the server closes right after it.

## The tests check

- Two sequential requests on one connection; three pipelined requests answered in order
- Connection: close from the client is answered with Connection: close; `connection: Keep-Alive, CLOSE` closes (tokens are case-insensitive); HTTP/1.0 closes by default and stays open with Connection: keep-alive
- An ignored 11-byte body and a half-read body don't corrupt the next request
- IdleTimeout 100 ms closes an idle connection; ReadHeaderTimeout 150 ms drops a client that never finishes its headers; timeouts reset per request on a busy connection
- MaxRequestsPerConn 3: the third response carries Connection: close and the connection closes after it
- net/http's client reuses one connection for three requests

## Uses

- Router (built in 6.3 · Router)

## In the wild

net/http's conn.serve loop

## New in Go

SetReadDeadline per phase · bufio.Reader.Peek · net.Error.Timeout

Standard library: `net`, `time`, `errors`
