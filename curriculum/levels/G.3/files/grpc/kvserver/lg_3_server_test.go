package kvserver_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"burrow/grpc/burrowpb"
	"burrow/grpc/kvserver"
	"burrow/internal/testkit"
	"burrow/store"
)

var _ burrowpb.KVServer = (*kvserver.Server)(nil)

// rig is one KV server on an in-memory listener and a client
// connection to it.
type rig struct {
	gs   *grpc.Server
	conn *grpc.ClientConn
	kv   burrowpb.KVClient
}

// dialBuf connects to lis through the in-memory dialer.
func dialBuf(t testing.TB, lis *bufconn.Listener, opts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	all := append([]grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, opts...)
	conn, err := grpc.NewClient("passthrough:///bufnet", all...)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return conn
}

// serve starts srv.GRPCServer() on a bufconn listener. Cleanup closes
// the connection, then stops the server gracefully.
func serve(t testing.TB, srv *kvserver.Server, opts ...grpc.ServerOption) *rig {
	t.Helper()
	gs := srv.GRPCServer(opts...)
	if gs == nil {
		t.Fatal("GRPCServer returned nil")
	}
	lis := bufconn.Listen(1 << 20)
	done := make(chan struct{})
	go func() {
		defer close(done)
		gs.Serve(lis)
	}()
	conn := dialBuf(t, lis)
	t.Cleanup(func() {
		conn.Close()
		stopped := make(chan struct{})
		go func() {
			gs.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Errorf("GracefulStop didn't return within 5s: a handler is still running after its client left")
			go gs.Stop()
			return
		}
		<-done
	})
	return &rig{gs: gs, conn: conn, kv: burrowpb.NewKVClient(conn)}
}

// memRig serves a fresh memory store.
func memRig(t testing.TB, opts ...store.Option) (*rig, *store.Memory) {
	t.Helper()
	m := store.NewMemory(opts...)
	t.Cleanup(func() { m.Close() })
	return serve(t, &kvserver.Server{Store: m}), m
}

func ctxT(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// reason returns the status code and the ErrorInfo reason of err.
func reason(t testing.TB, err error) (codes.Code, string) {
	t.Helper()
	st := status.Convert(err)
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			if info.GetDomain() != kvserver.Domain {
				t.Errorf("ErrorInfo domain is %q, want %q", info.GetDomain(), kvserver.Domain)
			}
			return st.Code(), info.GetReason()
		}
	}
	return st.Code(), ""
}

func put(t testing.TB, kv burrowpb.KVClient, key, value string) *burrowpb.Entry {
	t.Helper()
	r, err := kv.Put(ctxT(t), &burrowpb.PutRequest{Key: key, Value: []byte(value)})
	if err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
	return r.GetEntry()
}

func TestLG_3_PutGet(t *testing.T) {
	r, _ := memRig(t)
	ctx := ctxT(t)
	e := put(t, r.kv, "user:1", "ann")
	if e.GetKey() != "user:1" || string(e.GetValue()) != "ann" || e.GetVersion() != 1 || e.GetExpiresAtUnixNano() != 0 {
		t.Errorf("Put returned %v, want key user:1 value ann version 1 no expiry", e)
	}
	if e := put(t, r.kv, "user:1", "bob"); e.GetVersion() != 2 {
		t.Errorf("second Put: version %d, want 2", e.GetVersion())
	}
	g, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "user:1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e := g.GetEntry(); e.GetKey() != "user:1" || string(e.GetValue()) != "bob" || e.GetVersion() != 2 {
		t.Errorf("Get returned %v, want user:1 = bob at version 2", e)
	}
	bin := []byte{0, 0xff, '\n', 0}
	r.kv.Put(ctx, &burrowpb.PutRequest{Key: "bin", Value: bin})
	if g, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "bin"}); err != nil || !bytes.Equal(g.GetEntry().GetValue(), bin) {
		t.Errorf("binary value round trip: %v, %v", g.GetEntry().GetValue(), err)
	}
	put(t, r.kv, "user:2", "cy")
	put(t, r.kv, "cfg:x", "1")
	k, err := r.kv.Keys(ctx, &burrowpb.KeysRequest{Prefix: "user:"})
	if err != nil || !slices.Equal(k.GetKeys(), []string{"user:1", "user:2"}) {
		t.Errorf("Keys(user:) = %q, %v; want [user:1 user:2]", k.GetKeys(), err)
	}
	if _, err := r.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "user:1"}); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if _, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "user:1"}); status.Code(err) != codes.NotFound {
		t.Errorf("Get after Delete: code %v, want NotFound", status.Code(err))
	}
}

func TestLG_3_Expiry(t *testing.T) {
	clock := testkit.NewFakeClock()
	r, _ := memRig(t, store.WithClock(clock.Now))
	ctx := ctxT(t)
	p, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "lease", Value: []byte("me"), TtlMs: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.Now().Add(5 * time.Second).UnixNano(); p.GetEntry().GetExpiresAtUnixNano() != want {
		t.Errorf("Put ttl_ms 5000: expires_at_unix_nano %d, want %d (store clock + 5s)", p.GetEntry().GetExpiresAtUnixNano(), want)
	}
	clock.Advance(time.Second)
	tr, err := r.kv.Touch(ctx, &burrowpb.TouchRequest{Key: "lease", TtlMs: 60000})
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if want := clock.Now().Add(time.Minute).UnixNano(); tr.GetEntry().GetExpiresAtUnixNano() != want || tr.GetEntry().GetVersion() != 1 {
		t.Errorf("Touch ttl_ms 60000: %v, want version 1 expiring at %d", tr.GetEntry(), want)
	}
	if p, _ := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "lease", Value: []byte("x")}); p.GetEntry().GetExpiresAtUnixNano() != 0 {
		t.Errorf("a Put with ttl_ms 0 clears the expiry, got %d", p.GetEntry().GetExpiresAtUnixNano())
	}
	clock.Advance(2 * time.Minute)
	if _, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "lease"}); err != nil {
		t.Errorf("the key has no TTL any more and should still be there: %v", err)
	}
}

func TestLG_3_Conditional(t *testing.T) {
	r, _ := memRig(t)
	ctx := ctxT(t)
	c, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "k", Value: []byte("a"), IfVersion: proto.Uint64(0)})
	if err != nil || c.GetEntry().GetVersion() != 1 {
		t.Fatalf("Put if_version 0 on a missing key creates it at version 1: %v, %v", c.GetEntry(), err)
	}
	c, err = r.kv.Put(ctx, &burrowpb.PutRequest{Key: "k", Value: []byte("b"), IfVersion: proto.Uint64(1)})
	if err != nil || c.GetEntry().GetVersion() != 2 || string(c.GetEntry().GetValue()) != "b" {
		t.Fatalf("Put if_version 1 at version 1: %v, %v; want b at version 2", c.GetEntry(), err)
	}
	c, err = r.kv.Put(ctx, &burrowpb.PutRequest{Key: "k", Value: []byte("c"), IfVersion: proto.Uint64(2), TtlMs: 60000})
	if err != nil || c.GetEntry().GetVersion() != 3 || c.GetEntry().GetExpiresAtUnixNano() == 0 {
		t.Fatalf("Put with if_version and ttl_ms is a CAS with expiry: %v, %v", c.GetEntry(), err)
	}
	if _, err := r.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "k", IfVersion: proto.Uint64(3)}); err != nil {
		t.Fatalf("Delete if_version 3 at version 3: %v", err)
	}
	if _, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "k"}); status.Code(err) != codes.NotFound {
		t.Errorf("after a conditional Delete the key is gone; Get: %v", err)
	}
	// Unset and 0 are different requests: an unset if_version overwrites.
	put(t, r.kv, "k", "x")
	if e := put(t, r.kv, "k", "y"); e.GetVersion() != 2 {
		t.Errorf("Put without if_version is unconditional: version %d, want 2", e.GetVersion())
	}
}

func TestLG_3_StatusCodes(t *testing.T) {
	r, _ := memRig(t)
	ctx := ctxT(t)
	put(t, r.kv, "here", "1")
	put(t, r.kv, "here", "2") // version 2
	cases := []struct {
		name   string
		call   func() error
		code   codes.Code
		reason string
	}{
		{"Get missing", func() error {
			_, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "missing"})
			return err
		}, codes.NotFound, "NOT_FOUND"},
		{"Delete missing", func() error {
			_, err := r.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "missing"})
			return err
		}, codes.NotFound, "NOT_FOUND"},
		{"Touch missing", func() error {
			_, err := r.kv.Touch(ctx, &burrowpb.TouchRequest{Key: "missing", TtlMs: 1000})
			return err
		}, codes.NotFound, "NOT_FOUND"},
		{"Get empty key", func() error {
			_, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: ""})
			return err
		}, codes.InvalidArgument, "INVALID_KEY"},
		{"Put key with a space", func() error {
			_, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "a b", Value: []byte("v")})
			return err
		}, codes.InvalidArgument, "INVALID_KEY"},
		{"Keys is fine with an empty prefix", func() error {
			_, err := r.kv.Keys(ctx, &burrowpb.KeysRequest{})
			return err
		}, codes.OK, ""},
		{"Put negative ttl", func() error {
			_, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "t", Value: []byte("v"), TtlMs: -1})
			return err
		}, codes.InvalidArgument, "INVALID_TTL"},
		{"Touch ttl 0", func() error {
			_, err := r.kv.Touch(ctx, &burrowpb.TouchRequest{Key: "here"})
			return err
		}, codes.InvalidArgument, "INVALID_TTL"},
		{"Put create-only on an existing key", func() error {
			_, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "here", Value: []byte("v"), IfVersion: proto.Uint64(0)})
			return err
		}, codes.Aborted, "VERSION_MISMATCH"},
		{"Put stale if_version", func() error {
			_, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "here", Value: []byte("v"), IfVersion: proto.Uint64(1)})
			return err
		}, codes.Aborted, "VERSION_MISMATCH"},
		{"Put if_version on a missing key", func() error {
			_, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "missing", Value: []byte("v"), IfVersion: proto.Uint64(5)})
			return err
		}, codes.Aborted, "NOT_FOUND"},
		{"Delete stale if_version", func() error {
			_, err := r.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "here", IfVersion: proto.Uint64(9)})
			return err
		}, codes.Aborted, "VERSION_MISMATCH"},
		{"Delete if_version on a missing key", func() error {
			_, err := r.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "missing", IfVersion: proto.Uint64(3)})
			return err
		}, codes.Aborted, "NOT_FOUND"},
		{"Delete if_version 0", func() error {
			_, err := r.kv.Delete(ctx, &burrowpb.DeleteRequest{Key: "here", IfVersion: proto.Uint64(0)})
			return err
		}, codes.InvalidArgument, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			code, why := reason(t, err)
			if code != c.code {
				t.Fatalf("code %v, want %v (error: %v)", code, c.code, err)
			}
			if c.reason != "" && why != c.reason {
				t.Errorf("ErrorInfo reason %q, want %q: attach errdetails.ErrorInfo{Reason, Domain} with status.WithDetails", why, c.reason)
			}
		})
	}
	if g, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "here"}); err != nil || string(g.GetEntry().GetValue()) != "2" || g.GetEntry().GetVersion() != 2 {
		t.Errorf("failed writes must change nothing: here = %v, %v", g.GetEntry(), err)
	}
}

// errStore returns err from every call.
type errStore struct {
	store.Store
	err error
}

func (s errStore) Get(context.Context, string) (store.Entry, error) { return store.Entry{}, s.err }

func TestLG_3_StoreErrors(t *testing.T) {
	cases := []struct {
		err  error
		code codes.Code
	}{
		{fmt.Errorf("engine: %w", store.ErrNotFound), codes.NotFound},
		{store.ErrClosed, codes.Unavailable},
		{context.DeadlineExceeded, codes.DeadlineExceeded},
		{context.Canceled, codes.Canceled},
		{errors.New("disk on fire"), codes.Internal},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			r := serve(t, &kvserver.Server{Store: errStore{store.NewMemory(), c.err}})
			_, err := r.kv.Get(ctxT(t), &burrowpb.GetRequest{Key: "k"})
			if got := status.Code(err); got != c.code {
				t.Errorf("a store returning %q: code %v, want %v", c.err, got, c.code)
			}
		})
	}
}

// slowStore blocks every Get until its context ends and reports the
// context's error. It gives up after 3s so a handler that passes the
// wrong context fails instead of hanging.
type slowStore struct {
	store.Store
	seen chan error
}

func (s *slowStore) Get(ctx context.Context, key string) (store.Entry, error) {
	select {
	case <-ctx.Done():
		s.seen <- ctx.Err()
		return store.Entry{}, ctx.Err()
	case <-time.After(3 * time.Second):
		return store.Entry{}, errors.New("slowStore: the context never ended")
	}
}

func TestLG_3_Deadline(t *testing.T) {
	slow := &slowStore{Store: store.NewMemory(), seen: make(chan error, 2)}
	r := serve(t, &kvserver.Server{Store: slow})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "k"})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("Get with a 100ms deadline on a store that blocks: code %v, want DeadlineExceeded", status.Code(err))
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the call took %v; the deadline was 100ms", d)
	}
	select {
	case got := <-slow.seen:
		if !errors.Is(got, context.DeadlineExceeded) && !errors.Is(got, context.Canceled) {
			t.Errorf("the store's context ended with %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the store never saw its context end: pass the handler's ctx to every store call")
	}

	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "k"}); status.Code(err) != codes.Canceled {
		t.Errorf("Get cancelled by the client: code %v, want Canceled", status.Code(err))
	}
	select {
	case <-slow.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the store never saw the client's cancel")
	}
}

func TestLG_3_MessageSize(t *testing.T) {
	if kvserver.MaxRecvMsgSize != 1<<20+64<<10 {
		t.Errorf("MaxRecvMsgSize = %d, want 1<<20 + 64<<10", kvserver.MaxRecvMsgSize)
	}
	r, _ := memRig(t)
	ctx := ctxT(t)
	big := bytes.Repeat([]byte("x"), 1<<20)
	if _, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "big", Value: big}); err != nil {
		t.Errorf("a 1 MiB value must fit: %v", err)
	}
	huge := bytes.Repeat([]byte("x"), 2<<20)
	if _, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: "huge", Value: huge}); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("a 2 MiB request: code %v, want ResourceExhausted (grpc.MaxRecvMsgSize)", status.Code(err))
	}
	if g, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: "big"}); err != nil || len(g.GetEntry().GetValue()) != 1<<20 {
		t.Errorf("after an oversize request the server keeps serving: %v", err)
	}
}

func TestLG_3_Concurrent(t *testing.T) {
	testkit.RequireRace(t)
	r, _ := memRig(t)
	ctx := ctxT(t)
	var wg sync.WaitGroup
	for w := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				k := fmt.Sprintf("w%d-%d", w, i)
				if _, err := r.kv.Put(ctx, &burrowpb.PutRequest{Key: k, Value: []byte(k)}); err != nil {
					t.Error(err)
					return
				}
				if g, err := r.kv.Get(ctx, &burrowpb.GetRequest{Key: k}); err != nil || string(g.GetEntry().GetValue()) != k {
					t.Errorf("Get(%s) = %v, %v", k, g.GetEntry(), err)
					return
				}
			}
		}()
	}
	wg.Wait()
	k, _ := r.kv.Keys(ctx, &burrowpb.KeysRequest{Prefix: "w"})
	if len(k.GetKeys()) != 16*25 {
		t.Errorf("Keys after 400 concurrent Puts: %d", len(k.GetKeys()))
	}
}

func TestLG_3_Health(t *testing.T) {
	r, _ := memRig(t)
	h := healthpb.NewHealthClient(r.conn)
	for _, svc := range []string{"", "burrow.v1.KV"} {
		resp, err := h.Check(ctxT(t), &healthpb.HealthCheckRequest{Service: svc})
		if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("health Check(%q) = %v, %v; want SERVING", svc, resp.GetStatus(), err)
		}
	}
}

// reflect asks the reflection service one question.
func reflect(t testing.TB, conn *grpc.ClientConn, req *reflectionpb.ServerReflectionRequest) *reflectionpb.ServerReflectionResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatalf("reflection: %v", err)
	}
	if err := st.Send(req); err != nil {
		t.Fatalf("reflection send: %v", err)
	}
	resp, err := st.Recv()
	if err != nil {
		t.Fatalf("reflection: %v (did you call reflection.Register?)", err)
	}
	st.CloseSend()
	return resp
}

func TestLG_3_Reflection(t *testing.T) {
	r, _ := memRig(t)
	resp := reflect(t, r.conn, &reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{},
	})
	var names []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		names = append(names, s.GetName())
	}
	for _, want := range []string{"burrow.v1.KV", "grpc.health.v1.Health"} {
		if !slices.Contains(names, want) {
			t.Errorf("reflection lists %q, missing %s", names, want)
		}
	}
	resp = reflect(t, r.conn, &reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: "burrow.v1.KV"},
	})
	files := resp.GetFileDescriptorResponse().GetFileDescriptorProto()
	if len(files) == 0 {
		t.Fatalf("reflection has no file for burrow.v1.KV: %v", resp.GetErrorResponse())
	}
	var fd descriptorpb.FileDescriptorProto
	if err := proto.Unmarshal(files[0], &fd); err != nil || fd.GetName() != "burrow/v1/kv.proto" {
		t.Errorf("reflection's file for burrow.v1.KV is %q (%v), want burrow/v1/kv.proto", fd.GetName(), err)
	}
}

func TestLG_3_NoLeaks(t *testing.T) {
	// The first server and connection start goroutines grpc keeps for
	// the life of the process. Warm up, then count.
	warm := func(t *testing.T) {
		r, _ := memRig(t)
		put(t, r.kv, "k", "v")
	}
	t.Run("warmup", warm)
	testkit.NoLeaks(t)
	t.Run("run", warm)
}
