# 6.2 · Interconnect

Nodes talk to each other over your own frame codec.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/6.2/files/`, matching the earlier chapters.

## Build

- RPC on top of the 2.3 frames: request IDs and a method byte
- Many calls multiplexed over one connection
- A reader goroutine routes replies by ID
- Timeouts from `ctx`

## Pass when

1,000 concurrent calls from n1 to n2 over one TCP connection all return correctly. Killing n2 mid-flight fails every pending call promptly instead of hanging.

## New in Go

multiplexing · request correlation · reader goroutines · failing pending calls

Standard library: `net` · `sync` · `context` · `encoding/binary`
