package httpapi_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/access"
	"burrow/access/httpapi"
	"burrow/api"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type server struct {
	url string
	log *syncBuf
}

func start(t *testing.T, ts access.TeamStore) *server {
	t.Helper()
	logs := &syncBuf{}
	l := slog.New(slog.NewJSONHandler(logs, nil))
	h := httpapi.New(access.NewService(ts, access.Options{}), l)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &server{url: srv.URL, log: logs}
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

func (r reply) json(t *testing.T, v any) {
	t.Helper()
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("response body isn't the expected JSON: %v\n%s", err, r.body)
	}
}

func (s *server) do(t *testing.T, method, path, body string, hdr ...string) reply {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header, b}
}

func (s *server) expectError(t *testing.T, r reply, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Errorf("status %d, want %d (code %s); body %s", r.status, status, code, r.body)
		return
	}
	var e api.ErrorResponse
	r.json(t, &e)
	if e.Code != code || e.Error == "" {
		t.Errorf("error body %+v, want api.ErrorResponse with code %q and a message", e, code)
	}
}

type keyJSON struct {
	Name    string     `json:"name"`
	Role    string     `json:"role"`
	Created time.Time  `json:"created"`
	Revoked *time.Time `json:"revoked"`
}

type teamJSON struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Version uint64    `json:"version"`
	Keys    []keyJSON `json:"keys"`
}

type issued struct {
	Token   string  `json:"token"`
	Version uint64  `json:"version"`
	Key     keyJSON `json:"key"`
}

func etag(v uint64) string { return fmt.Sprintf(`"%d"`, v) }

func (s *server) create(t *testing.T, name string) teamJSON {
	t.Helper()
	r := s.do(t, "POST", "/v1/teams", `{"name":"`+name+`"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /v1/teams = %d %s, want 201", r.status, r.body)
	}
	var tm teamJSON
	r.json(t, &tm)
	return tm
}

func (s *server) issue(t *testing.T, id string, v uint64, name, role string) issued {
	t.Helper()
	r := s.do(t, "POST", "/v1/teams/"+id+"/keys", `{"name":"`+name+`","role":"`+role+`"}`,
		"If-Match", etag(v))
	if r.status != http.StatusCreated {
		t.Fatalf("POST keys %s = %d %s, want 201", name, r.status, r.body)
	}
	var k issued
	r.json(t, &k)
	if got := r.header.Get("ETag"); got != etag(k.Version) {
		t.Errorf("POST keys: ETag %q, want %q matching the version in the body", got, etag(k.Version))
	}
	return k
}

var reqID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestLA_4_CreateAndGet(t *testing.T) {
	s := start(t, access.NewMemStore())
	r := s.do(t, "POST", "/v1/teams", `{"name":"Platform"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /v1/teams = %d %s, want 201", r.status, r.body)
	}
	var tm teamJSON
	r.json(t, &tm)
	if tm.Name != "Platform" || tm.ID == "" || tm.Version == 0 || tm.Keys == nil || len(tm.Keys) != 0 {
		t.Errorf("created team %s, want id, name, version and \"keys\": []", r.body)
	}
	if got := r.header.Get("ETag"); got != etag(tm.Version) {
		t.Errorf("ETag %q, want %q (the team's version, quoted)", got, etag(tm.Version))
	}
	if got := r.header.Get("Location"); got != "/v1/teams/"+tm.ID {
		t.Errorf("Location %q, want /v1/teams/%s", got, tm.ID)
	}
	if !reqID.MatchString(r.header.Get("X-Request-ID")) {
		t.Errorf("X-Request-ID %q: wrap the mux in api.Chain with api.RequestID", r.header.Get("X-Request-ID"))
	}

	g := s.do(t, "GET", "/v1/teams/"+tm.ID, "")
	var got teamJSON
	g.json(t, &got)
	if g.status != 200 || got.ID != tm.ID || got.Version != tm.Version {
		t.Errorf("GET /v1/teams/%s = %d %s", tm.ID, g.status, g.body)
	}
	if g.header.Get("ETag") != etag(tm.Version) {
		t.Errorf("GET ETag %q, want %q", g.header.Get("ETag"), etag(tm.Version))
	}

	l := s.do(t, "GET", "/v1/teams", "")
	if l.status != 200 {
		t.Fatalf("GET /v1/teams = %d", l.status)
	}
	var lst []teamJSON
	l.json(t, &lst)
	if len(lst) != 1 || lst[0].ID != tm.ID {
		t.Errorf("GET /v1/teams = %s, want the one team", l.body)
	}
	empty := start(t, access.NewMemStore()).do(t, "GET", "/v1/teams", "")
	if strings.TrimSpace(string(empty.body)) != "[]" {
		t.Errorf("GET /v1/teams with no teams = %s, want []", empty.body)
	}
}

func TestLA_4_KeyLifecycle(t *testing.T) {
	s := start(t, access.NewMemStore())
	tm := s.create(t, "Platform")
	path := "/v1/teams/" + tm.ID + "/keys"

	s.expectError(t, s.do(t, "POST", path, `{"name":"ci","role":"write"}`),
		http.StatusPreconditionRequired, "precondition_required")
	s.expectError(t, s.do(t, "POST", path, `{"name":"ci","role":"write"}`, "If-Match", "1"),
		http.StatusBadRequest, "invalid_request")
	s.expectError(t, s.do(t, "POST", path, `{"name":"ci","role":"write"}`, "If-Match", etag(tm.Version+1)),
		http.StatusPreconditionFailed, "conflict")

	k := s.issue(t, tm.ID, tm.Version, "ci", "write")
	if !strings.HasPrefix(k.Token, "brw_") || k.Key.Name != "ci" || k.Key.Role != "write" ||
		k.Key.Created.IsZero() || k.Key.Revoked != nil || k.Version <= tm.Version {
		t.Errorf("issued key response %+v, want token, the new version and the key", k)
	}
	g := s.do(t, "GET", "/v1/teams/"+tm.ID, "")
	h := access.HashToken(k.Token)
	for _, secret := range []string{k.Token, hex.EncodeToString(h[:])} {
		if bytes.Contains(g.body, []byte(secret)) {
			t.Errorf("GET team shows the token or its hash; neither ever leaves the service after issuing:\n%s", g.body)
		}
	}
	var team teamJSON
	g.json(t, &team)
	if len(team.Keys) != 1 || team.Version != k.Version {
		t.Errorf("GET after issuing = %s", g.body)
	}

	a := s.do(t, "POST", "/v1/authenticate", `{"token":"`+k.Token+`"}`)
	var who struct {
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
		Key    string `json:"key"`
		Role   string `json:"role"`
	}
	a.json(t, &who)
	if a.status != 200 || who.TeamID != tm.ID || who.Team != "Platform" || who.Key != "ci" || who.Role != "write" {
		t.Errorf("POST /v1/authenticate = %d %s", a.status, a.body)
	}

	s.expectError(t, s.do(t, "DELETE", path+"/ci", ""), http.StatusPreconditionRequired, "precondition_required")
	s.expectError(t, s.do(t, "DELETE", path+"/ci", "", "If-Match", etag(tm.Version)),
		http.StatusPreconditionFailed, "conflict")
	rv := s.do(t, "DELETE", path+"/ci", "", "If-Match", etag(k.Version))
	var after teamJSON
	rv.json(t, &after)
	if rv.status != 200 || len(after.Keys) != 1 || after.Keys[0].Revoked == nil || after.Version <= k.Version {
		t.Errorf("revoke = %d %s, want 200 and the team with ci revoked", rv.status, rv.body)
	}
	if rv.header.Get("ETag") != etag(after.Version) {
		t.Errorf("revoke ETag %q, want %q", rv.header.Get("ETag"), etag(after.Version))
	}
	s.expectError(t, s.do(t, "POST", "/v1/authenticate", `{"token":"`+k.Token+`"}`),
		http.StatusUnauthorized, "unauthenticated")
	s.expectError(t, s.do(t, "DELETE", path+"/ci", "", "If-Match", etag(after.Version)),
		http.StatusUnprocessableEntity, "revoked")
}

func TestLA_4_ErrorMapping(t *testing.T) {
	s := start(t, access.NewMemStore())
	tm := s.create(t, "Full")
	v := tm.Version
	for i := range access.MaxKeys {
		v = s.issue(t, tm.ID, v, fmt.Sprintf("k%d", i), "read").Version
	}
	other := s.create(t, "Other")
	keys := "/v1/teams/" + tm.ID + "/keys"
	cases := []struct {
		name, method, path, body string
		hdr                      []string
		status                   int
		code                     string
	}{
		{"missing team", "GET", "/v1/teams/nope", "", nil, 404, "not_found"},
		{"empty name", "POST", "/v1/teams", `{"name":""}`, nil, 422, "invalid"},
		{"unknown field", "POST", "/v1/teams", `{"nom":"x"}`, nil, 400, "invalid_request"},
		{"not JSON", "POST", "/v1/teams", `name=x`, nil, 400, "invalid_request"},
		{"two values", "POST", "/v1/teams", `{"name":"x"}{"name":"y"}`, nil, 400, "invalid_request"},
		{"duplicate key", "POST", keys, `{"name":"k0","role":"read"}`, []string{"If-Match", etag(v)}, 422, "duplicate_key"},
		{"eleventh key", "POST", keys, `{"name":"k10","role":"read"}`, []string{"If-Match", etag(v)}, 422, "too_many_keys"},
		{"bad role", "POST", "/v1/teams/" + other.ID + "/keys", `{"name":"x","role":"root"}`,
			[]string{"If-Match", etag(other.Version)}, 422, "invalid"},
		{"keys of missing team", "POST", "/v1/teams/nope/keys", `{"name":"x","role":"read"}`,
			[]string{"If-Match", `"1"`}, 404, "not_found"},
		{"revoke unknown key", "DELETE", keys + "/nope", "", []string{"If-Match", etag(v)}, 404, "not_found"},
		{"unknown token", "POST", "/v1/authenticate", `{"token":"brw_nope"}`, nil, 401, "unauthenticated"},
		{"delete missing team", "DELETE", "/v1/teams/nope", "", nil, 404, "not_found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s.expectError(t, s.do(t, c.method, c.path, c.body, c.hdr...), c.status, c.code)
		})
	}
	if r := s.do(t, "DELETE", "/v1/teams/"+other.ID, ""); r.status != http.StatusNoContent {
		t.Errorf("DELETE /v1/teams/{id} = %d, want 204", r.status)
	}
	s.expectError(t, s.do(t, "GET", "/v1/teams/"+other.ID, ""), 404, "not_found")
}

type brokenStore struct{ err error }

func (b brokenStore) Get(context.Context, string) (*access.Team, error) { return nil, b.err }
func (b brokenStore) Save(context.Context, *access.Team, uint64) error  { return b.err }
func (b brokenStore) Delete(context.Context, string) error              { return b.err }
func (b brokenStore) List(context.Context) ([]*access.Team, error)      { return nil, b.err }
func (b brokenStore) FindByKey(context.Context, [32]byte) (*access.Team, error) {
	return nil, b.err
}

func TestLA_4_InternalErrorsStayInside(t *testing.T) {
	s := start(t, brokenStore{errors.New("dial tcp 10.0.0.7:8080: secret-host refused")})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/teams/x", ""},
		{"POST", "/v1/teams", `{"name":"x"}`},
		{"POST", "/v1/authenticate", `{"token":"brw_x"}`},
	} {
		r := s.do(t, c.method, c.path, c.body)
		s.expectError(t, r, 500, "internal")
		if bytes.Contains(r.body, []byte("secret-host")) {
			t.Errorf("%s %s sent the internal error to the client: %s. Log it; tell the client only "+
				"that it failed, with the request ID", c.method, c.path, r.body)
		}
		if id := r.header.Get("X-Request-ID"); id == "" || !bytes.Contains(r.body, []byte(id)) {
			t.Errorf("a 500 should name the request ID %q so the log line can be found: %s", id, r.body)
		}
	}
	if !strings.Contains(s.log.String(), "secret-host") {
		t.Errorf("the internal error should be in the server's log; log was:\n%s", s.log)
	}
	if strings.Contains(s.log.String(), "brw_x") {
		t.Errorf("a token was logged")
	}
}

type panicStore struct{ brokenStore }

func (panicStore) Get(context.Context, string) (*access.Team, error) { panic("boom in the store") }

func TestLA_4_PanicIsRecovered(t *testing.T) {
	s := start(t, panicStore{brokenStore{errors.New("x")}})
	s.expectError(t, s.do(t, "GET", "/v1/teams/x", ""), 500, "internal")
	s.expectError(t, s.do(t, "GET", "/v1/teams/x", ""), 500, "internal")
	s.expectError(t, s.do(t, "POST", "/v1/teams", `{"name":"x"}`), 500, "internal")
}

// blockingStore blocks until the request's context ends.
type blockingStore struct {
	brokenStore
	mu  sync.Mutex
	got error
}

func (b *blockingStore) Get(ctx context.Context, id string) (*access.Team, error) {
	<-ctx.Done()
	b.mu.Lock()
	b.got = ctx.Err()
	b.mu.Unlock()
	return nil, ctx.Err()
}

func TestLA_4_DeadlineReachesTheStore(t *testing.T) {
	bs := &blockingStore{}
	s := start(t, bs)
	begin := time.Now()
	r := s.do(t, "GET", "/v1/teams/x", "")
	took := time.Since(begin)
	s.expectError(t, r, http.StatusServiceUnavailable, "unavailable")
	if took < 500*time.Millisecond || took > 3*time.Second {
		t.Errorf("the request took %v; want about 1 s, from api.Timeout(time.Second) in the chain", took)
	}
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if !errors.Is(bs.got, context.DeadlineExceeded) {
		t.Errorf("the store's context ended with %v, want context.DeadlineExceeded: pass r.Context() "+
			"through the Service to the store", bs.got)
	}
}

func TestLA_4_AccessLog(t *testing.T) {
	s := start(t, access.NewMemStore())
	r := s.do(t, "POST", "/v1/teams", `{"name":"Logged"}`)
	id := r.header.Get("X-Request-ID")
	found := false
	for _, line := range strings.Split(s.log.String(), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil || m["msg"] != "request" {
			continue
		}
		if m["id"] == id && m["path"] == "/v1/teams" && m["status"] == float64(201) {
			found = true
		}
	}
	if !found {
		t.Errorf("no access log line {msg: request, id: %s, path: /v1/teams, status: 201}; "+
			"use api.AccessLog(l) with the logger New was given. Log:\n%s", id, s.log)
	}
}

func TestLA_4_TwoClientsOneVersion(t *testing.T) {
	s := start(t, access.NewMemStore())
	tm := s.create(t, "Race")
	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"name":"k%d","role":"read"}`, i)
			req, _ := http.NewRequest("POST", s.url+"/v1/teams/"+tm.ID+"/keys", strings.NewReader(body))
			req.Header.Set("If-Match", etag(tm.Version))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}()
	}
	wg.Wait()
	created, failed := 0, 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 412:
			failed++
		}
	}
	if created != 1 || failed != n-1 {
		t.Errorf("status codes %v for %d clients sending the same If-Match; want one 201 and the rest 412", codes, n)
	}
}
