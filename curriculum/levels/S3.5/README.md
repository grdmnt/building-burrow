# S3.5 · Duplicate Detection

Senders retry after timeouts. The broker drops the copies.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S3.5/files/`, matching the earlier chapters.

## Build

- Queues opt in with `RequiresDuplicateDetection` and a time window
- A repeated `MessageID` inside the window is acknowledged but silently discarded
- IDs fall out of the window as time passes
- The set of recent IDs survives restarts

## Pass when

A test sends, fakes a timeout and resends with the same `MessageID`: the consumer sees one message. Once the window passes, the same ID is accepted again.

## New in Go

idempotency · time-ordered expiry · map plus list

Standard library: `container/list` · `time` · `sync`
