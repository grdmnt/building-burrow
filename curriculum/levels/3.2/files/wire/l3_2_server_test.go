package wire_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/wire"
)

// startFrameServer serves st on a fresh listener and checks Serve returns nil
// once the listener closes at the end of the test.
func startFrameServer(t *testing.T, st store.Store) string {
	t.Helper()
	ln := testkit.Listen(t)
	errc := make(chan error, 1)
	go func() { errc <- (&wire.Server{Store: st}).Serve(ln) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("Serve should return nil once its listener closes, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Serve didn't return within 2s of its listener closing")
		}
	})
	return ln.Addr().String()
}

func roundTrip(t *testing.T, c net.Conn, req wire.Frame) wire.Frame {
	t.Helper()
	if err := wire.WriteFrame(c, req); err != nil {
		t.Fatalf("write %v: %v", req.Op, err)
	}
	resp, err := wire.ReadFrame(c)
	if err != nil {
		t.Fatalf("reading the reply to %v %q: %v", req.Op, req.Key, err)
	}
	return resp
}

func TestL3_2_ReplyTable(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	steps := []struct {
		name    string
		req     wire.Frame
		op      wire.Op
		val     string
		version uint64
	}{
		{"get missing", wire.Frame{Op: wire.OpGet, Key: "k"}, wire.OpNotFound, "", 0},
		{"set", wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v\x00\n")}, wire.OpOK, "", 1},
		{"get", wire.Frame{Op: wire.OpGet, Key: "k"}, wire.OpValue, "v\x00\n", 1},
		{"set again", wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("two")}, wire.OpOK, "", 2},
		{"cas stale", wire.Frame{Op: wire.OpCAS, Key: "k", Version: 1, Value: []byte("x")}, wire.OpMismatch, "", 2},
		{"cas right", wire.Frame{Op: wire.OpCAS, Key: "k", Version: 2, Value: []byte("three")}, wire.OpOK, "", 3},
		{"cas create on existing", wire.Frame{Op: wire.OpCAS, Key: "k", Version: 0, Value: []byte("x")}, wire.OpMismatch, "", 3},
		{"cas missing", wire.Frame{Op: wire.OpCAS, Key: "nope", Version: 4, Value: []byte("x")}, wire.OpNotFound, "", 0},
		{"cad stale", wire.Frame{Op: wire.OpCAD, Key: "k", Version: 1}, wire.OpMismatch, "", 3},
		{"cad right", wire.Frame{Op: wire.OpCAD, Key: "k", Version: 3}, wire.OpOK, "", 0},
		{"cad missing", wire.Frame{Op: wire.OpCAD, Key: "k", Version: 3}, wire.OpNotFound, "", 0},
		{"set for delete", wire.Frame{Op: wire.OpSet, Key: "d", Value: []byte("v")}, wire.OpOK, "", 1},
		{"delete", wire.Frame{Op: wire.OpDelete, Key: "d"}, wire.OpOK, "", 0},
		{"delete missing", wire.Frame{Op: wire.OpDelete, Key: "d"}, wire.OpNotFound, "", 0},
		{"ping", wire.Frame{Op: wire.OpPing, Value: []byte("hi")}, wire.OpPong, "hi", 0},
	}
	for _, s := range steps {
		resp := roundTrip(t, c, s.req)
		if resp.Op != s.op || string(resp.Value) != s.val || resp.Version != s.version {
			t.Errorf("%s: %v %q → %v %q version %d, want %v %q version %d",
				s.name, s.req.Op, s.req.Key, resp.Op, resp.Value, resp.Version, s.op, s.val, s.version)
		}
		if resp.Key != s.req.Key {
			t.Errorf("%s: replies echo the request key; got key %q", s.name, resp.Key)
		}
	}
}

func TestL3_2_TTLFields(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	r := roundTrip(t, c, wire.Frame{Op: wire.OpSetTTL, Key: "s", Value: []byte("v"), TTL: time.Hour})
	if r.Op != wire.OpOK || r.Version != 1 {
		t.Fatalf("OpSetTTL → %v version %d", r.Op, r.Version)
	}
	r = roundTrip(t, c, wire.Frame{Op: wire.OpGet, Key: "s"})
	if r.TTL <= 59*time.Minute || r.TTL > time.Hour {
		t.Errorf("OpValue.TTL should be the time remaining (about 1h), got %v", r.TTL)
	}
	roundTrip(t, c, wire.Frame{Op: wire.OpSet, Key: "plain", Value: []byte("v")})
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpGet, Key: "plain"}); r.TTL != 0 {
		t.Errorf("OpValue.TTL is 0 for a key without expiry, got %v", r.TTL)
	}
	r = roundTrip(t, c, wire.Frame{Op: wire.OpCAS, Key: "lock", Version: 0, Value: []byte("me"), TTL: 5 * time.Second})
	if r.Op != wire.OpOK || r.Version != 1 {
		t.Fatalf("OpCAS with TTL 5s → %v version %d", r.Op, r.Version)
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpGet, Key: "lock"}); r.TTL <= 0 || r.TTL > 5*time.Second {
		t.Errorf("OpCAS with TTL > 0 is CompareAndSwapWithTTL: the key should expire in about 5s, OpValue.TTL = %v", r.TTL)
	}
	r = roundTrip(t, c, wire.Frame{Op: wire.OpTouch, Key: "plain", TTL: time.Minute})
	if r.Op != wire.OpOK || r.Version != 1 || string(r.Value) != "v" || r.TTL != time.Minute {
		t.Errorf("OpTouch's OpOK carries Value, Version and the TTL just set: got %v %q version %d ttl %v", r.Op, r.Value, r.Version, r.TTL)
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpSet, Key: "plain", Value: []byte("v2")}); len(r.Value) != 0 || r.TTL != 0 {
		t.Errorf("OpSet's OpOK carries only Version; got Value %q TTL %v", r.Value, r.TTL)
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpTouch, Key: "missing", TTL: time.Minute}); r.Op != wire.OpNotFound {
		t.Errorf("OpTouch on a missing key → %v, want NOT_FOUND", r.Op)
	}
	for _, req := range []wire.Frame{
		{Op: wire.OpSetTTL, Key: "x", Value: []byte("v"), TTL: 0},
		{Op: wire.OpTouch, Key: "plain", TTL: -time.Second},
		{Op: wire.OpCAS, Key: "y", Version: 0, Value: []byte("v"), TTL: -time.Second},
	} {
		if r := roundTrip(t, c, req); r.Op != wire.OpError {
			t.Errorf("%v with ttl %v → %v, want ERROR (invalid ttl)", req.Op, req.TTL, r.Op)
		}
	}
}

func TestL3_2_Keys(t *testing.T) {
	st := store.NewMemory()
	c := dial(t, startFrameServer(t, st))
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpKeys, Key: "user:"}); r.Op != wire.OpKeyList || len(r.Value) != 0 || r.Key != "user:" {
		t.Errorf("OpKeys with no matches → %v %q key %q, want an empty KEY_LIST echoing the prefix", r.Op, r.Value, r.Key)
	}
	st.Set(context.Background(), "user:1", []byte("v"))
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpKeys, Key: "user:"}); string(r.Value) != "user:1" {
		t.Errorf("one key: KEY_LIST value %q", r.Value)
	}
	for i := 999; i >= 0; i-- {
		st.Set(context.Background(), fmt.Sprintf("k%04d", i), []byte("v"))
	}
	r := roundTrip(t, c, wire.Frame{Op: wire.OpKeys, Key: ""})
	keys := strings.Split(string(r.Value), "\n")
	if len(keys) != 1001 || keys[0] != "k0000" || keys[999] != "k0999" || keys[1000] != "user:1" {
		t.Errorf("an empty prefix lists every key, sorted, joined by \\n: got %d keys", len(keys))
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpKeys, Key: "a b"}); r.Op != wire.OpKeyList {
		t.Errorf("a prefix isn't a key: OpKeys with prefix \"a b\" should be served (KEY_LIST), got %v", r.Op)
	}
}

func TestL3_2_InvalidKeyKeepsConnection(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	r := roundTrip(t, c, wire.Frame{Op: wire.OpSet, Key: "has space", Value: []byte("v")})
	if r.Op != wire.OpError || !strings.Contains(string(r.Value), "invalid key") {
		t.Errorf("an invalid key gets OpError mentioning \"invalid key\", got %v %q", r.Op, r.Value)
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpGet, Key: "a b"}); r.Op != wire.OpError {
		t.Errorf("OpGet with an invalid key → %v, want ERROR", r.Op)
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpPing}); r.Op != wire.OpPong {
		t.Errorf("an OpError reply keeps the connection open; next PING got %v", r.Op)
	}
}

func TestL3_2_ReplyOpAsRequest(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	for _, op := range []wire.Op{wire.OpOK, wire.OpValue, wire.OpKeyList} {
		if r := roundTrip(t, c, wire.Frame{Op: op, Key: "k"}); r.Op != wire.OpError {
			t.Errorf("a client sending %v (a reply op) should get OpError, got %v", op, r.Op)
		}
	}
	if r := roundTrip(t, c, wire.Frame{Op: wire.OpPing}); r.Op != wire.OpPong {
		t.Errorf("the connection stays open after a reply op; PING got %v", r.Op)
	}
}

func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := io.ReadAll(c)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Errorf("expected the server to close the connection")
	}
}

func TestL3_2_GarbageClosesConnection(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	payload := append([]byte{0x7f}, make([]byte, 18)...)
	wire.WriteRecord(c, payload)
	resp, err := wire.ReadFrame(c)
	if err != nil || resp.Op != wire.OpError {
		t.Fatalf("after an unknown op the server should send one OpError, got (%v, %v)", resp.Op, err)
	}
	expectClosed(t, c)
}

func TestL3_2_OversizedFrameClosesConnection(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	go wire.WriteRecord(c, append(append([]byte{0x02}, make([]byte, 16)...), append([]byte{0, 1, 'k'}, make([]byte, 9<<20)...)...))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	rest, _ := io.ReadAll(c)
	if len(rest) > 0 {
		f, err := wire.ReadFrame(bytes.NewReader(rest))
		if err != nil || f.Op != wire.OpError {
			t.Errorf("the only thing allowed before closing is one OpError frame; got %d bytes", len(rest))
		}
	}
}

func TestL3_2_Pipelined(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	var batch bytes.Buffer
	for i := range 100 {
		wire.WriteFrame(&batch, wire.Frame{Op: wire.OpSet, Key: "p", Value: []byte{byte(i)}})
	}
	wire.WriteFrame(&batch, wire.Frame{Op: wire.OpGet, Key: "p"})
	c.Write(batch.Bytes())
	for i := range 100 {
		if f, err := wire.ReadFrame(c); err != nil || f.Op != wire.OpOK || f.Version != uint64(i+1) {
			t.Fatalf("pipelined reply %d: %v version %d (%v)", i, f.Op, f.Version, err)
		}
	}
	if f, _ := wire.ReadFrame(c); !bytes.Equal(f.Value, []byte{99}) {
		t.Errorf("after 100 pipelined SETs, GET returned %v, want [99]: requests on one connection run in order", f.Value)
	}
}

func TestL3_2_ManyClients(t *testing.T) {
	addr := startFrameServer(t, store.NewMemory())
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			key := fmt.Sprintf("client-%d", i)
			for j := range 50 {
				val := []byte{byte(i), byte(j)}
				wire.WriteFrame(c, wire.Frame{Op: wire.OpSet, Key: key, Value: val})
				if f, err := wire.ReadFrame(c); err != nil || f.Op != wire.OpOK || f.Key != key {
					t.Errorf("set: %v %q %v", f.Op, f.Key, err)
					return
				}
				wire.WriteFrame(c, wire.Frame{Op: wire.OpGet, Key: key})
				if f, err := wire.ReadFrame(c); err != nil || !bytes.Equal(f.Value, val) || f.Key != key {
					t.Errorf("client %d read back %q %v, want %q %v (%v)", i, f.Key, f.Value, key, val, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// blockingStore's Get blocks until release is closed.
type blockingStore struct {
	store.Store
	release chan struct{}
}

func (b *blockingStore) Get(ctx context.Context, k string) (store.Entry, error) {
	if k == "slow" {
		<-b.release
	}
	return b.Store.Get(ctx, k)
}

func TestL3_2_SlowConnectionDoesNotBlockOthers(t *testing.T) {
	bs := &blockingStore{Store: store.NewMemory(), release: make(chan struct{})}
	defer close(bs.release)
	addr := startFrameServer(t, bs)
	slow := dial(t, addr)
	wire.WriteFrame(slow, wire.Frame{Op: wire.OpGet, Key: "slow"})
	fast := dial(t, addr)
	fast.SetDeadline(time.Now().Add(2 * time.Second))
	if r := roundTrip(t, fast, wire.Frame{Op: wire.OpPing}); r.Op != wire.OpPong {
		t.Errorf("one connection stuck in the store must not delay another")
	}
}

func TestL3_2_NoLeaks(t *testing.T) {
	testkit.NoLeaks(t)
	st := store.NewMemory()
	defer st.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	done := make(chan struct{})
	go func() {
		(&wire.Server{Store: st}).Serve(ln)
		close(done)
	}()
	for range 5 {
		c, _ := net.Dial("tcp", ln.Addr().String())
		wire.WriteFrame(c, wire.Frame{Op: wire.OpPing})
		wire.ReadFrame(c)
		c.Close()
	}
	ln.Close()
	<-done
}
