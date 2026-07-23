---
tags:
  - specs
  - templating
  - secrets
---

# Resolvers

- **Status:** ✅ Accepted
- **Scope:** the `Resolver` abstraction, how the Loader invokes resolvers on the
  merged tree, the default resolvers (`env`, `secret`, `file`), how custom schemes
  register, and — the core of this spec — how the **`secret:` scheme reaches a
  vault through the background agent**, spawning and unlocking one the same way
  the CLI does when none is running. The token *grammar* (delimiters, escaping,
  nesting, where a token may appear) lives in [templating.md](templating.md); this spec covers
  what a resolver *does* with a token's `path`. Vault selection syntax
  (`[vault:]namespace/key`) is defined in [vault-registry.md](vault-registry.md) §5; the
  `Manager`/`VaultDriver` mechanics in [vault-drivers.md](vault-drivers.md).

## 1. Model

A **resolver** turns one token's `path` into a concrete string, for a given
**scheme**. The Loader walks the merged value tree (step 3 of the lifecycle,
[config-loading.md](config-loading.md) §5), and for each `$(scheme:path)` token
it dispatches to the resolver registered for `scheme`. A scheme with no
registered resolver is a load-time error (`ErrUnknownScheme`, §7).

```go
package flexconf

// Resolver turns a token's path into a value for one scheme. Implementations
// are invoked on the merged tree during Load (config-loading.md §5, step 3) and
// MAY be called many times per Load. A Resolver MUST NOT mutate shared state
// during Resolve; the Loader may call resolvers for independent tokens in any
// order (resolution order across tokens is unspecified — a resolved value is
// never re-scanned, templating.md).
type Resolver interface {
	// Scheme is the token scheme this resolver handles ("env", "secret", ...).
	Scheme() string

	// Resolve maps the path (the text after "scheme:") to a value. The returned
	// string is spliced into the scalar as-is; type conversion happens later at
	// bind time (config-loading.md §5, step 4). Errors are wrapped by the Loader
	// with the offending key path; a resolver MUST NOT include a resolved secret
	// value in its error (redaction, config-loading.md §6).
	Resolve(ctx context.Context, path string) (string, error)
}
```

- A resolved value is **inert**: it is never re-scanned for tokens (a `$(...)`
  inside a secret is literal). This is fixed in [templating.md](templating.md) and restated here
  because it is a resolver-visible guarantee.
- Resolvers are the **only** thing that turns a token into a value; binding,
  merging, and validation never see the token, only its resolved string.

## 2. Registration & the resolver set

The **default resolvers** — `env`, `secret`, `file` — are the resolver set `New`
installs on every `Loader`. Throughout the specs these three are always the
"default resolvers"; "resolver" unqualified means the abstraction of §1 (a default
resolver, a custom-scheme resolver, or a Loader-scoped one), never specifically
the built-in trio. Three extension points, mirroring the vault-driver pattern
([vault-drivers.md](vault-drivers.md) §9):

```go
package flexconf

// RegisterResolver registers a custom-scheme resolver process-wide, so importing
// a package for its side effect makes a scheme available by name. Registering a
// scheme that is already registered (including a default resolver:
// "env"/"secret"/"file") PANICS — schemes are a global namespace and silent
// shadowing is a footgun.
func RegisterResolver(r Resolver)

// WithResolver overrides or adds a resolver for THIS Loader only (tests, or an
// app that wants a bespoke secret/env source without touching the global set).
// A Loader-scoped resolver shadows a global one of the same scheme. It adds to /
// overrides whatever set is already in effect (the default set, or a set fixed by
// WithResolvers below).
func WithResolver(r Resolver) Option

// WithResolvers REPLACES this Loader's default resolver set with exactly the
// resolvers given, instead of the env/secret/file default. It is how a Loader is
// made progressively — or completely — static:
//
//   WithResolvers()               // EMPTY set: no token processing at all (§2.1)
//   WithResolvers(myEnv, myFile)   // exactly these; no secret: resolver present
//
// WithResolver still composes on top of the resulting set. Use this to build a
// Loader that must NOT carry a given scheme — most importantly the internal
// registry loader, which cannot have a secret: resolver (§2.1).
func WithResolvers(rs ...Resolver) Option
```

- The default `env`/`secret`/`file` resolvers are installed by `New`; their
  behaviour is tuned by Loader options (`WithEnv`, `WithSecretPolicy`, §3–§5)
  rather than by re-registering the scheme. `WithResolvers` is the only way to
  install a *different* (including empty) set in their place.
- Custom schemes are for **non-secret** composition (`$(file:...)`, a hypothetical
  `$(http:...)`); a custom scheme MUST NOT be used to smuggle secrets past the
  redaction/taint machinery — secret material only ever enters through `secret:`.

### 2.1 The empty resolver set — a *static* Loader

`WithResolvers()` with no arguments installs an **empty** set. Such a Loader does
**no token processing whatever**: it reads, merges, binds, and validates literal
YAML, and any `$(...)` occurrence in the input — a scalar scheme token *or* a
`$(config:...)` include ([templating.md](templating.md) §7) — is a load-time error
(`ErrUnknownScheme`, §7), because there is no handler registered for it. Include
expansion is part of the default processing the empty set removes, so a static
Loader neither resolves nor splices; the input is taken exactly as written.

This is the mechanism the **vault registry** uses to load itself through the
ordinary Loader pipeline ([vault-registry.md](vault-registry.md) §3,
[config-loading.md](config-loading.md) §1). Two independent reasons force an empty
set there:

- **A `secret:` resolver would be circular.** The `secret:` resolver resolves
  against the registry (§5.1, step 1). If the registry itself were loaded by a
  Loader carrying `secret:`, loading the registry would depend on the registry.
  The registry loader therefore MUST NOT carry a `secret:` resolver.
- **The registry is normatively static** — it MUST contain no `$(...)` tokens at
  all, not even `$(env:...)` ([vault-registry.md](vault-registry.md) §2.3). An
  empty set enforces that directly: any token fails loud rather than resolving.

So the registry is *not* a special-cased loader; it is exactly
`(the shared pipeline) + WithResolvers()` over the environment-derived layer files
([vault-registry.md](vault-registry.md) §3). `~`-expansion and relative-path
resolution ([vault-registry.md](vault-registry.md) §2.3) are **not** resolver work
— they are a post-bind normalization the registry applies to the bound
`VaultConf`, unaffected by the empty resolver set.

> **Note — replacement, not subtraction (v1).** `WithResolvers` replaces the set
> wholesale; there is no `WithoutResolver("secret")` that keeps the other defaults
> and drops one. The two shapes v1 supports are the full default set (from `New`,
> optionally extended with `WithResolver`) and an explicit replacement set (most
> commonly empty). A surgical "all defaults except secret" is niche and left to a
> caller-assembled explicit set; a first-class subtractive option is deferred.

## 3. `env:` — environment variables

```
$(env:NAME)
```

- Resolves to the value of environment variable `NAME`.
- **Missing variable is a hard error** (`ErrEnvNotSet`, §7) — fail loud, per
  [specs index](index.md). flexconf does not silently substitute empty string.
- The source is the process environment by default; `WithEnv(env func(string) (string, bool))`
  overrides it for a Loader (testability, mirrors the injection surface noted in
  [missing.md](missing.md) §2.7). Built-in default reads `os.LookupEnv`.
- `NAME` is opaque to flexconf; no interpolation or nesting inside it.

> **Decided — no `$(env:NAME:-default)`.** An env-only default (missing var →
> literal default, proposed in [missing.md](missing.md) §1.6) is **rejected**: a
> missing env var is always a hard error (above), and no scheme has defaulting
> syntax ([templating.md](templating.md) §10). Supply fallbacks via config-file
> defaults in the schema/binding layer instead.

## 4. `file:` — file contents

```
$(file:/abs/or/relative/path)
```

- Resolves to the **verbatim contents** of the file (bytes read as a UTF-8
  string; no trailing-newline trimming — use the value exactly as stored, so a
  PEM or multi-line token round-trips). Missing/unreadable file → `ErrFileNotFound`
  / a wrapped read error (§7).
- A **relative** path resolves against the directory of the config file that
  contained the token (same base rule as `$(config:...)` includes,
  [missing.md](missing.md) §1.1), **not** the process working directory — so a
  value's meaning does not depend on where the app was launched.
- The file source is injectable via `WithFS(fsys fs.FS)` (testability).
- `file:` reads **non-secret** content. Reading a credential from disk is what a
  vault driver (e.g. an encrypted-file driver, [missing.md](missing.md) §1.5) is
  for; `file:` values are **not** tainted/redacted.

## 5. `secret:` — vaults, through the agent

This is the scheme that unifies config and secrets. Its `path` is
`[<vault>:]<namespace>/<key>` (parsing rules: [vault-registry.md](vault-registry.md) §5); it
selects a **named vault** (or the default) and returns that secret's single
string value ([vault-drivers.md](vault-drivers.md) §7). Every resolved `secret:`
value is flagged **secret-origin**, so the Loader never prints it in an error
message ([errors.md](errors.md) §1). v1 has **no** config-dump feature, so there
is nothing to redact beyond error messages — there is no separate taint set or
`«redacted»` dump format ([errors.md](errors.md) §4).

### 5.1 Resolution goes through the agent by default

To match the CLI's mental model and avoid re-prompting for a master password on
every `Load`, the `secret:` resolver reaches each referenced vault **through the
background agent** ([cli.md](cli.md) §6) — the ssh-agent-style process that holds
one unlocked vault in memory keyed by `VaultID` ([vault-registry.md](vault-registry.md) §6):

1. Parse `path` → `(vaultName?, namespace/key)`; resolve `vaultName` (or the
   default) against the effective **vault registry** ([vault-registry.md](vault-registry.md)) to a
   `VaultConf` (driver + non-secret config) and derive its `VaultID`.
2. **If an agent is running for that `VaultID`**, send `get{addr}` over its socket
   and use the value. This is the common, no-prompt path: a vault unlocked once
   (by `secret unlock`, or by a prior token in this same `Load`) serves every
   later `Get` for free.
3. **If no agent is running**, do exactly what the CLI's auto-unlock does
   ([cli.md](cli.md) §4.1, §unlock): build the vault's **real** driver from the
   registry, `Configure` it, call `driver.Credentials()`, collect answers via the
   **process-wide `flexprompt` prompter** ([prompter.md](prompter.md)) in one
   interaction, spawn a detached agent for the `VaultID`, forward `unlock{answers}`
   to it, wipe the answers, then retry the `get`. The agent then stays resident and
   idle-locks per its timeout ([cli.md](cli.md) §6.5).

The resolver therefore never holds an unlocked vault itself; the **agent** is the
single owner of unlocked material, and the single-writer/serialized-access model
([vault-drivers.md](vault-drivers.md) §10a) holds unchanged.

### 5.2 The internal agent-proxy driver

The client side of §5.1 is an **agent-proxy `VaultDriver`** provided by the
internal agent package (`internal/agent`, §5.5) rather than bespoke resolver
code. It is not a publicly registered driver — an app would never write
`driver: agent` in a registry (a vault is defined by its *real* backend, and
proxying is a resolution concern, not a vault definition). It **proxies** to a
running agent for a target `VaultID`:

- **Configure** takes the *target vault name* (and a handle to the resolved
  registry) — the vault whose secrets it proxies. It performs no backend I/O.
- **Credentials/Unlock**: to spawn+unlock a missing agent it needs the *real*
  backend's credential declaration, so it reads the target vault's real driver
  from the registry and forwards that driver's `Credentials()`; `Unlock` forwards
  the collected answers to the spawned agent (never opening the backend itself).
- **Get/Set/List** dial the agent socket and forward the request, mapping agent
  responses back to the `flexvault` sentinels ([vault-drivers.md](vault-drivers.md)
  §8).

The `secret:` resolver builds one `flexvault.Manager` per referenced vault,
wrapping the internal agent-proxy driver. Uniform Manager lifecycle, no
special-casing in the loader — the "reach the agent, spawn if absent" behaviour is
entirely inside the proxy.

### 5.3 In-process (agent-less) mode

An agent is not always wanted — a one-shot CI job, a short-lived batch, or a
non-interactive `env-vault` backend ([missing.md](missing.md) §1.5) has nothing to
gain from a resident process and may not be able to spawn one (see §5.4). A Loader
MAY select **in-process** resolution:

```go
// WithSecretPolicy chooses how the secret: resolver reaches vaults:
//   PolicyAgent    (default) — proxy through the background agent, spawning one
//                  via the process prompter if none is running (§5.1).
//   PolicyInProcess — build a flexvault.Manager around the vault's REAL driver
//                  and unlock it in-process via the process prompter; no agent is
//                  spawned and unlocked material lives only for this Loader.
func WithSecretPolicy(p SecretPolicy) Option
```

In `PolicyInProcess`, each vault is unlocked **once per process** (the Loader
caches the unlocked Manager for the life of the Loader) and locked when the Loader
is closed. This still avoids per-token re-prompting within a run, just without
cross-process sharing.

### 5.4 Spawning constraint & fallback (self-exec)

Agent spawning is **self-exec**: the process re-executes `os.Args[0]` with a
marker and runs the agent loop at the top of `main` via the internal agent
package's `RunAgentIfRequested()` ([cli.md](cli.md) §6.2). A `flexcli`-mounted app
and the `cmd/flexconf` binary already call it. A program that uses **`flexconf` as
a library only** and never wired that entry point cannot host a self-exec agent —
re-running its `main` would not run the agent loop.

Rule (normative):

- A library consumer that wants agent-backed resolution MUST call
  `flexconf.RunAgentIfRequested()` early in `main` (a public re-export of the
  internal `RunAgentIfRequested`; it is a no-op unless the self-exec marker is
  present).
- If `PolicyAgent` is selected and the process **cannot** host a spawned agent
  (entry point not wired **and** no reusable agent already running), the resolver
  MUST fail loud with a clear, actionable error (`ErrAgentUnavailable`, §7)
  telling the caller to either wire the entry point or select `PolicyInProcess`.
  It MUST NOT silently hang or silently downgrade.

### 5.5 Package layering consequence (updates prior specs)

For the `secret:` resolver (in `flexconf`) to reach and spawn an agent **without**
importing `flexcli` (forbidden by [overview.md](overview.md) §6), the **agent
runtime lives in a module-root internal package, `internal/agent`** — importable
by every package in the module (`flexconf`, `flexcli`, `cmd/flexconf`) yet kept
out of the public API surface:

- **`internal/agent`** *(new)* — the agent runtime: the socket protocol, the
  client (`Dial`), the server loop (`Serve`), self-exec spawning
  (`RunAgentIfRequested`), `VaultID` derivation, socket/PID/lock-file management,
  the idle auto-lock, **and** the internal agent-proxy `VaultDriver` of §5.2. It
  imports `flexvault` + `flexprompt`; it is **not** part of the SDK's public API.
- **`flexconf`** imports `internal/agent` for the `secret:` resolver and
  re-exports `RunAgentIfRequested` (§5.4).
- **`flexcli`** keeps only the **Cobra command group** and process-option
  plumbing; it now *drives* `internal/agent` instead of *owning* the agent.
  `flexcli`'s `App.RunAgentIfRequested()` delegates to
  `internal/agent`'s `RunAgentIfRequested()`.

Dependency direction stays acyclic and downward:
`internal/agent → flexvault → flexprompt`, with both `flexconf → internal/agent`
and `flexcli → internal/agent`. This **supersedes** the "agent lives in `flexcli`"
placement in [cli.md](cli.md) §1 and the module-layout table in
[overview.md](overview.md) §6; the CLI behaviour those describe (§4.1, §6) is
unchanged — only the code's home moves.

## 6. Ordering within a `Load`

- Resolution runs on the **merged** tree, after layering, before binding
  ([config-loading.md](config-loading.md) §5). A value introduced by a
  higher-precedence layer may itself contain a token.
- Tokens are resolved independently; a resolved value is not re-scanned
  (§1, [templating.md](templating.md)). There is therefore no defined inter-token order and no
  token-to-token data flow.
- Within one `Load`, vault unlocks (hence prompts) are **serialized** — at most
  one `Dispatch` runs at a time ([config-loading.md](config-loading.md) §4,
  [prompter.md](prompter.md) §1). The first `secret:` token targeting a given
  vault triggers the unlock; later tokens for that vault reuse it (agent or
  in-process cache), so a `Load` prompts **at most once per referenced vault**.

## 7. Errors

Sentinel errors, usable with `errors.Is` (full taxonomy in [errors.md](errors.md)):

```go
var (
	ErrUnknownScheme    = errors.New("flexconf: no resolver registered for scheme")
	ErrEnvNotSet        = errors.New("flexconf: environment variable not set")     // env: (§3)
	ErrFileNotFound     = errors.New("flexconf: file not found for file: token")   // file: (§4)
	ErrAgentUnavailable = errors.New("flexconf: no agent and process cannot host one") // secret:, PolicyAgent (§5.4)
)
```

- The Loader wraps a resolver error with the offending **key path** and, for
  `secret:`, redacts any value ([config-loading.md](config-loading.md) §6).
- `secret:` also surfaces the `flexvault` sentinels (`ErrNotFound`, `ErrLocked`,
  `ErrAuth`, …, [vault-drivers.md](vault-drivers.md) §8) and, on cancelled
  prompting, `flexprompt.ErrPromptCancelled` ([prompter.md](prompter.md) §5) —
  terminal, no retry.

## 8. Resolved decisions

- **Resolver interface** — `Scheme()` + `Resolve(ctx, path)`, invoked on the
  merged tree; resolved values are inert (§1).
- **Registration** — default resolvers per-Loader; global `RegisterResolver` for
  custom schemes; `WithResolver` for Loader-scoped overrides; `WithResolvers` to
  replace the default set (empty = a static Loader, §2, §2.1).
- **Default resolvers** — `env` (hard-fail on missing), `file` (verbatim, relative
  to the containing file, non-secret), `secret` (§3–§5).
- **`secret:` goes through the agent by default** — an internal agent-proxy
  driver; spawn+unlock like the CLI when none is running (§5.1–§5.2).
- **In-process fallback mode** — `WithSecretPolicy(PolicyInProcess)`; unlock once
  per process, no agent (§5.3).
- **Agent runtime lives in `internal/agent`**; `flexcli` becomes a thin wrapper;
  layering stays downward-only (§5.5). Supersedes the agent's placement in
  [cli.md](cli.md) §1 and [overview.md](overview.md) §6.

## 9. Settled — out of scope for v1

These were once tracked as open; they are now firm v1 decisions.

- **Secret-origin redaction, no dump format.** Every `secret:` value is flagged
  secret-origin so it never appears in an error message ([errors.md](errors.md)
  §1). Because v1 has **no** config-dump feature, there is no `Dump()`/`«redacted»`
  format and no `redaction.md` — this is resolved, not deferred
  ([errors.md](errors.md) §4).
- **Lazy / rotating secrets — no.** v1 resolves **eagerly** at `Load`
  ([overview.md](overview.md) §8). There is no lazy/on-access mode; a rotating
  secret is re-read only on a fresh `Load`.
- **Agent cache coherence (accepted limitation, out of scope).** An unlocked agent holds the
  vault's decrypted contents in memory, so a value read from it may be **stale**
  if the underlying secret changes after the agent unlocked it — e.g. an
  out-of-band rotation, another tool editing the backend file, or a `set` that
  did **not** go through this agent. v1 makes no freshness guarantee across such
  external modifications: within the single-writer model
  ([vault-drivers.md](vault-drivers.md) §10a) writes funnel through the one agent
  and it updates its own view, but reads are otherwise served from the unlocked
  snapshot with no invalidation/TTL/re-read. A cache-invalidation or
  `status`-based freshness check is deferred to the caching/rotation work
  ([vault-drivers.md](vault-drivers.md) §11); until then, a consumer needing a
  guaranteed-fresh value should `secret lock` (or restart the agent) first.