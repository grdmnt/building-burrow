package access_test

import (
	"testing"

	"burrow/internal/testkit"
)

// Your own tests in access/ are graded here. Each mutant is your Team with
// one rule broken; at least one of your tests must fail for each.
func TestLA_1_YourTestsCatchBugs(t *testing.T) {
	testkit.CheckYourTests(t, testkit.MutationCheck{
		Dir:             "access",
		RequireSubtests: true,
		Mutants: []testkit.Mutant{
			{
				Bug:    "IssueKey accepts a second key with the same name",
				Target: "Team.IssueKey",
				Source: `import "time"

func (t *Team) IssueKey(name string, role Role, token string, now time.Time) (Key, error) {
	for i := range t.Keys {
		if t.Keys[i].Name == name {
			t.Keys[i].Name = name + "\x00hidden"
			k, err := t.OrigIssueKey(name, role, token, now)
			t.Keys[i].Name = name
			return k, err
		}
	}
	return t.OrigIssueKey(name, role, token, now)
}
`,
			},
			{
				Bug:    "IssueKey accepts an eleventh key",
				Target: "Team.IssueKey",
				Source: `import "time"

func (t *Team) IssueKey(name string, role Role, token string, now time.Time) (Key, error) {
	if len(t.Keys) == MaxKeys {
		first := t.Keys[0]
		t.Keys = t.Keys[1:]
		k, err := t.OrigIssueKey(name, role, token, now)
		t.Keys = append([]Key{first}, t.Keys...)
		return k, err
	}
	return t.OrigIssueKey(name, role, token, now)
}
`,
			},
			{
				Bug:    "RevokeKey on an already revoked key succeeds",
				Target: "Team.RevokeKey",
				Source: `import (
	"errors"
	"time"
)

func (t *Team) RevokeKey(name string, now time.Time) error {
	err := t.OrigRevokeKey(name, now)
	if errors.Is(err, ErrRevoked) {
		return nil
	}
	return err
}
`,
			},
			{
				Bug:    "Authenticate accepts a revoked key",
				Target: "Team.Authenticate",
				Source: `import "errors"

func (t *Team) Authenticate(token string) (Key, error) {
	k, err := t.OrigAuthenticate(token)
	if errors.Is(err, ErrRevoked) {
		h := HashToken(token)
		for _, k := range t.Keys {
			if k.Hash == h {
				return k, nil
			}
		}
	}
	return k, err
}
`,
			},
			{
				Bug:    "IssueKey records no key.issued event",
				Target: "Team.PullEvents",
				Source: `func (t *Team) PullEvents() []Event {
	var out []Event
	for _, e := range t.OrigPullEvents() {
		if e.Type != KeyIssued {
			out = append(out, e)
		}
	}
	return out
}
`,
			},
		},
	})
}
