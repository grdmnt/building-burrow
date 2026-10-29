# 5.4 · Test Jig

Test handlers end to end without starting servers by hand.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/5.4/files/`, matching the earlier chapters.

## Build

- Unit tests per handler with `httptest.NewRecorder`
- An integration test through the full middleware chain with `httptest.NewServer`
- A fake Store for error paths

## Pass when

`go test -race ./api/...` covers every route and status code from 5.1 plus the panic and timeout paths, in under 2 seconds.

## New in Go

httptest · t.TempDir · t.Cleanup · interface fakes

Standard library: `net/http/httptest` · `testing`
