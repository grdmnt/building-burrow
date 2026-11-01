# A.3 · Repository

> The version you read is the version you must still see. A key-value store gives you that guarantee for one key at a time, so design around one key.

## Your goal

Write package `access/kvrepo`, a `TeamStore` that keeps teams in Burrow. Each team is one JSON document under `access/team/<id>`, written with compare-and-swap so the Burrow entry's version *is* the team's version. Each API key also gets an index entry, `access/key/<sha256 hex>`, holding its team's ID, so `FindByKey` can go from a token to its team without reading every team.

`kvrepo` doesn't import the Burrow client. It declares its own four-method `KV` interface, which `*store.Memory` (1.1) and `*burrow.Client` (7.5) both already satisfy, so the same repository runs in-process in a test and over HTTP in production. The proof is A.2's conformance suite, `accesstest.RunTeamStore`, passing over both.

## What you're using from before

**Access service (A.2).** `kvrepo.Repo` satisfies `access.TeamStore`, so it plugs straight into `access.NewService`. Its contract is the one A.2 wrote down: `ErrTeamNotFound`, `ErrConflict`, everything else wrapped.

**CAS (1.3).** All writes are `CompareAndSwap` and `CompareAndDelete`. `CompareAndSwap(key, 0, v)` creates a key only if it is missing; `CompareAndSwap(key, n, v)` replaces it only if it is still at version `n`. That is exactly `Save(ctx, team, expectedVersion)`.

**Memory store (1.1).** `*store.Memory` is the `KV` in most of the tests, and you can look inside it to see what the repository wrote.

**HTTP client lib (7.5).** `*burrow.Client` is the `KV` in production. The tests point it at an `httptest` server running `api.Server`, as 7.5's page does:

```go
srv := httptest.NewServer(
	(&api.Server{Store: store.NewMemory()}).Handler())
c, _ := burrow.New(srv.URL, burrow.Options{})
repo := kvrepo.New(c) // the same code as kvrepo.New(memory)
```

## What's new here

**1. Repositories and adapters.** A *repository* gives the domain a collection-like view of stored aggregates (get, save, list) and hides how they are stored. In Rails, ActiveRecord is the model and the repository at once: `Team.find` and `team.save!` live on the same class as the validations. Here they are split: `access.Team` holds the rules, `kvrepo` holds the storage, and neither package's code mentions the other's concerns. `kvrepo` is an *adapter*: it adapts Burrow to the port `access` declared.

**2. The adapter owns its interface too.** `kvrepo` needs four Burrow methods, so it declares them:

```go
type KV interface {
	Get(ctx context.Context, key string) (store.Entry, error)
	CompareAndSwap(ctx context.Context, key string,
		ifVersion uint64, value []byte) (store.Entry, error)
	CompareAndDelete(ctx context.Context, key string,
		ifVersion uint64) error
	Keys(ctx context.Context, prefix string) ([]string, error)
}
```

That is A.2's lesson from the other side: `kvrepo` is now the consumer. The interface is smaller than `store.Store`, so it is easy to fake, and it leaves out `Set`, `Delete` and `Batch` on purpose: a repository built only on compare-and-swap can't overwrite a newer team by accident. `kvrepo` imports `burrow/store` for `store.Entry` and the sentinel errors, but never `burrow/sdk/burrow` or `net/http`; `main` hands it a `*burrow.Client` in A.4.

**3. A document type of its own.** The stored JSON is a separate, unexported struct with `json` tags (a *DTO*, data transfer object), and `kvrepo` converts between it and `access.Team`. The domain types carry no tags, so the domain doesn't know it is ever serialised, and you can change the stored format without touching the rules. The conversion is also where the hash becomes hex text and a zero `Revoked` time becomes an absent field.

```go
type teamDoc struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Keys []keyDoc `json:"keys"`
}

type keyDoc struct {
	Name    string     `json:"name"`
	Hash    string     `json:"hash"` // hex.EncodeToString(h[:])
	Role    string     `json:"role"`
	Created time.Time  `json:"created"`
	Revoked *time.Time `json:"revoked,omitempty"`
}
```

Note what isn't in it: the version. Burrow already stores a version with every entry, so `Get` sets `Team.Version` from `store.Entry.Version`, and `Save` sets it from the entry `CompareAndSwap` returns. One number, kept in one place.

**4. One aggregate, one key.** Burrow guarantees that a single `CompareAndSwap` either happens completely or not at all, for one key. It promises nothing across two keys: 7.2's `Batch` runs its operations one after another, and a crash can stop it halfway. So everything the rules depend on (the names, the count, the revoked flags) lives in the one document, and the version check on that one key protects all of it. That is the real reason the aggregate is bounded to 10 keys: it has to fit comfortably in one value.

`Save` is then a single call and two error mappings:

```go
e, err := r.kv.CompareAndSwap(ctx, teamKey(t.ID), expected, doc)
switch {
case errors.Is(err, store.ErrVersionMismatch),
	expected != 0 && errors.Is(err, store.ErrNotFound):
	return access.ErrConflict
case err != nil:
	return fmt.Errorf("kvrepo: save team %s: %w", t.ID, err)
}
t.Version = e.Version
```

A conditional write to a missing key may come back as either error depending on the store: `store.Memory` says `ErrNotFound`, while 7.2's HTTP API answers `412 version_mismatch` for any failed condition. Both mean "not the version you expected", so both are a conflict.

**5. A second key, written second.** The index entry `access/key/<hash>` is a different key, so it can't be written in the same atomic step as the team. Decide the order and live with the gap:

- Write the team first, then the index entries. If the second step fails, `Save` returns the error, the team is saved, and its new key just can't be found by token yet.
- Write every index entry on every save, with `CompareAndSwap(indexKey, 0, id)`: create it if it is missing, and treat `ErrVersionMismatch` (it already exists) as fine. Then the next successful save repairs anything an earlier one missed.
- Never trust the index on its own. `FindByKey` reads the ID from the index, loads that team, and checks that one of its keys really has the hash. An entry that points at a deleted team, or at a team that never had the key, is `ErrTeamNotFound`.

The index is a *hint*; the team document is the truth. That rule is what makes a non-atomic second write safe.

**6. Translating errors at the boundary.** Each method maps Burrow's errors to the port's: `store.ErrNotFound` from a `Get` is `access.ErrTeamNotFound`, and so is `store.ErrInvalidKey`, since an ID that can't even be a Burrow key can't name a team. Every other error is wrapped with `%w` and passed up, so `errors.Is(err, context.Canceled)` still works two layers higher. A JSON document that won't decode is an error too, never "not found".

**7. Why kvrepo isn't in an `internal/` directory.** Go gives one directory name a special meaning: a package under `x/internal/` can be imported only by code inside `x/`. `burrow/internal/testkit` is an example; only Burrow's own packages can use it. It is the right tool for helpers you don't want anyone outside to depend on. `kvrepo` can't go under `access/internal/`, though, because `cmd/accessd` is not inside `access/` and has to import it to build the real store. So it sits beside `access` as a normal package, and A.4's tests enforce the narrower rule instead: only programs under `cmd/` may import it.

## What to do

1. Create `access/kvrepo/` with `package kvrepo`: the `KV` interface, an exported repository type (call it `Repo`), and `New(kv KV)` returning a pointer to it. Add `var _ access.TeamStore = (*Repo)(nil)`.
2. Define the unexported document structs and two conversions, team to document and `store.Entry` to team (taking `Version` from the entry).
3. `Get`: read `access/team/<id>` and decode it. Missing (or an invalid key) is `access.ErrTeamNotFound`.
4. `Save`: encode, `CompareAndSwap` with `expectedVersion`, map conflicts as above, set `t.Version`, then create the index entry for every key, ignoring `store.ErrVersionMismatch`.
5. `Delete`: `Get` the entry, `CompareAndDelete` it at the version you read (a lost race is `ErrConflict`), then remove the team's index entries if they still point at it. That cleanup may fail quietly; `FindByKey` re-checks anyway.
6. `List`: `Keys("access/team/")`, then `Get` each one, skipping any that vanished in between. Burrow returns keys sorted, so the teams come out sorted by ID.
7. `FindByKey`: read the index entry, `Get` the team, check it holds the hash.
8. Wrap every other error with `%w`.

## Example

What the store holds after creating a team and issuing two keys:

```text
access/key/1f0d...c4   -> "5f0c...e9"                       v1
access/key/9b77...02   -> "5f0c...e9"                       v1
access/team/5f0c...e9  -> {"id":"5f0c...e9","name":"Platform",
                           "keys":[{"name":"ci",...},
                                   {"name":"ro",...}]}      v3
```

The team's `Version` is 3, the entry's version: one create and two key issues. Neither token appears anywhere.

The same service code, two transports:

```go
local := access.NewService(kvrepo.New(store.NewMemory()),
	access.Options{})

c, _ := burrow.New("http://127.0.0.1:8080", burrow.Options{})
remote := access.NewService(kvrepo.New(c), access.Options{})
```

## Done when

- [ ] `accesstest.RunTeamStore` passes over `kvrepo.New(store.NewMemory())` and over `kvrepo.New(burrow client -> httptest api.Server)`, under `-race`
- [ ] The store holds exactly one `access/team/<id>` per team and one `access/key/<hex>` per key, the team's version is the entry's, and no token is stored
- [ ] Every write is a compare-and-swap; a stale save is `ErrConflict` and writes nothing, not even an index entry
- [ ] A failed index write still leaves the team saved, and the next save repairs the index
- [ ] `FindByKey` rejects index entries the team doesn't back
- [ ] Outages and decode failures are errors that wrap their cause, never not-found or conflict
- [ ] `access.Team` and `access.Key` have no `json` tags

## Reference

```go
// package kvrepo (directory access/kvrepo/)

// KV is the part of a Burrow store kvrepo uses. *store.Memory and
// *burrow.Client both satisfy it.
type KV interface {
	Get(ctx context.Context, key string) (store.Entry, error)
	CompareAndSwap(ctx context.Context, key string,
		ifVersion uint64, value []byte) (store.Entry, error)
	CompareAndDelete(ctx context.Context, key string,
		ifVersion uint64) error
	Keys(ctx context.Context, prefix string) ([]string, error)
}

type Repo struct{ /* yours */ }

func New(kv KV) *Repo // *Repo satisfies access.TeamStore

// Layout:
//   access/team/<id>          JSON document; entry version is
//                             Team.Version
//   access/key/<sha256 hex>   the team's ID, as bytes
```

Decisions the tests rely on:

- The hash in the index key is `hex.EncodeToString(hash[:])`, lowercase.
- The team document is a JSON object; its field names are yours. It never contains a token.
- `Save` writes the team key first (a `CompareAndSwap` at `expectedVersion`), then one create-only `CompareAndSwap` per key; an index error after the team was written is returned wrapped with `%w`.
- After a failed `Save` (conflict), no index entry for the new key exists.
- `FindByKey` returns `ErrTeamNotFound` when the index entry is missing, points at a missing team, or points at a team without that hash.
- `New` must accept any value with the four `KV` methods and nothing more (the tests pass one).

## The tests check

- `RunTeamStore` over `store.Memory`, and over `burrow.Client` against `httptest` + `api.Server`
- `*store.Memory` and `*burrow.Client` satisfy `kvrepo.KV`; `New` accepts a type with exactly the four methods
- Key layout after one team and two keys: exactly three keys with the names above, the entry version equals `Team.Version`, the value is a JSON object without either token, and both index entries hold the team ID
- No `json` tag on any field of `access.Team` or `access.Key`
- The first write when creating a team is `CompareAndSwap` on `access/team/<id>`; after another writer changes the entry, `Save` at the old version is `ErrConflict`, leaves the other writer's value, and writes no index entry
- With index writes failing: `Save` returns an error wrapping the KV's, `Get` already shows the new key, `FindByKey` doesn't find it; after the next good save both keys are found
- A forged index entry and an orphaned one are `ErrTeamNotFound`, and `Service.Authenticate` turns the forged one into `ErrUnauthenticated`
- With the KV failing every call, all five methods return errors wrapping the failure and never `ErrTeamNotFound` or `ErrConflict`; an undecodable document is an error, not not-found
- Two services sharing one Burrow over HTTP: one sees the other's team, the second writer at a version gets `ErrConflict`, a token issued by one authenticates through the other, and six concurrent writers across both have exactly one winner

## Try it

```bash
./burrow watch
go test -race -run TestLA_3 -v ./access/kvrepo
go run ./cmd/burrowd serve -http 127.0.0.1:8080 &
go run ./cmd/burrowctl -http http://127.0.0.1:8080 keys access/
```

## In the wild

Kubernetes stores every object as one value in etcd and uses the entry's revision as `resourceVersion`: an update with a stale one is rejected with `409 Conflict`, the same rule as here. DynamoDB's single-table designs use conditional writes and secondary index items the same way, and its global secondary indexes are eventually consistent for the reason in point 5.

## New in Go

interfaces declared by the adapter · a DTO beside the domain type · encoding/json struct tags · omitempty with a pointer · hex for binary in JSON · versions from the store, not the document · compare-and-swap as a save · ordering non-atomic writes · re-verifying an index · translating sentinels at a boundary · %w through two layers · the internal/ directory rule

Standard library: `encoding/json`, `encoding/hex`, `errors`, `fmt`, `strings`, `context`
