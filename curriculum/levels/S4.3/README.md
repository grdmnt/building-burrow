# S4.3 · Sessions

Strict ordering per customer, while different customers are processed in parallel.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S4.3/files/`, matching the earlier chapters.

## Build

- Messages carry a `SessionID`; session-enabled entities reject messages without one
- `AcceptNextSession` locks a whole session to one receiver
- Messages within a session come out strictly in order
- `GetSessionState` and `SetSessionState` store a small blob per session

## Pass when

Three interleaved sessions and two receivers: each session is handled by one receiver at a time, in order, and session state survives a broker restart.

## New in Go

keyed locks · per-key FIFO · per-key state

Standard library: `sync` · `container/list` · `time`
