# 5.1 · Non-volatile Daemon

Two finished subsystems become a product. `burrowd serve -data` serves the frame protocol from the durable store, and the test proves it with the real binary: kill -9, restart, and `burrowctl` still reads every acknowledged key.

## Build

```go
// cmd/burrowd
//   burrowd serve [-addr 127.0.0.1:7070] [-data <dir>] [-snapshot-every 10000]
// With -data, serve from durable.Open(dir); without it, from a Memory store (dev mode).
// Snapshot after every -snapshot-every log records and on clean shutdown.
//   burrowd compact -data <dir>   takes a snapshot offline and exits 0
// Startup prints `listening on <addr>` only after replay has finished.
```

- `-data` flag wiring durable.Store into `serve`
- Snapshot on shutdown and whenever durable.Store.LogLen() reaches -snapshot-every
- `burrowd compact`

Decisions the tests rely on:

- `-snapshot-every` counts records appended since the last snapshot.
- `burrowd compact` is for a stopped daemon; it takes no lock.
- `listening on` is printed only after replay and snapshot loading have finished.

## The tests check

- Build the binary; start with -data in a temp dir; set 500 keys (some with TTL, some via CAS) with client.Client; SIGKILL; restart; every key reads back with the same Version and ExpiresAt
- SIGTERM: exit 0, snapshot.db exists, wal.log is under 64 bytes, and a restart serves the same state
- -snapshot-every 100 with 350 writes: snapshot.db exists mid-run and the log holds at most 100 records
- `burrowd compact -data` on a stopped data dir shrinks the log and preserves state
- `listening on` is not printed until replay of a 50,000-record log is complete (a client dialing right after the line sees all keys)
- Without -data the daemon still works from memory (3.5's tests still pass)

## Try it

```bash
go build -o burrowd ./cmd/burrowd
./burrowd serve -data ./data &
go run ./cmd/burrowctl set k v
kill -9 %1
./burrowd serve -data ./data &
go run ./cmd/burrowctl get k   # v
```

## Uses

- Shutdown (built in 3.5 · Power Down)
- Durable store (built in 4.2 · Non-volatile Store)
- Snapshotter (built in 4.4 · Compaction)
- Group commit (built in 4.5 · Overclock)
- Deadline-aware client (built in 3.4 · Deadlines)

## In the wild

This is `redis-server --appendonly yes` or `etcd --data-dir`; the shape of every stateful service's main()

## New in Go

composition · flags with defaults · process lifecycle testing

Standard library: `flag`, `os`, `os/signal`, `os/exec`
