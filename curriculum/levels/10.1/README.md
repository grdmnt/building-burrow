# 10.1 · Election

> Three machines, one vote each, and a rule that makes it impossible for two of them to think they are in charge at the same time.

## Your goal

Give each Burrow node a `raft.Node` that can hold an election. Raft is the consensus algorithm (the recipe nodes follow to agree on things) that etcd and Consul run on; this chapter builds it piece by piece. This level only does the first piece: nodes agree on **one leader per term**. A term is a counter that goes up every time someone starts an election, like a parliament's session number, and the rule is that each node casts at most one vote per term, so two leaders in the same term cannot happen.

All of a node's Raft state lives in one goroutine. Everything else, timers, incoming RPCs, status queries, sends it a message. That is the design 1.2 offered as an alternative to a mutex, and here it pays for itself.

## What you're using from before

**RPC (9.2 · Interconnect).** `rpc.Conn.Call(ctx, method, payload)` sends one request over a shared connection and returns the reply; `rpc.Server{Handle: fn}` answers calls. This level hides it behind a `Transport` interface so the tests can swap in an in-memory one. The real transport encodes a `RequestVoteRequest`, calls the peer, and decodes the reply:

```go
func (t *rpcTransport) RequestVote(ctx context.Context, peer string,
	req RequestVoteRequest) (RequestVoteResponse, error) {
	body, err := t.conn(peer).Call(ctx, methodRequestVote, encode(req))
	if err != nil {
		return RequestVoteResponse{}, err
	}
	return decodeVoteResponse(body)
}
```

On the receiving side the `rpc.Server` handler does not touch Raft state. It wraps the request in a message, drops it in the node's inbox, and waits for the reply channel:

```go
reply := make(chan RequestVoteResponse, 1)
n.inbox <- requestVote{from: peer, req: req, reply: reply}
resp := <-reply
```

**Quorum (9.3 · Quorum).** `quorum.Call[T](ctx, k, fns)` runs every `fn` in its own goroutine and returns as soon as `k` of them succeed. A candidate uses it to collect votes: one `fn` per peer, each returning an error when the vote is denied so that only granted votes count as successes. With 3 nodes a majority is 2, and you already voted for yourself, so `k` is 1:

```go
fns := make([]func(context.Context) (bool, error), 0, len(peers))
for _, p := range peers {
	fns = append(fns, func(ctx context.Context) (bool, error) {
		resp, err := t.RequestVote(ctx, p, req)
		if err != nil {
			return false, err
		}
		if !resp.VoteGranted {
			return false, errVoteDenied
		}
		return true, nil
	})
}
_, err := quorum.Call(ctx, need, fns) // nil error: you won
```

Run that in a goroutine, not in the loop, and send the outcome back to the inbox as a `voteResult` message.

## What's new here

**Reading a spec.** Raft comes with a one-page summary, Figure 2 of the paper ("In Search of an Understandable Consensus Algorithm"). This level implements the parts of it that elect a leader. In plain words:

- A node is a **follower**, a **candidate** or a **leader**. Everyone starts as a follower.
- A follower that hears nothing from a leader for an **election timeout** becomes a candidate: it adds 1 to its term, votes for itself, and asks every peer for a vote with a `RequestVote` RPC.
- A node grants a vote if the candidate's term is at least its own and it has not voted for anyone else in that term. One vote per term, no exceptions.
- A candidate with a majority of votes becomes leader and starts sending **heartbeats** (`AppendEntries` calls that carry no log entries yet) so followers keep resetting their election timeouts.
- Any message carrying a higher term than your own makes you adopt that term and become a follower, whatever you were doing. Terms only ever go up.

The election timeout is random, somewhere between the two values in `Config.ElectionTimeout`, so that two followers rarely time out together and split the vote forever.

**The actor pattern.** One goroutine owns the state; nobody else reads or writes it. Other goroutines ask by sending a message and, when they need an answer, include a channel for the reply. If you know Elixir, this is a GenServer: `Run` is the server loop, the inbox is the mailbox, `Status()` is a `call`. There is no mutex because there is no shared memory to guard.

```go
type statusQuery struct{ reply chan Status }

func (n *Node) Status() Status {
	reply := make(chan Status, 1)
	n.inbox <- statusQuery{reply: reply}
	return <-reply
}
```

The reply channel is buffered (size 1) so the loop never blocks on a caller that has given up.

**One inbox of a sealed interface.** All messages travel on one `chan Message`. `Message` is an interface with a single unexported method, `isMessage()`. Nothing outside this package can write a method with a lowercase name on it, so nothing outside the package can add a message type. The set is closed, which is what makes the next trick safe.

```go
type Message interface{ isMessage() }

type requestVote struct {
	from  string
	req   RequestVoteRequest
	reply chan RequestVoteResponse
}

func (requestVote) isMessage() {}
```

**Type switches.** A type switch picks a branch by the concrete type stored in an interface value. Inside each branch `m` already has that type, so you can read its fields without a cast:

```go
switch m := m.(type) {
case requestVote:
	m.reply <- n.handleRequestVote(m.from, m.req)
case voteResult:
	n.handleVoteResult(m)
case statusQuery:
	m.reply <- n.status()
}
```

**Select over many channels, and labeled break.** `select` waits on several channels at once and runs whichever case is ready first. A timer exposes its tick as a channel, `timer.C`, so timers and the inbox sit in the same `select`. The catch is that a plain `break` inside a `select` only leaves the `select`, not the `for` around it. A label on the loop fixes that:

```go
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case m := <-n.inbox:
			n.handle(m)
		case <-electionTimer.C:
			n.startElection()
		}
	}
```

`continue loop` works the same way when you want to skip to the next iteration from inside a nested loop.

**Timer resets.** `time.NewTimer(d)` fires once. To push the election timeout back every time a heartbeat arrives, call `timer.Reset(d)` with a fresh random duration. Since Go 1.23 a reset timer never delivers a stale tick, so the old drain-the-channel dance is gone: `Reset` is all you need. For the jitter, `math/rand/v2` has a generic `rand.N` that works directly on durations:

```go
lo, hi := cfg.ElectionTimeout[0], cfg.ElectionTimeout[1]
timeout := lo + rand.N(hi-lo)
electionTimer.Reset(timeout)
```

A leader does not need an election timer and a follower does not need a heartbeat timer. A nil channel blocks forever in `select`, so switch a case off by stopping the timer and keeping its `C` out of the way, or by swapping in a `nil` channel variable.

## What to do

1. Create package `raft` with `Config`, `Node`, `Status`, `New` and `Run`.
   - `Config.Peers` maps peer IDs to addresses; `Config.ID` is this node's own ID.
   - `Status` has `Term`, `Role` and `Leader` (the ID of the leader this node believes in, or empty).
2. Define `Transport` with one method, `RequestVote(ctx, peer, req)`, and the `RequestVoteRequest` and `RequestVoteResponse` types from Figure 2. Write an `rpc`-backed transport for the binary and an in-memory one for your own tests.
3. Define `Message` as the sealed interface and one struct per event: a vote request from a peer, the outcome of your own election, a status query. Give each the empty `isMessage()` method.
4. Write `Run` as the one goroutine that owns every field: the labeled `for`/`select` loop over the inbox, the election timer and the heartbeat timer. It returns when `ctx` ends.
5. Implement the rules from "Reading a spec" inside the loop: become a candidate on timeout, grant at most one vote per term, step down on a higher term, become leader on a majority, send heartbeats as leader. Reset the election timer on every heartbeat you receive.
6. Make `Status()` and the RPC handlers talk to the loop only through the inbox. No mutex anywhere in the package.
7. Add an RPC method byte for `RequestVote` to `burrowd` so nodes started with `-peers` actually vote.

## Example

Three nodes on an in-memory transport, each running its loop in its own goroutine:

```go
n1 := raft.New(cfg("n1"), tr)
n2 := raft.New(cfg("n2"), tr)
n3 := raft.New(cfg("n3"), tr)
for _, n := range []*raft.Node{n1, n2, n3} {
	go n.Run(ctx)
}
time.Sleep(time.Second)
fmt.Println(n1.Status(), n2.Status(), n3.Status())
```

```
{1 Follower n2} {1 Leader n2} {1 Follower n2}
```

Stop `n2` (cancel its context) and ask again after a couple of seconds:

```
{2 Leader n1} {1 Leader n2} {2 Follower n1}
```

`n2` still thinks it is the leader of term 1, which is harmless: anything it says carries a stale term and the others ignore it. Term 2 has exactly one leader.

## Done when

- [ ] Three nodes elect exactly one leader within 2 seconds
- [ ] Stopping the leader produces a new leader with a higher term within 3 seconds
- [ ] Running the election test 100 times (`-count=100`) never shows two leaders in one term
- [ ] A node that learns of a higher term steps down to follower and adopts it
- [ ] `Status()` and the RPC handlers only reach Raft state through the inbox: no mutex in the package, and `-race` stays clean with 100 concurrent `Status()` calls during an election

## Reference

The exact contract the tests will hold you to.

```go
package raft

type Node struct { /* yours */ }
type Config struct {
	ID              string
	Peers           map[string]string
	ElectionTimeout [2]time.Duration
	Heartbeat       time.Duration
}
func New(cfg Config, transport Transport) *Node
// Run is the goroutine that owns all state.
func (n *Node) Run(ctx context.Context) error
func (n *Node) Status() Status // Term, Role, Leader

// Transport: RequestVote(ctx, peer, req) (resp, error) over rpc.Conn;
// tests use an in-memory transport. Request and response carry the
// fields from Figure 2: term, candidateId, lastLogIndex, lastLogTerm
// in, and term, voteGranted out.

// Everything that can happen to a node arrives on one inbox channel as
// a Message, and the Run goroutine is the only code that touches Raft
// state. The interface is sealed: an unexported method means only this
// package can add message types, so a type switch over it is exhaustive
// by construction.
type Message interface{ isMessage() }
type requestVote struct {
	from  string
	req   RequestVoteRequest
	reply chan RequestVoteResponse
}
type voteResult struct {
	term    uint64
	granted bool
}
type statusQuery struct{ reply chan Status }
// ... each with func (requestVote) isMessage() {}

// Run's loop, in outline:
//   loop:
//     for {
//       select {
//       case <-ctx.Done():       break loop   // not a bare break
//       case m := <-n.inbox:
//         switch m := m.(type) { // m is concrete in each case
//         case requestVote:  ...
//         case voteResult:   ...
//         case statusQuery:  m.reply <- n.status()
//         }
//       case <-electionTimer.C:  n.startElection()
//       case <-heartbeatTimer.C: n.sendHeartbeats()
//       }
//     }
```

What this level builds:

- Roles and terms
- RequestVote RPC and reply handling
- Election timer with jitter, heartbeat timer
- The select loop over one inbox `chan Message` plus timers, with a type switch on a sealed interface and a labeled break to leave it

Decisions the tests rely on:

- A node votes at most once per term, and a candidate always votes for itself first.
- Any RPC request or reply carrying a term higher than the node's own makes it adopt that term as a follower before doing anything else.
- The election timeout is drawn fresh from `[ElectionTimeout[0], ElectionTimeout[1])` every time it is armed.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.1/files/`, matching the earlier chapters.

The suite will cover:

- Single leader per term across 100 runs on an in-memory transport
- Re-election after leader stop
- A node with a stale term steps down on a higher term
- Status() and the transport's handlers only ever talk to the Run goroutine through the inbox: no mutex in the package, and -race stays clean under 100 concurrent Status calls during an election

## Try it

```bash
./burrow watch
go test -race -count=100 -run Election ./raft
```

## In the wild

hashicorp/raft, etcd/raft; Elixir's :ra

## New in Go

actor-style state ownership · one inbox channel of a sealed interface · type switches · labeled break and continue · select over many channels · timer resets · reading a spec

Standard library: `time`, `math/rand/v2`, `context`
