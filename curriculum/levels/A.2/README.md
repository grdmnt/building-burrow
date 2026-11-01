# A.2 · Ports

> The consumer owns the interface. The service says what it needs from storage, in its own words, and anything that fits can be plugged in.

## Your goal

Write `access.Service`, the layer that runs one *use case* per method: create a team, issue a key, revoke a key, delete a team, get one, list them, and authenticate a token. Each method loads a team, asks the aggregate from A.1 to do the work, saves it, and only then publishes the events it recorded.

The service doesn't know where teams live. It declares what it needs as two small interfaces, called *ports*: `TeamStore` (get, save with an expected version, delete, list, find by key hash) and `EventSink` (publish events). It receives them, plus a clock and a random source, through `NewService` and an `Options` struct. To have something real to plug in, you also write `access.NewMemStore()`, an in-memory `TeamStore`, and make it pass the provided conformance suite `access/accesstest`, the same way 1.1's memory store passes `storetest`.

## What you're using from before

**Team aggregate (A.1).** Every command method is a thin wrapper around a `Team` method. The service supplies what A.1 refused to touch: the time, the random team ID and token, and the version check.

```go
t, err := s.store.Get(ctx, teamID)
// ... check t.Version
key, err := t.IssueKey(name, role, token, s.now())
events := t.PullEvents()
err = s.store.Save(ctx, t, version)
s.events.Publish(ctx, events)
```

From Burrow you reuse ideas rather than packages: the conformance-suite pattern (`storetest`, 1.1), the "context first on every method" rule (1.1), and the meaning of a version: "save this only if nobody has changed it since I read it" (1.3's compare-and-swap).

## What's new here

**1. Ports: the consumer owns the interface.** In Go a type satisfies an interface just by having its methods (1.1); it never says `implements`. So the interface can live with the code that *uses* it, not the code that provides it. `TeamStore` is declared in `access`, next to the `Service` that calls it, and lists exactly the five methods the service needs. A.3's Burrow repository will satisfy it without `access` knowing it exists, and so does the memory store you write here.

```go
// in package access, beside Service
type TeamStore interface {
	Get(ctx context.Context, id string) (*Team, error)
	Save(ctx context.Context, t *Team, expectedVersion uint64) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*Team, error)
	FindByKey(ctx context.Context, hash [32]byte) (*Team, error)
}
```

This is the opposite of Rails, where the model *is* the persistence (`Team.find`, `team.save!`). It is the *ports and adapters* idea (also called hexagonal architecture): the domain defines the port, an adapter fits it.

**2. Accept interfaces, return structs.** `NewService` takes a `TeamStore`, an interface, so callers can pass any store; it returns `*Service`, a concrete type, so callers get every method and `go doc` shows them. Returning an interface from a constructor hides the type for no gain. The same rule gave you `burrow.New` returning `*burrow.Client` in 7.5.

**3. Constructor injection, no container.** dry-system and dry-auto_inject build objects from a registry of names. Go doesn't need one: dependencies are constructor arguments, and `main` (A.4) calls the constructors in order. Optional ones go in an `Options` struct whose zero values mean sensible defaults, as in 7.5's `burrow.Options`:

```go
type Options struct {
	Clock  func() time.Time // nil: time.Now
	Rand   io.Reader        // nil: crypto/rand.Reader
	Events EventSink        // nil: events are dropped
}

svc := access.NewService(store, access.Options{})
```

A function value such as `func() time.Time` is the lightest way to inject a clock: `time.Now` fits as it is, and so does `testkit.FakeClock`'s `Now`.

**4. Randomness through `io.Reader`.** Team IDs (16 bytes) and token secrets (32 bytes) come from `Options.Rand`. `io.ReadFull(r, b)` fills all of `b` or returns an error; a bare `Read` may fill less. `hex.EncodeToString` turns the bytes into text: 32 characters for an ID, and `brw_` plus 64 for a token (the prefix makes a leaked token easy to recognise, as GitHub's `ghp_` does). In production the reader is `crypto/rand.Reader`; in tests it can be anything predictable.

**5. Context on every method.** Every service method takes `ctx context.Context` first and passes that same `ctx` to every store call. Never create `context.Background()` inside: the caller's context carries its deadline and cancellation (7.4), and dropping it means a slow store can't be stopped.

**6. Optimistic concurrency, end to end.** `IssueKey` and `RevokeKey` take the version the caller last saw. The service loads the team, returns `ErrConflict` at once if the version differs, makes the change, and calls `Save(ctx, t, version)`; the store saves only if the stored version is still `version`, and sets `t.Version` to the new one. If two callers both read version 4, both pass the first check, but only one `Save` wins and the other gets `ErrConflict`. Nothing is silently overwritten. This is ActiveRecord's `lock_version` column and `ActiveRecord::StaleObjectError`, with the version travelling all the way from the client (A.4 sends it in `If-Match`).

`CreateTeam` saves with expected version 0, which means "only if it doesn't exist yet".

**7. Errors that cross a boundary.** A `TeamStore` may return exactly two sentinels: `ErrTeamNotFound` and `ErrConflict`. Anything else is a failure of the store itself (a network error, a cancelled context) and comes back wrapped. The service passes all of them through unchanged, with one exception. `Authenticate` turns every "this token is no good" case (no team has that hash, the key is revoked, the team is gone) into one error, `ErrUnauthenticated`, so the caller learns nothing about which keys exist. It must *not* do that to a store failure: an outage that looked like a bad token would lock everyone out with a misleading message.

```go
t, err := s.store.FindByKey(ctx, HashToken(token))
if errors.Is(err, ErrTeamNotFound) {
	return nil, Key{}, ErrUnauthenticated
}
if err != nil {
	return nil, Key{}, err // an outage stays an outage
}
```

**8. Events after the save.** Publish only once `Save` succeeded, so nobody hears about a change that never happened. Pull the events from the team before saving, so they are never stored with it. If the process dies between the save and the publish, those events are lost; making that impossible needs an *outbox* (the events saved in the same write as the change, then forwarded), which is out of scope. `EventSink.Publish` returns nothing for the same reason: the change is already saved, so the command can't fail any more. Watching Burrow's own change stream (8.5) for `access/team/` keys would be the change-data-capture version of the same idea, and a good stretch once you get there.

**9. A store that copies.** `MemStore` is a map behind a `sync.Mutex`. It must copy on the way in and on the way out, `Keys` slice included (`slices.Clone`), or a caller who changes a team after `Save` would change the stored one too: the aliasing trap from 1.1. A copy must also leave the pending events behind.

**10. The conformance suite.** `access/accesstest` is provided, like `store/storetest`. It isn't a test file; it is a package whose `RunTeamStore(t, factory)` runs a dozen subtests against any `TeamStore`: save and get, conflicts, aliasing, events not stored, delete, sorted list, find by key hash, a cancelled context, and eight writers racing at the same version. A.3 runs the same suite against Burrow, locally and over HTTP.

```go
accesstest.RunTeamStore(t, func(t *testing.T) access.TeamStore {
	return access.NewMemStore()
})
```

## What to do

1. Add `ErrUnauthenticated`, `TeamStore`, `EventSink`, `Options`, `Service` and `NewService` to package `access` (any file, such as `service.go`).
2. `NewService` fills in `time.Now`, `crypto/rand.Reader` and a nil sink when the options leave them zero.
3. Write `CreateTeam`: 16 bytes from `Rand` as hex for the ID, `NewTeam`, then save with expected version 0.
4. Write `IssueKey` and `RevokeKey`: load the team, return `ErrConflict` if its version isn't the one passed in, call the aggregate, then save with that version. `IssueKey` makes the token `brw_` plus 32 random bytes in hex and returns it with the saved team.
5. Write `GetTeam`, `ListTeams` and `DeleteTeam` as straight calls to the store.
6. Write `Authenticate` as in point 7 above.
7. In every command, pull the events before `Save` and publish them (when there is a sink and at least one event) only after `Save` returned nil.
8. Write `MemStore` and `NewMemStore`: a map and a mutex, deep copies in and out, versions counting up from 1 per team, `ctx.Err()` checked first in every method, `List` sorted by ID, `FindByKey` searching every key of every team.
9. Run `go test ./access/` until `TestLA_2_MemStoreConformance` and the service tests pass.

## Example

```go
svc := access.NewService(access.NewMemStore(), access.Options{})
t, _ := svc.CreateTeam(ctx, "Platform")
// t.ID == "5f0c...e9" (32 hex), t.Version == 1

token, t, err := svc.IssueKey(ctx, t.ID, 1, "ci", access.RoleWrite)
// token == "brw_9a1d...", t.Version == 2

_, _, err = svc.IssueKey(ctx, t.ID, 1, "deploy", access.RoleRead)
// errors.Is(err, access.ErrConflict): someone (us) moved on to 2

team, key, err := svc.Authenticate(ctx, token)
// team.Name == "Platform", key.Name == "ci"

t, err = svc.RevokeKey(ctx, t.ID, 2, "ci")
_, _, err = svc.Authenticate(ctx, token)
// errors.Is(err, access.ErrUnauthenticated)
```

With a sink that prints, the three commands publish `team.created`, then `key.issued ci`, then `key.revoked ci`, each once.

## Done when

- [ ] `accesstest.RunTeamStore` passes over `access.NewMemStore()`, under `-race`
- [ ] IDs and tokens come from `Options.Rand`, times from `Options.Clock`, and zero `Options` work
- [ ] A stale or future version is `ErrConflict` and changes nothing; of eight concurrent `IssueKey` calls at one version exactly one succeeds
- [ ] Events are published once each, only after a successful save, and never come back from the store
- [ ] `Authenticate` says `ErrUnauthenticated` for unknown, revoked and orphaned tokens, but passes a store failure through
- [ ] Every store call receives the caller's context

## Reference

```go
// package access, added in this level
var ErrUnauthenticated = errors.New("access: unauthenticated")

// TeamStore may return ErrTeamNotFound and ErrConflict; any other
// error is wrapped with %w.
type TeamStore interface {
	// Get: ErrTeamNotFound when there is no such team.
	Get(ctx context.Context, id string) (*Team, error)
	// Save stores t if the stored version equals expectedVersion
	// (0: t must not exist yet) and sets t.Version to the new
	// version; otherwise ErrConflict and t is unchanged.
	Save(ctx context.Context, t *Team, expectedVersion uint64) error
	// Delete: ErrTeamNotFound when there is no such team.
	Delete(ctx context.Context, id string) error
	// List: every team, sorted by ID; empty, not an error, when none.
	List(ctx context.Context) ([]*Team, error)
	// FindByKey returns the team holding a key with this hash,
	// revoked or not; ErrTeamNotFound when no team does.
	FindByKey(ctx context.Context, hash [32]byte) (*Team, error)
}

// EventSink receives a command's events after its save succeeded.
type EventSink interface {
	Publish(ctx context.Context, events []Event)
}

type Options struct {
	Clock  func() time.Time // nil: time.Now
	Rand   io.Reader        // nil: crypto/rand.Reader
	Events EventSink        // nil: events are dropped
}

type Service struct{ /* yours */ }

func NewService(store TeamStore, opts Options) *Service

// ID: 16 bytes from Rand, hex. Saved with expected version 0.
func (s *Service) CreateTeam(ctx context.Context,
	name string) (*Team, error)
// version is the Team.Version the caller last saw. The token is
// "brw_" + 32 bytes from Rand in hex; it is returned only here.
func (s *Service) IssueKey(ctx context.Context, teamID string,
	version uint64, name string, role Role) (string, *Team, error)
func (s *Service) RevokeKey(ctx context.Context, teamID string,
	version uint64, name string) (*Team, error)
func (s *Service) GetTeam(ctx context.Context, id string) (*Team, error)
func (s *Service) ListTeams(ctx context.Context) ([]*Team, error)
func (s *Service) DeleteTeam(ctx context.Context, id string) error
// ErrUnauthenticated for any token that isn't an active key;
// store failures are returned as they are.
func (s *Service) Authenticate(ctx context.Context,
	token string) (*Team, Key, error)

type MemStore struct{ /* yours */ }

func NewMemStore() *MemStore // satisfies TeamStore
```

Decisions the tests rely on:

- A failed command returns zero values (`""`, `nil`) with its error, stores nothing and publishes nothing.
- `IssueKey` and `RevokeKey` compare the version before calling the aggregate, so a stale version is `ErrConflict` even when the change itself would have been invalid. Version 0 never matches an existing team.
- The team returned by a command is the saved one, with its new `Version`.
- `Publish` is called with all of one command's events at once, and not at all when there are none or no sink.
- `MemStore` versions start at 1 and go up by 1 per save; other stores may number differently, so the suite only checks that versions grow.
- `access/accesstest` is provided; don't edit it.

## The tests check

- `accesstest.RunTeamStore` over `NewMemStore()`: save then get, missing IDs, create only once, stale versions, no aliasing either way, no stored events, delete (and its keys stop being found), sorted list, `FindByKey` (including keys added later and revoked keys), a cancelled context on all five methods, eight concurrent writers with one winner
- `CreateTeam`: 32-hex ID, name, version, `GetTeam` agrees, one `team.created` at the injected clock's time, distinct IDs; empty and 65-byte names are `ErrInvalid`, store nothing and publish nothing
- Injected randomness: two services with identical `Rand` readers make identical IDs and tokens; tokens are `brw_` plus 64 hex characters; a failing `Rand` fails `CreateTeam` and stores nothing; zero `Options` work, including a nil sink
- `IssueKey`: the stored hash and created time, the token never stored, exactly `key.issued` published; stale, future and zero versions are `ErrConflict`, a missing team `ErrTeamNotFound`, duplicates and bad roles as in A.1, all leaving the store unchanged and publishing nothing
- `RevokeKey` and `Authenticate`: versions checked, `key.revoked` published, revoked and garbled tokens and a deleted team's token are `ErrUnauthenticated`, other keys keep working, `DeleteTeam` twice is `ErrTeamNotFound`
- A store that fails: `Authenticate`, `GetTeam` and `IssueKey` return the store's error, not `ErrUnauthenticated`
- A store whose `Save` always conflicts: commands return `ErrConflict` and nothing is published; with a working store the change is already visible when `Publish` runs, and each event arrives once
- A store that checks every call's context carries the caller's value
- Eight goroutines issuing keys at one version under `-race`: one success, seven `ErrConflict`, one key stored, one event published
- `ListTeams` empty, then sorted by ID

## Try it

```bash
./burrow watch
go test -race -run 'TestLA_2' -v ./access
go doc ./access/accesstest
```

## In the wild

The ports-and-adapters layout is Alistair Cockburn's; Go's own standard library is full of consumer-side interfaces (`io.Reader` is declared by the code that reads, and `os.File`, `bytes.Buffer` and `net.Conn` just happen to fit). Google's Wire generates exactly the constructor calls you would write by hand; most Go teams skip the generator.

## New in Go

interfaces declared by the consumer · accept interfaces, return structs · constructor injection · Options structs with zero-value defaults · func values as injected clocks · io.Reader as a random source · io.ReadFull · encoding/hex · optimistic concurrency through an expected version · passing errors across a boundary · publishing after the write · sync.Mutex · slices.Clone · a provided conformance suite

Standard library: `context`, `crypto/rand`, `encoding/hex`, `io`, `sync`, `slices`
