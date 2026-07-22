---
tags:
  - reference
  - configuration
---

# `flexconf` — loading configuration

Package `github.com/sylvanld/go-flexconf/flexconf` loads YAML configuration
from one or more **config directories** (layers) into Go structs.

Normative specs: [config-loading.md](../specs/config-loading.md),
[schema-and-binding.md](../specs/schema-and-binding.md).

## Quick start

```go
import "github.com/sylvanld/go-flexconf/flexconf"

type Config struct {
    Service string        `flexconf:"service,required"`
    Timeout time.Duration `flexconf:"timeout"`
}

ld := flexconf.New("/etc/myapp", "./config") // ordered layers, lowest → highest precedence

cfg := Config{Timeout: 30 * time.Second}     // defaults live in Go, pre-populated
if err := ld.Load("config.yaml", &cfg); err != nil {
    log.Fatal(err)
}
```

One `Loader` can load several kinds of config by name:
`ld.Load("config.yaml", &cfg)`, `ld.Load("agents.yaml", &agents)`.

## The Loader

```go
func New(dirs ...string) *Loader              // at least one dir; none PANICS
func (l *Loader) With(opts ...Option) *Loader // apply Options (returns a copy)
func (l *Loader) Load(name string, dst any) error
```

- `name` must be a plain file name with a `.yaml`/`.yml` extension. A path
  separator or `..` fails with `ErrInvalidName`; another extension with
  `ErrUnsupportedFormat`.
- If **no** layer contains `name`, `Load` fails with `ErrConfigNotFound` —
  there is deliberately no silent-absence mode.
- A present-but-empty file counts as present and contributes no keys.
- A `Loader` is safe for concurrent `Load` calls.
- Environment variables are **not** a layer — they only feed `$(env:...)`
  tokens (see [templating](templating.md)).

## Layering & merge

Directories are ordered lowest → highest precedence: a later directory
overrides an earlier one, per key.

- **Maps deep-merge by key** — keys only present in a lower layer are kept.
- **Scalars and sequences replace wholesale** — no element-wise list merge.
- Each layer's file is **shape-validated** against the schema before merging,
  so a key cannot silently change shape (scalar vs map vs sequence) across
  layers; the error names the offending file.

## Load lifecycle

`Load(name, &dst)` runs: **read** each layer (+ shape-validate) → **merge** →
**resolve** tokens → **bind** onto `dst` → **validate**. Every step fails loud
with the offending file and key path.

Binding is **all-or-nothing**: on any error, `dst` is left exactly as passed
(defaults intact); on success the fully bound and validated value is assigned.

## Errors

```go
var (
    ErrConfigNotFound    error // no layer provides the name
    ErrUnsupportedFormat error // non-.yaml/.yml extension
    ErrInvalidName       error // path separator or ".." in name
    ErrUnknownField      error // strict validation: key with no field
    ErrMissingRequired   error // required key absent from merged tree
    ErrTypeMismatch      error // value does not fit the field type
    ErrInvalidTag        error // malformed flexconf struct tag
)
```

Match with `errors.Is`. Values originating from `secret:` tokens are redacted
in error messages; the field and key path are still named.
