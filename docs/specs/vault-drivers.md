# Vault Drivers & Manager

- **Status:** ✅ Accepted
- **Scope:** the `VaultDriver` interface and its lifecycle (Configure →
  Credentials → Unlock), the `Manager` that drives an implementation, how it
  dispatches declared credentials to the `Prompter`, secret addressing, and the
  first concrete driver (KeePass). The `Prompter` itself — the interface,
  `PromptRequest`, the process-wide singleton, and the built-in prompters —
  lives in [prompter.md](prompter.md). Caching, rotation, the resolver
  integration, and multiple vaults are explicitly deferred (§11, §12).

This spec defines how flexconf talks to a secret backend. A **vault driver** is
the backend-specific implementation (KeePass first; HashiCorp Vault, cloud
secret managers, encrypted files later). A **manager** wraps a single driver and
is the object the rest of flexconf (and application code) uses. The manager is
what ultimately backs the `secret:` templating scheme (see
[overview.md](overview.md) §5 and _templating.md_).

> **Packages.** The types in this spec live in the module's `flexvault`
> package (see [overview.md](overview.md), "Module layout"): `flexvault` owns
> `VaultDriver`, `Manager`, `Capabilities`, the sentinel errors, driver
> registration, the config decoders, and the concrete drivers under
> `flexvault/driver/*` (e.g. `flexvault/driver/keepass`). The `Prompter` and its
> process-wide singleton live in the separate `flexprompt` leaf package
> ([prompter.md](prompter.md)). The config-loading package `flexconf` imports
> `flexvault` to back the `secret:` resolver; `flexvault` imports `flexprompt`;
> `flexprompt` imports neither.

## 1. Roles: driver vs. manager

Both expose the read/write surface (`Get`/`Set`/`List`), so their relationship
must be clear:

- **`VaultDriver`** — the *mechanics* of one backend. It owns its own
  configuration (`Configure`), **declares** which credentials it needs
  (`Credentials`), **consumes** the answers to open the backend (`Unlock`), and
  reads/writes entries. It is deliberately thin, never touches a terminal or an
  env var, and MAY assume it is not called concurrently (the manager serializes
  access).

- **`Manager`** — the *policy and lifecycle* layer around one driver. It:
  - drives the lifecycle in order and enforces state (reject `Get`/`Set`/`List`
    before a successful `Unlock` with `ErrLocked`);
  - owns the single credential **dispatch**: it asks the driver what it needs,
    collects it from the user in one interaction via the process-wide `Prompter`
    (`flexprompt.GetPrompter()`, [prompter.md](prompter.md), overridable
    per-Manager with `WithPrompter`), and passes the answers to `Unlock`;
  - applies the unlock retry policy (§5);
  - serializes concurrent access so drivers stay simple;
  - validates/normalizes secret addresses and presents a uniform error surface;
  - is the single object handed to the `secret:` resolver and to app code;
  - is the natural home for optional caching (deferred, see §11).

## 2. Driver lifecycle

A driver moves through a fixed sequence, orchestrated by the manager:

```
Configure ──▶ (Manager: Credentials ─▶ Dispatch) ──▶ Unlock(answers) ──▶ (Get / Set / List)* ──▶ Lock
   │                 │           │                        │
 non-secret     driver       Prompter asks the       driver consumes
 settings       declares     user for all of them    the answers to
 (path, addr)   its needs    as ONE interaction      open the backend
```

- **Configure** loads the driver's *non-secret* settings (file path, server
  address, read-only flag). No backend I/O, no secrets.
- **Credentials** returns the list of *secret* values the driver needs
  (`[]flexprompt.PromptRequest`), computed from its configured state. A driver
  needing no secret input returns an empty slice.
- **Dispatch** is performed by the Manager, not the driver: it hands the
  declared requests to the process-wide `Prompter` (`flexprompt.GetPrompter()`,
  [prompter.md](prompter.md)), which asks the user for all of them in a single
  interaction and returns an `answers` map keyed by `PromptRequest.ID`.
- **Unlock** consumes the `answers` map to open/authenticate the backend.

**Why declare/consume, and why the Manager owns dispatch.** We want all of a
vault's credentials gathered in *one* user interaction, and we want the
component driving the vault (the Manager) to control when that happens. The
tempting alternative — the driver calls `prompt()` several times "async" and the
Manager triggers a single dispatch in the middle — cannot be built cleanly in
Go: there is no way to detect that a goroutine has blocked waiting on an answer,
so the Manager could only know the driver is "done asking" via an explicit
signal, plus goroutines and promises. That pays the full cost of async to land
exactly where the simpler declare/consume split already is. So the driver
*declares* (`Credentials`) and later *consumes* (`Unlock(answers)`); the
`PromptRequest.ID` ([prompter.md](prompter.md) §1) is the join key between the
two (§4).

**Plaintext vs. derived/session material.** Short *plaintext* lifetime does not
mean zero retention. Many backends need derived material for the whole unlocked
session, not just to open the vault:

- **KeePass:** writing an entry (`Set`) re-encrypts and saves the `.kdbx`, which
  needs the *composite key* (derived from password [+ keyfile]). A writable
  vault MUST therefore retain the composite key for the session.
- **HashiCorp Vault:** the token authenticates *every* request, so it is held
  for the session.

The rule (normative): a driver SHOULD discard the plaintext credential (the
`answers` value) once the session key material is derived, SHOULD retain only
the *derived* material it needs for subsequent operations (and only when it will
need it — e.g. a read-only KeePass vault need not keep the composite key after
decrypt), and MUST clear all retained material on `Lock`.

### 2.1 Two kinds of input: vault config vs. vault secret

A vault needs two *different* kinds of input, and flexconf treats them
differently on purpose:

| | Vault **config** | Vault **secret** |
|---|---|---|
| Examples | KeePass file path; Vault URL, namespace, mount; read-only flag | KeePass master password; Vault token |
| Sensitivity | Non-secret | Secret |
| Declared by | the driver's config struct (`Configure`, §4) | `Credentials() []flexprompt.PromptRequest` (§4) |
| Collected by | `decode` bound by the host (§2.1) | `Manager` via the process-wide `flexprompt` prompter's `Dispatch` ([prompter.md](prompter.md)) |
| Typical source | the vault registry (`vaults.yaml`) **or** env vars (ad-hoc) | env, terminal, GUI dialog, CI secret store |

**How is vault config sourced?** The driver does not decide — it just calls
`decode(&itsConfigStruct)`. The **host** binds `decode` to a source. Vault
definitions live in **one** place — the vault **registry**
(`~/.config/flexconf/vaults.yaml`, [vaults.md](vaults.md)); apps do **not** define
vaults in their own `config.yaml`. So in the normal flexconf flow the host binds
`decode` to the selected vault's sub-tree of the registry (the `VaultConf`'s
driver keys). For standalone/programmatic use without a registry file, `flexvault`
ships adapters (`MapDecoder`/`EnvDecoder`) that bind `decode` to a map or to
environment variables — the *same* driver code either way.

```yaml
# ~/.config/flexconf/vaults.yaml — the sole home for vault definitions
vaults:
  personal:
    driver: keepass
    path: ~/secrets.kdbx   # static; no $(...) tokens (vaults.md §2.3)
    readonly: true
# the master password is NOT here — it is requested via the Prompter at unlock time
```

There is no "integrated mode" in which an app bundles vault config inside its own
`config.yaml`; the registry is always the source of a vault's non-secret config.

**Bootstrap ordering constraint (normative).** Vault config MUST be **static**:
its values are read literally and it MUST NOT contain any templating tokens
(`$(...)`) — neither `$(env:...)` nor `$(secret:...)`. `$(secret:...)` could
never work (the vault is what resolves `secret:`), and `$(env:...)` is excluded
so a definition shared across contexts behaves identically everywhere. This is
the static-definitions rule in [vaults.md](vaults.md) §2.3; see it for the
rationale and the `~`-expansion allowance.

## 3. Credential collection via the Prompter

A driver never reads a terminal, an env var, or a dialog directly. It *declares*
what it needs (via `Credentials`, §4); the Manager passes those requests to a
`Prompter`, which asks the user for **all of them in one interaction** and
returns the answers keyed by `PromptRequest.ID`.

The `Prompter` interface, the `PromptRequest` type, the process-wide singleton
(`SetPrompter`/`GetPrompter`), the built-in implementations
(`NewCLIPrompter`/`NewMapPrompter`/`NewEnvPrompter`), and the prompter sentinel
errors all live in the `flexprompt` leaf package, specified in
**[prompter.md](prompter.md)**. This spec references them; it does not redefine
them.

What is specific to vault drivers — how `Credentials`/`Unlock` declare and
consume the requests (§4), and how the `Manager` owns the single `Dispatch` and
its retry policy (§5) — is defined below.

## 4. `VaultDriver` interface

```go
// VaultDriver is implemented by each secret backend. A driver instance manages
// access to exactly one vault. The Manager serializes calls, so implementations
// need not be safe for concurrent use unless they document otherwise.
type VaultDriver interface {
    // Name returns the stable driver identifier (e.g. "keepass", "vault"),
    // used for registration/selection.
    Name() string

    // Configure loads this driver's non-secret settings. decode unmarshals the
    // driver's configuration section into a value the driver owns (typically a
    // driver-defined struct). Configure MUST NOT contact the backend and MUST
    // NOT read secrets. Called once, before Credentials/Unlock.
    Configure(decode func(target any) error) error

    // Credentials declares the secret values this driver needs to unlock, based
    // on its configured state (e.g. password Optional when a keyfile is set).
    // Returns an empty slice if no secret input is needed. Each request's ID is
    // the key the driver will read from Unlock's answers map.
    Credentials() []flexprompt.PromptRequest

    // Unlock opens the vault using the configured settings and the answers
    // gathered for the requests from Credentials (keyed by PromptRequest.ID).
    // It MUST succeed before Get/Set/List work. On bad credentials it MUST
    // return ErrAuth (wrapped with detail) and leave the vault locked. Drivers
    // SHOULD discard plaintext answers once derived material is computed (§2).
    Unlock(ctx context.Context, answers map[string]string) error

    // Capabilities reports what this configured vault supports (e.g. writes).
    Capabilities() Capabilities

    // Get retrieves the secret value at "namespace/key" (§6). ErrNotFound if
    // absent, ErrLocked if not unlocked. A secret is a single string value.
    Get(ctx context.Context, addr string) (string, error)

    // Set stores value at "namespace/key" (create or overwrite). ErrReadOnly if
    // the vault is not writable, ErrLocked if not unlocked.
    Set(ctx context.Context, addr string, value string) error

    // List enumerates the vault (§6): with namespace == "" it returns the
    // namespaces; with a namespace it returns the key names within it.
    // ErrLocked if not unlocked.
    List(ctx context.Context, namespace string) ([]string, error)

    // Lock releases access and clears sensitive in-memory material. After Lock,
    // Get/Set/List MUST return ErrLocked until Unlock succeeds again. Lock on an
    // already-locked driver is a no-op returning nil.
    Lock() error
}

// Capabilities reports what a configured vault supports. It has room to grow.
type Capabilities struct {
    Writable  bool // Set is supported (e.g. KeePass opened read/write)
    Creatable bool // the driver can create a brand-new empty vault (Initializer)
}
```

Notes:

- `context.Context` covers cancellation/timeouts for remote backends. Local
  drivers (KeePass) SHOULD honor cancellation where practical but MAY be
  effectively synchronous.
- Write support is optional: a read-only vault MUST report `Writable: false` and
  MUST return `ErrReadOnly` from `Set` rather than silently succeeding.
- Re-unlock after `Lock`: the Manager re-runs Credentials → Dispatch → Unlock,
  so a re-unlock re-prompts.
- `Capabilities()` is safe to call at any time; **before** `Configure` it returns
  the **zero value** (all fields false). It is only meaningful after `Configure`,
  once the driver knows its settings (e.g. `readonly`).
- `Lock` is **synchronous** and takes no context: it performs a best-effort flush
  of any pending state and then MUST clear all in-memory secret material **even if
  the flush fails**. `Lock` on an already-locked driver is a no-op returning nil.

### Optional vault creation — the `Initializer`

Some backends can create a brand-new empty vault (e.g. a fresh `.kdbx` file);
others (managed cloud vaults) are provisioned out-of-band. This is an **optional**
capability, kept off the core `VaultDriver` interface:

```go
// Initializer is implemented by drivers that can create a new, empty vault.
// A driver that implements it MUST report Capabilities().Creatable == true.
type Initializer interface {
    // InitCredentials declares the secret values needed to create the vault
    // (e.g. a new master password, typically with PromptRequest.Confirm set).
    // Distinct from Credentials(), which unlocks an EXISTING vault.
    InitCredentials() []flexprompt.PromptRequest

    // Init creates a new, empty vault at the configured location using answers
    // gathered for InitCredentials(). It MUST fail (not overwrite) if the vault
    // already exists. On success the vault is created but NOT left unlocked.
    Init(ctx context.Context, answers map[string]string) error
}
```

The `Manager` exposes this via `Create` (§5); the `flexcli secret init` command
(see [cli.md](cli.md)) drives it. Drivers that cannot create a vault simply do
not implement `Initializer` and report `Creatable: false`.

### The `Configure` input — `decode` callback

`Configure` receives a `decode func(target any) error` rather than a concrete
config type, so each driver defines and owns its config struct while flexconf
stays format-agnostic and can select drivers dynamically by name:

```go
type keepassConfig struct {
    Path     string `flexconf:"path"`
    KeyFile  string `flexconf:"keyfile"`  // path to key file (non-secret); contents read at Unlock
    ReadOnly bool   `flexconf:"readonly"`
}

func (d *driver) Configure(decode func(any) error) error {
    var c keepassConfig
    if err := decode(&c); err != nil { return err }
    if c.Path == "" { return errors.New("keepass: path is required") }
    d.cfg = c
    return nil
}
```

In the full flexconf flow, the loader supplies `decode` bound to the driver's
config sub-tree (already parsed from plain, non-secret sources). For standalone
use, `flexvault` provides adapters: `flexvault.MapDecoder(map[string]any)` and
`flexvault.EnvDecoder(prefix string)`, each returning a `func(any) error`.

### Declaring and consuming credentials — the `ID` constant

`Credentials` and `Unlock` are joined only by `PromptRequest.ID`. To keep them
from drifting (a typo would silently yield an empty answer), a driver package
defines each key as an exported constant used in both places — the constants also
document, for non-interactive prompters, exactly what keys a driver will ask for:

```go
// keepass package
const CredPassword = "password"

func (d *driver) Credentials() []flexprompt.PromptRequest {
    return []flexprompt.PromptRequest{{
        ID:       CredPassword,
        Label:    "KeePass master password (" + d.cfg.Path + ")",
        Secret:   true,
        Optional: d.cfg.KeyFile != "", // password may be optional when a keyfile is set
    }}
}

func (d *driver) Unlock(ctx context.Context, answers map[string]string) error {
    pw := answers[CredPassword] // same constant — no drift
    // build composite key from pw and/or keyfile, decrypt d.cfg.Path, ...
    // on bad credentials: return fmt.Errorf("keepass: %w", ErrAuth)
    _ = pw
    return nil
}
```

## 5. `Manager`

```go
// Manager wraps a single VaultDriver, drives its lifecycle, enforces state, and
// serializes access. It is safe for concurrent use.
type Manager struct {
    // unexported: driver, optional prompter override, mutex, unlocked flag, retry config, (later) cache
}

// NewManager returns a Manager driving the given driver. Credentials are
// collected via the process-wide flexprompt.GetPrompter() unless overridden with
// WithPrompter. Options tune behavior. The vault starts locked.
func NewManager(driver VaultDriver, opts ...Option) *Manager

// WithUnlockRetries caps the ErrAuth re-attempts on Unlock (default 3).
func WithUnlockRetries(n int) Option

// WithPrompter overrides the process-wide prompter for this Manager only
// (useful for tests and, later, multiple vaults with different prompters).
func WithPrompter(p flexprompt.Prompter) Option

// Granular lifecycle:
func (m *Manager) Configure(decode func(target any) error) error
func (m *Manager) Unlock(ctx context.Context) error // Credentials -> Dispatch -> Unlock(answers)

// Open is a convenience running Configure -> Unlock.
func (m *Manager) Open(ctx context.Context, decode func(target any) error) error

// Create initializes a brand-new empty vault (Configure -> InitCredentials ->
// Dispatch -> Init). It requires the driver to implement Initializer; otherwise
// it returns ErrUnsupported. It does NOT leave the vault unlocked — call Unlock
// (or Open) afterward. Fails if the vault already exists.
func (m *Manager) Create(ctx context.Context, decode func(target any) error) error

// Access:
func (m *Manager) Get(ctx context.Context, addr string) (string, error)
func (m *Manager) Set(ctx context.Context, addr string, value string) error
func (m *Manager) List(ctx context.Context, namespace string) ([]string, error)
func (m *Manager) Capabilities() Capabilities

func (m *Manager) Lock() error
func (m *Manager) IsUnlocked() bool
```

Manager rules:

- MUST enforce lifecycle order: `Unlock`/`Get`/`Set`/`List` before a successful
  `Configure` return `ErrNotConfigured` without calling the backend;
  `Get`/`Set`/`List` after `Configure` but before a successful `Unlock` return
  `ErrLocked` without calling the driver.
- `Unlock` when the vault is **already unlocked** is a **no-op that succeeds**
  (returns nil) and does **not** re-prompt. (The CLI's `--force` path, cli.md §4,
  is what deliberately re-prompts and re-unlocks.)
- `Unlock` MUST: call `driver.Credentials()`, pass the requests to the active
  prompter's `Dispatch` (one interaction) — the `WithPrompter` override if set,
  else `flexprompt.GetPrompter()`, read at dispatch time — then call
  `driver.Unlock(ctx, answers)`. It sets the unlocked state only if the driver's
  `Unlock` returns nil.
- Retry policy: on `ErrAuth` the Manager re-runs Dispatch → Unlock, up to
  `WithUnlockRetries` attempts (**default 3**). With a non-interactive prompter
  re-prompting is pointless, so the effective default is **1** attempt (the
  Manager SHOULD treat a prompter that returns identical answers, or reports
  itself non-interactive, as non-retryable). On `ErrPromptCancelled` it MUST
  stop immediately and return.
- MUST serialize all driver calls (a single `sync.Mutex` this revision — simple
  and safe; a read/write split is a later optimization).
- `Lock` clears the unlocked state and calls the driver's `Lock`.
- MUST validate/normalize the secret address before delegating (see §6).
- MUST NOT include secret values or credentials in errors it wraps.

### Typical usage

```go
// Once at startup, install the prompter for this process:
flexprompt.SetPrompter(flexprompt.NewCLIPrompter())

drv := keepass.New()                              // config comes via Configure
mgr := flexvault.NewManager(drv)                  // no prompter threaded through

// Non-secret driver config (in the full flow this comes from the loaded config).
cfg := map[string]any{"path": "secrets.kdbx"}
if err := mgr.Open(ctx, flexvault.MapDecoder(cfg)); err != nil {
    // Configure -> Credentials -> Dispatch (CLI asks for the master password) -> Unlock
    return err
}
defer mgr.Lock()

s, err := mgr.Get(ctx, "artifactory/token")
if err != nil { return err }
use(s)
```

## 6. Addressing: namespaces & keys

Secrets are addressed with a fixed two-level scheme — **`<namespace>/<key>`** —
not an arbitrary path tree:

- An address is exactly two non-empty, `/`-separated segments, case-sensitive,
  e.g. `artifactory/token`, `database/password`. Neither segment may be empty or
  itself contain `/`.
- The Manager validates the address before delegating and rejects anything that
  is not exactly two non-empty segments (leading/trailing whitespace is trimmed
  first).
- `Get(ctx, addr)` / `Set(ctx, addr, value)` take a full `namespace/key`.
- `List(ctx, namespace)`:
  - `namespace == ""` → returns the list of **namespaces**;
  - non-empty → returns the **key names** within that namespace.
  Both return `[]string`. Listing is a single level; there is no recursion
  because there is no deeper nesting.
  - An **unknown namespace** returns an **empty slice and a nil error** (not
    `ErrNotFound`) — listing a missing namespace is not an error. (`secret list`
    then prints nothing and exits 0.)
- `Set(ctx, addr, value)` is **create-or-overwrite**: it overwrites an existing
  key and, when the `namespace` does not yet exist, **auto-creates** it (e.g. the
  KeePass group) before writing the key. A missing namespace/key is therefore
  never an error for `Set` on a writable vault.

KeePass mapping: a top-level group is a namespace, and an entry within it is a
key. Nesting beyond two levels is not addressable through flexconf (documented
convention for `.kdbx` files used with it).

Two `.kdbx` ambiguities are resolved by documented convention (KeePass allows
what the two-level address model does not):

- An entry whose **title contains `/`** is **unaddressable** through flexconf
  (the `/` would be read as the namespace/key separator). Keep titles free of `/`.
- **Duplicate titles** within one group are permitted by KeePass; flexconf
  addresses by title, so on a collision **the first match wins**. The documented
  convention is to keep entry titles **unique within a group**.

## 7. Secret value type

A secret is **a single string value** — not a structured record. `Get` returns a
`string` and `Set` takes a `string`; `$(secret:namespace/key)` resolves to that
string. Backends that store richer entries (a KeePass entry has username, URL,
notes, …) expose only the primary value through flexconf; secondary fields are
not addressable (see [vaults.md](vaults.md) §5).

- For KeePass the value is the entry's `Password` field.
- Secret values and credential answers are `string` in v1. Go strings cannot be
  reliably zeroed; this is an accepted v1 trade-off for ergonomics. A future
  hardening pass may move secret material to `[]byte` — noted, not planned.

## 8. Errors

Sentinel errors, usable with `errors.Is`:

```go
// Package flexvault.
var (
    ErrLocked        = errors.New("flexvault: vault is locked")
    ErrNotFound      = errors.New("flexvault: secret not found")
    ErrReadOnly      = errors.New("flexvault: vault is read-only")
    ErrUnsupported   = errors.New("flexvault: operation not supported by driver")
    ErrAuth          = errors.New("flexvault: unlock failed")
    ErrNotConfigured = errors.New("flexvault: driver not configured")
)
```

- `ErrNotConfigured` is returned when `Unlock`/`Get`/`Set`/`List` is called before
  a successful `Configure` — a lifecycle-order violation distinct from
  `ErrLocked` (which means *configured but not unlocked*).

- Drivers SHOULD wrap these with backend detail via `fmt.Errorf("...: %w", ErrX)`
  but MUST NOT embed secret values or credentials.
- `ErrAuth` distinguishes bad credentials from other unlock failures (missing
  file, corrupt DB), which SHOULD use a distinct wrapped error.
- See also the `Prompter` errors in [prompter.md](prompter.md) §5.

## 9. Driver selection & registration sketch

To let the `secret:` resolver pick a backend by name at runtime without
compile-time coupling, drivers register a factory (in `flexvault`) from their
package `init()`:

```go
package flexvault

// Register associates a driver name with a factory.
func Register(name string, factory func() VaultDriver)

// New constructs a registered driver by name; the caller then Configures and
// Unlocks it (usually via a Manager).
func New(name string) (VaultDriver, error)
```

How a driver name and its config are chosen at runtime — the **named vault
registry**, its layering, the **default vault**, and the `$(secret:[vault:]...)`
reference syntax — is specified in [vaults.md](vaults.md). The resolver-side
wiring (how the `secret:` scheme drives the selected `Manager`) is deferred to
_resolvers.md_. This section exists so the interface above is designed with
selection in mind.

## 10. KeePass driver — first implementation

Package (proposed): `github.com/<org>/flexconf/flexvault/driver/keepass`,
registered as `"keepass"`.

```go
// New returns an unconfigured KeePass driver. Settings arrive via Configure;
// the .kdbx file is opened only at Unlock.
func New() VaultDriver

// Credential keys this driver declares (see §4).
const CredPassword = "password"
```

Config (`keepassConfig`, §4): `path` (required), `keyfile` (optional path),
`readonly` (optional, **defaults to `false`** — the Go `bool` zero value, so an
omitted key yields a writable vault). `Capabilities().Writable` is `!readonly`.

Read-only-ness is a **driver decision surfaced through `Capabilities().Writable`**
(§4): a driver that can only ever read (e.g. a managed cloud vault exposed
read-only) reports `Writable: false` unconditionally; a driver that can do both,
like KeePass, exposes a `readonly` config key (SHOULD default to `false`) so the
operator chooses. There is no separate "readonly" method — `Capabilities()` is
the single source of truth.

Credentials: `Credentials()` returns a single `PromptRequest{ID: CredPassword,
Secret: true}`, marked `Optional` when a keyfile is configured and suffices on
its own. `Unlock` reads `answers[CredPassword]` (see §4 sketch).

Mapping KeePass concepts to the flexconf model:

| KeePass | flexconf |
|---------|----------|
| Master password | `Credentials()` → `PromptRequest{ID: CredPassword}` |
| Key file (path) | `keepassConfig.KeyFile` (contents read at Unlock) |
| Top-level group | namespace (first address segment) |
| Entry within a group | key (second address segment) |
| Entry `Password` field | the secret value (`Get` return / `Set` input) |
| Entry `Title`/`UserName`/`URL`/`Notes`, custom fields | not exposed (secrets are a single value, §7) |

Behavior:

- `Unlock` reads `answers[CredPassword]`, builds the KeePass composite key from
  it and/or the key file, and decrypts the `.kdbx`. Bad credentials MUST return
  `ErrAuth`. The plaintext password is not retained past key derivation; the
  composite key is retained for the session only when `Writable`.
- `Get`/`List` operate on the in-memory decrypted database. `Get` returns the
  entry's `Password` as the value. `List("")` returns top-level group names;
  `List(group)` returns entry titles in that group.
- `Set` modifies the in-memory database and persists to file; MUST return
  `ErrReadOnly` when `readonly` is set.
- **Create** (`Initializer`): `InitCredentials()` returns a single
  `PromptRequest{ID: CredPassword, Secret: true, Confirm: true}` (new master
  password, entered twice); `Init` writes a new empty `.kdbx` at `path` and MUST
  fail if the file already exists. `Capabilities().Creatable` is `true`.
- `Lock` drops the decrypted database and clears the retained composite key.
- Candidate library: `github.com/tobischo/gokeepasslib` (confirm license &
  maintenance before adoption).

## 10a. Normative concurrency & single-writer model

Within one process the `Manager` serializes all driver calls (§5), so a driver
never sees concurrent access. **Across processes**, writes are serialized by
routing them through the **agent**: when an agent holds a `VaultID`, other
processes go through it ([cli.md](cli.md) §6), so every `Set` for that vault
funnels through one owner.

The one unprotected case is **no agent running plus two processes writing the same
backend file directly**. flexconf makes an explicit **single-writer assumption**
here: concurrent direct writers to the same vault file are **unsupported** and MAY
lose data. There is no cross-process file locking in v1. Operators who need
concurrent writers should keep an agent running (the single owner) or serialize
externally.

## 11. Caching & rotation — deferred

Out of scope here; noted so the interface doesn't preclude them:

- The manager is the intended home for an optional read-through cache with TTL.
- Rotating/dynamic secrets may need a lazy/refresh model rather than eager
  `Get`; ties into the eager-vs-lazy question in [overview.md](overview.md) §7.

## 12. Resolved decisions & deferred items

**Resolved (baked into this spec):**

- **Configure input** — the `decode` callback (§4). Ships `MapDecoder` and
  `EnvDecoder`; a loader-bound sub-tree decoder comes with the config-loading
  spec.
- **Credential collection** — declare (`Credentials`) / consume (`Unlock`) with
  a Manager-owned single `Dispatch`; `ID` constants as the join key (§2–§4).
- **Prompter delivery** — `flexprompt` package with a process-wide singleton
  (`SetPrompter`/`GetPrompter`), default error prompter when unset, per-Manager
  `WithPrompter` override ([prompter.md](prompter.md), §5 here).
- **Write support** — `Set` on the interface, `ErrReadOnly` + `Capabilities()`
  discovery (§4).
- **Vault creation** — optional `Initializer` interface + `Capabilities().Creatable`
  + `Manager.Create`; drives `flexcli secret init` (§4, cli.md).
- **Addressing** — fixed two-level `namespace/key`; `List` returns `[]string`
  (§6).
- **Secret material type** — `string` in v1 (§7).
- **Type naming** — the user-facing type is `Manager` (§1). `Vault` was
  considered as an alternative (`vault.Get(...)` reads well) but not adopted;
  non-normative and cheap to revisit. The backend interface is `VaultDriver`
  regardless.
- **Unlock retry** — Manager-configurable, default 3 for interactive prompters,
  1 (no retry) for non-interactive (§5).
- **Lifecycle & edge behavior** — pre-`Configure` calls return `ErrNotConfigured`
  (§5, §8); `Capabilities()` before `Configure` is the zero value (§4); re-`Unlock`
  when already unlocked is a no-op success (§5); `List` of an unknown namespace is
  an empty slice + nil error, `Set` is create-or-overwrite (auto-creating the
  namespace) (§6); `Lock` is synchronous best-effort and clears material even if
  the flush fails (§4).
- **Single-writer model** — cross-process writes serialize through the agent; two
  direct writers to one vault file are an unsupported single-writer assumption, no
  file locking in v1 (§10a).

**Deferred (out of scope, not open design questions):**

- **Multiple vaults** — *resolved* in [vaults.md](vaults.md): vaults are named
  in a registry and a token selects one via a `vault:` segment
  (`$(secret:vault:namespace/key)`), defaulting to the registry's default
  vault. The resolver-side wiring remains in _resolvers.md_.
- **Caching & rotation** — §11.
- **Resolver integration** — how the `secret:` scheme selects and drives the
  active manager (_resolvers.md_, _templating.md_).
- **Credential zeroing** — possible future move of secret material to `[]byte`
  (§7); ergonomic `string` chosen for v1.
