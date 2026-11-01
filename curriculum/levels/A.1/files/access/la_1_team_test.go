package access_test

import (
	"crypto/sha256"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"burrow/access"
)

var la1T0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func la1NewTeam(t *testing.T) *access.Team {
	t.Helper()
	team, err := access.NewTeam("t1", "Platform", la1T0)
	if err != nil {
		t.Fatalf("NewTeam(t1, Platform) = %v, want no error", err)
	}
	team.PullEvents()
	return team
}

func la1Issue(t *testing.T, team *access.Team, name string) access.Key {
	t.Helper()
	k, err := team.IssueKey(name, access.RoleWrite, "tok-"+name, la1T0)
	if err != nil {
		t.Fatalf("IssueKey(%q) = %v, want no error", name, err)
	}
	return k
}

// la1Snapshot copies the parts of a team a failed command must not change.
func la1Snapshot(team *access.Team) access.Team {
	c := *team
	c.Keys = slices.Clone(team.Keys)
	return c
}

func la1SameTeam(a, b access.Team) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Version == b.Version &&
		reflect.DeepEqual(a.Keys, b.Keys)
}

func TestLA_1_Constants(t *testing.T) {
	if access.RoleRead != "read" || access.RoleWrite != "write" {
		t.Errorf("RoleRead and RoleWrite should be \"read\" and \"write\" "+
			"(the words S.2's token file uses), got %q and %q",
			access.RoleRead, access.RoleWrite)
	}
	for _, r := range []access.Role{access.RoleRead, access.RoleWrite} {
		if !r.Valid() {
			t.Errorf("Role(%q).Valid() = false, want true", r)
		}
	}
	for _, r := range []access.Role{"", "admin", "READ", "write "} {
		if r.Valid() {
			t.Errorf("Role(%q).Valid() = true, want false", r)
		}
	}
	if access.MaxKeys != 10 {
		t.Errorf("MaxKeys = %d, want 10", access.MaxKeys)
	}
	if access.TeamCreated != "team.created" ||
		access.KeyIssued != "key.issued" ||
		access.KeyRevoked != "key.revoked" {
		t.Errorf("event types should be team.created, key.issued and "+
			"key.revoked, got %q %q %q",
			access.TeamCreated, access.KeyIssued, access.KeyRevoked)
	}
	errs := map[string]error{
		"ErrInvalid": access.ErrInvalid, "ErrDuplicateKey": access.ErrDuplicateKey,
		"ErrTooManyKeys": access.ErrTooManyKeys, "ErrRevoked": access.ErrRevoked,
		"ErrKeyNotFound": access.ErrKeyNotFound, "ErrTeamNotFound": access.ErrTeamNotFound,
		"ErrConflict": access.ErrConflict,
	}
	for n, e := range errs {
		if e == nil {
			t.Fatalf("%s is nil", n)
		}
		for m, f := range errs {
			if n != m && errors.Is(e, f) {
				t.Errorf("errors.Is(%s, %s) is true; each sentinel must be "+
					"its own errors.New value", n, m)
			}
		}
	}
}

func TestLA_1_HashToken(t *testing.T) {
	for _, tok := range []string{"", "brw_abc", strings.Repeat("x", 100)} {
		if got, want := access.HashToken(tok), sha256.Sum256([]byte(tok)); got != want {
			t.Errorf("HashToken(%q) should be sha256.Sum256 of the token's bytes", tok)
		}
	}
}

func TestLA_1_NewTeam(t *testing.T) {
	team, err := access.NewTeam("t1", "Platform", la1T0)
	if err != nil {
		t.Fatalf("NewTeam = %v", err)
	}
	if team.ID != "t1" || team.Name != "Platform" || len(team.Keys) != 0 ||
		team.Version != 0 {
		t.Errorf("NewTeam(t1, Platform) = %+v, want ID t1, Name Platform, "+
			"no keys, Version 0 (the store sets the version on Save)", *team)
	}
	ev := team.PullEvents()
	want := []access.Event{{Type: access.TeamCreated, TeamID: "t1", At: la1T0}}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("a new team should have recorded %+v, got %+v", want, ev)
	}
	if ev := team.PullEvents(); len(ev) != 0 {
		t.Errorf("PullEvents should forget what it returned; the second "+
			"call gave %+v", ev)
	}

	if _, err := access.NewTeam("t2", strings.Repeat("n", 64), la1T0); err != nil {
		t.Errorf("a 64-byte name is allowed, got %v", err)
	}
	bad := []struct{ name, id, team string }{
		{"empty name", "t1", ""},
		{"65-byte name", "t1", strings.Repeat("n", 65)},
		{"empty id", "", "Platform"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			team, err := access.NewTeam(c.id, c.team, la1T0)
			if !errors.Is(err, access.ErrInvalid) {
				t.Errorf("NewTeam(%q, %q) = %v, want an error wrapping ErrInvalid",
					c.id, c.team, err)
			}
			if team != nil {
				t.Errorf("a rejected NewTeam should return a nil *Team")
			}
		})
	}
}

func TestLA_1_IssueKey(t *testing.T) {
	team := la1NewTeam(t)
	at := la1T0.Add(time.Hour)
	k, err := team.IssueKey("ci", access.RoleRead, "brw_secret", at)
	if err != nil {
		t.Fatalf("IssueKey = %v", err)
	}
	want := access.Key{Name: "ci", Hash: sha256.Sum256([]byte("brw_secret")),
		Role: access.RoleRead, Created: at}
	if !reflect.DeepEqual(k, want) {
		t.Errorf("IssueKey returned %+v, want %+v (Revoked stays zero)", k, want)
	}
	if !k.Active() {
		t.Errorf("a new key should be Active")
	}
	if len(team.Keys) != 1 || !reflect.DeepEqual(team.Keys[0], want) {
		t.Errorf("team.Keys = %+v, want just the new key", team.Keys)
	}
	if ev := team.PullEvents(); !reflect.DeepEqual(ev, []access.Event{
		{Type: access.KeyIssued, TeamID: "t1", Key: "ci", At: at}}) {
		t.Errorf("IssueKey should record one key.issued event for ci at "+
			"the given time, got %+v", ev)
	}
	la1Issue(t, team, "deploy")
	if team.Keys[0].Name != "ci" || team.Keys[1].Name != "deploy" {
		t.Errorf("keys should stay in the order they were issued, got %+v",
			team.Keys)
	}
	v := reflect.ValueOf(team.Keys[0])
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.String &&
			strings.Contains(f.String(), "brw_secret") {
			t.Errorf("Key.%s holds the plaintext token. Store only its "+
				"SHA-256 hash; the token is shown once and never kept",
				v.Type().Field(i).Name)
		}
	}
}

func TestLA_1_IssueKeyRejects(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, team *access.Team)
		key   string
		role  access.Role
		token string
		want  error
	}{
		{"empty name", nil, "", access.RoleRead, "tok", access.ErrInvalid},
		{"65-byte name", nil, strings.Repeat("k", 65), access.RoleRead, "tok", access.ErrInvalid},
		{"no role", nil, "ci", "", "tok", access.ErrInvalid},
		{"unknown role", nil, "ci", "admin", "tok", access.ErrInvalid},
		{"empty token", nil, "ci", access.RoleRead, "", access.ErrInvalid},
		{"duplicate name", func(t *testing.T, team *access.Team) { la1Issue(t, team, "ci") },
			"ci", access.RoleRead, "other", access.ErrDuplicateKey},
		{"name of a revoked key", func(t *testing.T, team *access.Team) {
			la1Issue(t, team, "ci")
			if err := team.RevokeKey("ci", la1T0); err != nil {
				t.Fatal(err)
			}
		}, "ci", access.RoleRead, "other", access.ErrDuplicateKey},
		{"eleventh key", func(t *testing.T, team *access.Team) {
			for i := range 10 {
				la1Issue(t, team, "k"+string(rune('a'+i)))
			}
		}, "kz", access.RoleRead, "tok", access.ErrTooManyKeys},
		{"eleventh key with revoked ones", func(t *testing.T, team *access.Team) {
			for i := range 10 {
				la1Issue(t, team, "k"+string(rune('a'+i)))
			}
			team.RevokeKey("ka", la1T0)
			team.RevokeKey("kb", la1T0)
		}, "kz", access.RoleRead, "tok", access.ErrTooManyKeys},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			team := la1NewTeam(t)
			if c.setup != nil {
				c.setup(t, team)
			}
			team.PullEvents()
			before := la1Snapshot(team)
			_, err := team.IssueKey(c.key, c.role, c.token, la1T0)
			if !errors.Is(err, c.want) {
				t.Errorf("IssueKey(%q, %q, %q) = %v, want an error matching %v",
					c.key, c.role, c.token, err, c.want)
			}
			if !la1SameTeam(before, *team) {
				t.Errorf("a rejected IssueKey changed the team: before %+v, "+
					"after %+v. Check every rule before you change anything",
					before.Keys, team.Keys)
			}
			if ev := team.PullEvents(); len(ev) != 0 {
				t.Errorf("a rejected IssueKey recorded %+v; only a change "+
					"that happened records an event", ev)
			}
		})
	}
	team := la1NewTeam(t)
	for i := range 10 {
		la1Issue(t, team, "k"+string(rune('a'+i)))
	}
	if len(team.Keys) != access.MaxKeys {
		t.Errorf("ten keys should all be accepted, team has %d", len(team.Keys))
	}
}

func TestLA_1_RevokeKey(t *testing.T) {
	team := la1NewTeam(t)
	la1Issue(t, team, "ci")
	la1Issue(t, team, "deploy")
	team.PullEvents()
	at := la1T0.Add(2 * time.Hour)
	if err := team.RevokeKey("ci", at); err != nil {
		t.Fatalf("RevokeKey(ci) = %v", err)
	}
	if !team.Keys[0].Revoked.Equal(at) || team.Keys[0].Active() {
		t.Errorf("a revoked key should have Revoked set to the given time "+
			"and Active() false, got %+v", team.Keys[0])
	}
	if !team.Keys[1].Active() {
		t.Errorf("revoking ci also revoked deploy: %+v", team.Keys[1])
	}
	if len(team.Keys) != 2 {
		t.Errorf("a revoked key stays on the team (it is history), "+
			"got %d keys", len(team.Keys))
	}
	if ev := team.PullEvents(); !reflect.DeepEqual(ev, []access.Event{
		{Type: access.KeyRevoked, TeamID: "t1", Key: "ci", At: at}}) {
		t.Errorf("RevokeKey should record one key.revoked event, got %+v", ev)
	}

	before := la1Snapshot(team)
	if err := team.RevokeKey("ci", at.Add(time.Hour)); !errors.Is(err, access.ErrRevoked) {
		t.Errorf("revoking a revoked key = %v, want ErrRevoked", err)
	}
	if !la1SameTeam(before, *team) {
		t.Errorf("revoking twice changed the key: %+v. A revoked key stays "+
			"exactly as it was", team.Keys[0])
	}
	if err := team.RevokeKey("nope", at); !errors.Is(err, access.ErrKeyNotFound) {
		t.Errorf("revoking an unknown key = %v, want ErrKeyNotFound", err)
	}
	if ev := team.PullEvents(); len(ev) != 0 {
		t.Errorf("rejected RevokeKey calls recorded %+v", ev)
	}
}

func TestLA_1_Authenticate(t *testing.T) {
	team := la1NewTeam(t)
	la1Issue(t, team, "ci")
	if _, err := team.IssueKey("ro", access.RoleRead, "tok-ro", la1T0); err != nil {
		t.Fatal(err)
	}
	team.PullEvents()

	k, err := team.Authenticate("tok-ro")
	if err != nil || k.Name != "ro" || k.Role != access.RoleRead {
		t.Errorf("Authenticate(tok-ro) = (%+v, %v), want the ro key", k, err)
	}
	for _, tok := range []string{"tok-r", "tok-ro ", "tok-rp", "", "TOK-RO"} {
		if k, err := team.Authenticate(tok); !errors.Is(err, access.ErrKeyNotFound) {
			t.Errorf("Authenticate(%q) = (%+v, %v), want ErrKeyNotFound", tok, k, err)
		}
	}
	other, _ := access.NewTeam("t2", "Other", la1T0)
	other.IssueKey("x", access.RoleWrite, "tok-x", la1T0)
	if _, err := team.Authenticate("tok-x"); !errors.Is(err, access.ErrKeyNotFound) {
		t.Errorf("another team's token should be ErrKeyNotFound here, got %v", err)
	}

	team.RevokeKey("ci", la1T0)
	team.PullEvents()
	if k, err := team.Authenticate("tok-ci"); !errors.Is(err, access.ErrRevoked) {
		t.Errorf("a revoked key's token = (%+v, %v), want ErrRevoked. "+
			"Revoked keys never authenticate", k, err)
	}
	if _, err := team.Authenticate("tok-ro"); err != nil {
		t.Errorf("revoking ci broke ro: %v", err)
	}
	if ev := team.PullEvents(); len(ev) != 0 {
		t.Errorf("Authenticate is a query and must record nothing, got %+v", ev)
	}
}

func TestLA_1_EventsInOrder(t *testing.T) {
	team, _ := access.NewTeam("t9", "Nine", la1T0)
	team.IssueKey("a", access.RoleRead, "ta", la1T0.Add(1))
	team.IssueKey("b", access.RoleWrite, "tb", la1T0.Add(2))
	team.IssueKey("a", access.RoleWrite, "tc", la1T0.Add(3)) // rejected
	team.RevokeKey("a", la1T0.Add(4))
	want := []access.Event{
		{Type: access.TeamCreated, TeamID: "t9", At: la1T0},
		{Type: access.KeyIssued, TeamID: "t9", Key: "a", At: la1T0.Add(1)},
		{Type: access.KeyIssued, TeamID: "t9", Key: "b", At: la1T0.Add(2)},
		{Type: access.KeyRevoked, TeamID: "t9", Key: "a", At: la1T0.Add(4)},
	}
	if got := team.PullEvents(); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %+v\nwant     %+v", got, want)
	}
}

// The comparison of hashes must not stop at the first differing byte.
func TestLA_1_ConstantTimeCompare(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	found := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "ConstantTimeCompare" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "subtle" {
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Errorf("Authenticate should compare hashes with " +
			"subtle.ConstantTimeCompare from crypto/subtle; == on secrets " +
			"can return early and leak how many bytes matched")
	}
}
