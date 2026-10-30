# 11.3 · Checker

> A history is evidence. The checker is the prosecutor: five charges, and for each one the first operation that proves it.

## Your goal

Write `Check(ops) []Violation`: read a history from 11.2 and decide whether the cluster kept five promises. A violation names the rule that was broken, the operation that broke it, and a sentence saying why. A clean run returns nothing. The point is not just to pass: you will also build two deliberately broken versions of Burrow (one serves reads from followers, one skips fencing) and the checker must catch each one on the right rule.

## What you're using from before

**History recorder (11.2 · History Recorder).** The `history.Op` type and the JSON-lines file. The checker reads the file one line at a time into `[]Op`, then hands the slice to `Check`:

```go
var ops []history.Op
sc := bufio.NewScanner(f)
for sc.Scan() {
	var op history.Op
	if err := json.Unmarshal(sc.Bytes(), &op); err != nil {
		return nil, fmt.Errorf("line %d: %w", len(ops)+1, err)
	}
	ops = append(ops, op)
}
for _, v := range Check(ops) {
	fmt.Printf("%s: client %d %s %s at %s: %s\n", v.Rule, v.Op.Client,
		v.Op.Kind, v.Op.Key, v.Op.Start.Format(time.StampMilli),
		v.Detail)
}
```

Each probe's `Kind`, `Token` and `Version` fields carry what the matching rule needs: `put`/`get` with `Version` for the first three rules, `acquire`/`append`/`release` with `Token` for the lock rule, `watch` with `Token` as the event ID for the last one.

## What's new here

**Operation histories.** A history is a list of operations, each with a start time, an end time and a result, seen from the clients' side. You never look at the server. The question is always: is there *some* order in which the cluster could have run these operations, one at a time, that explains every result the clients saw? If yes, the history is fine. If no result could explain it, the cluster lied.

**Interval-aware reasoning.** Two operations whose intervals overlap were concurrent, and the cluster may have run them in either order. Two operations that do not overlap have a fixed order: the one that ended first happened first. So "a read saw an old value" is only a violation when the write it missed had *finished* before the read *started*. Sort by `Start` and keep that distinction in every rule:

```go
func before(a, b history.Op) bool { return a.End.Before(b.Start) }
```

**Consistency models, as five rules.** Each is one function from `[]Op` to `[]Violation`:

1. **No lost writes** (`lost_write`). A `put` that returned `OK` must be visible: some later read of the key returns that value or a newer version, or a newer `OK` write to the same key exists. The first acknowledged write that nothing ever confirms is the violation. This one catches most bugs; start here.
2. **Counter exactness** (`counter`). Every `cas` on `counter` that returned `OK` added exactly one. The last read of `counter` must equal the number of successful increments; a smaller number means an acknowledged increment was lost, a larger one means a CAS was applied twice.
3. **Monotonic reads** (`monotonic_read`). For one key, a `get` that started after another `get` ended must not return a smaller `Version`. Reads served from a lagging follower break this; that is what one of the sabotage builds does.
4. **Lock exclusivity** (`lock_exclusivity`). Fencing tokens of successful `append` ops on `lock-log` must be strictly increasing in history order, and no two `acquire`-to-`release` intervals with different tokens may overlap. A holder that kept writing after losing its lease shows up here.
5. **Watch completeness** (`watch_complete`). Every `OK` `put` to a watched key whose end precedes the last watch event has a `watch` op with its key and version, and watch event IDs are strictly increasing per watcher.

**slices.SortFunc.** Go 1.21's generic sort takes a comparison function returning negative, zero or positive, and `cmp.Compare` builds one for any ordered type. Sorting a copy keeps the caller's order intact:

```go
sorted := slices.Clone(ops)
slices.SortFunc(sorted, func(a, b history.Op) int {
	return a.Start.Compare(b.Start)
})
```

(`time.Time` has its own `Compare`; for integers it would be `cmp.Compare(a.Token, b.Token)`.) `slices.SortStableFunc` keeps equal elements in their original order when that matters.

**Build tags.** A file whose first line is a `//go:build` constraint is only compiled when the constraint holds. `go build -tags sabotage_stale` turns the tag on; without it the file does not exist as far as the compiler knows. Put each sabotage in a pair of files, so the real binary never contains it:

```go
//go:build sabotage_stale

package api

const staleReads = true // serve default reads locally
```

```go
//go:build !sabotage_stale

package api

const staleReads = false
```

The read handler checks `staleReads` once. With the constant `false`, the compiler drops the dead branch entirely.

**Naming the first offender.** A rule stops at its first violation and reports that operation. One violation per rule is enough to debug; a thousand is noise. A clean run returns a nil slice.

## What to do

1. Create package `checker` with `Violation`, `Check`, and a `Load(r io.Reader) ([]history.Op, error)` for JSON lines. Give `cmd/history` a `check` subcommand, or a separate `cmd/checker`, that prints violations and exits 1 if there are any.
2. Write the five rules as separate functions and have `Check` call each in order, collecting the results.
3. For each rule write two hand-made histories in a table test: one that passes and one that fails on the right operation.
4. Add two build tags to Burrow, `sabotage_stale` (serve default reads from the local store on followers) and `sabotage_nofence` (the lock keeps writing after losing its lease), and check that a run against each fails on the expected rule.
5. Run a real 10 second history through the checker and make it clean.

## Example

```bash
$ go run ./cmd/checker history.jsonl && echo CLEAN
CLEAN
$ go build -tags sabotage_stale -o burrowd-stale ./cmd/burrowd
$ # ... run devcluster with that binary, record 10 s ...
$ go run ./cmd/checker stale.jsonl
monotonic_read: client 2 get kv:4 at Oct 31 10:00:07.412: saw version 17
  after a read that ended at 10:00:07.398 saw version 19
exit status 1
```

A hand-made history in a test looks like this, and the checker must point at the third op:

```go
ops := []history.Op{
	{Client: 1, Kind: "put", Key: "a", Version: 2, OK: true,
		Start: t(0), End: t(1)},
	{Client: 2, Kind: "get", Key: "a", Version: 2, OK: true,
		Start: t(2), End: t(3)},
	{Client: 2, Kind: "get", Key: "a", Version: 1, OK: true,
		Start: t(4), End: t(5)},
}
v := Check(ops) // one violation, Rule "monotonic_read", Op is ops[2]
```

## Done when

- [ ] Each of the five rules has a passing and a failing hand-written history, and the failing one names the right op
- [ ] A real 10 second devcluster run comes back clean
- [ ] The `sabotage_stale` build fails on `monotonic_read` and the `sabotage_nofence` build fails on `lock_exclusivity`
- [ ] `Check` returns nil for a clean history and stops at the first offender per rule

## Reference

The exact contract the tests will hold you to.

```go
package checker

type Violation struct {
	Rule   string // lost_write | counter | monotonic_read |
	               // lock_exclusivity | watch_complete
	Op     history.Op
	Detail string
}
func Check(ops []history.Op) []Violation
// checker <history.jsonl>: one line per violation, exit 1 if any.
// Build tags for sabotage: sabotage_stale, sabotage_nofence.
```

What this level builds:

- Five rules as separate functions
- Interval-aware reasoning for concurrent ops
- A report that names the first offending op

Decisions the tests rely on:

- Ops are compared by interval: only an op that ended before another started is required to be visible to it.
- Each rule reports at most one violation, the earliest offending op by `Start`.
- `Check` does not modify or reorder the slice it is given.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.3/files/`, matching the earlier chapters.

The suite will cover:

- Hand-written histories for each rule, passing and failing
- A real 10 s devcluster run passes
- The two sabotage flags fail on the right rule

## Try it

```bash
./burrow watch
go run ./cmd/checker history.jsonl && echo CLEAN
go build -tags sabotage_stale -o burrowd-stale ./cmd/burrowd
```

## In the wild

Porcupine, Knossos, Elle; the properties here are a subset of Jepsen's

## New in Go

operation histories · consistency models · slices.SortFunc · build tags

Standard library: `encoding/json`, `slices`, `time`, `cmp`
