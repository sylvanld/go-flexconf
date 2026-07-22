# Prompter (`flexprompt`)

- **Status:** ✅ Accepted
- **Scope:** the `flexprompt` package — the `Prompter` interface, the
  `PromptRequest` type, the process-wide singleton (`SetPrompter`/`GetPrompter`),
  the built-in prompter implementations, and the prompter sentinel errors. How a
  driver *declares* the credentials it needs and how the `Manager` *dispatches*
  them to the prompter lives in [vault-drivers.md](vault-drivers.md) §2–§5.

A **Prompter** obtains input values (typically credentials) from wherever
flexconf is running — an interactive terminal, a GUI dialog, or a
non-interactive source (env vars, a preset map) in CI and tests. A driver never
reads a terminal, an env var, or a dialog directly: it *declares* what it needs
(via `Credentials`, [vault-drivers.md](vault-drivers.md) §4), and the
`Manager` passes those requests to a `Prompter`, which asks the user for **all
of them in one interaction**.

Prompting is a process-level concern — *where am I running* (CLI, GUI, CI) is a
fact about the whole program, not about any one vault — so flexconf exposes it as
its own package, **`flexprompt`**, with a **process-wide singleton** set once at
startup. This avoids threading a `Prompter` through every `NewManager` and
resolver call. `flexprompt` is the module's **leaf** package (it imports neither
`flexvault` nor `flexconf`), so `flexvault`, its driver packages, and `flexconf`
all import it without cycles. See [overview.md](overview.md) "Module layout".

## 1. Interface and request type

```go
package flexprompt

// Prompter obtains input values from wherever flexconf is running. Interactive
// implementations ask a human; non-interactive ones resolve each req.ID from a
// preconfigured source (env, map, config file). Callers depend only on this
// interface.
type Prompter interface {
    // Dispatch asks for all reqs as a SINGLE interaction and returns the answers
    // keyed by PromptRequest.ID. Optional requests with no value are omitted from
    // the result; a required request with no value yields ErrPromptUnavailable.
    //
    // On any non-nil error the returned map is nil and callers MUST ignore it;
    // "omit a key but succeed" is reserved for the optional-with-no-value case.
    // Every req.ID MUST be non-empty and unique within one call; a duplicate or
    // empty ID is a programming error and Dispatch returns an error rather than
    // silently dropping a request. A cancelled or expired ctx yields an error
    // wrapping ctx.Err(), which the Manager treats as terminal (like
    // ErrPromptCancelled — the unlock retry loop stops).
    Dispatch(ctx context.Context, reqs []PromptRequest) (map[string]string, error)
}

// PromptRequest describes one value a driver needs.
type PromptRequest struct {
    // ID is a stable machine key, e.g. "password", "keyfile-passphrase". It is
    // the join key: the driver declares it here and reads answers[ID] in Unlock.
    // Non-interactive prompters use it as the lookup key (env var, map key).
    // Drivers SHOULD define these as exported constants (see vault-drivers.md §4).
    ID string

    // Label is human-facing text for interactive prompters,
    // e.g. "KeePass master password for secrets.kdbx".
    Label string

    // Secret marks input that MUST be masked (not echoed) and MUST NOT be
    // logged. All credential requests set this true.
    Secret bool

    // Optional allows the value to be absent (key omitted from the answers map).
    Optional bool

    // Default is a suggested value for interactive prompters. MUST be empty
    // when Secret is true; built-in prompters defensively IGNORE Default when
    // Secret is true rather than panicking (a documented caller contract).
    Default string

    // Confirm asks interactive prompters to collect the value twice and require
    // the entries to match (double-entry). Used when a NEW secret is being set,
    // e.g. choosing a master password during vault creation (see cli.md `init`).
    // Non-interactive prompters ignore it. SHOULD be false for normal unlock;
    // built-in prompters honour it per-request and never panic on it.
    Confirm bool
}
```

These field invariants (`Secret`+`Default`, `Confirm` on a normal unlock) are
documented **caller contracts**. Built-in prompters enforce them **defensively**
(ignore `Default` when `Secret`, treat `Confirm` per-request) — they never panic
on a misuse.

A single interaction does not require a batching UI: a `CLIPrompter` may still
ask questions one after another; what matters is that the `Manager` collects the
whole set in one `Dispatch` before calling `Unlock`
([vault-drivers.md](vault-drivers.md) §2, §5).

**Concurrency.** A `Prompter` implementation's `Dispatch` need **not** be safe
for concurrent use. Within a single `Load`, the loader serializes vault unlocks
so at most one `Dispatch` runs at a time
([config-loading.md](config-loading.md) §4). Two concurrent `Load` calls that
both prompt are the caller's concern, not the prompter's — the process-wide
singleton accessors (`SetPrompter`/`GetPrompter`) remain concurrency-safe (§2)
regardless.

## 2. The singleton

```go
package flexprompt

// SetPrompter installs the process-wide Prompter. Typically called once at
// startup, e.g. flexprompt.SetPrompter(flexprompt.NewCLIPrompter()).
// Safe for concurrent use. SetPrompter(nil) resets to the default (§ below),
// which is convenient for restoring state between tests.
func SetPrompter(p Prompter)

// GetPrompter returns the process-wide Prompter. It NEVER returns nil: if none
// has been set it returns a default prompter whose Dispatch fails every required
// request with ErrNoPrompter. Safe for concurrent use.
func GetPrompter() Prompter
```

- Access is guarded internally (mutex or `atomic.Value`); `Set`/`Get` are safe
  for concurrent use.
- **Default when unset:** `GetPrompter` returns an *error prompter* that returns
  `ErrNoPrompter` for any required request. We deliberately do **not** default to
  the CLI prompter — a daemon that never called `SetPrompter` would otherwise
  hang reading stdin. Interactive apps opt in explicitly with one line.
- The `Manager` reads `GetPrompter()` **at dispatch time** (not at construction),
  so a `SetPrompter` performed after a `Manager` is built still takes effect. A
  `Manager` MAY override the process-wide prompter for itself with `WithPrompter`
  ([vault-drivers.md](vault-drivers.md) §5).

## 3. Choosing an implementation

Because the driver only ever *declares* requests and the installed `Prompter`
decides how they are answered, the *same driver code* works everywhere:

| Where flexconf runs | Prompter to install | Behavior |
|---------------------|---------------------|----------|
| Interactive CLI | `NewCLIPrompter()` | Prompts the terminal for each req; masks `Secret` input. |
| GUI / TUI | app-provided | Renders all reqs as one dialog / masked fields. |
| CI / non-interactive | `NewMapPrompter` / `NewEnvPrompter` | Resolves each `req.ID` from a preset map or env var; errors if a required one is missing. |
| Tests | `NewMapPrompter` | Returns canned values. |

Exactly **one** prompter is selected — the process-wide singleton (§2). flexconf
does not chain or layer prompters; a program picks the single implementation that
matches where it runs.

## 4. Provided implementations

```go
func NewCLIPrompter() Prompter                         // terminal; masks secrets (x/term)
func NewMapPrompter(values map[string]string) Prompter // resolve each req.ID from the map; non-interactive
func NewEnvPrompter(prefix string) Prompter            // req.ID -> ${PREFIX}${ID} env var
type PrompterFunc func(context.Context, []PromptRequest) (map[string]string, error) // adapter
```

## 5. Errors

Sentinel errors, usable with `errors.Is`:

```go
var (
    ErrPromptCancelled   = errors.New("flexprompt: prompt cancelled")     // user aborted
    ErrPromptUnavailable = errors.New("flexprompt: no value for prompt")  // required req unresolved (non-interactive)
    ErrNoPrompter        = errors.New("flexprompt: no prompter configured") // GetPrompter default; call SetPrompter
)
```

- `ErrPromptCancelled` is terminal: the `Manager` MUST stop its unlock retry loop
  immediately on it ([vault-drivers.md](vault-drivers.md) §5).
- `ErrNoPrompter` is what the default (unset) prompter returns; installing a
  prompter with `SetPrompter` (§2) resolves it.
