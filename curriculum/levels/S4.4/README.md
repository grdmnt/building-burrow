# S4.4 · Forwarding and Transactions

Chain entities together, and settle one message while sending others, all or nothing.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S4.4/files/`, matching the earlier chapters.

## Build

- Auto-forward: a queue or subscription with `ForwardTo` pushes everything into another entity
- A hop counter that dead-letters with `MaxTransferHopCountExceeded` when forwarding loops
- A transaction that completes an input message and sends output messages atomically
- Each transaction commits as a single WAL record

## Pass when

A forwarding loop between two queues dead-letters instead of spinning forever. Killing the broker mid-transaction never leaves the input completed without its outputs, or outputs sent while the input is still pending.

## New in Go

atomic multi-step operations · loop detection · crash testing

Standard library: `os` · `sync`
