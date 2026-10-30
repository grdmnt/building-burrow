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

// Everything that can happen to a node arrives on one inbox channel as a Message, and the
// Run goroutine is the only code that touches Raft state. The interface is sealed: an
// unexported method means only this package can add message types, so a type switch over
// it is exhaustive by construction.
type Message interface{ isMessage() }
type requestVote struct{ from string; req RequestVoteRequest; reply chan RequestVoteResponse }
type voteResult struct{ term uint64; granted bool }
type statusQuery struct{ reply chan Status }
// ... each with func (requestVote) isMessage() {}

// Run's loop, in outline:
//   loop:
//     for {
//       select {
//       case <-ctx.Done():            break loop            // a bare break would only leave the select
//       case m := <-n.inbox:
//         switch m := m.(type) {      // m is the concrete type inside each case
//         case requestVote:  ...
//         case voteResult:   ...
//         case statusQuery:  m.reply <- n.status()
//         }
//       case <-electionTimer.C:       n.startElection()
//       case <-heartbeatTimer.C:      n.sendHeartbeats()
//       }
//     }
```

- Roles and terms
- RequestVote RPC and reply handling
- Election timer with jitter, heartbeat timer
- The select loop over one inbox `chan Message` plus timers, with a type switch on a sealed interface and a labeled break to leave it

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.1/files/`, matching the earlier chapters.

## Pass when

Three nodes elect exactly one leader within 2 s; kill it and a new leader with a higher term appears within 3 s; -count=100 never shows two leaders in one term.

The suite will cover:

- Single leader per term across 100 runs on an in-memory transport
- Re-election after leader stop
- A node with a stale term steps down on a higher term
- Status() and the transport's handlers only ever talk to the Run goroutine through the inbox: no mutex in the package, and -race stays clean under 100 concurrent Status calls during an election

## Uses

- RPC (built in 9.2 · Interconnect)
- Quorum (built in 9.3 · Quorum)

## In the wild

hashicorp/raft, etcd/raft; Elixir's :ra

## New in Go

actor-style state ownership · one inbox channel of a sealed interface · type switches · labeled break and continue · select over many channels · timer resets · reading a spec

Standard library: `time`, `math/rand/v2`, `context`
