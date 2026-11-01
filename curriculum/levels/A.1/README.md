# A.1 · Aggregate

> Rules live on the type that owns the data. Write them in plain Go, with nothing to mock, and every layer above can trust them.

## Your goal

Chapter A answers the question most Rails developers ask in their first Go service: where do the controllers, the service objects and the models go? Over four levels you build a small second service, `accessd`, that manages *teams* and their *API keys* and keeps them in Burrow. Each level is one layer, and each layer is one Go package:

```text
access/            package access: the domain (A.1), the service (A.2)
access/kvrepo/     keeps teams in Burrow (A.3)
access/httpapi/    the HTTP edge (A.4)
cmd/accessd/       main: builds the real pieces and wires them (A.4)
```

This level is the innermost layer: `access.Team`, an *aggregate*. A team has a name, a list of keys and a version. A key has a name, a role (`read` or `write`), the SHA-256 hash of its secret token, when it was created and when it was revoked. You write the three things a team can do (be created, issue a key, revoke a key), the one question you can ask it (whose token is this?), and the rules that make those safe: key names are unique within a team, a team holds at most 10 keys, and a revoked key never authenticates and never comes back. Every change also records an *event*, a small value saying what happened, for A.2 to publish.

The package does no I/O at all. It doesn't read the clock, doesn't generate random tokens and doesn't know where teams are stored: the time and the token are arguments. That makes it the easiest code in the chapter to test, and you write those tests yourself, graded by mutation as in 0.2.

## What you're using from before

Nothing from Burrow's packages, on purpose: by the end of the chapter `access` must not import any of them, and A.4's tests check that. Chapter A comes after chapter 7 because A.3 stores teams through the HTTP client (7.5) and A.4 reuses the middleware (7.3, 7.4), not because the domain needs them.

What you do reuse are habits from earlier levels:

- **Sentinel errors (0.2).** `ErrInvalidKey` and `errors.Is` from 0.2 are exactly how the domain names its failures: `ErrDuplicateKey`, `ErrTooManyKeys` and the rest.
- **Your own tests, graded by mutation (0.2, 4.3, 7.1).** The grader plants a bug in your `Team`, runs only your tests, and expects one to fail.
- **Versions (1.3).** `Team.Version` will hold the Burrow entry version that 1.3's compare-and-swap checks. In this level it is just a field that nothing changes; A.2 and A.3 give it a job.

## What's new here

**1. Go has no Rails.** No framework decides where files go. A package is a folder, a folder's import path is its place in the module, and the compiler refuses import cycles, so dependencies always point one way. Everything else is a choice. This chapter uses one well-known layout (Ben Johnson's "standard package layout"): the domain types live in a package named after the business area, here `access`; every outside dependency gets its own sub-package named after what it adapts (`kvrepo` for the key-value store, `httpapi` for HTTP); and `main` wires them together. Callers read `access.Team` and `access.ErrRevoked`, which say what they are.

Avoid packages called `domain`, `models`, `app` or `service`. `domain.Team` says nothing that `Team` didn't, and a package named after a layer tends to collect every type in the program. If you catch yourself writing `package domain`, ask which business area it is and name it that. The tests in A.4 check which way the imports point, not what you call things inside a package.

**2. Aggregates and invariants.** An *invariant* is a rule that must hold whenever anyone can look at the data, such as "no two keys on a team share a name". An *aggregate* is a group of data that changes together and is saved as a whole, with one type, the *root*, that every change goes through, so it can keep the invariants. Here the root is `Team`: a key is part of its team, and you change a key only by calling a method on the team.

In Rails you'd reach for `has_many :keys` and `validates :name, uniqueness: { scope: :team_id }`, which checks the rule in Ruby and again with a database index. Here the rule is a few lines in `IssueKey`, and it is enough, because A.3 saves the whole team as one value under one key: there is no second row to race with.

**3. Methods that change their receiver.** A method declared on `*Team` (a *pointer receiver*) changes the team it is called on; on `Team` it would change a copy. Every command follows the same pattern: check every rule first, and only when all of them pass, change the fields and record the event. A command that fails leaves the team exactly as it was.

```go
func (t *Team) RevokeKey(name string, now time.Time) error {
	i := t.find(name) // your unexported helper
	if i < 0 {
		return ErrKeyNotFound
	}
	if !t.Keys[i].Active() {
		return ErrRevoked // already revoked: change nothing
	}
	t.Keys[i].Revoked = now
	t.record(KeyRevoked, name, now)
	return nil
}
```

**4. Exported fields are fine.** `Team.Keys` is exported, so any package could append to it and skip the rules. Go has no read-only fields; the alternatives are getters for everything or a separate type per layer, and both cost more than they save in a package this small. The convention is enough: the domain changes through methods, and the only other code that writes the fields is A.3's repository, rebuilding a team from storage.

**5. Errors are the domain's vocabulary.** Each way a command can fail is a sentinel error, a package-level `var` made with `errors.New`. Add detail with `%w`, which wraps the sentinel so `errors.Is` still finds it:

```go
var ErrInvalid = errors.New("access: invalid")

return Key{}, fmt.Errorf("%w: role must be read or write", ErrInvalid)

// a caller
if errors.Is(err, access.ErrInvalid) { /* 422 in A.4 */ }
```

If you know dry-monads, `(Key, error)` is Go's `Result`: the error is either `nil` (Success) or a value you inspect with `errors.Is` (Failure). There is no `bind`; you write `if err != nil { return ..., err }` at each step instead.

**6. Pass the world in.** A method that called `time.Now()` would make every test depend on the clock. So the commands take `now time.Time` as an argument, and `IssueKey` takes the plaintext token instead of generating one. A.2's service owns the clock and the random source and passes them in. The domain stays *pure*: same inputs, same result, no setup.

**7. Storing a hash, not the token.** A token is a secret that proves who you are, like a password. If you stored tokens and the store leaked, every key would leak with it. So the team keeps only `sha256.Sum256([]byte(token))`, a 32-byte fingerprint that can't be turned back into the token, and checks a presented token by hashing it and comparing fingerprints. The plaintext is shown to the user once, when the key is issued, and never kept.

```go
func HashToken(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}
```

`[32]byte` is an *array*, not a slice: its length is part of its type, it is copied when assigned, and two arrays compare with `==`. A fast hash like SHA-256 is right here because A.2 makes tokens from 32 random bytes, so there is nothing to guess; passwords that people choose need a slow hash such as bcrypt instead.

**8. Comparing secrets in constant time.** `==` on two hashes may stop at the first byte that differs, so a careful attacker can learn, from how long it took, how many leading bytes they got right. `subtle.ConstantTimeCompare(a, b)` from `crypto/subtle` always looks at every byte and returns 1 when they match. It takes slices; `h[:]` gives you a slice of an array.

```go
h := HashToken(token)
if subtle.ConstantTimeCompare(h[:], k.Hash[:]) == 1 {
	// match
}
```

**9. Recording events.** An *event* is a value that says what happened: `{Type: "key.issued", TeamID: "...", Key: "ci", At: now}`. Each successful command appends one to an unexported slice on the team, and `PullEvents` returns them and empties the slice. Nothing in this level publishes them: A.2 saves the team first and then hands the events to whoever is listening. If you've used Rails Event Store's `AggregateRoot`, this is its `unpublished_events`. It is not event sourcing: the team's current state is what gets stored, and the events are a side output.

**10. String types as enums.** `type Role string` with `const RoleRead Role = "read"` gives you a distinct type with a fixed set of values, but any string converts to it, so add a `Valid()` method and check it in `IssueKey`. S.2 uses `iota` for roles; a string type suits this level because the role is stored and sent as text, and `"read"` is easier to read in a stored document than `1`.

## What to do

1. Create `access/` with `package access`. File names are up to you (`team.go`, `errors.go`, `events.go` all work); only the names in the Reference are pinned.
2. Define the seven sentinel errors, `MaxKeys = 10`, `Role` with `RoleRead` and `RoleWrite` and `Valid()`, `Key` with `Active()`, and `HashToken`.
3. Define `Team`, `Event`, `EventType` and the three event types, plus an unexported `[]Event` field on `Team`.
4. Write `NewTeam(id, name, now)`: reject an empty ID and a name that is empty or longer than 64 bytes with `ErrInvalid`; otherwise return the team with a `team.created` event recorded.
5. Write `IssueKey(name, role, token, now)`. Check, in this order: the name is 1 to 64 bytes, the role is valid and the token isn't empty (all `ErrInvalid`); no key on the team has that name, revoked ones included (`ErrDuplicateKey`); the team has fewer than 10 keys, revoked ones included (`ErrTooManyKeys`). Then append the key with the token's hash and `Created: now`, record `key.issued`, and return the key.
6. Write `RevokeKey(name, now)`: `ErrKeyNotFound` for an unknown name, `ErrRevoked` if it is already revoked, otherwise set `Revoked` to `now` and record `key.revoked`.
7. Write `Authenticate(token)`: hash it, compare against every key with `subtle.ConstantTimeCompare`, and return the matching key; `ErrKeyNotFound` when none matches, `ErrRevoked` when the match is revoked. It records nothing.
8. Write `PullEvents()`.
9. Write your own tests in `access/` (`access/team_test.go` is a good name), table-driven with `t.Run`, with names that don't start with `TestL` plus a digit or capital letter. Cover at least: a second key with the same name, an eleventh key, revoking a key twice, a revoked key's token, and the `key.issued` event after `IssueKey`.
10. Keep `gofmt -l .` and `go vet ./...` quiet.

## Example

```go
now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
t, err := access.NewTeam("t1", "Platform", now)
k, err := t.IssueKey("ci", access.RoleWrite, "brw_s3cret", now)
// k.Hash == sha256.Sum256([]byte("brw_s3cret")), k.Active() == true

_, err = t.IssueKey("ci", access.RoleRead, "other", now)
// errors.Is(err, access.ErrDuplicateKey); t.Keys still has one key

got, err := t.Authenticate("brw_s3cret")      // got.Name == "ci"
err = t.RevokeKey("ci", now.Add(time.Hour))   // nil
_, err = t.Authenticate("brw_s3cret")         // ErrRevoked
err = t.RevokeKey("ci", now)                  // ErrRevoked

t.PullEvents()
// [{team.created t1  09:00} {key.issued t1 ci 09:00}
//  {key.revoked t1 ci 10:00}]
t.PullEvents() // [] (nil)
```

When your tests miss a rule:

```bash
$ go test ./access/
--- FAIL: TestLA_1_YourTestsCatchBugs (3.10s)
    your tests don't catch this bug: IssueKey accepts an eleventh key
FAIL
```

## Done when

- [ ] `NewTeam`, `IssueKey`, `RevokeKey` and `Authenticate` follow every rule above, and a failed command changes nothing and records nothing
- [ ] Keys store only `sha256.Sum256` of the token, and `Authenticate` compares with `subtle.ConstantTimeCompare`
- [ ] Every successful command records exactly one event, in order, and `PullEvents` empties the list
- [ ] Your own tests in `access/` use `t.Run`, pass, and catch all five planted bugs
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
// package access (directory access/)
var (
	ErrInvalid      = errors.New("access: invalid")
	ErrDuplicateKey = errors.New("access: duplicate key name")
	ErrTooManyKeys  = errors.New("access: too many keys")
	ErrRevoked      = errors.New("access: key revoked")
	ErrKeyNotFound  = errors.New("access: key not found")
	ErrTeamNotFound = errors.New("access: team not found")
	ErrConflict     = errors.New("access: version conflict")
)

const MaxKeys = 10 // per team, revoked keys included

type Role string

const (
	RoleRead  Role = "read"
	RoleWrite Role = "write"
)

func (r Role) Valid() bool

type Key struct {
	Name    string
	Hash    [32]byte // sha256 of the token; the token is never kept
	Role    Role
	Created time.Time
	Revoked time.Time // zero while the key is active
}

func (k Key) Active() bool

type Team struct {
	ID      string
	Name    string
	Keys    []Key  // in the order they were issued
	Version uint64 // set by the store (A.2, A.3); 0 for a new team
	// plus an unexported []Event
}

type EventType string

const (
	TeamCreated EventType = "team.created"
	KeyIssued   EventType = "key.issued"
	KeyRevoked  EventType = "key.revoked"
)

type Event struct {
	Type   EventType
	TeamID string
	Key    string // the key's name; "" for team.created
	At     time.Time
}

func HashToken(token string) [32]byte

func NewTeam(id, name string, now time.Time) (*Team, error)
func (t *Team) IssueKey(name string, role Role, token string,
	now time.Time) (Key, error)
func (t *Team) RevokeKey(name string, now time.Time) error
func (t *Team) Authenticate(token string) (Key, error)
// PullEvents returns the events recorded since the last call and
// forgets them.
func (t *Team) PullEvents() []Event
```

Decisions the tests rely on:

- Team and key names are 1 to 64 bytes (`len`). The ID only has to be non-empty. Rejections wrap `ErrInvalid` with `%w`.
- `IssueKey` checks in the order given in What to do, so a duplicate name on a full team is `ErrDuplicateKey`.
- Revoked keys stay in `Keys`, still count towards the 10, and still own their name.
- The returned `Key` and the one appended to `Keys` are equal; `NewTeam` returns `Version` 0 and no keys; a rejected `NewTeam` returns a nil `*Team`.
- Events carry the team's ID, the key's name and the `now` the command was given. `Authenticate` never records one.
- The package imports only `errors`, `fmt`, `time`, `crypto/sha256` and `crypto/subtle` in this level (A.2 adds a few more standard packages).
- Your tests live in `access/` (package `access` or `access_test`), their names don't start with `TestL` plus a digit or capital letter, at least one uses `t.Run`, and they pass on your code.

## The tests check

- `RoleRead`, `RoleWrite`, `MaxKeys`, the event type strings, and that the seven sentinels are distinct
- `HashToken` is `sha256.Sum256` of the token's bytes
- `NewTeam`: the fields, one `team.created` event, `PullEvents` empties; empty name, 65-byte name and empty ID are `ErrInvalid` with a nil team; 64 bytes is fine
- `IssueKey`: the returned and stored key (hash, role, created, not revoked), order of keys, one `key.issued` event, and no field of the key holding the plaintext token
- `IssueKey` rejections, each leaving the team unchanged and recording nothing: empty and 65-byte names, no role, `admin`, empty token, a duplicate name, the name of a revoked key, an eleventh key, an eleventh key when two are revoked
- `RevokeKey`: sets `Revoked`, leaves other keys alone, keeps the key, records `key.revoked`; twice is `ErrRevoked` and leaves the first revocation time; an unknown name is `ErrKeyNotFound`
- `Authenticate`: the right key; near misses, another team's token and the empty string are `ErrKeyNotFound`; a revoked key is `ErrRevoked`; nothing recorded
- A mixed sequence of commands, including a rejected one, records exactly the expected events in order
- The source of `access` calls `subtle.ConstantTimeCompare`
- TestLA_1_YourTestsCatchBugs: your tests use `t.Run`, pass on your code, and fail against each of five mutants: (1) a second key with the same name is accepted, (2) an eleventh key is accepted, (3) revoking a revoked key succeeds, (4) a revoked key authenticates, (5) `IssueKey` records no `key.issued` event

## Try it

```bash
./burrow watch
go test -v -skip 'TestL[0-9A-Z]' ./access   # just your own tests
go doc ./access Team
```

## In the wild

Ben Johnson's "Standard Package Layout" and his WTF Dial example app use this shape, with the domain in the root package; the aggregate idea is from Eric Evans's *Domain-Driven Design*. GitHub, Stripe and GitLab store only a hash of each API token and show the token once.

## New in Go

package layout by dependency, not by layer · pointer receivers that change state · check-then-change commands · exported fields and convention · sentinel errors as a domain vocabulary · passing time in · crypto/sha256 and [32]byte arrays · comparable arrays · crypto/subtle.ConstantTimeCompare · string types as enums · unexported fields that tests can't see · mutation-graded table tests

Standard library: `errors`, `fmt`, `time`, `crypto/sha256`, `crypto/subtle`
