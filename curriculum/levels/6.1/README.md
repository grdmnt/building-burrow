# 6.1 · Multi-core

Run three Burrow nodes on one machine from a single command.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/6.1/files/`, matching the earlier chapters.

## Build

- Node flags: `-id`, `-http`, `-peer-addr`, `-data`
- `cmd/devcluster` starts three burrowd processes with `os/exec`
- Prefix each node's log lines (`n1 |`, `n2 |`)
- Ctrl-C stops them all

## Pass when

`go run ./cmd/devcluster` starts n1 to n3 with prefixed logs. After Ctrl-C, `pgrep burrowd` finds nothing.

## New in Go

os/exec · child processes · piping output · config structs

Standard library: `os/exec` · `flag` · `bufio` · `io`
