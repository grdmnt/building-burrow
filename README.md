# Building Burrow

Learn Go by building **Burrow**, a replicated key-value service, from the standard library up: a versioned store with leases, a binary wire protocol, a write-ahead log, an HTTP/1.1 server written by hand, then `net/http` with JSON in and out, a client library, change streams, cluster RPC, Raft consensus, and a chaos test it has to survive.

It plays like Turing Complete and Exercism. Each level gives you one new idea, a spec and a hidden-until-unlocked test suite. You write the code by hand; the tests tell you when it's right. Every level produces one named part, every part is used by a later level, and the final level is a machine assembled from nothing but parts you built. Once you have built a component yourself, the game unlocks the built-in version: chapter 6's hand-written HTTP server is replaced by `net/http` in chapter 7, with the diff as the lesson.

## Setup

You need Go 1.24 or newer, and internet the first time you run `./burrow` (it builds its terminal UI, which uses Bubble Tea and Lip Gloss). The race detector needs cgo: on macOS that's the Xcode command line tools (`xcode-select --install`).

```bash
git clone <this repo> building-burrow
cd building-burrow
./burrow          # the full-screen browser: levels, their pages, the machine
./burrow next     # start level 0.1
```

Optional: `alias burrow="$PWD/burrow"` in your shell profile so you can type `burrow` from any folder in the repo.

## The loop

```bash
burrow show       # read the level's page: the goal, how it fits, steps, examples
burrow watch      # tests re-run every time you save, each test a lamp on the board   (h hint · r rerun · s spec · d done · q quit)
burrow hint       # reveal the next of three hints
burrow done       # verify everything passes and complete the level
burrow next       # start the next level, or choose when the path branches
```

Look around:

```bash
burrow            # browse: ↑↓ levels, → read, tab for the machine and the map
burrow machine    # Burrow as a machine, every part you've built lit up
burrow map        # the chapter tech tree
burrow status     # progress and what to do next
burrow list       # every level and its state
burrow test --all # re-run every level you've started, to catch regressions
```

Working with Claude:

```bash
burrow prompt hint     # copies a prompt with the spec, your code and your failing tests
burrow prompt review   # copies a code-review prompt once you're green
burrow prompt tests    # for levels whose tests aren't written yet
```

## How it's laid out

```
curriculum/            the levels: specs, hints and test suites (don't edit)
  curriculum.json
  levels/<id>/README.md
  levels/<id>/files/   test files that `burrow start` copies into your code
tools/burrow/          the CLI, built on Bubble Tea and Lip Gloss (its own module: your code stays standard library only)
  tui/                 the drawing kit: palette, canvas with wires and boxes, lamps
.burrow/progress.json  your progress; commit it to sync between machines
```

Your code goes at the repo root (`cmd/`, `store/`, `wire/`, `wal/`, `durable/`, `httpraw/`, `api/`, `burrow/`, …). The test files that `burrow start` installs sit next to it, named `l<level>_*_test.go`. `burrow done` checks they're unchanged; `burrow restore` puts them back.

## Rules

- Standard library only in the code you build. Tool dependencies (the optional M.1 level adds staticcheck with `go get -tool`) never reach the binary, and the optional Q.4 level uses the Azure Service Bus SDK in its own module.
- You write tests too. From 0.2 on, some levels grade your own tests by planting bugs in copies of your code (mutants) and checking your tests catch them.
- You write all the implementation. Tests, hints and reviews come from the curriculum (and Claude).
- Every test run uses `-race`.
- Time-based levels take an injectable clock (`store.WithClock`); the tests drive a `testkit.FakeClock`.
- `database/sql` is out of scope: the standard library ships no driver. Everything else a web service needs is here.

## Chapters

The tree branches early. After Boot Sector you can take Memory or Codec; after both, Wire or Persistence; after net/http, Leases & Watch, Cluster or Traffic Control. Each level's spec ends with a **Uses** list naming the parts it consumes and the levels that built them.

| | Chapter | Levels | What you build |
|---|---|---|---|
| 0 | Boot Sector | 2 | the module, a testable `main`, `go vet`, `ValidateKey` and your first graded table-driven tests |
| 1 | Memory | 5 | the `store.Store` contract and its Memory engine: mutex, versions and CAS, expiry on an injectable clock, a janitor |
| 2 | Codec | 2 | a length-prefixed record codec and the KV frame on top of it |
| 3 | Wire | 5 | an accept loop, a frame server, a client that is itself a Store, deadlines, graceful shutdown and `burrowd serve` |
| 4 | Persistence | 5 | a write-ahead log with iterators, a durable engine by embedding, checksums and truncation, snapshots, group commit |
| 5 | Assembly | 1 | the non-volatile daemon: kill -9 it, and `burrowctl` still reads |
| 6 | HTTP by Hand | 3 | an HTTP/1.1 parser, response writer and router on raw TCP |
| 7 | net/http | 6 | the same API on the standard server with ETags, JSON in and out, middleware, timeouts, a client library on `http.Client`, an admin port |
| 8 | Leases & Watch | 5 | a lease lock with fencing tokens, a broker, server-sent events, resume, a watch client as an iterator |
| 9 | Cluster | 3 | devcluster, multiplexed RPC on your codec, a generic quorum helper |
| 10 | Consensus | 6 | Raft: election, log shipping, the apply loop feeding watch, leader following, read index, persistence on your WAL |
| 11 | Chaos | 4 | a fault-injecting proxy, a history recorder, a checker, and the final boss |
| M | Toolchain | 1 | optional: a `tool` directive for staticcheck, go.sum, `go mod tidy`, and proof the binary stays standard-library only |
| H | HTTP Deep Cuts | 3 | optional: keep-alive, chunked streaming, hardening and conformance |
| T | Traffic Control | 2 | optional: a token bucket, a generic LRU with single-flight |
| S | TLS & Auth | 3 | optional: TLS and HTTP/2 on the API, bearer tokens, mutual TLS between cluster peers |
| D | Eventually Consistent | 3 | optional epilogue: a hash ring, quorum replication, and the checker catching it |
| Q | Queues | 4 | optional: a durable queue broker, peek-lock, competing consumers, then the real Azure Service Bus |

47 mandatory levels, 16 optional. Levels 0.1 to 7.2 and the optional HTTP Deep Cuts and Toolchain chapters have full test suites. Later chapters have specs and hints; their tests get written as you reach them (`burrow prompt tests`).
