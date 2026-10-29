# 5.2 · Signal Chain

Logging, request IDs and panic recovery as composable layers.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/5.2/files/`, matching the earlier chapters.

## Build

- `type Middleware func(http.Handler) http.Handler` and a `chain()` helper
- Request IDs from `crypto/rand`
- A slog access log with status and duration
- Recover from panics with a 500

## Pass when

Every request logs one JSON line with id, method, path, status and duration. A handler that panics returns 500 and the server keeps serving.

## New in Go

closures · decorators · wrapping interfaces · context values · structured logging

Standard library: `log/slog` · `crypto/rand` · `encoding/hex` · `context`
