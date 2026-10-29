# 10.1 · Election

Nodes agree on one leader per term. One goroutine owns all Raft state and handles events in a select loop, which is the actor pattern 1.2 offered as an alternative.

## Build

```go
package raft

type Node struct { /* yours */ }
type Config struct { ID string; Peers map[string]string; ElectionTimeout [2]time.Duration; Heartbeat time.Duration }
func New(cfg Config, transport Transport) *Node
func (n *Node) Run(ctx context.Context) error // the one goroutine that owns all state
func (n *Node) Status() Status                // Term, Role, Leader
// Transport: RequestVote(ctx, peer, req) (resp, error) over rpc.Conn; tests use an in-memory transport.
```

- Roles and terms
- RequestVote RPC and reply handling
- Election timer with jitter, heartbeat timer
- The select loop

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.1/files/`, matching the earlier chapters.

## Pass when

Three nodes elect exactly one leader within 2 s; kill it and a new leader with a higher term appears within 3 s; -count=100 never shows two leaders in one term.

The suite will cover:

- Single leader per term across 100 runs on an in-memory transport
- Re-election after leader stop
- A node with a stale term steps down on a higher term

## Uses

- RPC (built in 9.2 · Interconnect)
- Quorum (built in 9.3 · Quorum)

## In the wild

hashicorp/raft, etcd/raft; Elixir's :ra

## New in Go

actor-style state ownership · select over many channels · timer resets · reading a spec

Standard library: `time`, `math/rand/v2`, `context`
