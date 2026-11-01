package kvrepo_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/access"
	"burrow/access/accesstest"
	"burrow/access/kvrepo"
	"burrow/api"
	"burrow/sdk/burrow"
	"burrow/store"
)

// Both Burrow stores satisfy kvrepo's port without knowing it exists.
var (
	_ kvrepo.KV = (*store.Memory)(nil)
	_ kvrepo.KV = (*burrow.Client)(nil)
)

func memory(t *testing.T) *store.Memory {
	m := store.NewMemory()
	t.Cleanup(func() { m.Close() })
	return m
}

func httpClient(t *testing.T) *burrow.Client {
	t.Helper()
	srv := httptest.NewServer((&api.Server{Store: memory(t)}).Handler())
	t.Cleanup(srv.Close)
	c, err := burrow.New(srv.URL, burrow.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLA_3_ConformanceOverMemory(t *testing.T) {
	accesstest.RunTeamStore(t, func(t *testing.T) access.TeamStore {
		return kvrepo.New(memory(t))
	})
}

func TestLA_3_ConformanceOverHTTP(t *testing.T) {
	accesstest.RunTeamStore(t, func(t *testing.T) access.TeamStore {
		return kvrepo.New(httpClient(t))
	})
}

// onlyKV has exactly the four methods the Reference lists, and nothing
// else: no Set, no Delete, no Batch.
type onlyKV struct {
	m     *store.Memory
	mu    sync.Mutex
	fail  func(op, key string) error
	calls []string
}

func (o *onlyKV) note(op, key string) error {
	o.mu.Lock()
	o.calls = append(o.calls, op+" "+key)
	f := o.fail
	o.mu.Unlock()
	if f != nil {
		return f(op, key)
	}
	return nil
}

func (o *onlyKV) Get(ctx context.Context, key string) (store.Entry, error) {
	if err := o.note("get", key); err != nil {
		return store.Entry{}, err
	}
	return o.m.Get(ctx, key)
}

func (o *onlyKV) CompareAndSwap(ctx context.Context, key string, v uint64, val []byte) (store.Entry, error) {
	if err := o.note("cas", key); err != nil {
		return store.Entry{}, err
	}
	return o.m.CompareAndSwap(ctx, key, v, val)
}

func (o *onlyKV) CompareAndDelete(ctx context.Context, key string, v uint64) error {
	if err := o.note("cad", key); err != nil {
		return err
	}
	return o.m.CompareAndDelete(ctx, key, v)
}

func (o *onlyKV) Keys(ctx context.Context, prefix string) ([]string, error) {
	if err := o.note("keys", prefix); err != nil {
		return nil, err
	}
	return o.m.Keys(ctx, prefix)
}

func (o *onlyKV) writes() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var w []string
	for _, c := range o.calls {
		if strings.HasPrefix(c, "cas ") || strings.HasPrefix(c, "cad ") {
			w = append(w, c)
		}
	}
	o.calls = nil
	return w
}

type seq struct {
	mu sync.Mutex
	n  byte
}

func (r *seq) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range p {
		r.n++
		p[i] = r.n
	}
	return len(p), nil
}

func idx(token string) string {
	h := access.HashToken(token)
	return "access/key/" + hex.EncodeToString(h[:])
}

func TestLA_3_KeyLayout(t *testing.T) {
	ctx := context.Background()
	m := memory(t)
	var ts access.TeamStore = kvrepo.New(&onlyKV{m: m})
	svc := access.NewService(ts, access.Options{Rand: &seq{}})
	tm, err := svc.CreateTeam(ctx, "Platform")
	if err != nil {
		t.Fatal(err)
	}
	tokA, tm, err := svc.IssueKey(ctx, tm.ID, tm.Version, "a", access.RoleRead)
	if err != nil {
		t.Fatal(err)
	}
	tokB, tm, err := svc.IssueKey(ctx, tm.ID, tm.Version, "b", access.RoleWrite)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := m.Keys(ctx, "")
	want := []string{idx(tokA), idx(tokB), "access/team/" + tm.ID}
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Fatalf("store keys = %q\nwant %q\n(one key per team, access/team/<id>, plus one "+
			"access/key/<lowercase hex SHA-256> index entry per API key)", keys, want)
	}
	e, _ := m.Get(ctx, "access/team/"+tm.ID)
	if e.Version != tm.Version {
		t.Errorf("Team.Version is %d but the KV entry is at version %d; the team's version "+
			"is the entry's version", tm.Version, e.Version)
	}
	var doc map[string]any
	if err := json.Unmarshal(e.Value, &doc); err != nil {
		t.Errorf("the team's value should be a JSON object: %v\n%s", err, e.Value)
	}
	for _, tok := range []string{tokA, tokB} {
		if strings.Contains(string(e.Value), tok) || strings.Contains(string(e.Value), tok[4:]) {
			t.Errorf("the stored team contains a plaintext token:\n%s", e.Value)
		}
		ie, err := m.Get(ctx, idx(tok))
		if err != nil || string(ie.Value) != tm.ID {
			t.Errorf("index entry %s = (%q, %v), want the team ID %q", idx(tok), ie.Value, err, tm.ID)
		}
	}
}

func TestLA_3_DomainHasNoJSONTags(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(access.Team{}), reflect.TypeOf(access.Key{})} {
		for i := range typ.NumField() {
			f := typ.Field(i)
			if _, ok := f.Tag.Lookup("json"); ok {
				t.Errorf("access.%s.%s has a json tag. Keep the domain types persistence-ignorant: "+
					"give kvrepo its own document struct and convert at the boundary", typ.Name(), f.Name)
			}
		}
	}
}

func TestLA_3_WritesAreCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	m := memory(t)
	kv := &onlyKV{m: m}
	repo := kvrepo.New(kv)
	tm, _ := access.NewTeam("t1", "One", time.Now())
	if err := repo.Save(ctx, tm, 0); err != nil {
		t.Fatal(err)
	}
	if w := kv.writes(); len(w) == 0 || w[0] != "cas access/team/t1" {
		t.Errorf("creating a team should start with CompareAndSwap(access/team/t1, 0, ...), writes were %q", w)
	}
	// Another writer changes the team behind our back.
	stale := tm.Version
	if _, err := m.Set(ctx, "access/team/t1", []byte(`{"id":"t1","name":"Other","keys":[]}`)); err != nil {
		t.Fatal(err)
	}
	tm.IssueKey("k", access.RoleRead, "tok", time.Now())
	if err := repo.Save(ctx, tm, stale); !errors.Is(err, access.ErrConflict) {
		t.Errorf("Save over a newer entry = %v, want access.ErrConflict", err)
	}
	if e, _ := m.Get(ctx, "access/team/t1"); !strings.Contains(string(e.Value), "Other") {
		t.Errorf("a conflicting Save overwrote the other writer: %s", e.Value)
	}
	if _, err := m.Get(ctx, idx("tok")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a Save that lost the race still wrote an index entry for its key")
	}
}

func TestLA_3_IndexWrittenSecond(t *testing.T) {
	ctx := context.Background()
	m := memory(t)
	kv := &onlyKV{m: m}
	repo := kvrepo.New(kv)
	tm, _ := access.NewTeam("t1", "One", time.Now())
	if err := repo.Save(ctx, tm, 0); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("index write lost")
	kv.mu.Lock()
	kv.fail = func(op, key string) error {
		if op == "cas" && strings.HasPrefix(key, "access/key/") {
			return injected
		}
		return nil
	}
	kv.mu.Unlock()
	tm.IssueKey("a", access.RoleRead, "tok-a", time.Now())
	v := tm.Version
	if err := repo.Save(ctx, tm, v); err == nil {
		t.Fatalf("Save returned nil although its index write failed; report it")
	} else if !errors.Is(err, injected) {
		t.Errorf("Save's error %v should wrap the KV's error (use %%w)", err)
	}
	got, err := repo.Get(ctx, "t1")
	if err != nil || len(got.Keys) != 1 {
		t.Fatalf("after the failed index write, Get = (%+v, %v); the team itself should already "+
			"be saved, because the team is written first and the index second", got, err)
	}
	if _, err := repo.FindByKey(ctx, access.HashToken("tok-a")); !errors.Is(err, access.ErrTeamNotFound) {
		t.Errorf("FindByKey for an unindexed key = %v, want ErrTeamNotFound", err)
	}

	kv.mu.Lock()
	kv.fail = nil
	kv.mu.Unlock()
	got.IssueKey("b", access.RoleRead, "tok-b", time.Now())
	if err := repo.Save(ctx, got, got.Version); err != nil {
		t.Fatalf("Save = %v", err)
	}
	for _, tok := range []string{"tok-a", "tok-b"} {
		if f, err := repo.FindByKey(ctx, access.HashToken(tok)); err != nil || f.ID != "t1" {
			t.Errorf("FindByKey(%s) = (%v, %v) after the next Save; every Save writes the index "+
				"entries that are missing, which repairs the one that failed", tok, f, err)
		}
	}
}

func TestLA_3_IndexIsReverified(t *testing.T) {
	ctx := context.Background()
	m := memory(t)
	repo := kvrepo.New(m)
	svc := access.NewService(repo, access.Options{})
	tm, _ := svc.CreateTeam(ctx, "One")
	tok, tm, _ := svc.IssueKey(ctx, tm.ID, tm.Version, "real", access.RoleWrite)

	// An index entry for a token the team never issued.
	m.Set(ctx, idx("forged"), []byte(tm.ID))
	if f, err := repo.FindByKey(ctx, access.HashToken("forged")); !errors.Is(err, access.ErrTeamNotFound) {
		t.Errorf("FindByKey trusted an index entry the team doesn't back: (%v, %v). "+
			"Load the team and check it holds the hash", f, err)
	}
	if _, _, err := svc.Authenticate(ctx, "forged"); !errors.Is(err, access.ErrUnauthenticated) {
		t.Errorf("Authenticate(forged) = %v, want ErrUnauthenticated", err)
	}
	// An index entry pointing at a team that is gone.
	m.Set(ctx, idx("orphan"), []byte("no-such-team"))
	if _, err := repo.FindByKey(ctx, access.HashToken("orphan")); !errors.Is(err, access.ErrTeamNotFound) {
		t.Errorf("FindByKey for an orphaned index entry = %v, want ErrTeamNotFound", err)
	}
	if _, _, err := svc.Authenticate(ctx, tok); err != nil {
		t.Errorf("the real token stopped working: %v", err)
	}
}

func TestLA_3_ErrorsCrossTheBoundary(t *testing.T) {
	ctx := context.Background()
	m := memory(t)
	kv := &onlyKV{m: m}
	repo := kvrepo.New(kv)
	tm, _ := access.NewTeam("t1", "One", time.Now())
	repo.Save(ctx, tm, 0)

	outage := errors.New("burrow unreachable")
	kv.mu.Lock()
	kv.fail = func(op, key string) error { return outage }
	kv.mu.Unlock()
	calls := map[string]error{}
	_, calls["Get"] = repo.Get(ctx, "t1")
	calls["Save"] = repo.Save(ctx, tm, tm.Version)
	calls["Delete"] = repo.Delete(ctx, "t1")
	_, calls["List"] = repo.List(ctx)
	_, calls["FindByKey"] = repo.FindByKey(ctx, access.HashToken("x"))
	for name, err := range calls {
		if !errors.Is(err, outage) {
			t.Errorf("%s during an outage = %v, want an error wrapping the KV's error", name, err)
		}
		if errors.Is(err, access.ErrTeamNotFound) || errors.Is(err, access.ErrConflict) {
			t.Errorf("%s turned an outage into %v; only store.ErrNotFound means not found and "+
				"only a version mismatch means conflict", name, err)
		}
	}

	kv.mu.Lock()
	kv.fail = nil
	kv.mu.Unlock()
	m.Set(ctx, "access/team/bad", []byte("{not json"))
	if _, err := repo.Get(ctx, "bad"); err == nil || errors.Is(err, access.ErrTeamNotFound) {
		t.Errorf("Get of an undecodable team = %v, want a decode error (not ErrTeamNotFound)", err)
	}
}

// Two services, as two accessd processes would be, sharing one Burrow over
// HTTP: the version check is what keeps them honest.
func TestLA_3_TwoServicesOneBurrow(t *testing.T) {
	ctx := context.Background()
	c := httpClient(t)
	a := access.NewService(kvrepo.New(c), access.Options{})
	b := access.NewService(kvrepo.New(c), access.Options{})
	tm, err := a.CreateTeam(ctx, "Shared")
	if err != nil {
		t.Fatal(err)
	}
	seen, err := b.GetTeam(ctx, tm.ID)
	if err != nil || seen.Version != tm.Version {
		t.Fatalf("b.GetTeam = (%v, %v), want the team a created", seen, err)
	}
	tokA, _, errA := a.IssueKey(ctx, tm.ID, tm.Version, "from-a", access.RoleRead)
	_, _, errB := b.IssueKey(ctx, seen.ID, seen.Version, "from-b", access.RoleRead)
	if errA != nil || !errors.Is(errB, access.ErrConflict) {
		t.Fatalf("a then b at the same version: a=%v b=%v, want a=nil b=ErrConflict", errA, errB)
	}
	if _, _, err := b.Authenticate(ctx, tokA); err != nil {
		t.Errorf("b can't authenticate a token a issued: %v", err)
	}
	lst, err := b.ListTeams(ctx)
	if err != nil || len(lst) != 1 || len(lst[0].Keys) != 1 {
		t.Errorf("b.ListTeams = (%v, %v), want the one team with a's key", lst, err)
	}

	var wg sync.WaitGroup
	cur, _ := a.GetTeam(ctx, tm.ID)
	errs := make([]error, 6)
	for i := range errs {
		svc := a
		if i%2 == 1 {
			svc = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = svc.IssueKey(ctx, cur.ID, cur.Version, fmt.Sprintf("k%d", i), access.RoleRead)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, access.ErrConflict) {
			t.Errorf("concurrent IssueKey over HTTP = %v, want nil or ErrConflict", err)
		}
	}
	if final, _ := a.GetTeam(ctx, tm.ID); wins != 1 || len(final.Keys) != 2 {
		t.Errorf("%d concurrent writers won and the team has %d keys; want 1 winner, 2 keys", wins, len(final.Keys))
	}
}
