# S1.3 · Resume

Clients that reconnect pick up exactly where they left off.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S1.3/files/`, matching the earlier chapters.

## Build

- Monotonic event IDs sent as `id:` lines
- A ring buffer of recent events
- Honour the `Last-Event-ID` header on reconnect

## Pass when

Kill curl, write 5 keys, reconnect with `-H 'Last-Event-ID: <last id>'`: the 5 missed events arrive first, then live ones, with no gaps or duplicates.

## New in Go

ring buffers · replay then live · race-free handover

Standard library: `sync` · `strconv` · `net/http`
