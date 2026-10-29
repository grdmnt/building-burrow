# H.1 · Keep-Alive

Serve many requests over one connection, and protect the server from clients that hold connections hostage.

## Build

```go
// Server gains IdleTimeout, ReadHeaderTimeout, MaxRequestsPerConn.
```

- Request loop
- Connection header rules
- Deadlines per phase
- Drain up to 256 KiB

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/H.1/files/`, matching the earlier chapters.

## Pass when

Pipelining, Connection rules, drained bodies and all three timeouts behave like a production server; net/http's client reuses one connection for three requests.

The suite will cover:

- Two sequential and three pipelined requests
- Connection: close variants and HTTP/1.0
- Unread and half-read bodies don't corrupt the next request
- Idle and header timeouts; MaxRequestsPerConn

## Uses

- Router (built in 6.3 · Router)

## In the wild

net/http's conn.serve loop

## New in Go

SetReadDeadline per phase · bufio.Reader.Peek · net.Error.Timeout

Standard library: `net`, `time`, `errors`
