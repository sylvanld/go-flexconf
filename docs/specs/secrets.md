# `secrets` — store and driver contract

Package `secrets` defines the secret-storage abstraction: a high-level `Store`
API over a pluggable `Driver`. The `Store` adds key validation, lazy unlocking,
and timestamp bookkeeping; the `Driver` performs the actual backend access. Two
drivers exist in the toolkit — [`KeepassDriver`](keepass-driver.md) and
[`agent.Client`](agent.md) — and any type satisfying the interface works.

## Data model

### `Secret`

```go
type Secret struct {
    Key       string
    Value     string
    CreatedAt *time.Time // nil when unknown
    UpdatedAt *time.Time // nil when unknown
}
```

Timestamps are pointers so "unset" is distinguishable from the zero time.

## `Driver` interface

```go
type Driver interface {
    Unlock() error                  // prepare backend; safe to call repeatedly
    Get(string) (*Secret, error)    // ErrNotFound when the key is absent
    Set(Secret) error               // create or replace
    List() ([]Secret, error)        // every stored secret
    Delete(string) error            // remove by key
}
```

Contract expected of implementations:

- `Unlock` is idempotent and safe to call more than once.
- `Get` returns `ErrNotFound` (matchable with `errors.Is`) for a missing key.
- Implementations that cannot write return `ErrReadOnly` from `Set`/`Delete`.

## `Store`

`Store` is the high-level API. Build with `NewStore(driver)`. It holds the
driver and a private `unlocked` flag.

### Lazy unlocking

`Store` tracks whether it has unlocked its driver:

- `Unlock()` returns `ErrNoDriver` if no driver is configured; otherwise calls
  `Driver.Unlock()` **once** and records success. Subsequent calls are no-ops
  that return `nil`. If the driver's `Unlock` fails, the flag is not set, so a
  later call retries.
- Every data method (`Get`, `Set`, `List`, `Delete`) calls `Unlock()` first and
  aborts on its error. Callers may call `Unlock()` explicitly to surface unlock
  errors (e.g. a wrong password) early.

### Operations

| Method | Behaviour |
| --- | --- |
| `Get(key) (*Secret, error)` | `ErrEmptyKey` if `key == ""`; unlocks; delegates to `Driver.Get`. |
| `GetValue(key) (*string, error)` | Like `Get` but returns a pointer to just the `Value`. Propagates `Get` errors. |
| `Has(key) (bool, error)` | `true` if the secret exists; `false` (no error) if `Get` returns `ErrNotFound`; otherwise the underlying error. |
| `Set(secret) error` | `ErrEmptyKey` if `secret.Key == ""`; unlocks; stamps timestamps (below); delegates to `Driver.Set`. |
| `SetValue(key, value) error` | Convenience wrapper for `Set(Secret{Key: key, Value: value})`. |
| `List() ([]Secret, error)` | Unlocks; delegates to `Driver.List`. No ordering is guaranteed by the store. |
| `Delete(key) error` | `ErrEmptyKey` if `key == ""`; unlocks; delegates to `Driver.Delete`. |

### Timestamp stamping on `Set`

When `Set` runs (`now = time.Now()`):

- `UpdatedAt` is **always** set to `now`, overwriting any value on the incoming
  `Secret`.
- `CreatedAt` is set only if the incoming `Secret.CreatedAt` is `nil`:
  - If an entry already exists for the key **and** it carries a `CreatedAt`, that
    existing `CreatedAt` is preserved (the create time survives updates).
  - Otherwise `CreatedAt` is set to `now`.
- A caller that supplies a non-nil `CreatedAt` keeps it verbatim.

This look-up performs an extra `Driver.Get` before the `Driver.Set`.

## Errors

| Sentinel | Meaning |
| --- | --- |
| `ErrNoDriver` | A `Store` was used without a `Driver`. |
| `ErrEmptyKey` | An empty key was supplied to `Get`/`Set`/`Delete`. |
| `ErrNotFound` | The requested secret does not exist (drivers must return this). |
| `ErrReadOnly` | A write was attempted on a read-only driver. |

All are exported sentinels intended to be tested with `errors.Is`.

## Invariants

- No data method touches the backend before a successful `Unlock`.
- After any successful `Set`, the stored secret has a non-nil `UpdatedAt`, and a
  `CreatedAt` no later than it.
- Key `""` is never passed to the driver.
