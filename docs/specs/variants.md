# Variants & Registry

- **Status:** 📝 Draft
- **Scope:** the mechanism for **polymorphic configuration** — a config entry
  whose concrete Go type is chosen at load time by one of its own keys (a
  **discriminator**) — and the **registry** that instantiates every such entry,
  indexes it by **selectors**, and lets application code resolve it later by
  family + selectors. A variant may appear **anywhere** in a schema (a single
  field, a list element, or a map value); it is bound into its struct field like
  any other value, and — as a **convenience** — every variant instance is **also**
  collected into the family **registry** so that code elsewhere can reach it by
  *family + selectors* without holding a reference to the config struct. This spec
  owns the *variant* concept; how a variant field is declared in an app's schema
  and strictly bound is in [schema-and-binding.md](schema-and-binding.md), which
  delegates the polymorphic case here. A **worked example** is in §8. The intended
  convergence with the **vault registry** ([vault-registry.md](vault-registry.md))
  is described in §10 and is a **goal, not yet locked** (§10).

> **Status caveat.** This is a draft to agree the *model*, but all the model-level
> decisions are now settled (§11). What remains is the vault-registry convergence
> (§10), which is intentionally a separate follow-up, not an open point here.

## 1. What problem this solves

Two related needs, one mechanism:

1. **Config-time (binding).** A config block whose concrete type is chosen by one
   of its own fields — the single biggest binding feature the originating project
   needs ([missing.md](missing.md) §1.3). Each variant selects a **strict,
   different sub-schema** for its remaining keys.
2. **Runtime (resolution) — a convenience.** A variant is bound into its struct
   field like any other value. In addition, every variant instance is collected
   into a **registry**, so code anywhere in the application MAY obtain a configured
   instance by **family + selectors** — without a reference to the config struct
   that declared it. The struct field remains the primary binding target; the
   registry is a convenience index that decouples consumers from the config layout.

The same abstraction is intended to also back **vault selection** — a `Vault`
family with `keepass` / `hashicorp` variants chosen by the `driver` key in
[vault-registry.md](vault-registry.md) — so that registry stops being a special
case and becomes one consumer of this mechanism (§10).

## 2. Concepts & vocabulary

| Term | Meaning |
|------|---------|
| **Variant family** (`V`) | A Go **interface** naming a family of interchangeable implementations (e.g. `Vault`, or an app's `Store`). Encoded as the type parameter of `Registry[V]`. |
| **Variant** (kind) | A concrete type implementing `V`, identified within the family by a **discriminator value** (e.g. `keepass`, `redis`). |
| **Discriminator** | The config **key** whose value names the variant. Default `type`; configurable per family (the vault family uses `driver`). The value is a **literal**, never a token (§11). |
| **Factory** | `func() V` returning a **pre-populated** instance of a variant — its per-variant **defaults** (the instance-defaults model, [schema-and-binding.md](schema-and-binding.md)). |
| **Selectors** | Unordered `key=value` string metadata identifying a configured instance for resolution (e.g. `name=work`, `region=eu`). |
| **Variant location** | A schema position whose Go type is a family interface `V`, `[]V`, or `map[string]V` — where variant configs are expected. |
| **Registry[V]** | Holds the family's registered variants (kind → factory + sub-schema) and every instantiated **instance record**; performs config-time binding and selector-based resolution. `V` is the type parameter; the constraint `VariantType` is currently `any`. |
| **Instance record** | What the registry stores per instance: the instance (`V`), its bound config, and its selectors. |

## 3. Declaring a family and its variants

A family is a `Registry[V]`; each variant registers a discriminator name and a
factory:

```go
type Store interface { /* family API */ }

reg := flexconf.NewRegistry[Store]()                 // discriminator defaults to "type"
reg.RegisterVariant("redis",  func() Store { return &RedisStore{Timeout: 5 * time.Second} }) // per-variant defaults
reg.RegisterVariant("memory", func() Store { return &MemoryStore{} })
```

- `RegisterVariant(name, factory)` — `name` is the discriminator value; `factory`
  returns a **pre-populated** instance (its defaults). Re-registering a name is a
  programming error and **panics**.
- The concrete type the factory returns defines that variant's **sub-schema**: its
  exported, `flexconf`-tagged fields **minus** the discriminator key. Binding into
  it is **strict** — an unknown key is an error
  ([schema-and-binding.md](schema-and-binding.md)).
- The default discriminator key is `type`; `flexconf.WithDiscriminator("driver")`
  overrides it per family.

For the common case, families can be registered into a **process-wide registry**
instead of an explicit one — see §9.

## 4. Variant locations (polymorphic anywhere)

A **variant location** is recognised by its Go type in the schema: a field whose
type — or element/value type — is a registered family interface. All three
placements are supported, and appear anywhere in the tree:

**Single field** (`Cache Store`) — the field's config key contributes `name`:

```yaml
cache:                # name=cache
  type: redis
  addr: localhost:6379
```

**Map** (`Vaults map[string]Vault`) — each map key contributes `name`:

```yaml
vaults:
  work: { driver: keepass, path: ~/work.kdbx }   # name=work
  ci:   { driver: env }                          # name=ci
```

**List** (`Options []Store`) — list items have **no** implicit `name`; their
identity must come from the discriminator and/or selector-tagged fields (§6):

```yaml
options:
  - { type: redis,  region: eu }   # region is a selector field -> region=eu
  - { type: redis,  region: us }   # -> region=us
```

Each variant location is **homogeneous**: the field's interface type fixes the
family, and the discriminator selects the concrete variant *within* it. There is
no mixed-family list (an outer key selecting the family) — "polymorphic anywhere"
means any *position*, not families sharing one list.

## 5. Config-time binding (eager) and registration

Variant locations are handled during the Loader's **bind** step
([config-loading.md](config-loading.md) §5 step 4), after tokens are resolved. The
Loader routes each variant location to a registry by the family interface type: by
default the **process-wide registry** (§9), or an explicit per-family registry
supplied with `WithRegistry` for isolation (e.g. tests):

```go
ld := flexconf.New("./config")                 // uses the process-wide registry
// ld := flexconf.New("./config").WithRegistry(reg)   // or an explicit registry
if err := ld.Load("config.yaml", &cfg); err != nil { /* … */ }
```

For each variant found at a variant location:

1. **Route to the registry** whose family matches the location's interface type. A
   variant location whose family has no registered variants is a load-time error.
2. **Read the discriminator** value. Absent → error (`type is required`, listing
   known variants). It MUST be a plain string **literal** — never a token (§11).
3. **Look up the variant.** Unknown → error naming the value **and listing known
   variants** (`unknown driver "vaultx" (known: keepass, hashicorp)`), per the
   strict-validation contract.
4. **Instantiate** via the factory (defaults populated).
5. **Strictly bind** the entry's remaining keys onto the instance: unknown key →
   error; `required`/type enforced; a per-variant `Validate() error` hook, if
   implemented, runs after bind ([schema-and-binding.md](schema-and-binding.md)).
6. **Assign** the instance to its struct field (normal binding — the field is the
   primary target), then **derive selectors** (§6) and **register** the instance
   record as the convenience index.

**Unique identity (load-time).** Within a family, two registered instances MUST
NOT have **identical** full selector sets — such a pair could never be resolved
unambiguously (§7), so it is a load-time error (`ErrDuplicateVariant`) naming both
config locations.

Binding and registration are **all-or-nothing** with the rest of `Load`: any
failure aborts the whole load and the registry is left **unpopulated** — the
application never observes a half-filled registry ([config-loading.md](config-loading.md)
§4).

## 6. Selectors

Selectors are `key=value` string metadata used to find an instance. An instance's
selectors are derived, at registration, from three sources:

- **Discriminator — always.** `type=redis` / `driver=keepass`, so callers can
  resolve "any store of type redis."
- **`name` — from the nearest naming key.** The map key (map location) or the
  field key (single-field location). **List** items have no `name`.
- **Selector fields — declared by struct tag.** A variant field tagged
  `selector` contributes its bound value as a selector:

  ```go
  type RedisStore struct {
      Region string `flexconf:"region,selector"`   // -> selector region=<value>
      Addr   string `flexconf:"addr"`
  }
  ```

**Matching (subset).** A resolution request carries zero or more selectors; an
instance **matches** if it satisfies **all** requested `key=value` pairs. It is the
config author's/app's responsibility to define selectors that identify each
instance uniquely enough to resolve it (§5 rejects fully-identical sets at load;
partially-overlapping sets that a given request cannot disambiguate fail at
resolution, §7).

## 7. Runtime resolution

Application code retrieves an instance by family and selectors. Resolution is
always at **family granularity** — you ask for a member of the family `V`, not for
some narrower sub-interface:

```go
s, err := flexconf.Resolve[Store](reg, flexconf.Select("name", "cache"))
```

Resolution matches configured instances only — **there is no lazy creation**:

- **Exactly one match** → return it.
- **No match** → **failure** (`ErrVariantNotFound`).
- **More than one match** → **failure** (`ErrAmbiguousVariant`, listing the
  matching instances' selectors).

Resolution is read-only and pure: the registry is fully populated at load time
(§5) and never mutated by `Resolve`.

```
Startup                                Runtime
  Load config                          Resolve[V](reg, selectors…)
     │                                        │
  bind variant locations                 query registry
     │                                  ┌──── matches ────┐
  register(instance                     │        │        │
    + config + selectors)              one      none     many
     │                                   │        │        │
  registry fully populated            return   error    error
  (every configured variant                 (NotFound) (Ambiguous)
   now resolvable by selector)
```

## 8. Worked example

A concrete end-to-end use: an app that sends notifications through interchangeable
backends. The family is `Notifier`; the variants are `email` and `slack`.

**1 — Family interface and concrete variants.** Each variant declares its own
sub-schema via `flexconf` tags; a `team` field is marked as a selector.

```go
type Notifier interface {
    Notify(ctx context.Context, msg string) error
}

type EmailNotifier struct {
    From    string        `flexconf:"from"`
    Team    string        `flexconf:"team,selector"`
    Timeout time.Duration `flexconf:"timeout"`
}
func (e *EmailNotifier) Notify(ctx context.Context, msg string) error { /* … */ }

type SlackNotifier struct {
    Webhook string `flexconf:"webhook"`
    Team    string `flexconf:"team,selector"`
    Channel string `flexconf:"channel"`
}
func (s *SlackNotifier) Notify(ctx context.Context, msg string) error { /* … */ }
```

**2 — Register the variants** (here into the process-wide registry, §9; factories
carry per-variant defaults):

```go
func init() {
    flexconf.RegisterVariant[Notifier]("email", func() Notifier {
        return &EmailNotifier{Timeout: 10 * time.Second}
    })
    flexconf.RegisterVariant[Notifier]("slack", func() Notifier {
        return &SlackNotifier{Channel: "#alerts"}
    })
}
```

**3 — App schema places `Notifier` in all three positions** (single field, map,
list):

```go
type AppConfig struct {
    Default Notifier            `flexconf:"default_notifier"` // single field
    Named   map[string]Notifier `flexconf:"notifiers"`        // map
    Fanout  []Notifier          `flexconf:"fanout"`           // list
}
```

**4 — Config file:**

```yaml
default_notifier:                    # name=default_notifier ; type=slack ; team=platform
  type: slack
  webhook: https://hooks.example/deploy
  team: platform

notifiers:
  oncall:                            # name=oncall ; type=email ; team=platform
    type: email
    from: alerts@example.com
    team: platform
  billing:                           # name=billing ; type=email ; team=finance
    type: email
    from: billing@example.com
    team: finance

fanout:                              # list items: NO name
  - type: slack                      # type=slack ; team=platform
    webhook: https://hooks.example/noc
    team: platform
  - type: email                      # type=email ; team=noc
    from: noc@example.com
    team: noc
```

**5 — Load.** Binding populates the struct fields *and* registers all five
instances (with the selectors shown in the comments):

```go
var cfg AppConfig
if err := flexconf.New("./config").Load("config.yaml", &cfg); err != nil {
    log.Fatal(err)
}
```

**6 — Access.** Two equivalent ways to reach an instance:

```go
// (a) via the struct field — the primary target, always populated:
cfg.Default.Notify(ctx, "deploy started")
cfg.Named["billing"].Notify(ctx, "invoice ready")

// (b) via the registry — for code that has no reference to cfg:
n, err := flexconf.Get[Notifier](flexconf.Select("name", "oncall"))
// -> the oncall EmailNotifier

// selectors combine (subset match); this identifies exactly one:
n, err = flexconf.Get[Notifier](
    flexconf.Select("type", "email"),
    flexconf.Select("team", "finance"),
) // -> billing
```

**7 — The exactly-one rule in action:**

```go
// team=platform matches THREE instances (default_notifier, oncall, fanout[0]):
_, err := flexconf.Get[Notifier](flexconf.Select("team", "platform"))
// -> ErrAmbiguousVariant (lists the three matches)

// no instance has team=marketing:
_, err = flexconf.Get[Notifier](flexconf.Select("team", "marketing"))
// -> ErrVariantNotFound
```

The two list items need no `name`: `type=slack,team=platform` and
`type=email,team=noc` are already distinct selector sets, so neither collides
(§5's load-time uniqueness check) and each is uniquely resolvable.

## 9. The API (sketch — not final)

```go
package flexconf

// VariantType constrains a family's interface. It is `any` for now.
type VariantType any

type Registry[V VariantType] struct { /* … */ }

func NewRegistry[V VariantType](opts ...RegistryOption) *Registry[V]
func (r *Registry[V]) RegisterVariant(name string, factory func() V)

// Resolve returns the single configured instance of family V matching selectors,
// or an error if none or more than one matches (§7).
func Resolve[V VariantType](r *Registry[V], sel ...Selector) (V, error)

func Select(key, value string) Selector
func WithDiscriminator(key string) RegistryOption   // default "type"

// --- process-wide registry (convenience) ---
// RegisterVariant registers a variant of family V into the process-wide registry;
// Get resolves from it. The Loader uses the process-wide registry by default.
func RegisterVariant[V VariantType](name string, factory func() V)
func Get[V VariantType](sel ...Selector) (V, error)

// On the Loader: route variant locations to an explicit registry instead of the
// process-wide one (for isolation / tests).
func (l *Loader) WithRegistry(r any) *Loader
```

`Resolve`/`Get` are free functions (not methods) so the family type `V` is explicit
at the call site.

### 9.1 Package placement

The mechanism lives in a **module-internal** package (e.g. `internal/variant`) so a
single implementation can be shared without an import cycle:

- **`flexconf`** imports it and **re-exports** the public surface (`Registry`,
  `NewRegistry`, `RegisterVariant`, `Resolve`, `Get`, `Select`, `WithDiscriminator`)
  so application developers use it as `flexconf.*`.
- **`flexvault`** imports the same internal package directly for the vault-family
  case (§10), without importing `flexconf` — preserving the layering rule
  ([overview.md](overview.md) §6).

Re-exporting the **generic** `Registry[V]` type across the package boundary uses a
**generic type alias** — `type Registry[V any] = variant.Registry[V]` — which
requires **Go ≥ 1.24** (generic aliases). The module targets a current Go
(`go.mod`: `go 1.26`), so this is the chosen approach; generic functions
(`NewRegistry`, `Resolve`, `Get`) are thin re-export wrappers.

### 9.2 The process-wide registry

The process-wide registry is a single set of per-family registries keyed by the
family's Go type. It exists purely for convenience — apps that want isolation use
explicit `*Registry[V]` values instead. Notes:

- It is safe for concurrent **reads** (`Get`); registration (`RegisterVariant`)
  is expected during `init`/startup, before resolution.
- Its families default to the `type` discriminator; a family needing a different
  discriminator (e.g. the vault family's `driver`) uses an explicit registry.
- Because it is process-global, tests SHOULD prefer an explicit registry via
  `WithRegistry` to avoid cross-test leakage.

## 10. Intended convergence with the vault registry (goal, not yet locked)

The vault registry ([vault-registry.md](vault-registry.md)) is already shaped like
a variant family and SHOULD become one:

| Vault registry today | Variant mechanism |
|----------------------|-------------------|
| `vaults:` section | a `map[string]Vault` variant location (§4) |
| map key (`work`) | `name` selector |
| `driver:` key | the discriminator (`WithDiscriminator("driver")`) |
| driver's extra keys → `Configure` decode | the variant's strict sub-schema (§5) |
| `default:` name | the `name` selector used for unqualified `$(secret:…)` |
| `$(secret:[vault:]ns/key)` | `Resolve[Vault](reg, Select("name", vaultOrDefault))` |

Placement (§9.1) is chosen so this is *possible*; actually rewiring the vault
registry onto it is a **follow-up** to confirm before either spec changes.

## 11. Locked decisions

- **No lazy creation.** Resolution matches configured instances only; no match or
  multiple matches is a failure (§7).
- **Polymorphic anywhere; field populated + registry as convenience.** Variants may
  be a single field, a list element, or a map value; the instance is bound into its
  struct field **and also** indexed in the family registry for selector-based access
  (§1, §4). The struct field is the primary target; the registry is a convenience.
- **Discriminator is literal-only.** The discriminator value MUST be a plain string
  literal; it is never a token (so the concrete variant is statically known before
  scalar resolution). Sub-schema *values* may still be tokens, resolved before bind.
- **Resolution is family-granularity.** `Resolve`/`Get` ask for a member of the
  family `V`; there is no request for a narrower sub-interface.
- **Process-wide registry offered** alongside explicit registries; the Loader uses
  it by default, `WithRegistry` overrides for isolation (§9, §9.2).
- **Package placement** — module-internal, shared by `flexconf` (re-exported) and
  `flexvault` (§9.1).
- **Selectors declared by struct tag** (`flexconf:"…,selector"`), plus the implicit
  `name` and discriminator (§6). `VariantType` stays `any`.
- **Instance defaults** — factories return pre-populated instances → per-variant
  defaults ([schema-and-binding.md](schema-and-binding.md)).
- **All-or-nothing** — the registry is populated only on a fully successful `Load`
  (§5).
- **Strict validation** — unknown discriminator value **and** unknown sub-schema key
  are both hard errors; a per-variant `Validate()` hook runs after bind.
- **Generic re-export via type alias** — `flexconf` re-exports the internal
  `Registry[V]` with a Go generic type alias; the module targets Go ≥ 1.24
  (`go.mod`: `go 1.26`) (§9.1).
