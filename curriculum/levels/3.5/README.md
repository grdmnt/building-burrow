# 3.5 · Power Down

Stop on Ctrl-C without dropping requests that are already in progress. The connection tracker you write here is a package of its own, because the hand-written HTTP server (H.3) needs exactly the same drain logic.

## Build

```go
// conntrack/conntrack.go
package conntrack

// Tracker records which connections are idle (waiting for a request) and which are busy.
type Tracker struct { /* yours */ }

func New() *Tracker
func (t *Tracker) Add(c net.Conn)            // starts idle
func (t *Tracker) SetBusy(c net.Conn, busy bool)
func (t *Tracker) Remove(c net.Conn)
func (t *Tracker) Len() int
func (t *Tracker) CloseIdle() int            // closes and removes idle connections; returns how many
func (t *Tracker) CloseAll() int
// Drain closes idle connections now and again every few ms, until no connections remain
// or ctx is done, in which case it closes everything and returns ctx.Err().
func (t *Tracker) Drain(ctx context.Context) error

// wire/server.go additions
var ErrServerClosed = errors.New("wire: server closed")

// Shutdown stops accepting, lets in-flight requests finish and get their replies, closes
// idle connections at once and busy ones when they go idle. If ctx ends first it
// force-closes everything and returns ctx.Err(). Safe to call more than once, and concurrently.
// After Shutdown, Serve returns ErrServerClosed, including a Serve called later.
func (s *Server) Shutdown(ctx context.Context) error

// cmd/burrowd
//   burrowd serve [-addr 127.0.0.1:7070]
// Prints `listening on <actual address>` as its first stdout line (so -addr :0 is usable).
// Serves the frame protocol from a Memory store. On SIGINT or SIGTERM: logs `shutting down`
// to stderr, Shutdown with a 10 s budget, exits 0. Bad address exits 1; stray args exit 2.
```

- `conntrack.Tracker` with Drain
- Mark connections busy from a full request read until its reply is written
- `Server.Shutdown(ctx)` and `ErrServerClosed`
- `burrowd serve` with `signal.NotifyContext`

Decisions the tests rely on:

- `Drain` polls every 5 ms.
- A connection is busy from the moment a full frame has been read until its reply has been flushed.
- `Shutdown` closes the listener first, then drains. `burrowd serve` gives `Shutdown` a 10 s budget.

## The tests check

- conntrack: Add/SetBusy/Remove/Len; CloseIdle closes only idle connections and removes them; CloseAll closes busy and idle alike; Drain returns nil once a busy connection goes idle (and waits for it); Drain with a stuck connection returns ctx.Err() at the deadline and force-closes everything; 100 goroutines mutating the tracker under -race
- Shutdown with no connections is instant; 5 idle connections are closed right away (their reads see EOF)
- A request mid-flight (a fake store blocking 300 ms) still gets its reply, then that connection closes
- A 100 ms ctx against a 3 s request: Shutdown returns context.DeadlineExceeded and force-closes
- No new connections are served after Shutdown starts; concurrent and repeated Shutdowns all return nil; Serve after Shutdown returns ErrServerClosed
- testkit.NoLeaks across the whole suite
- The real binary: build, start with -addr 127.0.0.1:0, parse the `listening on` line, set and get a key with client.Client, send SIGINT then (fresh process) SIGTERM; both exit 0 within 5 s with `shutting down` on stderr
- burrowd serve with an unusable address exits 1 and explains on stderr; `serve extra` and `serve -bogus` exit 2

## Try it

```bash
go run ./cmd/burrowd serve &
go run ./cmd/burrowctl set k v
kill -TERM %1
```

## Uses

- Frame server (built in 3.2 · Switchboard)
- Deadline-aware client (built in 3.4 · Deadlines)
- burrowd skeleton (built in 0.1 · Power On)

## In the wild

net/http.Server.Shutdown tracks exactly these states (StateNew/Active/Idle); Kubernetes sends SIGTERM and waits terminationGracePeriodSeconds, which is this ctx

## New in Go

os/signal.NotifyContext · shutdown ordering · bounded waits · sync.Once · syscall.SIGTERM

Standard library: `os/signal`, `syscall`, `context`, `sync`, `errors`, `net`
