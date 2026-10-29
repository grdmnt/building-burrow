# 7.1 · Election

Nodes agree on one leader per term.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.1/files/`, matching the earlier chapters.

## Build

- Follower, candidate and leader roles
- Randomised election timeouts and the RequestVote RPC
- One goroutine owns all Raft state and handles events in a `select` loop

## Pass when

Three nodes elect exactly one leader within 2 seconds. Kill it and a new leader with a higher term appears within 3 seconds. A test harness asserts there are never two leaders in one term.

## New in Go

state owned by one goroutine · select over many channels · timer resets · reading a spec

Standard library: `time` · `math/rand/v2` · `context`
