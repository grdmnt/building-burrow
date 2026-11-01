package access_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/access"
	"burrow/internal/testkit"
)

// la2Sink records published events. It can look at the store when Publish is
// called, to check events arrive after the save.
type la2Sink struct {
	mu     sync.Mutex
	events []access.Event
	calls  int
	onPub  func([]access.Event)
}

func (s *la2Sink) Publish(ctx context.Context, events []access.Event) {
	if s.onPub != nil {
		s.onPub(events)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.events = append(s.events, events...)
}

func (s *la2Sink) take() []access.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := s.events
	s.events = nil
	return ev
}

// la2SeqReader is a deterministic stand-in for crypto/rand.
type la2SeqReader struct {
	mu sync.Mutex
	n  byte
}

func (r *la2SeqReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range p {
		r.n++
		p[i] = r.n
	}
	return len(p), nil
}

type la2FailReader struct{}

func (la2FailReader) Read(p []byte) (int, error) { return 0, errors.New("entropy ran out") }

type la2Fixture struct {
	svc   *access.Service
	store *access.MemStore
	sink  *la2Sink
	clock *testkit.FakeClock
}

func la2NewFixture(t *testing.T) *la2Fixture {
	t.Helper()
	f := &la2Fixture{store: access.NewMemStore(), sink: &la2Sink{}, clock: testkit.NewFakeClock()}
	f.svc = access.NewService(f.store, access.Options{
		Clock: f.clock.Now, Rand: &la2SeqReader{}, Events: f.sink})
	return f
}

func (f *la2Fixture) team(t *testing.T, name string) *access.Team {
	t.Helper()
	tm, err := f.svc.CreateTeam(context.Background(), name)
	if err != nil {
		t.Fatalf("CreateTeam(%q) = %v", name, err)
	}
	return tm
}

func (f *la2Fixture) key(t *testing.T, tm *access.Team, name string) string {
	t.Helper()
	tok, nt, err := f.svc.IssueKey(context.Background(), tm.ID, tm.Version, name, access.RoleWrite)
	if err != nil {
		t.Fatalf("IssueKey(%s, v%d, %q) = %v", tm.ID, tm.Version, name, err)
	}
	*tm = *nt
	return tok
}

func la2Types(ev []access.Event) []string {
	var s []string
	for _, e := range ev {
		s = append(s, string(e.Type)+":"+e.Key)
	}
	return s
}

var (
	la2IdRE    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	la2TokenRE = regexp.MustCompile(`^brw_[0-9a-f]{64}$`)
)

func TestLA_2_CreateTeam(t *testing.T) {
	f := la2NewFixture(t)
	ctx := context.Background()
	tm := f.team(t, "Platform")
	if !la2IdRE.MatchString(tm.ID) {
		t.Errorf("team ID %q should be 16 bytes from Options.Rand, hex-encoded (32 characters)", tm.ID)
	}
	if tm.Name != "Platform" || tm.Version == 0 {
		t.Errorf("CreateTeam returned %+v; want the name and the version Save set", *tm)
	}
	got, err := f.svc.GetTeam(ctx, tm.ID)
	if err != nil || got.ID != tm.ID || got.Version != tm.Version {
		t.Fatalf("GetTeam(%s) = (%+v, %v), want the created team", tm.ID, got, err)
	}
	ev := f.sink.take()
	if len(ev) != 1 || ev[0].Type != access.TeamCreated || ev[0].TeamID != tm.ID ||
		!ev[0].At.Equal(f.clock.Now()) {
		t.Errorf("published %+v, want one team.created for %s at the injected clock's time", ev, tm.ID)
	}
	if other := f.team(t, "Platform"); other.ID == tm.ID {
		t.Errorf("two teams got the same ID %s", tm.ID)
	}

	for _, name := range []string{"", strings.Repeat("n", 65)} {
		f.sink.take()
		before, _ := f.svc.ListTeams(ctx)
		if _, err := f.svc.CreateTeam(ctx, name); !errors.Is(err, access.ErrInvalid) {
			t.Errorf("CreateTeam(%d-byte name) = %v, want ErrInvalid from the aggregate", len(name), err)
		}
		after, _ := f.svc.ListTeams(ctx)
		if len(after) != len(before) {
			t.Errorf("a rejected CreateTeam stored a team")
		}
		if ev := f.sink.take(); len(ev) != 0 {
			t.Errorf("a rejected CreateTeam published %+v", ev)
		}
	}
}

func TestLA_2_InjectedRandomness(t *testing.T) {
	mk := func(r io.Reader) *access.Service {
		return access.NewService(access.NewMemStore(), access.Options{Rand: r})
	}
	ctx := context.Background()
	a, b := mk(&la2SeqReader{}), mk(&la2SeqReader{})
	ta, err1 := a.CreateTeam(ctx, "x")
	tb, err2 := b.CreateTeam(ctx, "x")
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if ta.ID != tb.ID {
		t.Errorf("two services with identical Options.Rand made IDs %s and %s; "+
			"read the ID's bytes from Options.Rand, not crypto/rand directly", ta.ID, tb.ID)
	}
	ka, _, err1 := a.IssueKey(ctx, ta.ID, ta.Version, "ci", access.RoleRead)
	kb, _, err2 := b.IssueKey(ctx, tb.ID, tb.Version, "ci", access.RoleRead)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if ka != kb || !la2TokenRE.MatchString(ka) {
		t.Errorf("tokens %q and %q: want identical brw_ + 64 hex characters (32 bytes from Options.Rand)", ka, kb)
	}

	bad := mk(la2FailReader{})
	if tm, err := bad.CreateTeam(ctx, "x"); err == nil {
		t.Errorf("CreateTeam with a failing Rand = (%+v, nil), want the read error", tm)
	}
	if lst, _ := bad.ListTeams(ctx); len(lst) != 0 {
		t.Errorf("CreateTeam with a failing Rand stored a team anyway")
	}

	def := access.NewService(access.NewMemStore(), access.Options{})
	t1, err1 := def.CreateTeam(ctx, "x")
	t2, err2 := def.CreateTeam(ctx, "y")
	if err1 != nil || err2 != nil || t1.ID == t2.ID || !la2IdRE.MatchString(t1.ID) {
		t.Errorf("with zero Options (crypto/rand, time.Now, no la2Sink): %v %v %v %v", t1, err1, t2, err2)
	}
	if _, _, err := def.IssueKey(ctx, t1.ID, t1.Version, "k", access.RoleRead); err != nil {
		t.Errorf("IssueKey with no Events la2Sink = %v; a nil la2Sink means events are dropped", err)
	}
}

func TestLA_2_IssueKey(t *testing.T) {
	f := la2NewFixture(t)
	ctx := context.Background()
	tm := f.team(t, "Platform")
	f.sink.take()
	v := tm.Version
	f.clock.Advance(time.Hour)
	tok, nt, err := f.svc.IssueKey(ctx, tm.ID, v, "ci", access.RoleRead)
	if err != nil {
		t.Fatalf("IssueKey = %v", err)
	}
	if !la2TokenRE.MatchString(tok) {
		t.Errorf("token %q should be brw_ followed by 64 hex characters", tok)
	}
	if nt.Version <= v || len(nt.Keys) != 1 {
		t.Errorf("IssueKey should return the saved team (new version, one key), got v%d %+v", nt.Version, nt.Keys)
	}
	got, _ := f.svc.GetTeam(ctx, tm.ID)
	if len(got.Keys) != 1 || got.Keys[0].Hash != access.HashToken(tok) ||
		!got.Keys[0].Created.Equal(f.clock.Now()) || got.Version != nt.Version {
		t.Errorf("stored team %+v should hold the key with the token's hash, created at the clock's time", got)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), tok) {
		t.Errorf("the stored team contains the plaintext token")
	}
	if ev := la2Types(f.sink.take()); fmt.Sprint(ev) != "[key.issued:ci]" {
		t.Errorf("published %v, want [key.issued:ci] and nothing else (no repeat of team.created)", ev)
	}

	cases := []struct {
		name    string
		team    string
		version uint64
		key     string
		role    access.Role
		want    error
	}{
		{"stale version", tm.ID, v, "deploy", access.RoleRead, access.ErrConflict},
		{"future version", tm.ID, nt.Version + 1, "deploy", access.RoleRead, access.ErrConflict},
		{"version 0", tm.ID, 0, "deploy", access.RoleRead, access.ErrConflict},
		{"missing team", "nope", 1, "deploy", access.RoleRead, access.ErrTeamNotFound},
		{"duplicate", tm.ID, nt.Version, "ci", access.RoleRead, access.ErrDuplicateKey},
		{"bad role", tm.ID, nt.Version, "deploy", "root", access.ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok, res, err := f.svc.IssueKey(ctx, c.team, c.version, c.key, c.role)
			if !errors.Is(err, c.want) {
				t.Errorf("IssueKey = %v, want %v", err, c.want)
			}
			if tok != "" || res != nil {
				t.Errorf("a failed IssueKey should return \"\" and a nil team, got %q %v", tok, res)
			}
			if now, _ := f.svc.GetTeam(ctx, tm.ID); now.Version != nt.Version || len(now.Keys) != 1 {
				t.Errorf("a failed IssueKey changed the stored team: v%d %+v", now.Version, now.Keys)
			}
			if ev := f.sink.take(); len(ev) != 0 {
				t.Errorf("a failed IssueKey published %+v", ev)
			}
		})
	}
}

func TestLA_2_RevokeAndAuthenticate(t *testing.T) {
	f := la2NewFixture(t)
	ctx := context.Background()
	tm := f.team(t, "Platform")
	ci := f.key(t, tm, "ci")
	ro := f.key(t, tm, "ro")
	f.sink.take()

	gt, k, err := f.svc.Authenticate(ctx, ci)
	if err != nil || gt.ID != tm.ID || k.Name != "ci" || k.Role != access.RoleWrite {
		t.Fatalf("Authenticate(ci token) = (%v, %+v, %v), want team %s key ci", gt, k, err, tm.ID)
	}
	for _, tok := range []string{"", "brw_nope", ci + "x", strings.ToUpper(ci)} {
		if _, _, err := f.svc.Authenticate(ctx, tok); !errors.Is(err, access.ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) = %v, want ErrUnauthenticated", tok, err)
		}
	}

	if _, err := f.svc.RevokeKey(ctx, tm.ID, tm.Version-1, "ci"); !errors.Is(err, access.ErrConflict) {
		t.Errorf("RevokeKey with a stale version = %v, want ErrConflict", err)
	}
	nt, err := f.svc.RevokeKey(ctx, tm.ID, tm.Version, "ci")
	if err != nil || nt.Version <= tm.Version {
		t.Fatalf("RevokeKey = (%v, %v), want the saved team with a new version", nt, err)
	}
	if ev := la2Types(f.sink.take()); fmt.Sprint(ev) != "[key.revoked:ci]" {
		t.Errorf("published %v, want [key.revoked:ci]", ev)
	}
	if _, _, err := f.svc.Authenticate(ctx, ci); !errors.Is(err, access.ErrUnauthenticated) {
		t.Errorf("a revoked token authenticates: %v, want ErrUnauthenticated", err)
	}
	if _, _, err := f.svc.Authenticate(ctx, ro); err != nil {
		t.Errorf("revoking ci broke ro: %v", err)
	}
	if _, err := f.svc.RevokeKey(ctx, tm.ID, nt.Version, "ci"); !errors.Is(err, access.ErrRevoked) {
		t.Errorf("revoking twice = %v, want ErrRevoked", err)
	}
	if _, err := f.svc.RevokeKey(ctx, tm.ID, nt.Version, "nope"); !errors.Is(err, access.ErrKeyNotFound) {
		t.Errorf("revoking an unknown key = %v, want ErrKeyNotFound", err)
	}

	if err := f.svc.DeleteTeam(ctx, tm.ID); err != nil {
		t.Fatalf("DeleteTeam = %v", err)
	}
	if _, _, err := f.svc.Authenticate(ctx, ro); !errors.Is(err, access.ErrUnauthenticated) {
		t.Errorf("a deleted team's token = %v, want ErrUnauthenticated", err)
	}
	if err := f.svc.DeleteTeam(ctx, tm.ID); !errors.Is(err, access.ErrTeamNotFound) {
		t.Errorf("DeleteTeam twice = %v, want ErrTeamNotFound", err)
	}
}

// la2BrokenStore fails every call with err, to see how errors cross the
// Service boundary.
type la2BrokenStore struct{ err error }

func (b la2BrokenStore) Get(ctx context.Context, id string) (*access.Team, error) {
	return nil, b.err
}

func (b la2BrokenStore) Save(ctx context.Context, t *access.Team, v uint64) error { return b.err }

func (b la2BrokenStore) Delete(ctx context.Context, id string) error { return b.err }

func (b la2BrokenStore) List(ctx context.Context) ([]*access.Team, error) { return nil, b.err }

func (b la2BrokenStore) FindByKey(ctx context.Context, h [32]byte) (*access.Team, error) {
	return nil, b.err
}

func TestLA_2_OutageIsNotUnauthenticated(t *testing.T) {
	outage := errors.New("store unreachable")
	svc := access.NewService(la2BrokenStore{err: outage}, access.Options{})
	_, _, err := svc.Authenticate(context.Background(), "brw_x")
	if !errors.Is(err, outage) || errors.Is(err, access.ErrUnauthenticated) {
		t.Errorf("Authenticate during a store outage = %v; want the store's error, "+
			"not ErrUnauthenticated (an outage must not look like a bad token)", err)
	}
	if _, err := svc.GetTeam(context.Background(), "x"); !errors.Is(err, outage) {
		t.Errorf("GetTeam during an outage = %v, want the store's error", err)
	}
	if _, _, err := svc.IssueKey(context.Background(), "x", 1, "k", access.RoleRead); !errors.Is(err, outage) {
		t.Errorf("IssueKey during an outage = %v, want the store's error", err)
	}
}

// la2ConflictStore wraps a real store but every Save loses the race.
type la2ConflictStore struct{ access.TeamStore }

func (la2ConflictStore) Save(ctx context.Context, t *access.Team, v uint64) error {
	return access.ErrConflict
}

func TestLA_2_EventsOnlyAfterSave(t *testing.T) {
	ctx := context.Background()
	mem := access.NewMemStore()
	seed := access.NewService(mem, access.Options{})
	tm, _ := seed.CreateTeam(ctx, "x")

	sk := &la2Sink{}
	svc := access.NewService(la2ConflictStore{mem}, access.Options{Events: sk})
	if _, err := svc.CreateTeam(ctx, "y"); !errors.Is(err, access.ErrConflict) {
		t.Errorf("CreateTeam when Save fails = %v, want the store's ErrConflict", err)
	}
	if _, _, err := svc.IssueKey(ctx, tm.ID, tm.Version, "k", access.RoleRead); !errors.Is(err, access.ErrConflict) {
		t.Errorf("IssueKey when Save fails = %v, want ErrConflict", err)
	}
	if ev := sk.take(); len(ev) != 0 {
		t.Errorf("events were published for changes that were never saved: %+v", ev)
	}

	// With a working store, the change is visible by the time Publish runs.
	sk2 := &la2Sink{}
	svc2 := access.NewService(mem, access.Options{Events: sk2})
	sk2.onPub = func(ev []access.Event) {
		for _, e := range ev {
			got, err := mem.Get(ctx, e.TeamID)
			if err != nil {
				t.Errorf("Publish(%s) ran before the team was saved: %v", e.Type, err)
				continue
			}
			if e.Type == access.KeyIssued && len(got.Keys) == 0 {
				t.Errorf("Publish(key.issued) ran before the key was saved")
			}
		}
	}
	nt, _ := svc2.CreateTeam(ctx, "z")
	svc2.IssueKey(ctx, nt.ID, nt.Version, "k", access.RoleRead)
	if got := la2Types(sk2.take()); fmt.Sprint(got) != "[team.created: key.issued:k]" {
		t.Errorf("published %v, want [team.created: key.issued:k], each exactly once", got)
	}
}

type la2CtxKey struct{}

// la2CtxStore checks that every call carries the caller's context.
type la2CtxStore struct {
	access.TeamStore
	t *testing.T
}

func (s la2CtxStore) check(ctx context.Context, m string) {
	if ctx.Value(la2CtxKey{}) != "caller" {
		s.t.Errorf("TeamStore.%s got a context that isn't the caller's; pass ctx through, "+
			"never context.Background()", m)
	}
}

func (s la2CtxStore) Get(ctx context.Context, id string) (*access.Team, error) {
	s.check(ctx, "Get")
	return s.TeamStore.Get(ctx, id)
}

func (s la2CtxStore) Save(ctx context.Context, t *access.Team, v uint64) error {
	s.check(ctx, "Save")
	return s.TeamStore.Save(ctx, t, v)
}

func (s la2CtxStore) Delete(ctx context.Context, id string) error {
	s.check(ctx, "Delete")
	return s.TeamStore.Delete(ctx, id)
}

func (s la2CtxStore) List(ctx context.Context) ([]*access.Team, error) {
	s.check(ctx, "List")
	return s.TeamStore.List(ctx)
}

func (s la2CtxStore) FindByKey(ctx context.Context, h [32]byte) (*access.Team, error) {
	s.check(ctx, "FindByKey")
	return s.TeamStore.FindByKey(ctx, h)
}

func TestLA_2_ContextReachesTheStore(t *testing.T) {
	svc := access.NewService(la2CtxStore{access.NewMemStore(), t}, access.Options{})
	ctx := context.WithValue(context.Background(), la2CtxKey{}, "caller")
	tm, err := svc.CreateTeam(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	tok, tm, err := svc.IssueKey(ctx, tm.ID, tm.Version, "k", access.RoleRead)
	if err != nil {
		t.Fatal(err)
	}
	svc.Authenticate(ctx, tok)
	svc.GetTeam(ctx, tm.ID)
	svc.ListTeams(ctx)
	svc.RevokeKey(ctx, tm.ID, tm.Version, "k")
	svc.DeleteTeam(ctx, tm.ID)
}

func TestLA_2_ConcurrentIssueNoLostUpdate(t *testing.T) {
	testkit.RequireRace(t)
	f := la2NewFixture(t)
	tm := f.team(t, "x")
	f.sink.take()
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = f.svc.IssueKey(context.Background(), tm.ID, tm.Version,
				fmt.Sprintf("k%d", i), access.RoleRead)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, access.ErrConflict) {
			t.Errorf("concurrent IssueKey = %v, want nil or ErrConflict", err)
		}
	}
	got, _ := f.svc.GetTeam(context.Background(), tm.ID)
	if wins != 1 || len(got.Keys) != 1 {
		t.Errorf("%d writers at the same version succeeded and the team has %d keys; "+
			"want exactly one of each (the others get ErrConflict)", wins, len(got.Keys))
	}
	if ev := f.sink.take(); len(ev) != 1 {
		t.Errorf("published %d events for one successful change", len(ev))
	}
}

func TestLA_2_ListTeams(t *testing.T) {
	f := la2NewFixture(t)
	ctx := context.Background()
	if lst, err := f.svc.ListTeams(ctx); err != nil || len(lst) != 0 {
		t.Errorf("ListTeams on an empty store = (%v, %v)", lst, err)
	}
	var ids []string
	for _, n := range []string{"a", "b", "c"} {
		ids = append(ids, f.team(t, n).ID)
	}
	lst, err := f.svc.ListTeams(ctx)
	if err != nil || len(lst) != 3 {
		t.Fatalf("ListTeams = (%v, %v), want 3 teams", lst, err)
	}
	for i := 1; i < len(lst); i++ {
		if lst[i-1].ID >= lst[i].ID {
			t.Errorf("ListTeams should be sorted by ID: %s before %s", lst[i-1].ID, lst[i].ID)
		}
	}
}
