# `settings` + `flexconf` — application configuration

This spec covers two layers of the flexconf configuration story:

- **`settings`** — a thin, dependency-free helper that resolves *where* an
  application keeps its configuration on disk. It computes paths and (optionally)
  creates the directory; it does not read or write configuration content.
- **`flexconf`** — the top-level loading façade built on top of `settings` that
  turns a config file on disk into the typed struct an application decodes into.
  It adds a **templating** pass (`$(env:…)`, `$(secret:…)`, `$(config:…)`) so a
  committed file can vary per host and pull credentials from the
  [`secrets`](secrets.md) store without carrying them in the clear. It is the
  module-root package, so an app gets the whole story from one import
  (`flexconf.Load(cfg, &out)`), with `settings`/`secrets`/`agent` reachable
  underneath when finer control is wanted.

The split mirrors the toolkit's layering: `settings` owns *location*, `flexconf`
owns *content*, and `flexconf` reuses the existing [`secrets`](secrets.md)
package (rather than inventing a parallel secret abstraction) to resolve
`$(secret:…)` references. Keeping `settings` free of any dependency on `secrets`
or a YAML library is a deliberate invariant — see [Package layering](#package-layering).

---

# Part 1 — `settings`: configuration location

Package `settings` resolves where an application keeps its configuration on
disk. It is a thin, dependency-free helper: it computes paths and (optionally)
creates the directory, but does not read or write configuration content itself.

## Types

### `AppConfig`

Immutable value describing one application's config location — the *app
context*, as distinct from `flexconf.Settings` (loaded content). Fields are
unexported; construct with `New` and read through accessors.

- **app name** — the application identifier supplied to `New`.
- **path** — the resolved settings directory.

### `Option`

Applied during `New` to customise resolution: `WithPath`, `WithEnv`.

## Constructors and options

### `New(appName string, opts ...Option) (*AppConfig, error)`

Builds an `AppConfig` for `appName`, resolving the settings directory by this
precedence:

1. `WithPath(path)` if non-empty.
2. `<APP>_CONFIG` from the environment if set and non-empty — the config
   **directory**.
3. `DefaultPath(appName)`.

- Returns `ErrEmptyAppName` if `appName` is `""`.
- Options are applied **before** resolution, so an explicit `WithPath` outranks
  the environment regardless of argument order.
- `DefaultPath` is consulted only when nothing above it applies; its error is
  propagated.
- With no options and no `<APP>_CONFIG`, `Path()` equals `DefaultPath(appName)`.

### `WithPath(path string) Option`

Overrides the settings directory, outranking `<APP>_CONFIG`.

- A non-empty `path` replaces everything below it in the precedence order.
- An **empty** `path` is a deliberate no-op, so an unset override never clobbers
  the resolution below it.

### `<APP>_CONFIG` — the directory override

`New` reads it itself; the app writes no `os.Getenv`:

```go
cfg, err := settings.New(appName)
// EXAMPLE_CONFIG set   → that directory
// EXAMPLE_CONFIG unset → ~/.config/<appName>/  (the default)
```

**It names the config *directory*, never a file.** The main config file is
always `config.yaml` inside it, so the variable relocates the directory as a
unit — `config.yaml` and `secrets.kdbx` move together.

That "as a unit" property is the reason resolution lives here and *only* here.
An override interpreted independently by a second component would relocate some
paths and not others, leaving an app reading its config from one directory and
its secrets from another. There is one resolution point, so there is nothing to
disagree with.

### `EnvVar(appName string) string` (package function)

Returns the variable name: uppercased app name, every non-alphanumeric character
→ `_`, suffixed `_CONFIG`. `"my-app"` → `MY_APP_CONFIG`. An app name that maps to
the empty string yields `APP_CONFIG`.

### `WithEnv(env Env) Option`

Injects the environment the lookup reads (`Lookup(name) (string, bool)`), so
tests never depend on the process environment. A `nil` env is ignored.

`Env` is declared in `settings` rather than reused from the loader because the
loader imports this package — sharing the type would invert that dependency. The
method sets are identical, so a `loader.MapEnv` satisfies `settings.Env`
directly.

## Path resolution

### `DefaultPath(appName string) (string, error)` (package function)

Returns `<user-config-dir>/<appName>`.

- Returns `ErrEmptyAppName` if `appName` is `""`.
- `<user-config-dir>` comes from `os.UserConfigDir()`; any error there is
  propagated. On Linux this is `$XDG_CONFIG_HOME` when set, otherwise
  `~/.config` — so the default is typically `~/.config/<appName>/`.

## Accessors

| Method | Behaviour |
| --- | --- |
| `AppName() string` | The app name passed to `New`. |
| `Path() string` | The resolved settings directory (override or default). |
| `DefaultPath() (string, error)` | The default path for this app, **regardless** of any `WithPath` override — recomputed from the app name. |
| `IsDefault() bool` | `true` iff the resolved path equals the default path. Returns `false` if the default cannot be computed. |
| `File(name string) string` | `filepath.Join(Path(), name)` — the path to a file inside the settings directory. Pure string join; the file need not exist. |

## Side effects

### `EnsureDir() error`

Creates the settings directory and any missing parents with mode `0o755`
(`os.MkdirAll`). Idempotent; a pre-existing directory is not an error. This is
the only method in the package that touches the filesystem.

## Errors

| Sentinel | Condition |
| --- | --- |
| `ErrEmptyAppName` | `New` / `DefaultPath` called with an empty app name. |

Errors from `os.UserConfigDir()` and `os.MkdirAll()` are returned as-is
(unwrapped).

## Invariants

- A successfully constructed `AppConfig` always has a non-empty app name and a
  non-empty path.
- `File(name)` is always rooted at `Path()`.
- Accessors never mutate the value; `AppConfig` is effectively read-only after
  `New`.
- **The settings directory is resolved exactly once**, in `New`. No other
  component re-derives it from the environment, so every path built via `File`
  relocates together.
- **`settings` imports nothing from `secrets`, `flexconf`, or a YAML library** —
  it is the base layer everything else builds on.

---

# Part 2 — `flexconf`: loading and templating

Package `flexconf` (the module root) turns a configuration file into the typed
struct an application decodes into. It owns the steps that run **before any
semantic decoding**: read the file, parse it to a generic node tree, and resolve
three kinds of reference in that tree — `$(env:…)`, `$(secret:…)`,
`$(config:…)` — then unmarshal the substituted tree into the caller's struct.

Two things pull on that loading step, and they are why templating exists:

- **Configs must not carry secrets in the clear.** Credentials (API tokens,
  cookies, bot tokens) must not live as literals in a committed file. The
  toolkit already has a [`secrets`](secrets.md) store for them; `$(secret:NAME)`
  is the **only** channel by which a stored secret reaches the config, with the
  fail-loud, never-log-a-secret discipline the rest of the repo insists on.
- **A committed config wants to vary per environment.** The same file should run
  on a laptop and a server with only the environment differing — via
  `$(env:NAME)` — without hand-editing YAML or forking per host.

The abstraction is deliberately narrow: templating computes **what the config
text is**; the application's own decode/validate decides **what it means**.
Templating never interprets an application's blocks — it only substitutes values
and splices includes, handing a generic tree to the decoder.

## The load pipeline

Loading is a fixed, ordered pipeline. Each step consumes the previous step's
output and does exactly one thing, so a failure is always attributable to a
single stage — and, per the repo's **fail-loud, before the app does any work**
invariant, every check that *can* run at load time does.

```
  config file (bytes)
        │
        ▼
  1. Locate    resolve the config file path
        │      (option → cfg.File("config.yaml"); the directory, incl. its
        │       <APP>_CONFIG override, was resolved by settings.New)
        ▼
  2. Read      read the file
        │
        ▼
  3. Parse     unmarshal into a generic YAML node tree (structure only)
        │
        ▼
  4. Bootstrap template the `secrets` block with env + config ONLY (no secret),
        │      then build the secret Store from it            ← driver selection
        ▼
  5. Template  substitute $(env:…)/$(secret:…) in scalar leaves and splice
        │      $(config:…) includes (recursively), tracking secret-sourced
        │      nodes as tainted                                ← THIS SPEC
        ▼
  6. Decode    unmarshal the templated tree into the caller's typed struct
               (the application owns merge/validate from here)
```

Steps 1–5 are owned by `flexconf`; step 6 is a `yaml.Unmarshal` into the struct
the caller hands `Load`. `flexconf` never sees the app's schema — it decodes into
an opaque `out any`, so merge and validation stay the application's job.

Defaults are the one exception, and only because they need no schema knowledge:
the app *declares* them as a pre-populated value of its own type, and `flexconf`
merely decodes over it. See [Defaults](#defaults).

### Why substitute on the node tree, not the raw text

The obvious implementation is `envsubst`-style: replace in the raw bytes, then
parse. It is rejected because injected values would be interpreted as YAML. A
secret or an environment value containing a `:`, a leading `-`, a `#`, a newline,
or leading whitespace would silently change the document's *structure* — at best
a parse error, at worst a value that quietly lands under the wrong key. That is
exactly the "config quietly does the wrong thing" failure the repo forbids.

Instead, step 3 parses the file into a generic node tree first, and step 5 walks
that tree and operates on **scalar leaves only**:

- **Keys are never templated** — only values. `$(env:X)` as a mapping key is
  left literal (and will almost certainly fail the app's unknown-key check at
  decode, which is the right outcome).
- An **`env`/`secret`** substitution always re-emits a **plain scalar string**.
  It cannot introduce a mapping, a sequence, or a new document node, so no
  injected value can alter structure.
- A **`config`** reference replaces its node with the **parsed** tree of the
  referenced file — nodes produced by a real YAML parser, well-formed by
  construction. Structure comes only from a real parse, never from string
  splicing.
- Because `env`/`secret` substitution is scoped to leaves, such a reference works
  both as a whole value (`token: $(secret:api/token)`) and embedded in a larger
  string (`path: $(env:HOME)/.cache/app`). A `config` reference, producing a
  subtree rather than text, must be the **whole** value — see the syntax rules.

## Templating

### Syntax

A reference is `$( <namespace>:<name> )` — namespace and name separated by a
colon, wrapped in `$( … )`. Whitespace inside the parens is insignificant
(`$(env:X)` ≡ `$( env:X )`). Three namespaces ship:

| Reference | Resolves to | Produces | In logs? |
|---|---|---|---|
| `$(env:NAME)` | the process environment variable `NAME` | scalar string | **yes** — non-secret |
| `$(secret:NAME)` | `NAME` from the configured [`secrets.Store`](secrets.md) | scalar string | **never** — tainted & redacted |
| `$(config:PATH)` | the parsed contents of the YAML file at `PATH` | subtree | n/a (structure, not a value) |

**Why `$( … )` and not `${{ … }}`.** The braces `{` and `}` are YAML *flow*
indicators, so a `${{ … }}` reference parses cleanly in block context but raises
a YAML error inside a flow mapping (`{ k: ${{ … }} }`) — it must be quoted
there. `(` and `)` are **not** YAML indicators in any context, so a `$( … )`
reference is unquoted-safe everywhere. The config never sprouts defensive quotes
around a reference.

- **`env`** is for configuration that merely *varies* per host and is safe to see
  in a log line: paths, ids, a timezone, a tuning number.
- **`secret`** is for credentials; resolving through it marks the node **tainted**
  so the loader redacts it everywhere the config is dumped. The namespace is the
  author's declaration of "is this a secret?", and the loader enforces the
  consequences.
- **`config`** splices another file in — the composition primitive for breaking a
  long config into per-block files.

**Name grammar is per-namespace:**

- `env` — `[A-Za-z_][A-Za-z0-9_]*`, the shell env-var shape (`$(env:APP_SEED)`).
- `secret` — a **path-like** key: segments of `[A-Za-z0-9_.-]+` joined by `/`
  (`$(secret:api/token)`). This is exactly the key shape the
  [`secrets`](secrets.md) store already uses (KeePass entry titles, path-like
  keys); the whole `api/token` string is passed to `Store.GetValue`.
- `config` — a **filesystem path** to a `.yaml`/`.yml` file, resolved **relative
  to the file that contains the reference** (not the process CWD), so a moved
  config tree keeps working. The path is a literal — it is not itself templated,
  which keeps the include graph statically knowable.

An unknown namespace (`$(envv:X)`, `$(foo:X)`) is a **load error**, not a
passthrough — a typo must fail loud, not silently survive as a literal.

### Defaults and escaping

- **Default value (`env` only).** `$(env:NAME:-fallback)` resolves to `fallback`
  when `NAME` is unset — the shell `:-` form, no quotes needed. Everything after
  `:-` up to the closing `)` is the literal fallback, used verbatim and *not*
  re-templated. `secret` has **no** default form (a silently-defaulted credential
  is a footgun) and `config` has none (a missing include is always fatal). One
  limitation: a fallback cannot contain a literal `)`.
- **Escaping.** `$$(` emits a literal `$(` — the only escape.

### `config` includes

A `config` reference is replaced by the **whole parsed tree** of the target file:

```yaml
plugins:
  - $(config:plugins/http.yaml)          # each file holds one mapping
  - $(config:plugins/db.yaml)
logging: $(config:logging.yaml)          # or a whole block
```

- **Must be the entire value.** Because it yields structure, not text, a `config`
  reference cannot be embedded in a larger scalar (`foo$(config:x.yaml)` is a
  load error). It stands alone as a mapping value or a sequence item.
- **Included files are templated too**, recursively, against the same environment
  and secret store; a child may itself `config`-include a grandchild.
- **Cycles are fatal.** The loader tracks the include stack (by absolute path); a
  file that transitively includes itself is a load error naming the cycle.
- **Taint propagates.** A `secret` reference inside an included file taints its
  node exactly as in the root file — redaction is a property of the merged tree,
  not of which file a node came from.

### Resolution and failure

- **Unresolved reference is fatal.** An unset `$(env:NAME)` with no default, a
  `$(secret:NAME)` the store can't supply (`secrets.ErrNotFound`), or a
  `$(config:PATH)` that is missing/unreadable/unparseable is a **load error**
  raised before the app runs — never an empty string, never a skipped field.
- **A malformed reference is fatal too.** A contiguous `$(` that does not parse
  as `$( namespace:name )` is a load error, not silently-passed text.
- **`env`/`secret` values are not re-scanned.** A resolved *scalar* value is not
  searched for further `$( … )`, so a secret whose value happens to contain `$(`
  is data, not a template — closing an injection avenue.
- **`config` paths are literal** — include resolution can't be steered by an
  environment value.
- **Errors accumulate.** The pass reports *every* unresolved/invalid reference in
  one go (with `file:line`, across included files), not just the first.

```go
// Template rewrites scalar leaves of the parsed tree in place: env/secret
// references become plain strings; a config reference is replaced by the parsed,
// recursively-templated tree of the target file. It records secret-sourced nodes
// so later dumps can redact them, and returns a joined error listing every
// unresolved or invalid reference (fail loud, before decode). `stack` carries the
// include chain (absolute paths) for cycle detection.
func Template(n *yaml.Node, dir string, env Env, secrets SecretResolver, stack []string) (tainted NodeSet, err error)
```

## Secret references — reusing the `secrets` package

`env` needs no configuration — it is the process environment. `secret` needs a
resolver to look names up against, and here `flexconf` **reuses the existing
[`secrets.Store`](secrets.md)** rather than defining a new store abstraction. The
loader consumes a minimal interface that `*secrets.Store` satisfies:

```go
// SecretResolver resolves a $(secret:NAME) reference to its value. The name is
// the whole path-like key as written (e.g. "api/token"). A missing key must
// surface as secrets.ErrNotFound so the templating pass can raise one uniform
// "unresolved $(secret:NAME)" error with the config file:line.
type SecretResolver interface {
    Secret(name string) (value string, err error)
}
```

An adapter wraps the toolkit's store:

```go
// StoreResolver adapts a *secrets.Store to a SecretResolver. It calls
// Store.GetValue, which lazily Unlock()s the driver on first use, so unlocking
// (a KeePass password prompt, an agent handshake) happens exactly once, on the
// first $(secret:…) hit — not at config-file open.
func StoreResolver(s *secrets.Store) SecretResolver
```

`GetValue` returning `secrets.ErrNotFound` → the loader records
`unresolved $(secret:NAME) at file:line`. Any other error (locked driver, wrong
password, agent unreachable) propagates verbatim so the operator sees the real
cause.

### Taint and redaction — the taint set is load-bearing

Every place that renders the config back out (a `--print-config` dump, a debug
log, an error that echoes a block) **must** consult the taint set returned by
`Template` and replace secret-sourced scalars with `«redacted»`. A value that
arrived via `$(secret:…)` is structurally marked, so redaction is not a per-field
allowlist someone can forget to extend — it covers any field a secret was
templated into. `env` values are **not** redacted: `env` is the explicit
"safe to log" channel, and redacting it would erase the distinction between the
namespaces.

## Secret driver selection

`flexconf` resolves `$(secret:…)` through a `*secrets.Store`, and a store is only
as good as its [`Driver`](secrets.md). The toolkit already ships two drivers —
[`KeepassDriver`](keepass-driver.md) (prompts for a master password) and
[`agent.Client`](agent.md) (talks to an already-unlocked background agent) — and
which one a config load should use depends on the deployment. This is the one
genuinely new design question in reconciling the specs, so it gets its own
treatment.

The design reuses the **existing `secrets.Driver` abstraction** end to end: there
is no parallel "secret provider" interface (the earlier design's `SecretStore` +
`secrets.provider` registry is dropped). Selecting a secret backend means
selecting a `secrets.Driver`, and there are three complementary ways to do it,
in precedence order: an **injected store** (DI), a **`secrets` block** in the
config, or the **zero-config default** — each covering a different kind of app.

### Zero-config default: reuse what `secretcli` builds

With **no `secrets` block and no injected store**, `flexconf` builds exactly the
stack the [`secretcli`](secretcli.md) command tree already manages, so
config-loading and the `<app> secrets …` CLI share one store with zero wiring:

1. An `agent.Client` at `agent.SocketPath(cfg.AppName())`. If an agent is
   running (the common case after `<app> secrets unlock`), `$(secret:…)`
   resolves non-interactively — no prompt during startup.
2. **Fallback** if no agent answers: a **read-only** `KeepassDriver` at
   `cfg.File("secrets.kdbx")`, which prompts once on the terminal. Read-only is
   the right default for a config *reader* — it never creates a database and
   drops the master key after unlock.

So a config containing `token: $(secret:api/token)` works out of the box against
the same `secrets.kdbx` the CLI writes, with no `secrets` block at all.

### Config-selected driver: the `secrets` block

An app that wants the *config file* to choose the backend declares a `secrets`
block naming a driver, resolved through a small registry keyed by name:

```yaml
secrets:
  driver: keepass                        # registered driver name
  keepass:
    path: $(env:APP_SECRETS:-)           # default: cfg.File("secrets.kdbx")
    readonly: true
```

```go
// SecretDriverFactory builds a secrets.Driver from its config sub-block. It gets
// the resolved *settings.AppConfig (for defaults like cfg.File("secrets.kdbx")),
// the driver's own opts node (already templated with env+config only), and the
// loader Env (for the env driver and for tests).
type SecretDriverFactory func(cfg *settings.AppConfig, opts *yaml.Node, env Env) (secrets.Driver, error)

// RegisterSecretDriver registers a driver factory under a name. Called from
// init(); duplicate names panic; an unknown name in a config is a fatal load
// error — the same registry+factory discipline as the rest of the toolkit.
func RegisterSecretDriver(name string, f SecretDriverFactory)
```

Built-in drivers (each a thin wrapper over existing packages):

| `driver:` | Backend | Notes |
|---|---|---|
| `agent` *(default)* | `agent.Client` | non-interactive; falls back to `keepass` if no agent runs |
| `keepass` | `KeepassDriver` | `path` (default `cfg.File("secrets.kdbx")`), `readonly` (default `true` for a reader) |
| `env` | reads `$NAME` for `secret:NAME` | the taint/redaction guarantee without a separate store |
| `exec` | runs a command with the name as an argument, reads stdout | covers `pass`, `sops`, `vault` |
| `none` | a store whose `Get` always returns `ErrNotFound` | any `$(secret:…)` is a fatal "no secret backend configured" — for configs that must be secret-free |

Future backends drop in as new factories with zero loader changes.

### Bootstrap: no chicken-and-egg

The `secrets` block is templated with **`env` (+ `config`) only** — its own
`$(secret:…)` references are a **load error**, because the store that would
resolve them does not exist yet. Concretely (pipeline step 4): the loader
templates `secrets` env-only, builds the driver, wraps it in
`secrets.NewStore(driver)`, then (step 5) templates the rest of the tree with all
three namespaces. This keeps "where do secrets come from" answerable without
circularity.

### DI override: inject a store directly

Independently of any config block, the app (or a test) can hand the loader a
ready `*secrets.Store`, bypassing the registry entirely:

```go
flexconf.Load(cfg, &out, flexconf.WithSecretStore(store))   // e.g. an in-memory test store
```

An injected store wins over the `secrets` block and the zero-config default. This
mirrors `cmd/example`, where the app already wires the `secrets`/`agent` stack
itself and simply reuses it for config loading.

### Why config-select *and* inject, but not a bespoke provider interface

- **Injection (DI)** fits the toolkit's existing pattern — `cmd/example` builds
  the stack in Go and knows which driver it wants. It is the recommended path for
  a single-binary app and needs no config surface.
- **A `secrets` block** is there for apps that genuinely want the *deployment* to
  pick the backend (a laptop using `agent`, a CI runner using `env`, a server
  using a read-only `keepass`) without a rebuild.
- **Reusing `secrets.Driver`** — rather than the earlier design's separate
  `SecretStore`/`secrets.provider` — means one secret abstraction across the CLI,
  the agent, and config loading. A driver written once (e.g. `exec`) serves all
  three.

All three mechanisms ship together, sharing one `secrets.Driver` registry. Their
precedence — injected store → `secrets` block → zero-config default — means a
single-binary app can ignore the block entirely (default or DI), while a
deployment that needs to pick its backend does so in the config without a
rebuild, and neither path costs the other anything.

## Public API (`flexconf`)

```go
// Load resolves the config file, reads+parses+templates it, and unmarshals the
// result into out (a pointer to the app's config struct). Steps 1–5 are owned by
// flexconf; the final yaml decode into out is step 6.
func Load(cfg *settings.AppConfig, out any, opts ...Option) error

// LoadFile runs steps 1–5 and returns the templated tree + taint set without
// decoding, for callers that also want Dump (redacted rendering).
func LoadFile(cfg *settings.AppConfig, opts ...Option) (*Settings, error)

type Settings struct { Tree *yaml.Node; Taint NodeSet; Default *yaml.Node }
func (s *Settings) Decode(out any) error   // step 6, strips the loader-owned `secrets` block first
func (s *Settings) Dump() ([]byte, error)  // re-render with tainted scalars → «redacted»

// Options:
func WithConfigFile(path string) Option       // explicit path (highest precedence)
func WithSecretStore(s *secrets.Store) Option // inject a resolver; wins over the block/default
func WithSecretResolver(r SecretResolver) Option
func WithEnv(env Env) Option                  // inject the environment (tests)
func WithFS(fsys fs.FS) Option                // read root+includes from fsys (tests)
```

Config-file location precedence (pipeline step 1):

1. `WithConfigFile(path)` if non-empty.
2. `cfg.File("config.yaml")` — always this name, inside the resolved settings
   directory.

**The loader reads no environment variable of its own to locate the file.** The
directory — including its `<APP>_CONFIG` override — is resolved once by
`settings.New`, and the loader simply joins `config.yaml` onto it. Relocating
config means pointing `<APP>_CONFIG` at a different *directory*, not naming a
different file.

## Defaults

A default is **a pre-populated value of the app's own type**, never a parallel
description of the schema. This is the whole design constraint: a `default:` tag
or a YAML default-string is a second copy of the schema that drifts from the
first one silently. A Go value cannot drift — it is type-checked against the
struct it defaults.

The mechanism rests on one property of `yaml.v3`: **decoding into a non-zero
value leaves fields the document does not mention untouched.** So "apply
defaults" is just "decode the default first, then decode the file over it", and
the merge is per key, for free.

```go
// Defaults builds a Settings whose fallback tree is v marshalled to YAML.
func Defaults(v any) Settings

type Settings struct {
    Tree    *yaml.Node  // what was loaded (nil if the block was absent)
    Taint   NodeSet
    Default *yaml.Node  // the declared fallback, decoded *under* Tree
}
```

- `Decode` applies `Default` first, then `Tree` over it. A block the config
  omits entirely still yields fully-populated settings; a block naming only some
  keys inherits the rest.
- `UnmarshalYAML` sets `Tree` but **preserves** `Default` — otherwise capturing a
  block would silently discard its fallback and degrade the merge to a wholesale
  replacement.
- `MarshalYAML` renders `Tree`, or `Default` when nothing was loaded. This is
  what lets a whole pre-populated config struct marshal straight to a default
  config file.

### Polymorphic defaults

A variant's factory already returns a *fresh* target, so a factory returning a
pre-populated value **is** that variant's default — `Decode` decodes the block
onto it. Two additions make the fallback selectable and renderable:

```go
func (p *PolymorphicSettings[I]) SetDefault(name string) *PolymorphicSettings[I]
func (p *PolymorphicSettings[I]) Default() (I, error)
func (p *PolymorphicSettings[I]) DefaultSettings() (Settings, error)
```

- `SetDefault` names the variant a block resolves to when it omits the
  discriminator, or is absent entirely. It **panics** on an unregistered name —
  same fail-loud registry discipline as `Register`'s duplicate check, catching a
  wiring bug at startup rather than at the first load.
- Without a `SetDefault`, a missing discriminator stays a **fatal error**. Adding
  the default mechanism must not relax the existing fail-loud behaviour.
- `DefaultSettings` renders the default variant *plus the discriminator naming
  it* — a complete, re-loadable block rather than an empty stub. The variant's
  own struct never declares the discriminator, so it is spliced back in as the
  block's first key.

```go
var vaults = flexconf.NewPolymorphicSettings[vault]("type").
    Register("keepass", func() vault { return &keepassVault{Path: "secrets.kdbx", ReadOnly: true} }).
    Register("env",     func() vault { return &envVault{Prefix: "APP_"} }).
    SetDefault("keepass")
```

### Declaring an application's defaults

The app composes the same pieces into one function returning a fresh, fully
populated config. It is a `func` rather than a shared value so each call yields
defaults untouched by anything that decoded over them earlier:

```go
func defaultConfig() any {
    vaultDefaults, _ := vaults.DefaultSettings()
    return &config{
        Name:  "example",
        HTTP:  flexconf.Defaults(&httpConfig{BaseURL: "https://api.example.com", Timeout: 30}),
        Vault: vaultDefaults,
    }
}
```

Marshalling that value is what `settings init` writes — see
[`cli/settings`](./settingscli.md).

## Config shape

Templating touches every block, so references can appear anywhere a scalar value
can. The one loader-owned block is `secrets` (optional; omit for the zero-config
default):

```yaml
secrets:                                   # optional; omit to use the agent→keepass default
  driver: agent

http:
  base_url: $(env:APP_BASE_URL:-https://api.example.com)   # env with a default
  token:    $(secret:api/token)            # secret → tainted, redacted, from the store

logging: $(config:logging.yaml)            # whole block pulled from another file

plugins:
  - name: search
    endpoint: $(env:SEARCH_URL)
  - $(config:plugins/db.yaml)              # a plugin defined in its own file
```

Rules, matching the fail-loud discipline of the other specs:

- **Every reference must resolve at load** — an unset `$(env:NAME)` with no
  default, an absent `$(secret:NAME)`, or a missing/unparseable `$(config:PATH)`
  is a startup error. Errors accumulate and report `file:line` across includes.
- **Unknown or malformed reference is fatal.** Only `env`, `secret`, and `config`
  exist; a literal `$(` is escaped as `$$(`.
- **`config` includes must be whole values, resolve relative to their own file,
  and may not form a cycle** — each condition is a load error.
- **`secrets.driver` must name a registered factory**; an unknown driver is
  fatal, exactly like an unknown `secrets` name elsewhere.
- **`secret` in the `secrets` block itself is an error** (bootstrap: env + config
  only).
- **Only `env` has a default form** — a missing `secret` or `config` always errors.
- Templating runs **before** the app's decode, so an unknown *key* produced by a
  mistemplated value still hits the app's usual unknown-key error at decode;
  templating loosens nothing about that discipline.

## Determinism & testability

- **Injected `Env` and `SecretResolver`.** `Template` takes both as interfaces,
  so tests supply a fixed map for each and assert exact substitution, exact taint
  set, and exact error text — no real environment or store touched.
- **File reads for `config` includes go through an injectable fs** (an `fs.FS`
  rooted at the config dir), so include resolution, relative paths, and cycle
  detection are testable against an in-memory tree with no disk.
- **The pass is a pure function of `(tree, dir, env, secrets, fs)`** — same
  inputs, same output tree and same accumulated errors, byte for byte.
- **Redaction is testable in isolation:** given a tree + taint set, dumping must
  produce `«redacted»` for exactly the tainted nodes and verbatim values
  elsewhere.
- Table-driven cases assert: (a) whole-value and embedded substitution, including
  a reference inside a flow mapping (`{ k: $(env:X) }`) with no quoting; (b)
  `$$(` escaping; (c) `env` `:-` default used iff unset; (d) unresolved refs
  accumulate rather than short-circuit; (e) a secret value containing `$( … )`
  is not re-expanded; (f) an injected value with a `:` or newline stays a single
  scalar (the anti-injection property); (g) a `config` include splices the child
  tree, is templated recursively, propagates taint, and a self-include errors as
  a cycle rather than looping; (h) driver selection: an injected store wins over
  a `secrets` block wins over the zero-config default; an unknown `driver:` is
  fatal; a `$(secret:…)` inside the `secrets` block is fatal.

---

# Package layering

| Package | Depends on | Owns |
|---|---|---|
| `settings` | stdlib only | config **location** (paths, dir creation) |
| `secrets` | stdlib only | the `Store`/`Driver` secret abstraction |
| `flexconf` (root) | `settings`, `secrets`, YAML | config **content** (read, parse, template, decode) |

Keeping `settings` dependency-free is the load-bearing constraint: any app can
use it for paths without pulling in a YAML parser or the secret machinery. The
templating loader that *does* need those is the module-root `flexconf` package,
one layer up, and the secret backend it resolves against is the same
`secrets.Store`/`Driver` the CLI and agent already use. An app that only wants
paths imports `settings`; an app that wants the whole story imports `flexconf`
and gets `Load` plus everything beneath it.

## Mapping the concepts to the loader

| Concept | Where it lives | Notes |
|---|---|---|
| config directory | `settings.AppConfig` | `~/.config/<app>/`; `WithPath` → `<APP>_CONFIG` → default. The single resolution point. |
| config file location | `config.Load` step 1 | `WithConfigFile` → `cfg.File("config.yaml")`; no envvar of its own |
| `$(env:NAME)` | `Template`, `Env` interface | non-secret, per-host; may appear in logs |
| `$(secret:NAME)` | `Template` + `SecretResolver` over `secrets.Store` | tainted → redacted; the only credential channel |
| `$(config:PATH)` | `Template` splice + injectable fs | compose config from files; recursive, cycle-checked |
| secret driver selection | injected store → `secrets` block registry → `agent`→`keepass` default | one name ↔ one `secrets.Driver`; unknown is fatal |
| never-log-a-credential | taint set + redaction on dump | structural, not a per-field allowlist |

## Notes / open questions

- **`~` expansion.** `path: ~/.config/...` and `$(env:HOME)/...` both appear
  above. Decide where tilde expansion happens (loader vs. each field) and apply
  it consistently; templating itself does not expand `~`.
- **Non-interactive loads.** Because a config load may happen in a service with
  no terminal, the `agent` driver (non-interactive) is the default and a
  terminal-prompting `keepass` fallback should be suppressible (`driver: none` or
  an injected store) so startup fails loud instead of blocking on a prompt.
- **`config` include vs. merge.** An include *splices* a subtree; it does not
  deep-merge. A general "base + overlay" merge, if ever wanted, is a separate
  feature the app layers on after decode, not on templating.
- **Depth limit for includes.** Cycles already error; consider also a max include
  depth as a guard against a pathologically deep (but acyclic) graph.
- **`env`/`secret` in a non-string position.** Substitution happens on the parsed
  node tree, not by re-parsing text, so an injected value can never introduce a
  mapping, sequence, tag, or alias regardless of its tag — a scalar node stays a
  scalar. Given that, the substituted scalar is left **untagged** (`Tag: ""`) so
  it resolves like any hand-written value: `$(env:PORT)` → `8080` decodes into an
  `int`, `$(env:DEBUG)` → `true` into a `bool`, and text into a `string`.
  Applications use native Go types (and arbitrary structs) with no wrapper types.
  The destination field type still governs, so a `string` field keeps a
  numeric-looking secret verbatim (`"0042"` does not become `42`). A reference
  that must yield a sequence or mapping is what `config` is for — `env`/`secret`
  remain scalar-only by design.
