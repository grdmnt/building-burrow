# 7.2 · Log Shipping

The leader replicates a log; entries commit once a majority has them.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.2/files/`, matching the earlier chapters.

## Build

- AppendEntries with the `prevLogIndex` and `prevLogTerm` check
- `nextIndex` and `matchIndex` per follower
- Advance `commitIndex`; heartbeats are empty AppendEntries

## Pass when

Partition a follower, write 100 entries, heal it: it catches up and all three logs are identical.

## New in Go

log matching · follower catch-up · majority math

Standard library: `sync` · `context`
