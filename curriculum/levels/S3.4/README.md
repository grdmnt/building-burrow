# S3.4 · Scheduled and Deferred

Messages that arrive in the future, and messages a consumer sets aside to handle later.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S3.4/files/`, matching the earlier chapters.

## Build

- `ScheduleMessage(msg, enqueueAt)` returns a sequence number; `CancelScheduled(seq)` removes it
- A min-heap releases scheduled messages on time
- `Defer(token)` hides a locked message from normal receives
- `ReceiveDeferred(seqs...)` is the only way to get deferred messages back

## Pass when

A message scheduled 5 minutes out is invisible until the injected clock passes it, then arrives normally. A deferred message never comes back from `Receive`, only from `ReceiveDeferred` with its sequence number.

## New in Go

heap.Interface · timer wake-ups · lookup by sequence number

Standard library: `container/heap` · `time` · `sync`
