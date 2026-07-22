---
tags:
  - specs
  - configuration
  - templating
---

# Configuration Loading

- **Status:** 📝 Draft
- **Scope:** where application configuration lives (**config directories** as
  layers), the **Loader** and its `Load(name, &dst)` method, how the same-named
  file is **merged** across layers, and the **load lifecycle** (read → merge →
  resolve → bind → validate). The token grammar and resolution live in
  [templating.md](templating.md) and [resolvers.md](resolvers.md); struct binding, defaults, `required`, and
  validation rules live in _schema-and-binding.md_. Secret backends are in
  [vault-drivers.md](vault-drivers.md) and [vault-registry.md](vault-registry.md).

## 1. Model: config directories as layers

An application's configuration is read from one or more **config directories**.
A `Loader` is constructed with an **ordered list** of them; each directory is a
**layer**. Within the directories, an individual configuration is selected **by
file name** (`config.yaml`, `agents.yaml`, …) and bound into a Go struct.

Two ideas are deliberately separate:

- **Which directories** — fixed when the `Loader` is built (the layer list).
- **Which file** — chosen per call to `Load(name, &dst)`.

This lets **one** `Loader` load **several kinds** of config for the same app —
each named file bound into its own struct — while the directory/layering policy
is configured once.

```go
ld := flexconf.New("/etc/myapp", "./config")   // ordered layers (§3)

var cfg    AppConfig
var agents AgentsConfig
if err := ld.Load("config.yaml", &cfg); err != nil { /* … */ }
if err := ld.Load("agents.yaml", &agents); err != nil { /* … */ }
```

**Environment variables are not a config layer.** They participate only as
`$(env:...)` tokens resolved inside config *values* (see [templating.md](templating.md) /
[resolvers.md](resolvers.md)), never as an implicit overriding source in the directory merge.
This keeps the merge input to exactly the files on disk, so what a directory
contributes is unambiguous. (The app config directories here are distinct from
the **vault registry** at `~/.config/flexconf/vaults.yaml`, [vault-registry.md](vault-registry.md);
that registry is not loaded through this Loader.)

> **Not to be confused with `EnvDecoder`.** A *vault driver's own* non-secret
> config (a KeePass path, a server URL) is a separate concern that MAY be sourced
> from environment variables via `flexvault.EnvDecoder` ([vault-drivers.md](vault-drivers.md)
> §2.1). That is vault **config** sourcing, not app config loading — the two are
> unrelated, and neither makes env a layer in *this* Loader's directory merge.

## 2. File format & name resolution

- Config files are **YAML**. The `name` passed to `Load` includes its extension
  (`.yaml`/`.yml`); any other extension MUST be a load-time error
  (`ErrUnsupportedFormat`, §6). (Additional formats are deliberately out of scope
  for now; they can be added later.)
- `name` MUST be a plain file name (a single path segment). It MUST NOT contain
  a path separator or `..`; a Loader MUST reject such names (`ErrInvalidName`, §6)
  rather than escape its configured directories.
- The **same** `name` (identical bytes, extension included) is looked up in every
  layer. flexconf does not merge across *different* names in one call.
- **Symlinks** inside a config directory are followed like any file (the OS
  default). The `name`-sanitization above is the only path guard; flexconf does
  **not** police a symlink whose target resolves *outside* the config directory —
  config directories are assumed operator-controlled.

## 3. Layering & precedence

- The Loader's directory list is ordered **lowest to highest precedence**: a
  **later** directory in the list overrides an **earlier** one. This matches the
  base-then-override mental model and the same "later wins" rule used by the vault
  registry ([vault-registry.md](vault-registry.md) §3).
- For a given `Load(name, …)`, each layer that **contains** `name` contributes;
  layers that do not are simply skipped (not an error).
- A present-but-**empty** file counts as **present** (it satisfies the
  "some layer contains `name`" requirement) and contributes an **empty tree** (no
  keys); it is not an error and merges as nothing.
- If **no** layer contains `name`, `Load` MUST fail with `ErrConfigNotFound`
  (fail loud, per [README.md](README.md) — never bind a silently empty config).

### Normative merge semantics

Merging is over the parsed value tree of the *same* file across layers:

- **Maps deep-merge by key.** A key present in a higher-precedence layer
  overrides the same key in a lower one; keys present only in a lower layer are
  retained.
- **Scalars and sequences replace, never append.** A higher-precedence layer's
  list value replaces the lower layer's list wholesale (no element-wise merge),
  mirroring the whole-entry rule in [vault-registry.md](vault-registry.md) §3 and avoiding
  order-dependent surprises.
- Merging happens **before** token resolution, so a value introduced by a
  higher-precedence layer may itself contain a token ([overview.md](overview.md)
  §3).
- **A key's shape cannot silently change across layers.** Because each layer's
  file is validated against the schema before merge (§5, step 1a), a scalar in one
  layer and a map/sequence in another for the same field cannot both be valid —
  one layer fails validation at its source and `Load` aborts naming that file. The
  merge therefore never has to arbitrate a scalar↔map↔sequence conflict. (A
  **polymorphic** field — one whose concrete shape is chosen dynamically, e.g. a
  discriminated union — is the deliberate exception: differing shapes are
  legitimate there and the higher layer replaces; see _schema-and-binding.md_.)

## 4. The Loader

```go
package flexconf

// New returns a Loader that reads config files from dirs, ordered lowest to
// highest precedence (a later dir overrides an earlier one). At least one dir
// is required; passing none is a programming error and New PANICS.
func New(dirs ...string) *Loader

// Load reads the file `name` from each configured directory, merges the layers
// by precedence (§3), resolves $(scheme:path) tokens on the merged tree, binds
// the result into dst (a non-nil pointer to a struct), and validates it. It
// fails with ErrConfigNotFound if no layer provides `name`.
func (l *Loader) Load(name string, dst any) error
```

- A `Loader` is safe for concurrent `Load` calls; it holds no per-load state.
  **Within a single `Load`**, vault unlocks (and therefore prompts) are
  **serialized** — at most one `Dispatch` runs at a time — so prompters need not
  be concurrency-safe ([prompter.md](prompter.md) §1). Two concurrent `Load`
  calls that both trigger prompts are the **caller's** concern: flexconf does not
  coordinate prompting across independent `Load` invocations.
- `Load` MUST NOT mutate `dst` on failure beyond what a partial decode may have
  written before the error — callers SHOULD treat `dst` as undefined when `Load`
  returns non-nil. (Whether binding is all-or-nothing is finalized in
  _schema-and-binding.md_.)

## 5. Load lifecycle

`Load(name, &dst)` runs a fixed sequence ([overview.md](overview.md) §3):

1. **Read** — locate `name` in each layer and parse each present copy into a
   value tree (format by extension, §2). Any `$(config:path)` **includes** in a
   file are expanded here, before merge — splicing the referenced YAML tree in
   place (no deep-merge), with cycle detection and a depth cap
   ([templating.md](templating.md) §7). Includes compose *different* files within
   one logical config and are a **distinct axis** from the directory layering of
   §3 (which merges the *same* file across dirs); the two combine — each layer's
   file is fully include-expanded first, then layers merge.
   1a. **Per-file shape validation** — validate each parsed layer's tree against
   `dst`'s schema, checking the **shape** (map / scalar / sequence) of the keys it
   contains. A file whose value shape contradicts the field's kind is a config
   error and `Load` **aborts, naming the offending file**. This is what makes a
   shape change across layers impossible to reach silently (§3). It checks only
   the *keys present* (partial files are valid); **required-field** presence is
   checked later, on the merged tree (step 5). Polymorphic fields are exempt (§3).
2. **Merge** — combine the per-layer trees by precedence into one raw tree (§3).
3. **Resolve** — expand `$(scheme:path)` tokens in string values via the
   registered resolvers (`env`, `secret`, …). `secret:` delegates to the selected
   vault ([vault-registry.md](vault-registry.md), [vault-drivers.md](vault-drivers.md)). Details:
   [templating.md](templating.md), [resolvers.md](resolvers.md).
4. **Bind** — map the resolved tree onto `dst`'s fields (struct tags, defaults).
   A resolved value binds to the **field's type** exactly as the same literal
   would: `"8080"`→`int`, `"30s"`→`time.Duration`, `"true"`→`bool`. This applies
   to values produced by token resolution too — a `$(env:PORT)` bound to an `int`
   field is parsed as an int *after* resolution. A value that does **not** fit the
   field **fails loud** with an error naming the **field, key path, and offending
   value** — **except** that a value originating from a `secret:` token is
   **redacted** in the message (the field/key path is still named). Details:
   _schema-and-binding.md_.
5. **Validate** — enforce `required` (on the merged tree), types, and any custom
   rules; report the first (or all) violations. Details: _schema-and-binding.md_.

Each step fails loud and early: an unparseable file, a per-file shape violation,
an unresolvable token, a type mismatch, or a failed validation stops the load
with a clear error.

## 6. Errors

Sentinel errors, usable with `errors.Is` (final taxonomy in _errors.md_):

```go
var (
    ErrConfigNotFound    = errors.New("flexconf: config file not found in any layer")
    ErrUnsupportedFormat = errors.New("flexconf: unsupported config file format")   // non-.yaml/.yml extension (§2)
    ErrInvalidName       = errors.New("flexconf: invalid config name")              // path separator or ".." in name (§2)
)
```

- Parse, resolve, bind, and validate failures wrap their stage's error with the
  offending file/layer and key path for diagnosis; they MUST NOT embed resolved
  secret values ([vault-drivers.md](vault-drivers.md) §8). In particular, a
  bind-time type mismatch on a value that came from a `secret:` token names the
  field and key path but **redacts the value** (§5, step 4).

## 7. Open questions / deferred

- **Directory discovery vs. explicit list.** v1 takes an explicit `dirs` list;
  whether to add well-known/XDG discovery or an env override (à la
  `FLEXCONF_VAULTS`, [vault-registry.md](vault-registry.md) §3) is deferred.
- **Optional vs. required files per call.** A `LoadOptional` variant (return
  nil / leave defaults when absent) may complement the fail-loud `Load`.
- **Additional file formats** (TOML/JSON, …) — deferred; v1 is YAML-only (§2),
  addable later without changing the load model.
- **Watch/reload** for long-lived processes — out of scope for v1 (ties to the
  eager-vs-lazy question in [overview.md](overview.md) §7).