# 9.1 · Multi-core

Run three Burrow nodes on one machine from a single command, wait until each reports ready on its admin port, and stop them all on Ctrl-C.

## Build

```go
// burrowd serve gains -id n1 -peers n1=127.0.0.1:7001,n2=...,n3=... -peer-addr 127.0.0.1:7001
// devcluster [-n 3] [-base-port 7000]: starts nodes, prefixes stdout/stderr lines with `n1 |`, polls /readyz with burrow's http client until all ready, prints `cluster ready`, stops all on SIGINT.
```

- Node flags
- `os/exec` with StdoutPipe and a prefixing scanner
- Readiness polling
- Clean teardown

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/9.1/files/`, matching the earlier chapters.

## Pass when

go run ./cmd/devcluster starts n1 to n3 with prefixed logs and prints `cluster ready`; after Ctrl-C, pgrep burrowd finds nothing.

The suite will cover:

- Three nodes become ready and serve independent stores
- Killing devcluster with SIGINT leaves no child processes
- Log lines are prefixed per node

## Try it

```bash
go run ./cmd/devcluster
# Ctrl-C, then:
pgrep burrowd || echo clean
```

## Uses

- Durable daemon (built in 5.1 · Non-volatile Daemon)
- Admin port (built in 7.6 · Gauges)
- HTTP client lib (built in 7.5 · Client Library)

## In the wild

goreman/foreman, docker compose, kind; the readiness loop is what Kubernetes does for you

## New in Go

os/exec · exec.CommandContext · pipes · config structs

Standard library: `os/exec`, `flag`, `bufio`, `io`
