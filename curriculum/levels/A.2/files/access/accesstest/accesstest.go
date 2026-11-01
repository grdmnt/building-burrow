// Package accesstest is a conformance suite every access.TeamStore must
// pass. It's provided by the curriculum: you don't edit it, you make your
// stores pass it. The same suite runs against access.NewMemStore (A.2) and
// against kvrepo over a Burrow store, local and over HTTP (A.3).
//
//	accesstest.RunTeamStore(t, func(t *testing.T) access.TeamStore {
//		return access.NewMemStore()
//	})
package accesstest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"burrow/access"
)

// Factory returns a new, empty store. Register teardown with t.Cleanup.
type Factory func(t *testing.T) access.TeamStore

var at = time.Date(2026, 5, 6, 7, 8, 9, 123456789, time.UTC)

func team(t *testing.T, id string, keys ...string) *access.Team {
	t.Helper()
	tm, err := access.NewTeam(id, "Team "+id, at)
	if err != nil {
		t.Fatalf("NewTeam: %v", err)
	}
	for i, k := range keys {
		role := access.RoleRead
		if i%2 == 1 {
			role = access.RoleWrite
		}
		if _, err := tm.IssueKey(k, role, Token(id, k), at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("IssueKey: %v", err)
		}
	}
	return tm
}

// Token is the plaintext token the suite issues for key k of team id.
func Token(id, k string) string { return "tok-" + id + "-" + k }

func save(t *testing.T, s access.TeamStore, tm *access.Team, expected uint64) {
	t.Helper()
	if err := s.Save(context.Background(), tm, expected); err != nil {
		t.Fatalf("Save(%s, expected %d) = %v, want nil", tm.ID, expected, err)
	}
}

func get(t *testing.T, s access.TeamStore, id string) *access.Team {
	t.Helper()
	tm, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get(%s) = %v, want the team", id, err)
	}
	return tm
}

// Equal reports how a and b differ (ignoring events), or "" when they
// match. Times are compared with Equal, so a store may lose the monotonic
// clock reading or the location.
func Equal(a, b *access.Team) string {
	if a.ID != b.ID || a.Name != b.Name || a.Version != b.Version {
		return fmt.Sprintf("ID/Name/Version %q/%q/%d vs %q/%q/%d",
			a.ID, a.Name, a.Version, b.ID, b.Name, b.Version)
	}
	if len(a.Keys) != len(b.Keys) {
		return fmt.Sprintf("%d keys vs %d", len(a.Keys), len(b.Keys))
	}
	for i := range a.Keys {
		x, y := a.Keys[i], b.Keys[i]
		if x.Name != y.Name || x.Hash != y.Hash || x.Role != y.Role ||
			!x.Created.Equal(y.Created) || !x.Revoked.Equal(y.Revoked) {
			return fmt.Sprintf("key %d: %+v vs %+v", i, x, y)
		}
	}
	return ""
}

// RunTeamStore checks the TeamStore contract.
func RunTeamStore(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("SaveThenGet", func(t *testing.T) {
		s := newStore(t)
		tm := team(t, "a1", "ci", "deploy")
		tm.RevokeKey("ci", at.Add(time.Minute))
		save(t, s, tm, 0)
		if tm.Version == 0 {
			t.Fatalf("Save should set team.Version to the stored version; it is still 0")
		}
		got := get(t, s, "a1")
		if d := Equal(got, tm); d != "" {
			t.Errorf("Get returned a different team than was saved: %s", d)
		}
	})

	t.Run("MissingIsErrTeamNotFound", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"nope", "has space", ""} {
			if tm, err := s.Get(ctx, id); !errors.Is(err, access.ErrTeamNotFound) || tm != nil {
				t.Errorf("Get(%q) = (%v, %v), want (nil, ErrTeamNotFound)", id, tm, err)
			}
		}
		if err := s.Delete(ctx, "nope"); !errors.Is(err, access.ErrTeamNotFound) {
			t.Errorf("Delete(missing) = %v, want ErrTeamNotFound", err)
		}
	})

	t.Run("CreateOnlyOnce", func(t *testing.T) {
		s := newStore(t)
		save(t, s, team(t, "a1"), 0)
		again := team(t, "a1", "x")
		if err := s.Save(ctx, again, 0); !errors.Is(err, access.ErrConflict) {
			t.Errorf("Save with expectedVersion 0 of an existing team = %v, want ErrConflict", err)
		}
		if again.Version != 0 {
			t.Errorf("a failed Save must not change team.Version, got %d", again.Version)
		}
		if got := get(t, s, "a1"); len(got.Keys) != 0 {
			t.Errorf("a conflicting Save overwrote the team: %+v", got.Keys)
		}
	})

	t.Run("StaleVersionIsConflict", func(t *testing.T) {
		s := newStore(t)
		save(t, s, team(t, "a1"), 0)
		first := get(t, s, "a1")
		second := get(t, s, "a1")
		v := first.Version
		first.IssueKey("one", access.RoleRead, "t1", at)
		save(t, s, first, v)
		if first.Version <= v {
			t.Errorf("version should grow on every save: %d after %d", first.Version, v)
		}
		second.IssueKey("two", access.RoleRead, "t2", at)
		if err := s.Save(ctx, second, v); !errors.Is(err, access.ErrConflict) {
			t.Fatalf("Save with a stale version = %v, want ErrConflict (lost update)", err)
		}
		got := get(t, s, "a1")
		if len(got.Keys) != 1 || got.Keys[0].Name != "one" || got.Version != first.Version {
			t.Errorf("after the conflict the store should still hold the first writer's team, got %+v v%d",
				got.Keys, got.Version)
		}
		if err := s.Save(ctx, team(t, "ghost"), 5); !errors.Is(err, access.ErrConflict) {
			t.Errorf("Save of a missing team with expectedVersion 5 = %v, want ErrConflict", err)
		}
		if _, err := s.Get(ctx, "ghost"); !errors.Is(err, access.ErrTeamNotFound) {
			t.Errorf("a conflicting Save created the team anyway")
		}
	})

	t.Run("NoAliasing", func(t *testing.T) {
		s := newStore(t)
		tm := team(t, "a1", "ci")
		save(t, s, tm, 0)
		tm.Keys[0].Name = "changed-after-save"
		tm.Name = "changed"
		got := get(t, s, "a1")
		if got.Keys[0].Name != "ci" || got.Name != "Team a1" {
			t.Fatalf("changing a team after Save changed the stored copy: %+v", got)
		}
		got.Keys[0].Role = access.RoleWrite
		got.Keys = append(got.Keys, access.Key{Name: "extra"})
		again := get(t, s, "a1")
		if len(again.Keys) != 1 || again.Keys[0].Role != access.RoleRead {
			t.Errorf("changing a team returned by Get changed the stored copy: %+v", again.Keys)
		}
	})

	t.Run("EventsAreNotStored", func(t *testing.T) {
		s := newStore(t)
		tm := team(t, "a1", "ci") // has team.created and key.issued pending
		save(t, s, tm, 0)
		if ev := get(t, s, "a1").PullEvents(); len(ev) != 0 {
			t.Errorf("a loaded team should have no pending events, got %+v. "+
				"Events belong to the change that recorded them, not to the stored state", ev)
		}
		if lst, _ := s.List(ctx); len(lst) == 1 {
			if ev := lst[0].PullEvents(); len(ev) != 0 {
				t.Errorf("List returned a team with pending events %+v", ev)
			}
		}
	})

	t.Run("Delete", func(t *testing.T) {
		s := newStore(t)
		save(t, s, team(t, "a1", "ci"), 0)
		save(t, s, team(t, "a2"), 0)
		if err := s.Delete(ctx, "a1"); err != nil {
			t.Fatalf("Delete = %v", err)
		}
		if _, err := s.Get(ctx, "a1"); !errors.Is(err, access.ErrTeamNotFound) {
			t.Errorf("Get after Delete = %v, want ErrTeamNotFound", err)
		}
		if err := s.Delete(ctx, "a1"); !errors.Is(err, access.ErrTeamNotFound) {
			t.Errorf("second Delete = %v, want ErrTeamNotFound", err)
		}
		if _, err := s.FindByKey(ctx, access.HashToken(Token("a1", "ci"))); !errors.Is(err, access.ErrTeamNotFound) {
			t.Errorf("FindByKey for a deleted team's key = %v, want ErrTeamNotFound", err)
		}
		get(t, s, "a2")
		save(t, s, team(t, "a1"), 0) // the ID is free again
	})

	t.Run("List", func(t *testing.T) {
		s := newStore(t)
		lst, err := s.List(ctx)
		if err != nil || len(lst) != 0 {
			t.Fatalf("List of an empty store = (%v, %v), want no teams", lst, err)
		}
		for _, id := range []string{"c3", "a1", "b2"} {
			save(t, s, team(t, id, "k"), 0)
		}
		s.Delete(ctx, "b2")
		lst, err = s.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, tm := range lst {
			ids = append(ids, tm.ID)
			if tm.Version == 0 || len(tm.Keys) != 1 {
				t.Errorf("List should return whole teams with their versions, got %+v", tm)
			}
		}
		if !slices.Equal(ids, []string{"a1", "c3"}) {
			t.Errorf("List IDs = %v, want [a1 c3] (sorted by ID, deleted ones gone)", ids)
		}
	})

	t.Run("FindByKey", func(t *testing.T) {
		s := newStore(t)
		save(t, s, team(t, "a1", "ci", "deploy"), 0)
		save(t, s, team(t, "a2", "ci"), 0)
		for _, c := range []struct{ id, key string }{{"a1", "ci"}, {"a1", "deploy"}, {"a2", "ci"}} {
			tm, err := s.FindByKey(ctx, access.HashToken(Token(c.id, c.key)))
			if err != nil || tm.ID != c.id {
				t.Errorf("FindByKey(hash of %s's %s token) = (%v, %v), want team %s", c.id, c.key, tm, err, c.id)
			} else if d := Equal(tm, get(t, s, c.id)); d != "" {
				t.Errorf("FindByKey should return the whole team, as Get does: %s", d)
			}
		}
		if tm, err := s.FindByKey(ctx, access.HashToken("unknown")); !errors.Is(err, access.ErrTeamNotFound) || tm != nil {
			t.Errorf("FindByKey(unknown) = (%v, %v), want (nil, ErrTeamNotFound)", tm, err)
		}

		// A key issued later is found once the team is saved.
		tm := get(t, s, "a2")
		tm.IssueKey("late", access.RoleRead, Token("a2", "late"), at)
		save(t, s, tm, tm.Version)
		if got, err := s.FindByKey(ctx, access.HashToken(Token("a2", "late"))); err != nil || got.ID != "a2" {
			t.Errorf("a key added by a later Save isn't found: (%v, %v)", got, err)
		}

		// Revoked keys still lead to their team; the team says no.
		tm = get(t, s, "a1")
		tm.RevokeKey("ci", at)
		save(t, s, tm, tm.Version)
		got, err := s.FindByKey(ctx, access.HashToken(Token("a1", "ci")))
		if err != nil || got.ID != "a1" {
			t.Fatalf("FindByKey for a revoked key = (%v, %v), want team a1 (Authenticate on the team rejects it)", got, err)
		}
		if _, err := got.Authenticate(Token("a1", "ci")); !errors.Is(err, access.ErrRevoked) {
			t.Errorf("the team FindByKey returned doesn't know the key is revoked: %v", err)
		}
	})

	t.Run("CancelledContext", func(t *testing.T) {
		s := newStore(t)
		save(t, s, team(t, "a1", "ci"), 0)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		calls := map[string]error{}
		_, calls["Get"] = s.Get(cctx, "a1")
		calls["Save"] = s.Save(cctx, team(t, "a2"), 0)
		calls["Delete"] = s.Delete(cctx, "a1")
		_, calls["List"] = s.List(cctx)
		_, calls["FindByKey"] = s.FindByKey(cctx, access.HashToken(Token("a1", "ci")))
		for name, err := range calls {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s with a cancelled context = %v, want an error wrapping context.Canceled "+
					"(wrap with %%w so the cause survives the boundary)", name, err)
			}
		}
		get(t, s, "a1")
		if _, err := s.Get(ctx, "a2"); !errors.Is(err, access.ErrTeamNotFound) {
			t.Errorf("Save with a cancelled context stored the team anyway")
		}
	})

	t.Run("ConcurrentWritersOneWins", func(t *testing.T) {
		s := newStore(t)
		save(t, s, team(t, "a1"), 0)
		const n = 8
		teams := make([]*access.Team, n)
		for i := range teams {
			teams[i] = get(t, s, "a1")
			teams[i].IssueKey(fmt.Sprintf("k%d", i), access.RoleRead, fmt.Sprintf("t%d", i), at)
		}
		v := teams[0].Version
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := range teams {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = s.Save(ctx, teams[i], v)
			}()
		}
		wg.Wait()
		wins := 0
		for i, err := range errs {
			switch {
			case err == nil:
				wins++
			case !errors.Is(err, access.ErrConflict):
				t.Errorf("writer %d: %v, want nil or ErrConflict", i, err)
			}
		}
		if wins != 1 {
			t.Errorf("%d of %d writers with the same expected version succeeded, want exactly 1", wins, n)
		}
		if got := get(t, s, "a1"); len(got.Keys) != 1 {
			t.Errorf("the stored team has %d keys, want the winner's one", len(got.Keys))
		}
	})
}
