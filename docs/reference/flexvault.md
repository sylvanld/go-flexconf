---
tags:
  - reference
  - vault
  - secrets
---

# `flexvault` — secret backends

Package `github.com/sylvanld/go-flexconf/flexvault` provides pluggable secret
backends. A **`VaultDriver`** implements the mechanics of one backend; a
**`Manager`** wraps a single driver and is the object the rest of flexconf
(and your code) uses.

Normative spec: [vault-drivers.md](../specs/vault-drivers.md).

## Quick start

```go
import (
    "github.com/sylvanld/go-flexconf/flexprompt"
    "github.com/sylvanld/go-flexconf/flexvault"
    _ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass" // register the driver
)

flexprompt.SetPrompter(flexprompt.NewCLIPrompter())

drv, err := flexvault.New("keepass")
mgr := flexvault.NewManager(drv)
if err := mgr.Open(ctx, flexvault.MapDecoder(map[string]any{"path": "secrets.kdbx"})); err != nil {
    return err // Configure → Credentials → prompt → Unlock
}
defer mgr.Lock()

token, err := mgr.Get(ctx, "artifactory/token")
```

## Lifecycle

```
Configure ──▶ (Credentials ─▶ Prompter Dispatch) ──▶ Unlock ──▶ Get/Set/List* ──▶ Lock
```

The Manager enforces the order:

- `Unlock`/`Get`/`Set`/`List` before `Configure` → `ErrNotConfigured`.
- `Get`/`Set`/`List` before a successful `Unlock` → `ErrLocked`.
- `Unlock` when already unlocked is a **no-op success** (no re-prompt).
- On `ErrAuth`, `Unlock` re-prompts up to `WithUnlockRetries(n)` attempts
  (default 3); a cancelled prompt (`flexprompt.ErrPromptCancelled`) stops
  immediately.

## `Manager` API

```go
func NewManager(driver VaultDriver, opts ...Option) *Manager
func WithUnlockRetries(n int) Option              // default 3
func WithPrompter(p flexprompt.Prompter) Option   // override the process prompter

func (m *Manager) Configure(decode func(target any) error) error
func (m *Manager) Unlock(ctx context.Context) error
func (m *Manager) Open(ctx context.Context, decode func(target any) error) error   // Configure → Unlock
func (m *Manager) Create(ctx context.Context, decode func(target any) error) error // new vault (Initializer)
func (m *Manager) Get(ctx context.Context, addr string) (string, error)
func (m *Manager) Set(ctx context.Context, addr string, value string) error
func (m *Manager) List(ctx context.Context, namespace string) ([]string, error)
func (m *Manager) Capabilities() Capabilities
func (m *Manager) Lock() error
func (m *Manager) IsUnlocked() bool
```

The Manager serializes all driver calls, so drivers need not be
concurrency-safe. Credentials are collected via the process-wide
`flexprompt.GetPrompter()` (read at dispatch time) unless overridden with
`WithPrompter`.

## Secret addressing

Secrets use a fixed two-level address: **`namespace/key`** — exactly two
non-empty, case-sensitive segments (`artifactory/token`). The Manager
validates addresses before delegating; `ParseAddress(addr)` exposes the same
validation.

- `Get`/`Set` take a full `namespace/key`. `Set` is create-or-overwrite and
  auto-creates a missing namespace.
- `List(ctx, "")` returns namespaces; `List(ctx, ns)` returns key names.
  Listing an unknown namespace returns an empty slice, not an error.
- A secret is a **single string value** — no structured records.

## Implementing a driver

Implement `VaultDriver` and register from `init()`:

```go
func init() { flexvault.Register("mybackend", func() flexvault.VaultDriver { return &driver{} }) }
```

- `Configure(decode)` receives a callback that unmarshals the driver's own
  config struct (tagged `flexconf:"..."`); no backend I/O, no secrets.
- `Credentials()` *declares* what secret input is needed
  (`[]flexprompt.PromptRequest`); `Unlock(ctx, answers)` *consumes* the
  answers keyed by `PromptRequest.ID`. Define IDs as exported constants.
- Wrap sentinels with detail (`fmt.Errorf("...: %w", flexvault.ErrAuth)`) and
  never embed secret values in errors.
- Optionally implement `Initializer` (`InitCredentials`/`Init`) to support
  vault creation, and report `Capabilities().Creatable`.

### Config decoders

For standalone use without a registry file:

```go
flexvault.MapDecoder(map[string]any{"path": "a.kdbx"}) // strict: unknown keys error
flexvault.EnvDecoder("FLEXCONF_")                       // path ← FLEXCONF_PATH, …
```

## Errors

```go
var (
    ErrLocked        error // configured but not unlocked
    ErrNotFound      error // secret address absent
    ErrReadOnly      error // write to a non-writable vault
    ErrUnsupported   error // capability not supported (e.g. Create)
    ErrAuth          error // bad credentials
    ErrNotConfigured error // lifecycle-order violation
)
```

Match with `errors.Is`.
