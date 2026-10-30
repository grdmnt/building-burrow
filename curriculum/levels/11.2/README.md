# 11.2 · History Recorder

> Before you can prove the cluster kept its promises, you need a faithful record of what every client asked and what it was told.

## Your goal

Write `history`, a load generator that runs four kinds of **probe** against the cluster at once and writes down every operation it performs: what was asked, when it started, when it finished, and what came back. The output is a **history**, one JSON object per line, which is the evidence you need to check later that the cluster never lied. The four probes exercise four promises: plain reads and writes, exact counters built on CAS, a lease lock whose fencing tokens must come out in order, and a watch that must see every event.

## What you're using from before

**HTTP client lib (7.5 · Client Library).** `burrow.Client` is what every probe talks through, one client per probe goroutine, pointed at a node URL. With 10.4 it follows the leader on its own, so a probe keeps calling `Set` and `Get` while the leader changes under it:

```go
c, _ := burrow.New(nodeURL, burrow.Options{})
start := time.Now()
e, err := c.Set(ctx, key, value)
rec.write(Op{Client: id, Kind: "put", Key: key, Value: string(value),
	Start: start, End: time.Now(), OK: err == nil, Version: e.Version})
```

**Lease lock (8.1 · Lease Lock).** `lock.Acquire(ctx, c, key, ttl)` blocks until this client holds the lock and gives back a `Token()`, the fencing token, which is larger for every later holder. The lock probe acquires, appends its token to one shared key, and releases:

```go
l, err := lock.Acquire(ctx, c, "lock", 2*time.Second)
// append l.Token() to the "lock-log" key with CAS, then
err = l.Release(ctx)
```

**Watch client (8.5 · Watch Client).** `c.Watch(ctx, prefix, afterID)` is an iterator over events that reconnects by itself. The watch probe ranges over it and records every event ID it sees:

```go
for ev, err := range c.Watch(ctx, "kv:", 0) {
	if err != nil { /* record and continue, or stop on ctx */ }
	rec.write(Op{Client: id, Kind: "watch", Key: ev.Entry.Key,
		Token: ev.ID, Version: ev.Entry.Version, OK: true, ...})
}
```

## What's new here

**Concurrent clients.** Each probe is a goroutine with its own `burrow.Client` and its own seeded random source, running until `ctx` ends. A `sync.WaitGroup` waits for all of them before the file is closed. The only thing they share is the recorder, so the recorder is the one place that needs a mutex:

```go
var wg sync.WaitGroup
for id, probe := range probes {
	wg.Add(1)
	go func() {
		defer wg.Done()
		probe(ctx, id, rec)
	}()
}
wg.Wait()
```

(Since Go 1.22 each loop iteration has its own `id` and `probe`, so the closure captures the right ones without the old `id := id` dance.)

**Time measurement.** Record `Start` before the call and `End` after it. The checker needs the interval, not an instant: two operations whose intervals overlap were concurrent, and either order is allowed; an operation that started after another ended must respect it. `time.Now()` in Go carries a monotonic reading alongside the wall clock, and `t2.Sub(t1)` uses it, so the interval is right even if the wall clock jumps. JSON only stores the wall time, which is fine for ordering operations from one process.

**JSON lines.** One JSON object per line, newline-terminated, no outer array. The format streams: a writer appends as it goes, a reader decodes one line at a time, and a run killed halfway leaves a file that still parses up to the last full line. `json.NewEncoder(w).Encode(op)` writes exactly that (it appends the newline). Wrap the writer in a `bufio.Writer` and flush on close; under a mutex, since every probe writes to it.

```go
func (r *Recorder) write(op Op) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enc.Encode(op)
}
```

While developing, `jq -c 'select(.Kind=="lock")' history.jsonl` is your friend.

**A seeded workload.** Like 11.1, every random choice (which key, which value, read or write) comes from a `*rand.Rand` built from `-seed`, one per probe so they do not fight over it. The same seed gives the same sequence of requests; the cluster's answers may differ, and that difference is what the checker reads.

## What to do

1. Define `Op` in package `history` as in the reference, and create `cmd/history` with flags `-urls` (comma-separated node URLs), `-seed`, `-duration` and `-out` (default stdout).
2. Write the recorder: a mutex, a `bufio.Writer`, a `json.Encoder`; `write(op)` and `Close()`.
3. Write the four probes, each a function `(ctx, id, rec)` with its own client and generator:
   - **kv**: random `put` and `get` over a small key set (`kv:0` to `kv:9`), recording the version returned.
   - **counter**: read `counter`, CAS it to value plus one with the version read, record `ok` or the mismatch; never retry blindly.
   - **lock**: `Acquire`, record it with the token, append the token to `lock-log` with CAS, `Release`, record that too.
   - **watch**: range over `Watch` on `kv:` and record every event with its ID.
4. Start several of each probe (say 4 kv, 2 counter, 3 lock, 1 watch) spread across the node URLs, wait for `-duration`, cancel, `wg.Wait()`, close the recorder.
5. Run it for 30 seconds against devcluster and look at the file with `jq`.

## Example

```bash
$ go run ./cmd/devcluster &
$ go run ./cmd/history \
    -urls http://127.0.0.1:8081,http://127.0.0.1:8082 \
    -seed 1 -duration 30s -out history.jsonl
$ wc -l history.jsonl
48213 history.jsonl
$ head -c 400 history.jsonl
{"Client":0,"Kind":"put","Key":"kv:3","Value":"a81f","Token":0,
 "Start":"2026-10-31T10:00:00.001Z","End":"2026-10-31T10:00:00.004Z",
 "OK":true,"Err":"","Version":1}
$ jq -c 'select(.Kind=="lock")' history.jsonl | head -2
{"Client":7,"Kind":"acquire","Key":"lock","Token":12,...,"OK":true}
{"Client":7,"Kind":"append","Key":"lock-log","Token":12,...,"OK":true}
```

Every line is one operation with a start, an end, and an outcome.

## Done when

- [ ] Every op has `Start` before `End` and a non-empty `Kind`
- [ ] The watch probe records strictly increasing event IDs, one per successful change to a watched key
- [ ] Two runs with the same seed produce the same sequence of requests from every probe
- [ ] A 30 second run against devcluster contains every probe kind

## Reference

The exact contract the tests will hold you to.

```go
// history/op.go, imported by cmd/history
package history

type Op struct {
	Client  int
	Kind    string // put|get|cas|acquire|append|release|watch
	Key     string
	Value   string
	Token   uint64 // fencing token for lock ops; event ID for watch ops
	Start   time.Time
	End     time.Time
	OK      bool
	Err     string
	Version uint64
}
// Probes: kv (random put/get), counter (CAS increments), lock
// (acquire, append token to a shared log key, release), watch (records
// every event ID seen).
// history -urls <u1,u2,...> -seed N -duration 30s [-out history.jsonl]
```

What this level builds:

- Probe goroutines on burrow.Client
- JSON-lines writer
- Seeded workload

Decisions the tests rely on:

- Output is one JSON object per line, in the order the recorder received them, flushed on exit.
- `Start` is taken immediately before the client call and `End` immediately after it returns.
- The lock probe appends its fencing token to one shared key with CAS, so overlapping holders would produce out-of-order tokens for the checker to find.
- A failed op has `OK: false` and `Err` set to the error text; `Version` is 0 unless the response carried one.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.2/files/`, matching the earlier chapters.

The suite will cover:

- Every op has start < end and a kind
- Watch probe records strictly increasing IDs with no event missing (IDs are log indexes, so they skip where an entry changed nothing)
- Seeded runs produce the same op sequence

## Try it

```bash
./burrow watch
go run ./cmd/history -urls http://127.0.0.1:8081 -seed 1 -duration 10s \
    -out history.jsonl
jq -c 'select(.OK==false)' history.jsonl | head
```

## In the wild

Jepsen's generators; k6 and vegeta for plain load

## New in Go

concurrent clients · JSON lines · time measurement

Standard library: `encoding/json`, `time`, `sync`, `math/rand/v2`
