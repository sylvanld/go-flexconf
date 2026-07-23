---
icon: lucide/braces
tags:
  - reference
  - configuration
---

# Schema & binding

How an application declares the shape of its configuration and how loaded
values bind onto Go types.

Normative spec: [schema-and-binding.md](../specs/schema-and-binding.md).

## The `flexconf` struct tag

```go
type Config struct {
    Service string        `flexconf:"service,required"`
    Timeout time.Duration `flexconf:"timeout"`
    Ignored string        `flexconf:"-"`
    Port    int           // untagged → key "port" (lowercased field name)
}
```

Grammar: `flexconf:"name,opt1,opt2"` — a name, then comma-separated options.

| Name form | Meaning |
|-----------|---------|
| `service` | Bind config key `service`. |
| `-` | Skip the field entirely (never bound, never validated). |
| empty / no tag | Key defaults to the lowercased field name. |

| Option | Meaning |
|--------|---------|
| `required` | Key must be present in the **merged** tree (presence, not non-emptiness — `service: ""` satisfies it). |
| `selector` | Expose the field's value as a variant selector (see [variants](variants.md)). |
| `inline` | Promote a nested struct's fields into the parent level. |

Key matching is **case-sensitive and exact**. Unexported fields are ignored.

### Embedding

An anonymous (embedded) struct is inlined by default — its fields become
top-level keys. Give it an explicit name tag to nest it under a key instead:

```go
type Service struct {
    Common                       // region:, env: at top level
    Name string `flexconf:"name"`
}
type Service2 struct {
    Common `flexconf:"common"`   // keys live under common:
}
```

## Defaults — pre-populate the instance

Supply defaults by pre-populating the destination before `Load`; the file
overrides only the keys it actually contains:

```go
cfg := Config{Timeout: 30 * time.Second}
_ = ld.Load("config.yaml", &cfg)   // absent timeout: → stays 30s
```

Because the merge is presence-driven, `port: 0` in a file explicitly
overrides to `0`, while an absent `port` keeps the default — no zero-value
ambiguity. Don't combine a default with `required` (required wins).

## Supported field types

- Scalars: `string`, `bool`, all sized `int`/`uint`, `float32/64`.
- `time.Duration` (`"30s"`) and `time.Time` (RFC 3339).
- Slices and maps (string keys) of any supported element type.
- Nested structs, pointers (allocated on demand).
- Any type implementing `encoding.TextUnmarshaler` — the extension point for
  custom scalar types (IPs, enums, levels…).
- `any` / `map[string]any` — binds arbitrary subtrees generically (this is
  also how a schema accepts deliberately arbitrary keys).

**Coercion:** a resolved scalar binds as the same literal would — `"8080"` →
`int`, `"true"` → `bool`, `"30s"` → `time.Duration` — including values that
came from token resolution. A value that does not fit fails with
`ErrTypeMismatch` naming the field, key path, and value (redacted if it came
from a secret).

## Strict validation

- **Unknown keys are errors** (`ErrUnknownField`) at every level — a typo is
  never silently dropped. There is no lenient opt-out.
- **`required`** is checked on the merged tree (`ErrMissingRequired`).
- **`Validate() error` hook:** any bound value implementing it is called
  after it is fully bound, **bottom-up** (children before parents) — the
  place for cross-field rules:

```go
func (c *Config) Validate() error {
    if c.Timeout <= 0 { return fmt.Errorf("timeout must be positive") }
    return nil
}
```

## All-or-nothing

`Load` binds into a temp seeded from your (default-populated) destination and
assigns it back **only on full success** — on error the destination is
untouched.
