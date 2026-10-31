package kvserver_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"burrow/grpc/burrowpb"
	"burrow/grpc/kvserver"
	"burrow/internal/testkit"
	"burrow/store"
)

// logBuf is a goroutine-safe log destination.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// lines returns the JSON log lines with the given msg.
func (l *logBuf) lines(t testing.TB, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a log line isn't JSON (use slog.NewJSONHandler): %q", line)
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

// rpcLine finds the access log line for method with request ID id.
func rpcLine(t testing.TB, logs *logBuf, method, id string) map[string]any {
	t.Helper()
	var found map[string]any
	testkit.Eventually(t, 2*time.Second, func() bool {
		for _, m := range logs.lines(t, "rpc") {
			if m["method"] == method && m["id"] == id {
				found = m
				return true
			}
		}
		return false
	}, "an \"rpc\" log line with method %s and id %s; log so far:\n%s", method, id, logs)
	return found
}

func loggedRig(t testing.TB, st store.Store) (*rig, *logBuf) {
	t.Helper()
	logs := &logBuf{}
	return serve(t, &kvserver.Server{Store: st, Logger: slog.New(slog.NewJSONHandler(logs, nil))}), logs
}

func newMem(t testing.TB) *store.Memory {
	m := store.NewMemory()
	t.Cleanup(func() { m.Close() })
	return m
}

func isHexID(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16
}

func TestLG_4_RequestID(t *testing.T) {
	r, logs := loggedRig(t, newMem(t))
	ctx := ctxT(t)
	seen := map[string]bool{}
	for i := range 200 {
		var md metadata.MD
		var err error
		if i%2 == 0 {
			_, err = r.kv.Put(ctx, &burrowpb.PutRequest{Key: "k", Value: []byte("v")}, grpc.Trailer(&md))
		} else {
			_, err = r.kv.Get(ctx, &burrowpb.GetRequest{Key: "missing"}, grpc.Trailer(&md))
		}
		if i%2 == 0 && err != nil {
			t.Fatal(err)
		}
		ids := md.Get("x-request-id")
		if len(ids) != 1 || !isHexID(ids[0]) {
			t.Fatalf("call %d: response trailer x-request-id = %q, want 32 hex characters (on errors too)", i, ids)
		}
		if seen[ids[0]] {
			t.Fatalf("request ID %s repeated: use crypto/rand", ids[0])
		}
		seen[ids[0]] = true
	}
	var md, hdr metadata.MD
	r.kv.Get(ctx, &burrowpb.GetRequest{Key: "missing"}, grpc.Trailer(&md), grpc.Header(&hdr))
	if len(hdr.Get("x-request-id")) > 0 {
		t.Errorf("x-request-id goes in the trailers, not the headers: a response that sent " +
			"headers is committed, and the client's retry policy can't retry it")
	}
	line := rpcLine(t, logs, "/burrow.v1.KV/Get", md.Get("x-request-id")[0])
	if line["code"] != "NotFound" {
		t.Errorf("access log code = %v, want NotFound (status.Code(err).String())", line["code"])
	}
}

func TestLG_4_RequestIDHonoured(t *testing.T) {
	r, logs := loggedRig(t, newMem(t))
	ctx := metadata.AppendToOutgoingContext(ctxT(t), "x-request-id", "trace-abc-123")
	var md metadata.MD
	if _, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "k", Value: []byte("v")}, grpc.Trailer(&md)); err != nil {
		t.Fatal(err)
	}
	if got := md.Get("x-request-id"); len(got) != 1 || got[0] != "trace-abc-123" {
		t.Errorf("a client that sends x-request-id gets it back; trailer = %q", got)
	}
	line := rpcLine(t, logs, "/burrow.v1.KV/Put", "trace-abc-123")
	if line["code"] != "OK" {
		t.Errorf("access log code = %v, want OK", line["code"])
	}
	if _, ok := line["duration"].(float64); !ok {
		t.Errorf("access log line has no numeric duration: %v", line)
	}

	long := strings.Repeat("x", 200)
	ctx = metadata.AppendToOutgoingContext(ctxT(t), "x-request-id", long)
	md = nil
	r.kv.Get(ctx, &burrowpb.GetRequest{Key: "k"}, grpc.Trailer(&md))
	if got := md.Get("x-request-id"); len(got) != 1 || !isHexID(got[0]) {
		t.Errorf("an x-request-id over 128 bytes is replaced by a fresh one; trailer = %.40q", got)
	}
}

// idStore records the request ID its Get sees in the context.
type idStore struct {
	store.Store
	ids chan string
}

func (s idStore) Get(ctx context.Context, key string) (store.Entry, error) {
	s.ids <- kvserver.RequestIDFrom(ctx)
	return s.Store.Get(ctx, key)
}

func TestLG_4_RequestIDInContext(t *testing.T) {
	st := idStore{Store: newMem(t), ids: make(chan string, 1)}
	r := serve(t, &kvserver.Server{Store: st})
	var md metadata.MD
	r.kv.Get(ctxT(t), &burrowpb.GetRequest{Key: "k"}, grpc.Trailer(&md))
	if got, want := <-st.ids, md.Get("x-request-id"); len(want) != 1 || got != want[0] {
		t.Errorf("kvserver.RequestIDFrom(ctx) in the store = %q, response trailer = %q", got, want)
	}
	if got := kvserver.RequestIDFrom(context.Background()); got != "" {
		t.Errorf("RequestIDFrom on a bare context = %q, want \"\"", got)
	}
}

type panicStore struct{ store.Store }

func (panicStore) Get(context.Context, string) (store.Entry, error) {
	panic("secret-panic-value")
}

func TestLG_4_Recover(t *testing.T) {
	r, logs := loggedRig(t, panicStore{newMem(t)})
	ctx := ctxT(t)
	var md metadata.MD
	_, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "k"}, grpc.Trailer(&md))
	if status.Code(err) != codes.Internal {
		t.Fatalf("a panicking handler: code %v, want Internal", status.Code(err))
	}
	if strings.Contains(err.Error(), "secret-panic-value") {
		t.Errorf("the panic value reached the client (%q); log it, don't send it", err)
	}
	if len(md.Get("x-request-id")) != 1 {
		t.Errorf("the response to a panic still carries x-request-id in its trailer")
	}
	testkit.Eventually(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), "secret-panic-value")
	}, "the panic value in the log")
	for _, m := range logs.lines(t, "panic") {
		if m["level"] != "ERROR" {
			t.Errorf("the panic is logged at %v, want ERROR", m["level"])
		}
	}
	if len(logs.lines(t, "panic")) == 0 {
		t.Errorf("no log line with msg \"panic\":\n%s", logs)
	}
	if _, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "k", Value: []byte("v")}); err != nil {
		t.Errorf("the server keeps serving after a panic: %v", err)
	}
}

// rpcPanics is a slog handler that panics on access log lines.
type rpcPanics struct{ slog.Handler }

func (h rpcPanics) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "rpc" {
		panic("AccessLog panicked: Recover must be the outermost interceptor so it catches this")
	}
	return h.Handler.Handle(ctx, r)
}

func TestLG_4_RecoverIsOutermost(t *testing.T) {
	l := slog.New(rpcPanics{slog.NewJSONHandler(io.Discard, nil)})
	r := serve(t, &kvserver.Server{Store: newMem(t), Logger: l})
	_, err := r.kv.Put(ctxT(t), &burrowpb.PutRequest{Key: "k", Value: []byte("v")})
	if status.Code(err) != codes.Internal {
		t.Errorf("a panic inside AccessLog: code %v, want Internal", status.Code(err))
	}
}

// Streams get the same chain. The test registers its own streaming
// service next to KV: Count checks the request ID in the stream's
// context and sends three messages; Boom panics.
var testStreams = &grpc.ServiceDesc{
	ServiceName: "lg4.Test",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{
		{StreamName: "Count", ServerStreams: true, Handler: func(_ any, ss grpc.ServerStream) error {
			if err := ss.RecvMsg(&emptypb.Empty{}); err != nil {
				return err
			}
			if kvserver.RequestIDFrom(ss.Context()) == "" {
				return status.Error(codes.FailedPrecondition, "no request ID in the stream's context: wrap the ServerStream and override Context()")
			}
			for range 3 {
				if err := ss.SendMsg(&emptypb.Empty{}); err != nil {
					return err
				}
			}
			return nil
		}},
		{StreamName: "Boom", ServerStreams: true, Handler: func(any, grpc.ServerStream) error {
			panic("stream-panic-value")
		}},
	},
}

func streamRig(t *testing.T) (*grpc.ClientConn, *logBuf) {
	t.Helper()
	logs := &logBuf{}
	gs := (&kvserver.Server{Store: newMem(t), Logger: slog.New(slog.NewJSONHandler(logs, nil))}).GRPCServer()
	gs.RegisterService(testStreams, struct{}{})
	lis := bufconn.Listen(1 << 20)
	go gs.Serve(lis)
	conn := dialBuf(t, lis)
	t.Cleanup(func() {
		conn.Close()
		gs.Stop()
	})
	return conn, logs
}

func openStream(t *testing.T, conn *grpc.ClientConn, method string) grpc.ClientStream {
	t.Helper()
	cs, err := conn.NewStream(ctxT(t), &grpc.StreamDesc{ServerStreams: true}, method)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.SendMsg(&emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	cs.CloseSend()
	return cs
}

func TestLG_4_StreamInterceptors(t *testing.T) {
	conn, logs := streamRig(t)
	cs := openStream(t, conn, "/lg4.Test/Count")
	n := 0
	for {
		err := cs.RecvMsg(&emptypb.Empty{})
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		n++
	}
	if n != 3 {
		t.Errorf("received %d messages, want 3", n)
	}
	ids := cs.Trailer().Get("x-request-id")
	if len(ids) != 1 || !isHexID(ids[0]) {
		t.Fatalf("a stream's trailer x-request-id = %q, want 32 hex characters", ids)
	}
	line := rpcLine(t, logs, "/lg4.Test/Count", ids[0])
	if line["code"] != "OK" {
		t.Errorf("stream access log code = %v, want OK", line["code"])
	}
	if line["sent"] != float64(3) {
		t.Errorf("stream access log sent = %v, want 3 (count SendMsg calls in your wrapped stream)", line["sent"])
	}

	boom := openStream(t, conn, "/lg4.Test/Boom")
	if err := boom.RecvMsg(&emptypb.Empty{}); status.Code(err) != codes.Internal {
		t.Errorf("a panicking stream handler: %v, want code Internal", err)
	} else if strings.Contains(err.Error(), "stream-panic-value") {
		t.Errorf("the panic value reached the client: %v", err)
	}
	testkit.Eventually(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), "stream-panic-value")
	}, "the stream's panic value in the log")
	if _, err := burrowpb.NewKVClient(conn).Keys(ctxT(t), &burrowpb.KeysRequest{}); err != nil {
		t.Errorf("the server keeps serving after a stream panic: %v", err)
	}
}

func TestLG_4_NilLogger(t *testing.T) {
	r := serve(t, &kvserver.Server{Store: panicStore{newMem(t)}})
	if _, err := r.kv.Get(ctxT(t), &burrowpb.GetRequest{Key: "k"}); status.Code(err) != codes.Internal {
		t.Errorf("with no Logger a panic is still Internal, got %v", err)
	}
	if _, err := r.kv.Keys(ctxT(t), &burrowpb.KeysRequest{}); err != nil {
		t.Errorf("a nil Logger means no access log, not a crash: %v", err)
	}
}
