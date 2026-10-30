# 5.1 · Non-volatile Daemon

> Two finished parts become a product. The server you wrote in chapter 3 and the store you wrote in chapter 4 meet in one `main`.

## Your goal

Give `burrowd serve` a `-data <dir>` flag. With it, the daemon serves the frame protocol from the durable store instead of the in-memory one, so a `kill -9` (the signal a process cannot catch or ignore) loses nothing that a client was told succeeded. The daemon also has to keep the log from growing forever: it takes a snapshot when it shuts down cleanly and whenever the log reaches `-snapshot-every` records. A new `burrowd compact -data <dir>` command takes that same snapshot for a daemon that is not running.

There is almost no new logic here. The work is wiring, plus proving the wiring with the real binary: build it, start it, kill it, start it again, and read the keys back.

## What you're using from before

**Durable store (4.2 · Non-volatile Store).** `durable.Open(dir)` returns a `*durable.Store` that is a `store.Store` backed by `<dir>/wal.log`. It replays the log before it returns, so the moment `Open` comes back the store holds everything that was ever acknowledged. `LogLen()` tells you how many records have been appended since the last snapshot. This level picks it as the engine when `-data` is set:

```go
ds, err := durable.Open(dataDir)
if err != nil {
    fmt.Fprintf(stderr, "burrowd: %v\n", err)
    return 1
}
defer ds.Close()
```

**Snapshotter (4.4 · Compaction).** `ds.Snapshot()` writes `snapshot.db` and resets the log, so `LogLen()` goes back to 0. This level calls it in two places: from a goroutine when `LogLen()` reaches the threshold, and once more on shutdown after the last request has been answered.

**Shutdown (3.5 · Power Down).** `burrowd serve` already has `signal.NotifyContext`, a 10 s budget and `wire.Server.Shutdown(ctx)`, which drains in-flight requests. The one change: the final snapshot goes after `Shutdown` returns and before the store is closed, so no write can race the file.

```go
<-ctx.Done()                 // SIGINT or SIGTERM arrived
srv.Shutdown(shutdownCtx)    // 3.5: drains, no new writes after this
ds.Snapshot()                // 4.4: state to snapshot.db, log emptied
ds.Close()
```

**Group commit (4.5 · Overclock).** Nothing to call. The batched fsync lives inside the log, which is why 500 writes from the test finish in well under a second instead of 500 fsyncs later.

**Deadline-aware client (3.4 · Deadlines).** The tests talk to the running daemon with `client.Dial` and the usual `Set`, `SetWithTTL`, `CompareAndSwap`, `Get` and `Keys`. You use the same client through `burrowctl`.

## What's new here

**Composition through an interface.** In 3.5 `serve` built a `store.NewMemory()` and handed it to `wire.Server{Store: ...}`. The field's type is the interface `store.Store`, not `*store.Memory`, so the engine is a one-line decision and nothing downstream changes:

```go
var st store.Store = store.NewMemory()
if dataDir != "" {
    ds, err := durable.Open(dataDir)
    // ...
    st = ds
}
srv := &wire.Server{Store: st}
```

Keep the `*durable.Store` in its own variable too (`ds` above) because `Snapshot` and `LogLen` are not part of `store.Store`; they exist only on the concrete type. This is what the interface bought you in chapter 1: the network layer never learns which engine it is talking to.

**Flags with defaults.** `flag.FlagSet` (you used it for `burrowctl` in 3.3) turns a default and a help string into a parsed value. A string flag that is empty by default is the usual way to say "optional":

```go
fs := flag.NewFlagSet("serve", flag.ContinueOnError)
addr := fs.String("addr", "127.0.0.1:7070", "listen address")
dataDir := fs.String("data", "", "data directory (empty: in-memory)")
every := fs.Int("snapshot-every", 10000, "snapshot after N log records")
if err := fs.Parse(args); err != nil {
    return 2
}
// *dataDir == "": the Memory store; otherwise durable.Open(*dataDir)
```

`fs.String` returns a `*string`, so read it with `*dataDir`. `compact` gets its own `FlagSet` with just `-data`, and no `-data` is a usage mistake (exit 2), the same as a bad command in 0.1.

**A background job on a ticker.** The log length only grows when clients write, and you do not want a client's `Set` to wait for a whole snapshot. So a goroutine checks `LogLen()` on a `time.Ticker` and snapshots when it is past the threshold. The shape is the one from the janitor in 1.5: a loop with `select` over the ticker and a `done` channel, stopped and waited for during shutdown.

```go
go func() {
    t := time.NewTicker(50 * time.Millisecond)
    defer t.Stop()
    for {
        select {
        case <-done:
            return
        case <-t.C:
            if ds.LogLen() >= *every {
                ds.Snapshot() // pauses writers only briefly (4.4)
            }
        }
    }
}()
```

**Testing a process, not a function.** The suite does not call `run` for the daemon tests. It runs `go build`, starts the binary with `os/exec`, reads the `listening on` line, talks to it over TCP, and then sends it a signal. Two signals matter here. `SIGTERM` is a polite request: your `signal.NotifyContext` sees it and the daemon shuts down cleanly. `SIGKILL` (`kill -9`) is not delivered to the program at all; the kernel just stops it. Nothing you write runs after a `SIGKILL`, so the only thing that survives it is what was already on disk. That is the whole point of chapter 4, and this level is where you get to see it.

## What to do

1. Add `-data` (string, default empty) and `-snapshot-every` (int, default 10000) to `serve`'s flag set, next to `-addr`.
2. Pick the engine. No `-data`: `store.NewMemory()`, exactly as before. With `-data`: `durable.Open(dir)`; an `Open` error is reported on stderr with exit 1. Keep a `*durable.Store` variable as well as the `store.Store` one.
3. Only after `Open` has returned (replay and snapshot loading are done) listen and print `listening on <addr>`. If you were already listening first, move the `Open` call up.
4. Start the snapshot goroutine when a durable store is in use: on a ticker, `if ds.LogLen() >= every { ds.Snapshot() }`. Log a snapshot error to stderr; don't exit.
5. On `SIGINT` or `SIGTERM`: stop the ticker goroutine, run `Shutdown` with the 10 s budget as before, then `Snapshot()`, then `Close()`, then exit 0.
6. Add the `compact` command: `burrowd compact -data <dir>` opens the store, calls `Snapshot()`, closes it and returns 0. Without `-data` print a usage error and return 2. It is for a stopped daemon; it takes no lock.
7. Update the usage text so `help` mentions `compact` and the new flags.

## Example

```bash
$ go build -o burrowd ./cmd/burrowd
$ ./burrowd serve -data ./data &
listening on 127.0.0.1:7070
$ go run ./cmd/burrowctl set colour green
OK 1
$ go run ./cmd/burrowctl ttl session 1h abc
OK 1
$ kill -9 %1                      # no chance to clean up
$ ./burrowd serve -data ./data &
listening on 127.0.0.1:7070
$ go run ./cmd/burrowctl get colour
green
$ ls data
wal.log
$ kill -TERM %1                   # a clean stop snapshots
shutting down
$ ls -l data
-rw-r--r--  1 you  staff  301 snapshot.db
-rw-r--r--  1 you  staff    0 wal.log
$ ./burrowd compact -data ./data; echo "exit: $?"
exit: 0
$ ./burrowd compact; echo "exit: $?"
burrowd: compact needs -data
exit: 2
```

With `-snapshot-every 100`, writing 350 keys in a loop makes `snapshot.db` appear partway through and keeps `wal.log` small while the daemon runs.

## Done when

- [ ] `burrowd serve -data <dir>` serves from `durable.Open(dir)`; without `-data` it serves from memory as before
- [ ] 500 keys set through the client (plain, with TTL, and by CAS) all come back after `kill -9` and a restart, with the same version and the same absolute expiry
- [ ] `SIGTERM` exits 0 within 10 s and leaves `snapshot.db` plus a nearly empty `wal.log`; a restart still serves every key
- [ ] With `-snapshot-every 100` the log never holds more than 100 records for long, and a restart has the latest value
- [ ] `burrowd compact -data <dir>` empties the log and keeps the state; `compact` with no `-data` exits 2
- [ ] `listening on` is printed only after replay, so a client that connects right after it sees every key

## Reference

The exact contract the tests hold you to.

```go
// cmd/burrowd
//   burrowd serve [-addr 127.0.0.1:7070] [-data <dir>]
//                 [-snapshot-every 10000]
// With -data, serve from durable.Open(dir); without it, from a Memory
// store (dev mode).
// Snapshot after every -snapshot-every log records and on clean
// shutdown.
//   burrowd compact -data <dir>   takes a snapshot offline and exits 0
// Startup prints `listening on <addr>` only after replay has finished.
```

Decisions the tests rely on:

- `-snapshot-every` counts records appended since the last snapshot.
- `burrowd compact` is for a stopped daemon; it takes no lock.
- `listening on` is printed only after replay and snapshot loading have finished.

## The tests check

- Build the binary; start with -data in a temp dir; set 500 keys (some with TTL, some via CAS) with client.Client; SIGKILL; restart; every key reads back with the same Version and an ExpiresAt that hasn't moved
- SIGTERM: exit 0 within 10 s, snapshot.db exists, wal.log is under 64 bytes, and a restart serves the same 50 keys
- -snapshot-every 100 with 350 writes: snapshot.db appears mid-run, the log holds at most 100 records (read with wal.Open on a copy), and a restart has the latest value
- `burrowd compact -data` on a stopped data dir empties the log and preserves state; `compact` without -data is a usage error (exit 2)
- `listening on` is not printed until replay of a 50,000-record log is complete: a client dialing right after the line sees every key

## Try it

```bash
./burrow watch
go build -o burrowd ./cmd/burrowd
./burrowd serve -data ./data &
go run ./cmd/burrowctl set k v
kill -9 %1
./burrowd serve -data ./data &
go run ./cmd/burrowctl get k   # v
```

## In the wild

This is `redis-server --appendonly yes` or `etcd --data-dir`; the shape of every stateful service's main().

## New in Go

composition through an interface · flags with defaults · SIGTERM vs SIGKILL · process lifecycle testing with os/exec

Standard library: `flag`, `os`, `os/signal`, `os/exec`, `time`
