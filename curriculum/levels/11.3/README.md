# 11.3 · Checker

Prove the cluster didn't lie. Five rules, each named, each pointing at the first offending operation.

## Build

```go
func Check(ops []Op) []Violation // Violation{Rule string; Op Op; Detail string}
```

- Five rules as separate functions
- Interval-aware reasoning for concurrent ops
- A report that names the first offending op

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.3/files/`, matching the earlier chapters.

## Pass when

A clean run passes; a run against a build flag that serves reads from followers fails on monotonic reads and names the op; a build flag that skips fencing fails lock exclusivity.

The suite will cover:

- Hand-written histories for each rule, passing and failing
- A real 10 s devcluster run passes
- The two sabotage flags fail on the right rule

## Uses

- History recorder (built in 11.2 · History Recorder)

## In the wild

Porcupine, Knossos, Elle; the properties here are a subset of Jepsen's

## New in Go

operation histories · consistency models · slices.SortFunc

Standard library: `encoding/json`, `slices`, `time`
