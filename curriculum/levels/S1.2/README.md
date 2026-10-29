# S1.2 · Event Stream

Push changes to HTTP clients as server-sent events.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S1.2/files/`, matching the earlier chapters.

## Build

- `GET /v1/watch?prefix=user:` streams events as `text/event-stream`
- A heartbeat comment every 15 seconds
- Unsubscribe when the client disconnects

## Pass when

`curl -N 'localhost:8080/v1/watch?prefix=user:'` prints an event within a second of `PUT /v1/kv/user:1`. Closing curl drops the subscriber count back down.

## New in Go

server-sent events · flushing · request context cancellation

Standard library: `net/http` · `fmt` · `time`
