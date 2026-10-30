# 9.1 · Multi-core

> One command, three servers, and a clean shutdown that leaves nothing behind.

## Your goal

Write `cmd/devcluster`, a program that starts three `burrowd` processes on one machine, shows their logs side by side with a `n1 |` prefix on every line, waits until each one reports ready, prints `cluster ready`, and stops them all on Ctrl-C. The three nodes don't talk to each other yet; that is the next level. This level is about *supervising child processes*: starting them, reading their output, knowing when they are up, and making sure they die when you do.

On the way, `burrowd serve` gains the flags a node in a cluster needs: its own name, the addresses of its peers, and the address it will listen on for them.

## What you're using from before

**Durable daemon (5.1 · Non-volatile Daemon).** `burrowd serve -addr -data` is the program devcluster runs, three times, each with its own `-data` directory so they don't share a log. devcluster builds the argument list and starts it with `os/exec`:

```go
args := []string{"serve", "-id", n.ID, "-addr", n.Addr, "-http", n.HTTP,
	"-admin", n.Admin, "-data", n.Data,
	"-peer-addr", n.PeerAddr, "-peers", peers}
cmd := exec.CommandContext(ctx, "burrowd", args...)
```

**Admin port (7.6 · Gauges).** Each node's `-admin` port serves `GET /readyz`, which is 503 until replay has finished and 200 after. "Ready" for devcluster means every node's `/readyz` returned 200. Give each node its own admin port and poll it.

**HTTP client lib (7.5 · Client Library).** The readiness poll uses the configured `http.Client` from 7.5 (the one with timeouts and keep-alive set), not `http.DefaultClient`. Export a small constructor for it if you haven't already (the name is yours):

```go
hc := burrow.NewHTTPClient() // the *http.Client that burrow.New builds
resp, err := hc.Get("http://" + n.Admin + "/readyz")
ready := err == nil && resp.StatusCode == 200
```

## What's new here

**Running another program with `os/exec`.** `exec.Command(name, args...)` describes a process; `Start` runs it and returns at once; `Wait` blocks until it exits and reports its exit status. `name` is looked up on `PATH` unless it contains a slash, so build `burrowd` first or point at `./burrowd`.

**`exec.CommandContext`.** The same, but the child is killed when `ctx` is cancelled. Give every node the one context that Ctrl-C cancels and teardown is mostly done for you. Set `cmd.Cancel` to send `SIGTERM` instead of the default `SIGKILL` so the nodes shut down the clean way from 3.5, and `cmd.WaitDelay` so a node that ignores it is killed anyway:

```go
cmd := exec.CommandContext(ctx, "./burrowd", args...)
cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
cmd.WaitDelay = 5 * time.Second
```

**Pipes.** `cmd.StdoutPipe()` returns an `io.ReadCloser` connected to the child's stdout. Read it in a goroutine with a `bufio.Scanner` and print each line with a prefix. Do the same for `StderrPipe`. Read both to the end before calling `Wait`, and never leave a pipe unread: a child that fills a pipe nobody drains blocks on its next write and hangs forever.

```go
out, _ := cmd.StdoutPipe()
go func() {
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		fmt.Printf("%s | %s\n", n.ID, sc.Text())
	}
}()
```

**Config structs.** A node has seven or eight settings. Passing them as separate arguments is a mess; a `Node` struct with named fields is the Go way, and a function that computes the struct for node `i` from `-base-port` keeps the port scheme in one place:

```go
type Node struct {
	ID, Addr, HTTP, Admin, PeerAddr, Data string
}

func node(i, base int, dir string) Node {
	id := fmt.Sprintf("n%d", i)
	return Node{
		ID:       id,
		PeerAddr: fmt.Sprintf("127.0.0.1:%d", base+i),
		Addr:     fmt.Sprintf("127.0.0.1:%d", base+100+i),
		// ...
		Data: filepath.Join(dir, id),
	}
}
```

The port scheme above is one that works; any scheme is fine as long as the nodes never collide and `-peers` lists the peer addresses.

## What to do

1. In `burrowd serve`, add `-id`, `-peers` and `-peer-addr`. Parse `-peers` as `name=host:port` pairs separated by commas into a `map[string]string`. Store them on the server config; nothing uses them yet. Reject a `-peers` entry without `=` with a usage error.
2. Create `cmd/devcluster/main.go` with `-n` (default 3) and `-base-port` (default 7000). Keep `main` to one line and the work in `run(args, stdout, stderr) int`, as in 0.1.
3. Build the `[]Node` from `-n` and `-base-port`, with a temp directory (or `./data/<id>`) for each node's `-data`.
4. Make a context cancelled on `SIGINT` and `SIGTERM` with `signal.NotifyContext`.
5. For each node: `exec.CommandContext` with that context, `Cancel` set to `SIGTERM`, `WaitDelay` set, both pipes drained by goroutines that prefix lines with `<id> | `, then `Start`.
6. Poll every node's `/readyz` every 100 ms until all return 200, giving up with an error after 30 s. Then print `cluster ready`.
7. Wait for the context to end, then `Wait` on every child. Exit 0 if the shutdown was from a signal.
   - If any node exits early on its own, treat it as a failure: cancel the context, wait for the rest, exit 1.
8. Check with `pgrep burrowd` after Ctrl-C. Nothing should be left.

## Example

```bash
$ go build -o burrowd ./cmd/burrowd
$ go run ./cmd/devcluster
n1 | listening on 127.0.0.1:7101
n2 | listening on 127.0.0.1:7102
n1 | http on 127.0.0.1:7201
n3 | listening on 127.0.0.1:7103
n2 | http on 127.0.0.1:7202
n3 | http on 127.0.0.1:7203
cluster ready
^C
n1 | shutting down
n2 | shutting down
n3 | shutting down
$ pgrep burrowd || echo clean
clean
```

The three stores are independent for now: a key set on n1 is not on n2.

```bash
$ go run ./cmd/burrowctl -http http://127.0.0.1:7201 set k v
OK 1
$ go run ./cmd/burrowctl -http http://127.0.0.1:7202 get k
not found
```

## Done when

- [ ] `go run ./cmd/devcluster` starts `n1` to `n3`, each becomes ready on its own admin port, and `cluster ready` is printed
- [ ] The three nodes serve independent stores
- [ ] Every log line from a node is prefixed with its ID
- [ ] After SIGINT, devcluster exits and no `burrowd` process is left running
- [ ] `burrowd serve -id n1 -peers n1=127.0.0.1:7001,n2=... -peer-addr 127.0.0.1:7001` is accepted; a malformed `-peers` is a usage error

## Reference

```go
// burrowd serve gains:
//   -id n1
//   -peers n1=127.0.0.1:7001,n2=127.0.0.1:7002,n3=127.0.0.1:7003
//   -peer-addr 127.0.0.1:7001
// Parsed and stored; nothing listens on -peer-addr until 9.2.

// devcluster [-n 3] [-base-port 7000]
//   starts n nodes, each with its own -data directory and ports
//   prefixes every stdout/stderr line with `n1 |`
//   polls each node's /readyz with burrow's http client until all
//   are ready
//   prints `cluster ready`
//   stops all nodes on SIGINT and waits for them
```

- Node flags on `burrowd serve`
- `os/exec` with `StdoutPipe` and a prefixing scanner, one goroutine per pipe
- Readiness polling on `/readyz`
- Clean teardown: no child outlives devcluster

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/9.1/files/`, matching the earlier chapters.

Pass when: `go run ./cmd/devcluster` starts n1 to n3 with prefixed logs and prints `cluster ready`; after Ctrl-C, `pgrep burrowd` finds nothing.

The suite will cover:

- Three nodes become ready and serve independent stores
- Killing devcluster with SIGINT leaves no child processes
- Log lines are prefixed per node

## Try it

```bash
./burrow watch
go build -o burrowd ./cmd/burrowd
go run ./cmd/devcluster
# Ctrl-C, then:
pgrep burrowd || echo clean
```

## In the wild

goreman/foreman, docker compose, kind; the readiness loop is what Kubernetes does for you.

## New in Go

os/exec · exec.CommandContext · pipes · config structs

Standard library: `os/exec`, `flag`, `bufio`, `io`
