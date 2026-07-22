---
tags:
  - reference
  - variants
---

# Variants & registry

Polymorphic configuration: a config entry whose concrete Go type is chosen at
load time by a **discriminator** key (default `type`), bound against that
variant's strict sub-schema, and indexed in a **registry** for selector-based
lookup.

Normative spec: [variants.md](../specs/variants.md).

## Declaring a family

```go
type Notifier interface {
    Notify(ctx context.Context, msg string) error
}

type EmailNotifier struct {
    From string `flexconf:"from"`
    Team string `flexconf:"team,selector"`   // exposed as selector team=<value>
}
func (e *EmailNotifier) Notify(...) error { ... }

// Process-wide (the Loader uses this by default):
flexconf.RegisterVariant[Notifier]("email", func() Notifier {
    return &EmailNotifier{ /* per-variant defaults */ }
})

// Or an explicit registry (isolation, tests, custom discriminator):
reg := flexconf.NewRegistry[Notifier](flexconf.WithDiscriminator("kind"))
reg.RegisterVariant("email", ...)
ld := flexconf.New(dir).WithRegistry(reg)
```

Factories return **pre-populated** instances — that is where per-variant
defaults live. Registering a name twice panics.

## Variant locations

Any schema position whose type is a family interface is a variant location —
single field, map value, or list element:

```go
type AppConfig struct {
    Default Notifier            `flexconf:"default_notifier"` // name=default_notifier
    Named   map[string]Notifier `flexconf:"notifiers"`        // name=<map key>
    Fanout  []Notifier          `flexconf:"fanout"`           // list items: no name
}
```

```yaml
default_notifier:
  type: slack                 # discriminator — a literal, never a token
  webhook: https://hooks.example/deploy
  team: platform              # selector-tagged field
```

Rules enforced at load:

- Missing discriminator → error listing the known variants.
- Unknown discriminator value → error naming it and listing known variants.
- The discriminator must be a **plain string literal** (a token is rejected);
  other sub-schema values may be tokens.
- Sub-schema binding is **strict**: unknown keys error; `required`, coercion
  and the `Validate()` hook apply as usual.
- Two instances with identical full selector sets fail the load
  (`ErrDuplicateVariant`, naming both locations).
- Registration is **all-or-nothing** with the Load: a failed load leaves the
  registry as it was.

## Selectors & resolution

An instance's selectors are: the discriminator (`type=email`), the nearest
naming key (`name=<field or map key>`; list items have none), and every
`selector`-tagged field.

```go
n, err := flexconf.Get[Notifier](flexconf.Select("name", "oncall"))        // process-wide
n, err := flexconf.Resolve[Notifier](reg, flexconf.Select("team", "noc"))  // explicit registry
```

Matching is subset-based and **exactly-one-or-fail**:

| Matches | Result |
|---------|--------|
| exactly one | the instance |
| none | `ErrVariantNotFound` |
| more than one | `ErrVariantAmbiguous` (listing the matches) |

There is no lazy creation — resolution only ever returns instances the
loaded config actually configured. The struct field remains the primary
binding target; the registry is a convenience index.
