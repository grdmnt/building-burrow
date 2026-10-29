# 8.1 · Fault Injector

A network proxy that makes the cluster's life miserable.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.1/files/`, matching the earlier chapters.

## Build

- A TCP proxy between nodes that can delay, drop and partition connections
- A small HTTP admin API to control it
- devcluster routes node-to-node traffic through it

## Pass when

`curl -X POST ':9000/partition?nodes=n1'` isolates n1, the other two elect a leader, and healing reunites the cluster.

## New in Go

bidirectional copying · half-close · seeded randomness

Standard library: `net` · `io` · `math/rand/v2` · `net/http`
