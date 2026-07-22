# Templating

- **Status:** 📝 Draft
- **Scope:** the token **grammar** (`$(scheme:path)`), where tokens may appear,
  how they combine with literal text, escaping, the (deliberate) **absence** of
  nesting, the **node-tree** resolution model and the observable typing it
  produces, and the structural **`$(config:path)`** include/splice token. What a
  token's `path` *means* per scheme (the value it produces) is owned by
  [resolvers.md](resolvers.md); vault-selection parsing inside a `secret:` path is
  owned by [vault-registry.md](vault-registry.md) §5. This spec fixes syntax and
  the substitution mechanics; resolvers fix semantics.

## 1. Token grammar

A **token** is the only templating construct. Its form:

```
token  = "$(" scheme ":" path ")"
scheme = letter { letter | digit }              ; lowercase ASCII, e.g. env, secret, file, config
path   = { any character except ")" and ":" }   ; opaque to the engine, interpreted per scheme
letter = "a".."z"
```

- `scheme` is lowercase ASCII, starting with a letter (`env`, `secret`, `file`,
  `config`, or a custom registered scheme, [resolvers.md](resolvers.md) §2). An
  unknown scheme is a load-time error (`ErrUnknownScheme`).
- A token **ends at the first `)`**. A `path` therefore **cannot contain `)`**;
  this is a documented limitation (secret addresses and env names never need it,
  and a file path that does should be referenced another way). There is no
  balanced-paren matching.
- Everything between `$(` and the closing `)` after the first `:` is the `path`,
  passed verbatim to the scheme (the engine does not trim, split, or interpret
  it — except for the whole-value/typing rules below and `config:` in §7).

## 2. Node-tree model (normative)

Templating operates on the **parsed YAML node tree**, never on raw file bytes.
This is normative because it dictates observable behavior:

- **Values only, never keys.** The engine walks scalar **values**; mapping
  **keys** are never scanned for tokens. `$(env:X): y` keeps the literal key
  `$(env:X)`.
- **Scalar-content injection only.** A resolved scalar token can only ever become
  the **content of a scalar**. It can **never** introduce a mapping, a sequence,
  an alias, a tag, or a new document — so a resolved value (e.g. a secret whose
  text happens to be `{a: 1}` or `- x`) is injected as a plain string and cannot
  alter the document's structure. (The sole structural construct is `$(config:…)`,
  §7, which is an *include* resolved as a distinct step, not a scalar
  substitution.)
- **Resolved values are inert.** After a token resolves, its value is **not
  re-scanned**: a `$(...)` sequence *inside* a resolved secret/env/file value is
  literal, never a second token. There is consequently **no nesting** — a token's
  `path` is not itself templated (see §5).
- **Untagged results.** A substituted scalar is left **untagged** (YAML `Tag=""`),
  so normal type inference applies at bind time (§6).

## 3. Placement & combining with literal text

For the scalar schemes (`env`, `secret`, `file`, custom):

- A token MAY stand as an entire scalar value:
  `token: $(secret:artifactory/token)`.
- A token MAY be embedded in literal text and combined with other tokens within
  one scalar: `url: https://$(env:HOST):$(env:PORT)/api`.
- Multiple tokens in one scalar are each resolved and spliced into place; the
  surrounding literal text is preserved exactly.

`$(config:…)` is the exception: it MUST be a **whole value** and MUST NOT be
embedded in text (§7).

## 4. Escaping

The two-character opener `$(` is the only trigger. To emit a **literal** `$(` in
a value, write `$$(` — the engine replaces `$$(` with the literal `$(` and does
**not** treat it as a token:

```yaml
price: "$$(not-a-token)"     # value is literally: $(not-a-token)
```

- A lone `$` **not** followed by `(` is always literal and needs no escaping
  (`cost: $5` is just `$5`).
- The escape is scoped to the opener: only the sequence `$$(` is special. `$$`
  elsewhere is left untouched.

## 5. No nesting (deliberate)

flexconf does **not** support nested or recursive tokens:

- A token's `path` is **literal** — it is never scanned for inner tokens, so
  `$(secret:$(env:NS)/key)` is **not** "resolve env first, then secret." The
  `secret:` resolver receives the path `$(env:NS)/key` verbatim (and will reject
  it as a malformed address).
- Resolved values are inert (§2), so composition cannot happen after resolution
  either.

The rationale is the same as the node-tree choice: a statically knowable,
non-recursive substitution is safe (no injection, no resolution-order surprises)
and easy to reason about. Composition that genuinely needs a computed path is an
application concern, done in Go after `Load`.

## 6. Typing of resolved scalars

Because a substituted scalar is left untagged (§2), its Go type is inferred from
the **resolved content** and how it sits in the scalar, exactly as a literal
would be — token resolution does not wrap values in a string type:

- **Whole-value token, no surrounding text** → typed by content. If `PORT=8080`,
  `port: $(env:PORT)` binds to an `int` field as `8080`; `flag: $(env:FLAG)` with
  `FLAG=true` binds to a `bool`. (This mirrors [config-loading.md](config-loading.md)
  §5 step 4: `"8080"`→`int`, `"true"`→`bool`, `"30s"`→`time.Duration`.)
- **Token combined with literal text, or multiple tokens in one scalar** → the
  result is always a **string** (`https://host:8080/api` is not a number).

A value that does not fit its target field fails loud at bind time, naming the
field and key path — with the offending value **redacted** when it originated
from a `secret:` token ([config-loading.md](config-loading.md) §5, §6).

## 7. The `$(config:path)` include token

`$(config:path)` **splices** the referenced YAML file's parsed tree into the
document in place of the token. It composes *different files within one logical
config*, a distinct axis from the Loader's *directory layering* (which merges the
*same* file across dirs, [config-loading.md](config-loading.md) §3).

```yaml
agents: $(config:agents.yaml)     # value becomes the whole tree parsed from agents.yaml
toolsets:
  - $(config:base.yaml)           # may also stand as a sequence item
```

Rules (normative):

- **Whole value only.** The token MUST be the **entire** scalar (a map value or a
  sequence item). It MUST NOT be embedded in literal text or combined with other
  tokens; a `config:` token mixed with text is a load-time error.
- **Splice, not merge.** The referenced tree **replaces** the node verbatim — map,
  sequence, or scalar. There is **no deep-merge** with sibling keys (a deliberate
  non-feature; use directory layering for merging).
- **Literal path.** `path` is a **literal** file path, never itself templated
  (consistent with §5), so the full include graph is statically knowable before
  any scalar token resolves.
- **Extension.** `path` MUST end in `.yaml` or `.yml`; any other extension is a
  load-time error.
- **Relative to the containing file.** A relative `path` resolves against the
  **directory of the file that contains the token**, not the process working
  directory (same base rule as `$(file:…)`, [resolvers.md](resolvers.md) §4).
- **Containment.** The resolved path MUST satisfy `fs.ValidPath` semantics and MUST
  NOT escape the config directory (a `..` traversal out of the tree is the error
  `path escapes the config directory`).
- **Cycle detection.** An include cycle is a load-time error reporting the **full
  chain** (e.g. `a.yaml → b.yaml → a.yaml`).
- **Depth cap.** Includes nest up to `maxIncludeDepth = 16`; exceeding it is a
  load-time error.

### 7.1 When includes expand (lifecycle placement)

Include expansion is a **structural, pre-resolution** step:

1. During **read** ([config-loading.md](config-loading.md) §5 step 1), each layer's
   file is parsed and its `config:` includes are expanded recursively into that
   layer's full node tree (depth cap + cycle detection apply across the whole
   chain). Included subtrees are then indistinguishable from inline content.
2. **Layer merge** (step 2) then operates on the fully-expanded per-layer trees.
3. **Scalar token resolution** (`env`/`secret`/`file`/custom, step 3) runs last,
   on the merged tree — so tokens *inside an included file* resolve normally, and
   a value introduced by a higher-precedence layer may still contain scalar
   tokens.

Thus `config:` is resolved *before* the scalar schemes and before merge; the
scalar schemes are resolved *after* merge. `config:` never sees a scalar token's
output and vice versa (no interaction, no ordering ambiguity).

## 8. Per-scheme grammar summary

Syntax here; semantics in [resolvers.md](resolvers.md).

| Scheme | Path grammar | Placement | Notes |
|--------|--------------|-----------|-------|
| `env`    | `NAME`                         | value, embeddable | Missing var is a **hard error** — there is **no** `:-default` syntax (§10). |
| `secret` | `[vault:]namespace/key`        | value, embeddable | Vault-selection parsing in [vault-registry.md](vault-registry.md) §5; value is tainted for redaction. |
| `file`   | `/abs/or/relative/path`        | value, embeddable | Verbatim contents; relative to the containing file. |
| `config` | `relative-or-child/path.yaml`  | **whole value only** | Structural splice; §7. |

## 9. Errors

Grammar/expansion sentinels (final taxonomy in _errors.md_; scheme-semantic
errors live in [resolvers.md](resolvers.md) §7):

```go
var (
	ErrBadToken       = errors.New("flexconf: malformed template token")        // e.g. no ":", unterminated "$(" 
	ErrIncludeEmbedded = errors.New("flexconf: $(config:…) must be a whole value") // §7
	ErrIncludeExtension = errors.New("flexconf: include path must end in .yaml/.yml") // §7
	ErrIncludeEscape   = errors.New("flexconf: include path escapes the config directory") // §7
	ErrIncludeCycle    = errors.New("flexconf: include cycle detected")          // §7 (message names the chain)
	ErrIncludeTooDeep  = errors.New("flexconf: include depth exceeds maxIncludeDepth")  // §7
)
```

Errors name the offending **file and key path**; a scalar-token error whose value
came from `secret:` redacts the value ([config-loading.md](config-loading.md) §6).

## 10. Resolved decisions

- **Grammar** — `$(scheme:path)`, lowercase scheme, path opaque and ended by the
  first `)` (no `)` in paths, no balanced-paren matching) (§1).
- **Node-tree model, normative** — values only, scalar-content-only injection,
  resolved values inert, results untagged (§2). Keys are never templated.
- **Combining** — scalar schemes embeddable and multi-per-scalar; a whole-value
  token is typed by content, a mixed scalar is a string (§3, §6).
- **Escaping** — `$$(` → literal `$(`; a lone `$` is literal (§4).
- **No nesting** — token paths are literal, never recursively templated (§5).
- **`$(config:path)` includes — YES.** Whole-value splice (no deep-merge), literal
  path, `.yaml`/`.yml`, relative to the containing file, `fs.ValidPath`
  containment, cycle detection, `maxIncludeDepth = 16` (§7). Expanded at read,
  before merge and before scalar resolution (§7.1).
- **`$(env:NAME:-default)` — NO.** A missing environment variable is always a
  **hard error** ([resolvers.md](resolvers.md) §3); there is no defaulting syntax
  in the grammar for any scheme. Config-file defaults (via the schema/binding
  layer) are the way to supply a fallback.

## 11. Open questions / deferred

- **Additional custom schemes' grammar** — custom schemes reuse the `$(scheme:path)`
  grammar unchanged; any scheme needing richer path syntax is the scheme's own
  concern, not the engine's.
- **Redaction dump format** — templating *marks* `secret:` outputs tainted; the
  `Dump()`/`«redacted»` mechanism is owned by _redaction.md_
  ([missing.md](missing.md) §1.2).
