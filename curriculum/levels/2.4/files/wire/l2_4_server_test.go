package wire_test

import (
	"bytes"
	"errors"
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

func startFrameServer(t *testing.T, st store.Store) string {
	t.Helper()
	ln := testkit.Listen(t)
	srv := &wire.Server{Store: st}
	go srv.Serve(ln)
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

func TestL2_4_ServerOps(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	steps := []struct {
		req     wire.Frame
		wantOp  wire.Op
		wantVal string
	}{
		{wire.Frame{Op: wire.OpGet, Key: "k"}, wire.OpNotFound, ""},
		{wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v\x00\n")}, wire.OpOK, ""},
		{wire.Frame{Op: wire.OpGet, Key: "k"}, wire.OpValue, "v\x00\n"},
		{wire.Frame{Op: wire.OpDelete, Key: "k"}, wire.OpOK, ""},
		{wire.Frame{Op: wire.OpDelete, Key: "k"}, wire.OpNotFound, ""},
		{wire.Frame{Op: wire.OpPing, Value: []byte("hi")}, wire.OpPong, "hi"},
	}
	for _, s := range steps {
		resp := roundTrip(t, c, s.req)
		if resp.Op != s.wantOp || string(resp.Value) != s.wantVal {
			t.Errorf("%v %q → %v %q, want %v %q", s.req.Op, s.req.Key, resp.Op, resp.Value, s.wantOp, s.wantVal)
		}
		if resp.Key != s.req.Key {
			t.Errorf("replies echo the request key: %v %q got key %q", s.req.Op, s.req.Key, resp.Key)
		}
	}
}

func TestL2_4_ServerInvalidKey(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	resp := roundTrip(t, c, wire.Frame{Op: wire.OpSet, Key: "has space", Value: []byte("v")})
	if resp.Op != wire.OpError || !strings.Contains(string(resp.Value), "invalid key") {
		t.Errorf("an invalid key gets an OpError mentioning \"invalid key\", got %v %q", resp.Op, resp.Value)
	}
	if resp := roundTrip(t, c, wire.Frame{Op: wire.OpPing}); resp.Op != wire.OpPong {
		t.Errorf("an OpError reply keeps the connection open; next PING got %v", resp.Op)
	}
}

func TestL2_4_ServerRejectsResponseOps(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	resp := roundTrip(t, c, wire.Frame{Op: wire.OpOK})
	if resp.Op != wire.OpError {
		t.Errorf("a client sending OpOK (a reply op) should get OpError, got %v", resp.Op)
	}
}

func TestL2_4_ServerClosesOnGarbage(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	c.Write([]byte{0x7f, 1, 2, 3})
	resp, err := wire.ReadFrame(c)
	if err != nil || resp.Op != wire.OpError {
		t.Fatalf("after an unknown op the server should send one OpError, got (%v, %v)", resp.Op, err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = c.Read(make([]byte, 1))
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Errorf("then close the connection, because the byte stream can't be trusted any more; read got %v", err)
	}
}

func TestL2_4_ServerClosesOnOversizedFrame(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	c.Write([]byte{0x02, 0xff, 0xff, 0xff, 0xff})
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	rest, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("expected the server to close the connection after an oversized frame: %v", err)
	}
	if len(rest) > 0 {
		f, err := wire.ReadFrame(bytes.NewReader(rest))
		if err != nil || f.Op != wire.OpError {
			t.Errorf("the only thing allowed before closing is one OpError frame; got % x", rest)
		}
	}
}

func TestL2_4_ServerManyClients(t *testing.T) {
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
			key := "client-" + string(rune('A'+i))
			for j := range 50 {
				val := []byte{byte(i), byte(j)}
				wire.WriteFrame(c, wire.Frame{Op: wire.OpSet, Key: key, Value: val})
				if f, err := wire.ReadFrame(c); err != nil || f.Op != wire.OpOK {
					t.Errorf("set: %v %v", f.Op, err)
					return
				}
				wire.WriteFrame(c, wire.Frame{Op: wire.OpGet, Key: key})
				if f, err := wire.ReadFrame(c); err != nil || !bytes.Equal(f.Value, val) {
					t.Errorf("client %d read back %v, want %v (%v)", i, f.Value, val, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestL2_4_ServerPipelined(t *testing.T) {
	c := dial(t, startFrameServer(t, store.NewMemory()))
	var batch bytes.Buffer
	for i := range 100 {
		wire.WriteFrame(&batch, wire.Frame{Op: wire.OpSet, Key: "p", Value: []byte{byte(i)}})
	}
	wire.WriteFrame(&batch, wire.Frame{Op: wire.OpGet, Key: "p"})
	c.Write(batch.Bytes())
	for i := range 100 {
		if f, err := wire.ReadFrame(c); err != nil || f.Op != wire.OpOK {
			t.Fatalf("pipelined reply %d: %v %v", i, f.Op, err)
		}
	}
	if f, _ := wire.ReadFrame(c); !bytes.Equal(f.Value, []byte{99}) {
		t.Errorf("after 100 pipelined SETs, GET returned %v, want [99]: requests on one connection run in order", f.Value)
	}
}
