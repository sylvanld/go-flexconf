---
tags:
  - reference
  - templating
---

# Templating & resolvers

Config **values** may contain templating tokens, resolved at load time. The
person authoring the config — not the application — decides where a value
comes from.

Normative specs: [templating.md](../specs/templating.md),
[resolvers.md](../specs/resolvers.md).

## Token grammar

```
$(scheme:path)
```

```yaml
token: $(secret:artifactory/token)          # whole value
url: https://$(env:HOST):$(env:PORT)/api    # embedded + combined
cert: $(file:cert.pem)                      # file contents, verbatim
agents: $(config:agents.yaml)               # structural include (whole value only)
```

- A token ends at the **first `)`** — a path cannot contain `)`.
- **Values only, never keys.** `$(env:X): y` keeps the literal key.
- **No nesting**: a token's path is literal; resolved values are inert (a
  `$(...)` inside a resolved value stays literal text).
- **Escaping**: `$$(` emits a literal `$(`. A lone `$` needs no escaping.
- **Typing**: a whole-value token is typed by its resolved content
  (`PORT=8080` binds to an `int`); a token mixed with literal text always
  yields a string.

## Built-in schemes

| Scheme | Behaviour |
|--------|-----------|
| `env:NAME` | Value of the environment variable. **Missing is a hard error** (`ErrEnvNotSet`) — there is no `:-default` syntax; use Go-side defaults instead. |
| `file:path` | Verbatim file contents (no newline trimming). Relative paths resolve against the directory of the containing config file. Non-secret. |
| `secret:[vault:]namespace/key` | Secret from a named (or default) vault; value is redacted in any error. See [secrets](secrets.md). |
| `config:path.yaml` | **Include**: splices the referenced YAML tree in place of the token. Whole value only; expanded at read time, before merge. |

### `$(config:)` include rules

- Whole value only — mixing with text is `ErrIncludeEmbedded`.
- Splice, not merge: the referenced tree replaces the node verbatim.
- Literal `.yaml`/`.yml` path, relative to the containing file, contained to
  the config directory (`ErrIncludeEscape` otherwise).
- Cycles are detected (`ErrIncludeCycle`, naming the chain); nesting is
  capped at 16 (`ErrIncludeTooDeep`).
- Tokens *inside* included files resolve normally afterwards.

## Custom resolvers

```go
type Resolver interface {
    Scheme() string
    Resolve(ctx context.Context, path string) (string, error)
}

flexconf.RegisterResolver(r)         // process-wide; duplicate scheme PANICS
ld := flexconf.New(dir).With(
    flexconf.WithResolver(r),        // this Loader only; shadows same scheme
    flexconf.WithResolvers(rs...),   // REPLACE the default set entirely
)
```

- `WithResolvers()` with **no arguments** makes the Loader fully **static**:
  no token processing, no includes — any `$(...)` in the input fails with
  `ErrUnknownScheme`. (This is how the vault registry loads itself.)
- Custom schemes are for non-secret composition; secret material only enters
  through `secret:`.

## Injection for tests

```go
flexconf.WithEnv(func(name string) (string, bool) { ... }) // env: source
flexconf.WithFS(fsys fs.FS)                                 // file:/config: source
```

## Errors

```go
var (
    ErrBadToken         error // malformed token (no ":", unterminated "$(")
    ErrUnknownScheme    error // no resolver registered for scheme
    ErrEnvNotSet        error // env: variable missing
    ErrFileNotFound     error // file: target missing/unreadable
    ErrIncludeEmbedded  error // config: mixed with text
    ErrIncludeExtension error // include path not .yaml/.yml
    ErrIncludeEscape    error // include escapes the config directory
    ErrIncludeCycle     error // include cycle (message names the chain)
    ErrIncludeTooDeep   error // nesting beyond 16
)
```

Errors name the offending file and key path; values that came from `secret:`
are redacted.
