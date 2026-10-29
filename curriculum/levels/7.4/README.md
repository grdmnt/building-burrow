# 7.4 · Non-volatile

Make Raft survive full restarts by reusing your WAL.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.4/files/`, matching the earlier chapters.

## Build

- Persist `currentTerm`, `votedFor` and log entries with the Chapter 3 WAL before replying to RPCs
- Snapshots via your 3.3 compaction
- InstallSnapshot for followers that fall far behind

## Pass when

Restart all three nodes at once: no acknowledged write is lost and terms never go backwards.

## New in Go

what must be durable, and when · reuse across layers

Standard library: `os`
