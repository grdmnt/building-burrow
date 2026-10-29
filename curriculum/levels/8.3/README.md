# 8.3 · Leader Down

Final boss. Kill the leader during a write storm. Burrow must keep its promises.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.3/files/`, matching the earlier chapters.

## Build

- A scenario runner: start devcluster behind the chaos proxy
- Run the load generator, `kill -9` the leader, partition a follower
- Heal, restart, then run the checker

## Pass when

10 consecutive seeded runs pass the checker with zero lost acknowledged writes, using only the Go standard library.

## New in Go

everything so far

Standard library: `everything so far`
