# Building Burrow

Learn Go by building **Burrow**, a distributed key-value store, from the standard library up: raw TCP, your own wire protocol, a write-ahead log, an HTTP/1.1 server written by hand, then `net/http`, clustering, Raft consensus and a chaos test it has to survive.

It plays like Exercism or Turing Complete. Each level gives you a spec and a hidden-until-unlocked test suite. You write the code by hand; the tests tell you when it's right. Every level produces a component later levels build on.

## Setup

You need Go 1.24 or newer. The race detector needs cgo: on macOS that's the Xcode command line tools (`xcode-select --install`).

```bash
git clone <this repo> building-burrow
cd building-burrow
./burrow          # dashboard
./burrow next     # start level 0.1
```

Optional: `alias burrow="$PWD/burrow"` in your shell profile so you can type `burrow` from any folder in the repo.

## The loop

```bash
burrow show       # read the level's spec: exact API, rules, what the tests check
burrow watch      # tests re-run every time you save   (h hint · r rerun · s spec · d done · q quit)
burrow hint       # reveal the next of three hints
burrow done       # verify everything passes and complete the level
burrow next       # start the next level, or choose when the path branches
```

Look around:

```bash
burrow map        # the chapter tech tree
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
tools/burrow/          the CLI (standard library only, like everything else)
.burrow/progress.json  your progress; commit it to sync between machines
```

Your code goes at the repo root (`cmd/`, `store/`, `wire/`, `wal/`, …). The test files that `burrow start` installs sit next to it, named `l<level>_*_test.go`. `burrow done` checks they're unchanged; `burrow restore` puts them back.

## Rules

- Standard library only. The single exception is the final Azure Service Bus level, which uses the Azure SDK in its own module.
- You write all the implementation. Tests, hints and reviews come from the curriculum (and Claude).
- Every test run uses `-race`.

## Chapters

| | Chapter | What you build |
|---|---|---|
| 0 | Boot Sector | the module, a testable `main`, table-driven tests |
| 1 | Memory | a concurrent in-memory store with TTLs |
| 2 | Wire | TCP, a line protocol, a binary frame codec, a client, graceful shutdown |
| 3 | Persistence | a write-ahead log with checksums, snapshots and group commit |
| 4 | HTTP by Hand | an HTTP/1.1 server on raw TCP: parsing, routing, keep-alive, chunked streaming |
| 5 | net/http | the same API on the standard server, middleware, timeouts, observability |
| 6 | Cluster | multiple nodes, RPC, membership, consistent hashing, quorums |
| 7 | Consensus | Raft |
| 8 | Chaos | fault injection and a history checker; the final boss |
| Side 1–4 | optional | live change feeds, traffic control, and Azure Service Bus from first principles |

Chapters 0–4 have full test suites. Later chapters have specs and hints; their tests get written as you reach them (`burrow prompt tests`).
