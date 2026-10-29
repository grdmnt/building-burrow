# S2.3 · Single Flight

Collapse a stampede of identical cache misses into one store read.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S2.3/files/`, matching the earlier chapters.

## Build

- Concurrent misses for the same key share one in-flight call
- Every waiter gets the same value or the same error
- The in-flight entry is removed when the call finishes

## Pass when

100 concurrent GETs for one cold key cause exactly 1 store read, and all 100 get the value. A failed read is shared too, but isn't cached.

## New in Go

coordinating waiters · WaitGroup as a latch · shared results

Standard library: `sync`
