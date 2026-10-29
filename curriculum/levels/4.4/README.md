# 4.4 · Keep-Alive

Serve many requests over one connection, and protect the server from clients that hold connections hostage.

## Build

```go
// Server gains:
//   IdleTimeout        time.Duration // between requests; 0 means 30s
//   ReadHeaderTimeout  time.Duration // to receive a full request head; 0 means 10s
//   MaxRequestsPerConn int           // 0 means 100
```

- HTTP/1.1 connections stay open by default. HTTP/1.0 ones close unless the client sends `Connection: keep-alive` (then reply with it too).
- `Connection: close` from the client (tokens are case-insensitive, comma-separated) means: reply with `Connection: close`, then close.
- Before reading the next request, **skip whatever body the handler didn't read**. If more than 256 KiB is left, close instead.
- The last request allowed on a connection gets `Connection: close`.

## The tests check

- Two sequential and three pipelined requests on one connection, answered in order.
- `Connection: close`, `connection: Keep-Alive, CLOSE`, HTTP/1.0 with and without keep-alive.
- An ignored body and a half-read body don't corrupt the next request.
- An idle connection is closed after `IdleTimeout`; a slowloris client that never finishes its headers is dropped after `ReadHeaderTimeout`; timeouts reset per request.
- `MaxRequestsPerConn: 3` closes after the third.
- `net/http`'s client reuses one connection for three requests.

## New in Go

`SetReadDeadline` per phase · `bufio.Reader.Peek` · `net.Error.Timeout()` · resource limits
