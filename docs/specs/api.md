---
tags:
  - specs
  - api
  - configuration
  - secrets
---

# Public API Surface

- **Status:** ✅ Accepted
- **Scope:** the **exported Go surface** of the module — the types, functions,
  and options an application actually calls, gathered per package. This spec is
  the *index* of the public surface; the **semantics** of each symbol are
  normative in its owning topic spec (linked per section). It exists so the v1
  surface is visible as a whole and so cross-cutting questions ("is `WithFS` in
  v1?") have a single answer. Anything not listed here is **not** public in v1.

The module root is `github.com/sylvanld/go-flexconf`; packages are laid out per
[overview.md](overview.md) §6.

## 1. `flexconf` — config loading

Semantics: [config-loading.md](config-loading.md), [templating.md](templating.md),
[resolvers.md](resolvers.md), [schema-and-binding.md](schema-and-binding.md).

```go
// Loader construction and use.
func New(dirs ...string) *Loader            // ordered layers, lowest→highest; none PANICS
func (l *Loader) Load(name string, dst any) error

// Loader options (variadic on a future WithOptions surface / New; see note).
type Option

func WithResolver(r Resolver) Option         // add/override a scheme for this Loader (resolvers §2)
func WithResolvers(rs ...Resolver) Option    // REPLACE the default resolver set; empty = static Loader (resolvers §2, §2.1)
func WithEnv(env func(string) (string, bool)) Option // env source for env: (resolvers §3) — IN v1
func WithFS(fsys fs.FS) Option               // file source for file:/config: (resolvers §4) — IN v1
func WithSecretPolicy(p SecretPolicy) Option // agent vs in-process (resolvers §5.3)

// Secret resolution policy.
type SecretPolicy int
const (
    PolicyAgent     SecretPolicy = iota // default: proxy through the background agent
    PolicyInProcess                     // unlock the real driver in-process, no agent
)

// Custom-scheme resolvers.
type Resolver interface {
    Scheme() string
    Resolve(ctx context.Context, path string) (string, error)
}
func RegisterResolver(r Resolver)            // process-wide; duplicate scheme PANICS (resolvers §2)

// Agent entry point for library consumers (resolvers §5.4).
func RunAgentIfRequested() error             // no-op unless the self-exec marker is present
```

- **`WithFS`/`WithEnv` are part of v1.** They are the injection surface tests use
  to avoid touching the real filesystem/environment; this resolves the
  "injectable binder dependencies" item once parked for this spec
  ([schema-and-binding.md](schema-and-binding.md) §11).
- The sentinel errors this package returns are listed in
  [errors.md](errors.md) §2.1.

!!! note "Option delivery"

    Whether options are passed to `New(dirs, opts...)` or via a `With…` chain on
    `*Loader` is an implementation detail settled at code time; both forms bind
    the same `Option` values above. The *set* of options is what this spec fixes.

## 2. Variant registry (re-exported from `internal/variant`)

Semantics: [variants.md](variants.md). Re-exported into `flexconf` via a Go
generic type alias (Go ≥ 1.24, [variants.md](variants.md) §9.1) so apps use them
as `flexconf.*`:

```go
type VariantType any
type Registry[V VariantType] = variant.Registry[V]   // generic type alias

func NewRegistry[V VariantType](opts ...RegistryOption) *Registry[V]
func (r *Registry[V]) RegisterVariant(name string, factory func() V)

func Resolve[V VariantType](r *Registry[V], sel ...Selector) (V, error)
func Select(key, value string) Selector
func WithDiscriminator(key string) RegistryOption     // default "type"

// Process-wide convenience registry (the Loader uses it by default).
func RegisterVariant[V VariantType](name string, factory func() V)
func Get[V VariantType](sel ...Selector) (V, error)
func (l *Loader) WithRegistry(r any) *Loader          // route variants to an explicit registry
```

The same `internal/variant` package also backs the vault registry
([variants.md](variants.md) §10, [vault-registry.md](vault-registry.md) §7);
that reuse is internal and adds no public symbols here.

## 3. `flexvault` — secret backends

Semantics: [vault-drivers.md](vault-drivers.md); errors in [errors.md](errors.md)
§2.2. Public surface (summary; the owning spec is authoritative):

```go
type VaultDriver interface { /* Configure, Credentials, Unlock, Get, Set, List, Capabilities … */ }
type Manager struct { /* … */ }               // wraps one driver: Unlock/Get/Set/List
type Capabilities struct { /* … */ }
type Initializer interface { /* create a new vault (secret init) */ }

func Register(name string, factory func() VaultDriver) // driver registration (init side-effect)
func New(name string) (VaultDriver, error)             // build a registered driver by name

func MapDecoder(m map[string]any) Decoder     // non-secret VaultConf decoding
func EnvDecoder(prefix string) Decoder        // …from environment (vault-drivers §2.1)

var (
    ErrNotFound    error
    ErrLocked      error
    ErrAuth        error
    ErrUnsupported error
)
```

Concrete drivers live in `flexvault/driver/*` (e.g. `flexvault/driver/keepass`)
and register from `init()`; importing one for its side effect is all that is
needed to make it selectable by name.

## 4. `flexprompt` — credential collection

Semantics: [prompter.md](prompter.md); error in [errors.md](errors.md) §2.3.

```go
type Prompter interface { /* Dispatch(PromptRequest) … */ }
type PromptRequest struct { /* declared fields to collect */ }

func SetPrompter(p Prompter)                  // process-wide singleton
func GetPrompter() Prompter

func NewCLIPrompter(opts ...CLIOption) Prompter
func NewMapPrompter(answers map[string]string) Prompter // tests
func NewEnvPrompter(prefix string) Prompter

var ErrPromptCancelled error
```

## 5. `flexcli` — optional CLI (embedded or standalone)

Semantics: [cli.md](cli.md). Public surface is the mountable Cobra command group
and its process options:

```go
type App struct { /* … */ }
func (a *App) SecretCommand() *cobra.Command  // mount the `secret` group in an app CLI
func (a *App) RunAgentIfRequested() error     // delegates to internal/agent (cli §6.2)

type GlobalOptions struct { /* standalone cmd/flexconf wiring */ }
```

`flexcli` is not needed for programmatic use; an app that only loads config
depends on `flexconf` (which re-exports `RunAgentIfRequested`, §1) and never
imports `flexcli`.

## 6. Deferred / out of scope for v1

- **`flexconf.Decoder` structured hook** — a whole-subtree custom-decode interface
  (`interface{ DecodeFlexconf(Node) error }`) is **not** in v1. `encoding.TextUnmarshaler`
  covers scalar custom types and the variant mechanism covers "type chosen from
  config" ([schema-and-binding.md](schema-and-binding.md) §4 note). Revisit only
  on a concrete whole-subtree need; it would be **additive**, not breaking.
- **A deferred-decode `Settings` object / `Dump()`** — v1's only entry point is
  `Load(name, &dst)`; there is no intermediate resolved-tree object exposed and no
  config-dump ([errors.md](errors.md) §4). Not in v1.
- **Directory discovery / app-name-derived config dir** — v1 takes an explicit
  `dirs` list ([config-loading.md](config-loading.md) §7). Deferred, not rejected.

## 7. Resolved decisions

- **One index, semantics owned per topic.** This spec lists the surface; each
  symbol's behaviour is normative in its owning spec.
- **`WithFS`/`WithEnv` are in v1** (§1); the injectable-dependency question is
  closed.
- **`Decoder` hook and a `Settings`/`Dump()` object are not in v1** (§6).
- **Not-listed-here means not-public.** The v1 public surface is exactly §§1–5.
