# 8.2 · History Checker

Record what clients saw and prove the cluster didn't lie.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.2/files/`, matching the earlier chapters.

## Build

- A load generator that logs each operation with start time, end time and result
- A checker: every acknowledged write is readable later, and reads of a key never go back in time

## Pass when

A clean run passes. Deliberately serving reads from followers makes the checker fail and name the bad operation.

## New in Go

concurrent clients · operation histories · consistency checking

Standard library: `encoding/json` · `time` · `sync` · `slices`
