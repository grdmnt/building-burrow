# H.3 · Conformance

Harden the server against injection and smuggling, add `Expect: 100-continue` and graceful shutdown on `conntrack`, prove it against net/http with differential tests and fuzzing, then read how net/http does it all.

## Build

```go
func (s *Server) Shutdown(ctx context.Context) error // same contract as wire.Server.Shutdown, on conntrack.Tracker
```

- Hardening cases
- 100-continue
- Shutdown via conntrack
- Then read net/http's server.go and write down three things it handles that yours doesn't

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/H.3/files/`, matching the earlier chapters.

## Pass when

Your parser agrees with net/http on 9 differential requests, survives fuzzing, never emits CR/LF from a header value, handles 1,000 keep-alive requests from 50 clients, and shuts down gracefully.

The suite will cover:

- Differential parsing
- Fuzz target
- Injection and smuggling cases
- Shutdown idle/in-flight/deadline

## Uses

- Chunked codec (built in H.2 · Streaming)
- Keep-alive loop (built in H.1 · Keep-Alive)
- Shutdown (built in 3.5 · Power Down)

## In the wild

h2spec and the HTTP/1.1 conformance suites that nginx and Envoy run

## New in Go

differential testing · fuzzing parsers · reading the standard library

Standard library: `net/http (tests only)`, `testing`
