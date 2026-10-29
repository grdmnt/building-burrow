# 1.1 · Register File

An in-memory key-value store. Small, but it has to be correct about missing keys, empty values and shared memory.

## Build

```go
// store/memory.go
var ErrNotFound = errors.New("not found")

type Memory struct { /* ... */ }

func NewMemory() *Memory

func (m *Memory) Get(ctx context.Context, key string) ([]byte, error)
func (m *Memory) Set(ctx context.Context, key string, value []byte) error
func (m *Memory) Delete(ctx context.Context, key string) error
func (m *Memory) Len() int
```

- Every method validates the key with `ValidateKey` first.
- Every method returns `ctx.Err()` without doing anything if the context is already cancelled. (Taking `ctx` now saves you changing every signature later.)
- `Get` of a missing key returns `(nil, ErrNotFound)`. `Delete` of a missing key returns `ErrNotFound`.
- A key set to an empty or nil value **exists**: `Get` returns an empty slice and a nil error.

## The tests check

- Set/Get/overwrite/delete, and setting a deleted key again.
- `Len` counts keys; overwriting doesn't add one.
- **Aliasing:** changing the slice you passed to `Set` must not change the stored value, and changing what `Get` returned must not either. Two `Get` calls never share memory.
- Binary values with `\x00` and `\xff`, and a 1.2 MB value.
- Invalid keys fail with `ErrInvalidKey` on all three methods (and `Get` doesn't say `ErrNotFound`).
- A cancelled context returns `context.Canceled`, and a cancelled `Set` or `Delete` changes nothing.
- 10,000 keys; two stores from `NewMemory` don't share data.

## New in Go

maps · pointer receivers · constructors · `context.Context` · slice aliasing · `errors.Is`
