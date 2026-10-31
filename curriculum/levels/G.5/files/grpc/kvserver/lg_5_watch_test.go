package kvserver_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"burrow/grpc/burrowpb"
	"burrow/grpc/kvserver"
	"burrow/internal/testkit"
	"burrow/store"
	"burrow/watch"
)

var _ kvserver.Broker = (*watch.Broker)(nil)

// countingBroker is a real broker that counts open subscriptions, so a
// test can see that every stream unsubscribed.
type countingBroker struct {
	*watch.Broker
	open atomic.Int64
}

func (c *countingBroker) wrap(stop func()) func() {
	c.open.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() { c.open.Add(-1) })
		stop()
	}
}

func (c *countingBroker) Subscribe(prefix string) (<-chan watch.Event, uint64, func()) {
	ch, last, stop := c.Broker.Subscribe(prefix)
	return ch, last, c.wrap(stop)
}

func (c *countingBroker) SubscribeFrom(prefix string, after uint64) (<-chan watch.Event, func(), error) {
	ch, stop, err := c.Broker.SubscribeFrom(prefix, after)
	if err != nil {
		return ch, stop, err
	}
	return ch, c.wrap(stop), nil
}

type watchBench struct {
	*rig
	b    *countingBroker
	st   *watch.Store
	logs *logBuf
}

// watchRig serves a memory store wrapped with watch.Wrap, publishing to
// a broker with the given buffer and history.
func watchRig(t testing.TB, buffer, history int, opts ...store.Option) *watchBench {
	t.Helper()
	b := &countingBroker{Broker: watch.NewBroker(buffer, history)}
	var ws *watch.Store
	opts = append(opts, store.WithOnEvict(func(e store.Entry) { ws.OnEvict(e) }))
	m := store.NewMemory(opts...)
	t.Cleanup(func() { m.Close() })
	ws = watch.Wrap(m, b.Broker)
	logs := &logBuf{}
	r := serve(t, &kvserver.Server{Store: ws, Broker: b, Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	return &watchBench{rig: r, b: b, st: ws, logs: logs}
}

func (w *watchBench) set(t testing.TB, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, err := w.st.Set(context.Background(), k, []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
}

// watchStream opens a Watch with its own cancel.
func watchStream(t testing.TB, kv burrowpb.KVClient, req *burrowpb.WatchRequest) (grpc.ServerStreamingClient[burrowpb.Event], context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s, err := kv.Watch(ctx, req)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return s, cancel
}

func recv(t testing.TB, s grpc.ServerStreamingClient[burrowpb.Event]) *burrowpb.Event {
	t.Helper()
	ev, err := s.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	return ev
}

func expectReady(t testing.TB, s grpc.ServerStreamingClient[burrowpb.Event], id uint64) {
	t.Helper()
	ev := recv(t, s)
	if ev.GetType() != burrowpb.EventType_EVENT_TYPE_READY || ev.GetId() != id {
		t.Fatalf("first event is %v %d, want EVENT_TYPE_READY with id %d", ev.GetType(), ev.GetId(), id)
	}
	if ev.GetEntry() != nil {
		t.Errorf("READY carries no entry, got %v", ev.GetEntry())
	}
}

func expectIDs(t testing.TB, s grpc.ServerStreamingClient[burrowpb.Event], ids ...uint64) {
	t.Helper()
	for _, id := range ids {
		if ev := recv(t, s); ev.GetId() != id {
			t.Fatalf("got event %d (%v %s), want %d", ev.GetId(), ev.GetType(), ev.GetEntry().GetKey(), id)
		}
	}
}

func TestLG_5_ReadyThenLive(t *testing.T) {
	w := watchRig(t, 64, 64)
	w.set(t, "user:1", "user:2", "cfg:x") // 1, 2, 3
	s, _ := watchStream(t, w.kv, &burrowpb.WatchRequest{Prefix: "user:"})
	expectReady(t, s, 3)

	ctx := ctxT(t)
	w.kv.Put(ctx, &burrowpb.PutRequest{Key: "user:3", Value: []byte("cy")}) // 4
	w.kv.Put(ctx, &burrowpb.PutRequest{Key: "cfg:y", Value: []byte("1")})   // 5, filtered
	w.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "user:1"})                // 6

	ev := recv(t, s)
	if ev.GetId() != 4 || ev.GetType() != burrowpb.EventType_EVENT_TYPE_PUT {
		t.Fatalf("got %v %d, want EVENT_TYPE_PUT 4", ev.GetType(), ev.GetId())
	}
	if e := ev.GetEntry(); e.GetKey() != "user:3" || string(e.GetValue()) != "cy" || e.GetVersion() != 1 {
		t.Errorf("put event entry = %v, want user:3 = cy at version 1", e)
	}
	ev = recv(t, s)
	if ev.GetId() != 6 || ev.GetType() != burrowpb.EventType_EVENT_TYPE_DELETE || ev.GetEntry().GetKey() != "user:1" {
		t.Errorf("got %v %d %v, want EVENT_TYPE_DELETE 6 for user:1 (cfg:y is filtered out)", ev.GetType(), ev.GetId(), ev.GetEntry())
	}
}

func TestLG_5_Expire(t *testing.T) {
	clock := testkit.NewFakeClock()
	w := watchRig(t, 64, 64, store.WithClock(clock.Now))
	ctx := ctxT(t)
	w.kv.Put(ctx, &burrowpb.PutRequest{Key: "lease", Value: []byte("me"), TtlMs: 1000})
	s, _ := watchStream(t, w.kv, &burrowpb.WatchRequest{Prefix: "lease"})
	expectReady(t, s, 1)
	clock.Advance(2 * time.Second)
	w.kv.Get(ctx, &burrowpb.GetRequest{Key: "lease"})
	ev := recv(t, s)
	if ev.GetType() != burrowpb.EventType_EVENT_TYPE_EXPIRE || ev.GetEntry().GetKey() != "lease" ||
		ev.GetEntry().GetExpiresAtUnixNano() == 0 {
		t.Errorf("got %v %v, want EVENT_TYPE_EXPIRE for lease with its expiry", ev.GetType(), ev.GetEntry())
	}
}

func TestLG_5_Resume(t *testing.T) {
	w := watchRig(t, 64, 64)
	w.set(t, "k1", "k2", "k3", "k4", "k5")

	s, _ := watchStream(t, w.kv, &burrowpb.WatchRequest{Prefix: "k", After: proto.Uint64(2)})
	expectReady(t, s, 2)
	expectIDs(t, s, 3, 4, 5)
	w.set(t, "k6")
	expectIDs(t, s, 6)

	// after set to 0 is not the same as after unset: it means
	// "everything you still have".
	s, _ = watchStream(t, w.kv, &burrowpb.WatchRequest{Prefix: "k", After: proto.Uint64(0)})
	expectReady(t, s, 0)
	expectIDs(t, s, 1, 2, 3, 4, 5, 6)

	s, _ = watchStream(t, w.kv, &burrowpb.WatchRequest{Prefix: "k"})
	expectReady(t, s, 6)
	w.set(t, "k7")
	expectIDs(t, s, 7)
}

func TestLG_5_CannotResume(t *testing.T) {
	w := watchRig(t, 64, 8)
	for i := range 20 {
		w.set(t, fmt.Sprintf("k%02d", i))
	}
	for _, after := range []uint64{11, 99} {
		s, _ := watchStream(t, w.kv, &burrowpb.WatchRequest{After: proto.Uint64(after)})
		_, err := s.Recv()
		code, why := reason(t, err)
		if code != codes.OutOfRange || why != "CANNOT_RESUME" {
			t.Errorf("Watch after %d with history 8 and last 20: %v %q, want OutOfRange CANNOT_RESUME", after, code, why)
		}
	}
	s, _ := watchStream(t, w.kv, &burrowpb.WatchRequest{After: proto.Uint64(12)})
	expectReady(t, s, 12)
	expectIDs(t, s, 13, 14, 15, 16, 17, 18, 19, 20)
	testkit.Eventually(t, 2*time.Second, func() bool { return w.b.open.Load() == 1 },
		"one open subscription (a refused resume must not subscribe); open: %d", w.b.open.Load())
}

// A writer runs the whole time while a client reconnects again and
// again with the last ID it saw. The IDs it collects must have no gaps
// and no duplicates.
func TestLG_5_HandoverUnderLoad(t *testing.T) {
	testkit.RequireRace(t)
	w := watchRig(t, 4096, 1<<14)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			w.st.Set(context.Background(), fmt.Sprintf("k%d", i%100), []byte("v"))
			time.Sleep(100 * time.Microsecond)
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
	}()

	req := &burrowpb.WatchRequest{}
	var last uint64
	var got []uint64
	for round := range 100 {
		s, cancel := watchStream(t, w.kv, req)
		ready := recv(t, s)
		if ready.GetType() != burrowpb.EventType_EVENT_TYPE_READY {
			t.Fatalf("round %d: first event %v, want READY", round, ready.GetType())
		}
		if round == 0 {
			last = ready.GetId()
		} else if ready.GetId() != last {
			t.Fatalf("round %d: resumed READY id %d, want the after value %d", round, ready.GetId(), last)
		}
		for range 1 + rand.IntN(20) {
			ev := recv(t, s)
			got = append(got, ev.GetId())
			last = ev.GetId()
		}
		cancel()
		req = &burrowpb.WatchRequest{After: proto.Uint64(last)}
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]+1 {
			t.Fatalf("IDs across reconnects jump from %d to %d: a gap or a duplicate at the replay/live handover", got[i-1], got[i])
		}
	}
}

func TestLG_5_Cancel(t *testing.T) {
	w := watchRig(t, 64, 64)
	var cancels []context.CancelFunc
	var streams []grpc.ServerStreamingClient[burrowpb.Event]
	for i := range 3 {
		req := &burrowpb.WatchRequest{}
		if i == 2 {
			req.After = proto.Uint64(0)
		}
		s, cancel := watchStream(t, w.kv, req)
		expectReady(t, s, 0)
		streams = append(streams, s)
		cancels = append(cancels, cancel)
	}
	if n := w.b.open.Load(); n != 3 {
		t.Fatalf("%d open subscriptions with three streams, want 3", n)
	}
	for i, cancel := range cancels {
		cancel()
		if _, err := streams[i].Recv(); status.Code(err) != codes.Canceled {
			t.Errorf("Recv after cancel: %v, want Canceled", err)
		}
	}
	testkit.Eventually(t, 2*time.Second, func() bool { return w.b.open.Load() == 0 },
		"every subscription closed after its client cancelled; open: %d (defer the stop function)", w.b.open.Load())
}

func TestLG_5_StreamIsLogged(t *testing.T) {
	w := watchRig(t, 64, 64)
	ctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(ctxT(t), "x-request-id", "watch-trace-1"))
	defer cancel()
	s, err := w.kv.Watch(ctx, &burrowpb.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	expectReady(t, s, 0)
	w.set(t, "a")
	expectIDs(t, s, 1)
	cancel()
	line := rpcLine(t, w.logs, "/burrow.v1.KV/Watch", "watch-trace-1")
	if line["code"] != "Canceled" || line["sent"] != float64(2) {
		t.Errorf("Watch access log: code %v sent %v, want Canceled and 2 (READY and one event)", line["code"], line["sent"])
	}
}

// closingBroker hands out channels that are already closed, the way
// *watch.Broker drops a subscriber that fell behind.
type closingBroker struct{}

func (closingBroker) Subscribe(string) (<-chan watch.Event, uint64, func()) {
	ch := make(chan watch.Event)
	close(ch)
	return ch, 7, func() {}
}

func (closingBroker) SubscribeFrom(p string, _ uint64) (<-chan watch.Event, func(), error) {
	ch, _, stop := closingBroker{}.Subscribe(p)
	return ch, stop, nil
}

func TestLG_5_FellBehind(t *testing.T) {
	r := serve(t, &kvserver.Server{Store: newMem(t), Broker: closingBroker{}})
	s, _ := watchStream(t, r.kv, &burrowpb.WatchRequest{})
	expectReady(t, s, 7)
	if _, err := s.Recv(); status.Code(err) != codes.Unavailable {
		t.Errorf("when the broker closes the channel: %v, want Unavailable (the client resumes)", err)
	}
}

func TestLG_5_NoBroker(t *testing.T) {
	r := serve(t, &kvserver.Server{Store: newMem(t)})
	s, _ := watchStream(t, r.kv, &burrowpb.WatchRequest{})
	if _, err := s.Recv(); status.Code(err) != codes.Unimplemented {
		t.Errorf("Watch on a Server with no Broker: %v, want Unimplemented", err)
	}
}

func TestLG_5_NoLeaks(t *testing.T) {
	run := func(t *testing.T) {
		w := watchRig(t, 64, 64)
		for range 3 {
			s, cancel := watchStream(t, w.kv, &burrowpb.WatchRequest{})
			recv(t, s)
			cancel()
		}
		// Left open: closing the connection must end the handler, or
		// GracefulStop never returns.
		s, err := w.kv.Watch(context.Background(), &burrowpb.WatchRequest{After: proto.Uint64(0)})
		if err != nil {
			t.Fatal(err)
		}
		recv(t, s)
	}
	t.Run("warmup", run)
	testkit.NoLeaks(t)
	t.Run("run", run)
}
