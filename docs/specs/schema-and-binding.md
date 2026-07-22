---
tags:
  - specs
  - configuration
  - variants
---

# Schema & Binding

- **Status:** 📝 Draft
- **Scope:** how an application **declares** the shape of its configuration (Go
  types + the `flexconf` struct tag), how **defaults** are supplied, how the
  resolved value tree is **bound** onto those types, and the **strict validation**
  applied at load. The load lifecycle that invokes all this is in
  [config-loading.md](config-loading.md) §5; token resolution feeding the bind step
  is in [templating.md](templating.md) / [resolvers.md](resolvers.md); the
  **polymorphic** case (a field whose concrete type is chosen by a discriminator)
  is delegated to [variants.md](variants.md).

Each substantive alternative to a recommended rule is captured in a `!!! note`
admonition next to the rule it would replace.

## 1. Declaring a schema

An application describes its configuration as Go **structs** with `flexconf` tags.
The tag is format-agnostic (it mirrors `yaml:"..."` semantics but is its own tag),
so the same struct can back other formats later without re-tagging
([overview.md](overview.md) §2).

```go
type Config struct {
    Service     string        `flexconf:"service,required"`
    Timeout     time.Duration `flexconf:"timeout"`
    Artifactory struct {
        URL   string `flexconf:"url"`
        Token string `flexconf:"token"`   // may resolve from a secret; the type doesn't care
    } `flexconf:"artifactory"`
}
```

Binding is a **reflection walk over the resolved value tree** (a format-neutral
tree of maps, sequences, and scalars produced by read → merge → resolve), driven by
the `flexconf` tags — flexconf does **not** delegate to a YAML decoder, so the tag
and its rules are the single source of truth regardless of on-disk format.

## 2. The `flexconf` struct tag

Grammar mirrors the standard library convention — a name, then comma-separated
options:

```
`flexconf:"name,opt1,opt2,..."`
```

**Name rules**

- `flexconf:"service"` — bind the config key `service` to this field.
- `flexconf:"-"` — **skip** this field entirely (never bound, never validated).
- `flexconf:",required"` (empty name) or **no tag at all** on an exported field —
  the key defaults to the **lowercased field name** (`Timeout` → `timeout`), matching
  `yaml.v3`. This default is a **convenience only** and deliberately not pushed
  further: there is no case-conversion (e.g. snake_case) and no loader-level renaming
  policy. A field that wants a different key just names it explicitly
  (`flexconf:"http_port"`).
- Unexported fields are always ignored.

**Options**

| Option | Meaning |
|--------|---------|
| `required` | The key MUST be present in the merged config tree (§5). |
| `selector` | Expose this field's bound value as a variant **selector** ([variants.md](variants.md) §6). Only meaningful on a variant's own struct. |
| `inline` | Promote an embedded/nested struct's (or map's) fields into the parent level, instead of nesting under a key. |

**Key matching is case-sensitive and exact** against the resolved name — no
case-insensitive fallback (it is error-prone).

**Embedding.** An anonymous (embedded) struct field is treated as `inline` by
default — its fields are promoted into the parent, rather than nested under a key of
their own:

```go
type Common struct {
    Region string `flexconf:"region"`
    Env    string `flexconf:"env"`
}

type Service struct {
    Common                       // embedded, no tag → region & env are top-level keys
    Name string `flexconf:"name"`
}
```

```yaml
name: api          # Service.Name
region: eu         # Service.Common.Region (promoted)
env: prod          # Service.Common.Env    (promoted)
```

To **nest** the embedded type under a key instead, give it an explicit name tag —
then it behaves like an ordinary nested field:

```go
type Service struct {
    Common `flexconf:"common"`   // named → keys live under `common:`
    Name   string `flexconf:"name"`
}
```

```yaml
name: api
common:
  region: eu
  env: prod
```

## 3. Defaults — a pre-populated instance

Defaults are supplied by **pre-populating the destination struct** before `Load`.
The file overrides only the keys it actually contains (presence-driven merge,
[config-loading.md](config-loading.md) §3), so a field the config omits keeps its
pre-set value.

```go
cfg := Config{Timeout: 30 * time.Second}          // default lives in Go, type-checked
if err := ld.Load("config.yaml", &cfg); err != nil { /* … */ }
// absent `timeout:` → stays 30s ; present `timeout: 5s` → overrides
```

- Defaults are **type-safe** (real Go values, no string parsing) and support
  structured values (slices, maps, nested structs, computed values) that a tag
  string could not express.
- A default and `required` are **contradictory**: `required` is about *config
  presence* (§5), which a pre-set default never satisfies. Do not mark a defaulted
  field `required`; if both appear, `required` wins (the config must still provide
  the key and the default is effectively dead).
- Zero-value ambiguity does **not** arise: because the merge is key-presence-driven,
  `port: 0` in the file overrides to `0`, while an absent `port` keeps the default —
  no non-zero sentinel is consulted.

!!! note

    **Alternative: struct-tag defaults (`default=30s`).** A declarative
    `flexconf:"timeout,default=30s"` is self-documenting on the field and familiar
    from libraries like `envconfig`. It was **not** chosen for v1 because the value
    is a string parsed at load (runtime errors, drift, scalar-only) and adds a
    parser + error class. It could be added later as **non-breaking sugar** for
    scalar leaves, layered *under* the instance defaults.

## 4. Binding model

For each struct field the binder locates the matching key in the resolved tree and
assigns its value. **Supported target types:**

- **Scalars** — `string`, `bool`, all sized `int`/`uint`, `float32/64`.
- **`time.Duration`** (from a string like `"30s"`) and **`time.Time`** (RFC 3339).
- **Slices and maps** of any supported element/value type.
- **Nested structs** (recursively) and **pointers** (allocated on demand when the
  key is present).
- Any type implementing **`encoding.TextUnmarshaler`** — bound from a scalar string.
  This is the recommended extension point for custom scalar types (IP addresses,
  enums, URLs, …), reusing a standard-library interface.

!!! note

    **Alternative: a bespoke `flexconf.Decoder` hook.** For *structured* custom
    decoding (a type that wants the whole sub-tree, not just a scalar), a dedicated
    `interface{ DecodeFlexconf(Node) error }` could be offered. It is deferred: the
    **variant** mechanism ([variants.md](variants.md)) already covers the main
    "concrete type chosen from config" need, and `encoding.TextUnmarshaler` covers
    scalar custom types. Revisit in _api.md_ if a real case needs whole-subtree
    custom decode.

**Scalar coercion.** A resolved scalar binds to the field's type exactly as the
same literal would: `"8080"` → `int`, `"true"` → `bool`, `"30s"` →
`time.Duration`. This applies equally to values produced by token resolution
(`$(env:PORT)` into an `int` field is parsed as an int *after* resolution) —
consistent with [config-loading.md](config-loading.md) §5 step 4 and
[templating.md](templating.md) §6.

**Redaction.** A value that does not fit its field **fails loud**, naming the
**field, key path, and offending value** — **except** that a value originating from
a `secret:` token has its value **redacted** (the field/key path is still named)
([config-loading.md](config-loading.md) §6, [vault-drivers.md](vault-drivers.md) §8).

## 5. Strict validation

Validation is **strict by default** and applied uniformly.

**Unknown keys are errors.** Any key in the resolved tree with no matching struct
field is a load-time error (`ErrUnknownField`), at **every** struct level (top,
nested, embedded, and variant sub-schemas). This strictness is **always on** — there
is deliberately **no** lenient opt-out, now or later. This is the mechanism behind
the "a config that loads is guaranteed to instantiate" principle — a typo is never
silently dropped. A schema that genuinely needs to accept **arbitrary** keys models
that explicitly with a `map[string]…` field (which binds every key), rather than
relaxing strictness globally.

**Required.** A field tagged `required` MUST have its key present in the **merged**
tree (checked after merge, on the whole config — not per layer). Absence is
`ErrMissingRequired`, naming the field and key path. Presence is what is checked,
not value non-emptiness — an explicit `service: ""` satisfies `required`.

!!! note

    **Alternative considered: non-zero requirement.** `required` could instead demand
    a non-zero *final* value (after defaults and merge). It was rejected as
    surprising — it conflates "operator must supply this" with "must not be the zero
    value," and makes `required` interact with defaults in confusing ways. Presence
    on the merged tree is the crisp rule.

**Types** are enforced by the coercion in §4; a mismatch is `ErrTypeMismatch`.

**Custom validation — the `Validate()` hook.** Any bound value whose type
implements `interface{ Validate() error }` has `Validate` called **after** it is
fully bound. Hooks run **bottom-up** (innermost values first), on every nested and
variant value that implements the interface — so a parent can assume its children
are already validated. This is where an app enforces
cross-field and cross-reference rules (unknown references, cycles, one-per-kind
invariants) the generic engine cannot know.

```go
func (c *Config) Validate() error {
    if c.Timeout <= 0 { return fmt.Errorf("timeout must be positive") }
    return nil
}
```

!!! note

    **Alternative considered: top-level-only validation.** `Validate()` could be
    called only on the root `dst`. The recursive/bottom-up form was chosen instead so
    nested and variant types own their invariants locally (a `RedisStore` validates
    itself), which composes better than funnelling everything through the root.

## 6. Polymorphic fields → variants

A field whose Go type is a **registered variant family interface** (`V`, `[]V`, or
`map[string]V`) is a **variant location**: its concrete type is chosen by a
discriminator key, bound against that variant's strict sub-schema, and the instance
is also indexed in the family registry. The full model — discriminator, selectors,
registration, resolution — is in [variants.md](variants.md). Variant sub-schema
binding obeys **this** spec (tags, strictness, `Validate()`, coercion).

## 7. All-or-nothing binding

`Load` never leaves `dst` half-written on failure
([config-loading.md](config-loading.md) §4):

1. A **temp** value is created and **seeded from a copy of the caller's `dst`** so
   the pre-populated defaults (§3) are preserved.
2. Bind + validate run against the temp.
3. On **full success**, the temp is assigned to `*dst`. On **any error**, `*dst` is
   left exactly as the caller passed it, and any variant registry populated during
   the attempt is discarded ([variants.md](variants.md) §5).

## 8. Lifecycle placement

Within `Load` ([config-loading.md](config-loading.md) §5):

- **Step 1a — per-file shape validation:** each layer's parsed tree is checked
  against `dst`'s schema for **shape** (map/scalar/sequence) of the keys it
  contains, so a shape change across layers is impossible to reach silently. This
  spec defines the schema that check consults; it validates only *keys present*
  (partial files are fine).
- **Step 4 — bind:** §4 here, on the merged + resolved tree.
- **Step 5 — validate:** §5 here (required on the merged tree, types, `Validate()`).

## 9. Errors

Sentinel errors, usable with `errors.Is` (final taxonomy in _errors.md_):

```go
var (
    ErrUnknownField    = errors.New("flexconf: unknown config key")          // §5 strict
    ErrMissingRequired = errors.New("flexconf: required config key missing") // §5 required
    ErrTypeMismatch    = errors.New("flexconf: value does not fit field type")// §4 coercion
    ErrInvalidTag      = errors.New("flexconf: invalid flexconf struct tag")  // §2 (programming error)
)
```

- Bind/validate errors wrap the offending **field, key path, and layer/file**; a
  value from a `secret:` token is **redacted** (§4).
- A `Validate()` hook's returned error is wrapped with the field/key path of the
  value it was called on.

## 10. Resolved decisions

- **Schema = Go structs + `flexconf` tag**, format-agnostic; binding is a
  reflection walk over the resolved tree, not a YAML decode (§1).
- **Tag grammar** mirrors `yaml`: `name`, `-` skip, empty/absent → lowercased field
  name; options `required`, `selector`, `inline`; case-sensitive exact match;
  embedded structs inline by default (§2).
- **Defaults = pre-populated instance** (§3); tag `default=` deferred as possible
  sugar.
- **Type coverage** includes scalars, `time.Duration`/`time.Time`, slices, maps,
  nested structs, pointers, and `encoding.TextUnmarshaler` (§4).
- **Always strict** — unknown keys error at every level; no lenient opt-out ever;
  arbitrary keys are modelled with a `map[string]…` field (§5).
- **`required` = presence on the merged tree** (§5).
- **`Validate()` hook** called recursively, bottom-up (§5).
- **All-or-nothing** via a temp seeded from `dst` (§7).
- **Polymorphic** delegated to [variants.md](variants.md) (§6).

## 11. Open questions / deferred

- **`flexconf.Decoder` structured hook** (§4 note) — defer to _api.md_ pending a
  concrete need.
- **Injectable binder dependencies** (`WithFS`/`WithEnv`, [missing.md](missing.md)
  §2.7) — testability surface, owned by _api.md_.