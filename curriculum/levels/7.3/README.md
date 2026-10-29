# 7.3 · State Machine

Put the KV store on top of Raft for linearizable writes.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.3/files/`, matching the earlier chapters.

## Build

- Apply committed entries to your Store in order
- Client writes go to the leader and wait for commit and apply
- Followers redirect with 307 to the leader
- Reads go through the leader with a read-index check

## Pass when

Writes through any node reach the leader and survive a leader failover. Followers never serve stale reads in the default mode.

## New in Go

apply loops · waiting on results by index · redirects · linearizability

Standard library: `net/http` · `context`
