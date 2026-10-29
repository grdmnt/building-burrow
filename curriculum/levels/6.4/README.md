# 6.4 · Address Decoder

Spread keys across nodes so each one owns a slice of the keyspace.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/6.4/files/`, matching the earlier chapters.

## Build

- A hash ring with virtual nodes using `hash/fnv`
- Lookups with a sorted slice and `slices.BinarySearch`
- Any node forwards a request to the key's owner

## Pass when

10,000 keys land within ±15% of even across 3 nodes. Removing a node moves only about a third of the keys. A GET to any node returns the right value.

## New in Go

consistent hashing · slices package · proxying · forwarding loops

Standard library: `hash/fnv` · `slices` · `net/http/httputil`
