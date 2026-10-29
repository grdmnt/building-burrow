# S3.6 · Competing Consumers

Many workers drain one queue quickly without processing anything twice.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S3.6/files/`, matching the earlier chapters.

## Build

- Long-polling `Receive(ctx, max)` that waits for messages instead of busy-looping
- Client-side prefetch of up to N locked messages
- Several consumers competing on one queue
- Locks on prefetched messages keep ticking while they sit in the buffer

## Pass when

Three consumers drain 10,000 messages: every message is completed once and each consumer handles at least 25%. With a large prefetch and slow consumers, a test shows buffered locks expiring and those messages being redelivered.

## New in Go

waiting without polling · fairness · flow control

Standard library: `sync` · `context` · `time`
