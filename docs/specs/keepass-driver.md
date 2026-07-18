# `secrets.KeepassDriver` — KeePass-backed driver

`KeepassDriver` implements [`secrets.Driver`](secrets.md) on top of a
password-protected KeePass (`.kdbx`) file, using `github.com/tobischo/gokeepasslib/v3`.
It is the concrete backend that actually reads and writes encrypted secrets.

## Mapping: secret ↔ KeePass entry

| `Secret` field | KeePass entry field |
| --- | --- |
| `Key` | entry **Title** |
| `Value` | entry **Password** (stored *protected*/encrypted) |
| `CreatedAt` | entry `Times.CreationTime` |
| `UpdatedAt` | entry `Times.LastModificationTime` |

New databases and new entries are placed in a group named `flexconf`.

## Configuration

```go
type KeepassDriver struct {
    Path           string                  // location of the .kdbx file
    PromptPassword func() (string, error)  // optional password source
    ReadOnly       bool                    // read-only mode (see below)
    // ... unexported: db, loaded
}
```

Construct with `NewKeepassDriver(path)`. `Path` may also be set directly.

### Password acquisition

`Unlock` obtains the master password via `promptPassword`:

- If `PromptPassword` is set, it is called (used by tests and by the agent, which
  supplies the password read from an inherited pipe).
- Otherwise the password is read from the controlling terminal (`os.Stdin`)
  **without echo**, after printing `Password for <path>: ` to stderr.

## `Unlock() error`

Idempotent (`loaded` guard: a second call returns `nil` immediately).

1. Errors with `keepass: no database path configured` if `Path == ""`.
2. Obtains the password (above).
3. Stats `Path`:
   - **File exists** → `open`: decode + `UnlockProtectedEntries`. A decode failure
     is wrapped as `keepass: unable to open database (wrong password?): <err>`.
     If `ReadOnly`, the master credentials are then dropped from memory
     (`db.Credentials = nil`) — decrypted entries remain readable but the file
     can no longer be re-encrypted.
   - **File missing** (`os.ErrNotExist`):
     - If `ReadOnly` → error `keepass: database <path> does not exist` (a
       read-only driver never creates a database).
     - Otherwise → `create`: build a fresh empty database with a single `flexconf`
       group and write it out, protected by the entered password.
   - **Other stat error** → returned as-is.
4. On success sets `loaded = true`.

## Locked-state guard

`Get`, `Set`, `List`, and `Delete` return `errLocked`
(`keepass: database is locked; call Unlock first`) if called before a successful
`Unlock`. In normal use the `Store` calls `Unlock` first, so callers rarely see
this.

## Operations

### `Get(key) (*Secret, error)`

Searches the whole group tree for an entry whose Title equals `key`. Returns the
mapped `Secret`, or `secrets.ErrNotFound` if none matches.

### `Set(secret) error`

- Returns `ErrReadOnly` in read-only mode.
- Builds an entry from the secret (Title, protected Password, and any non-nil
  timestamps).
- If an entry with the same Title exists anywhere in the tree, it is replaced
  **in place**, preserving its existing UUID.
- Otherwise the entry is appended to the **root** (`Groups[0]`, the `flexconf`
  group for driver-created databases).
- Persists via `save` (atomic).

### `List() ([]Secret, error)`

Walks every group recursively and returns every entry as a `Secret`. Order
follows the tree/entry order in the file (the CLI sorts by key for display; the
driver itself does not sort).

### `Delete(key) error`

- Returns `ErrReadOnly` in read-only mode.
- Removes the first entry whose Title equals `key` from its containing group;
  returns `secrets.ErrNotFound` if absent.
- Persists via `save`.

## Persistence (`save`)

Writes are **atomic and safe against partial writes**:

1. `LockProtectedEntries` (encrypt in-memory protected values); a deferred
   `UnlockProtectedEntries` re-decrypts afterwards so the driver stays usable for
   further operations without re-reading the file.
2. Encode to a temp file `.kdbx-*` created in the **same directory** as `Path`
   (so the final rename is on one filesystem). Encode failure removes the temp
   file and wraps the error as `keepass: unable to encode database: <err>`.
3. Close, then `os.Rename` the temp file over `Path`.

On any pre-rename error the temp file is cleaned up.

## Read-only mode semantics

Setting `ReadOnly = true` yields a driver that:

- Requires the database to already exist (won't create one).
- After unlocking, does **not** retain the master key in memory.
- Rejects `Set`/`Delete` with `ErrReadOnly`.

This is the intended backend when you want a component that provably cannot write
and does not hold the master key (the opposite trade-off from the writable agent).

## Errors

| Error | Condition |
| --- | --- |
| `keepass: no database path configured` | `Unlock` with empty `Path`. |
| `keepass: database <path> does not exist` | `ReadOnly` unlock of a missing file. |
| `keepass: unable to open database (wrong password?): …` | Decode failure (typically wrong password or corrupt file). |
| `keepass: unable to encode database: …` | Encode failure during `save`. |
| `errLocked` | Data method used before `Unlock`. |
| `secrets.ErrNotFound` | `Get`/`Delete` of an absent key. |
| `secrets.ErrReadOnly` | `Set`/`Delete` in read-only mode. |

## Invariants

- The on-disk file is never left partially written: readers see either the old
  or the new complete database.
- Protected values are encrypted at rest; they are only decrypted in memory
  between `UnlockProtectedEntries` and the next `LockProtectedEntries`.
- Entry identity (UUID) is stable across updates to the same key.
