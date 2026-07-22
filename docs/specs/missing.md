# Missing / Candidate Features

- **Status:** 🗒️ Notes (non-normative)
- **Purpose:** A gap analysis of the current spec set against two real
  codebases: the prior/alternate implementation at `~/tools/flexconf` (the most
  complete existing attempt) and `~/sylvan/ai-tools/cds-ai-agent` (the project
  whose needs originated flexconf). Each item says *what* it is, *where* it was
  seen, *why* it matters, and *which spec* should own it. Nothing here is
  normative yet — this is a to-triage list, not a decision.

Legend for "seen in": **[T]** = `~/tools/flexconf`, **[C]** = `cds-ai-agent`.

---

## 1. High-impact gaps

These are features that exist (and are load-bearing) in the source projects but
have no home in the current spec. They change the SDK's surface, so decide on
them before the TODO specs (_templating.md_, _schema-and-binding.md_,
_resolvers.md_) are written.

### 1.1 A `config:` include/splice token

`~/tools/flexconf` supports a third token namespace the current grammar
([overview.md](overview.md) §4) does not mention:

```yaml
agents: $(config:agents.yaml)          # splices another YAML file as this value
list:
  - $(config:base.yaml)                # can also stand as a sequence item
```

- `$(config:path)` must be a **whole value** (cannot be embedded in text), must
  end `.yaml`/`.yml`, and **splices** the referenced tree in place — it does
  **not** deep-merge (a deliberate non-feature there).
- Path is relative to the *file containing the reference* and is a **literal**
  (never itself templated), so the include graph is statically knowable.
- Guardrails: `fs.ValidPath` escape check ("path escapes the config directory"),
  cycle detection reporting the full chain (`a → b → a`), depth cap
  (`maxIncludeDepth = 16`).

**Why it matters:** cds-ai-agent [C] keeps everything in one big file today and
would clearly benefit from splitting `agents:` / `toolsets:` into includes. This
is a different composition axis than the Loader's *directory layering*
([config-loading.md](config-loading.md) §3): layering merges the *same* file
across dirs; `config:` composes *different* files within one logical config.
**Decide whether flexconf wants both.** → _templating.md_ + a note in
[config-loading.md](config-loading.md).

### 1.2 Secret redaction / taint tracking / config dump

The current spec only says errors "MUST NOT embed resolved secret values"
([config-loading.md](config-loading.md) §6, [vault-drivers.md](vault-drivers.md)
§8) — a prohibition with no mechanism. `~/tools/flexconf` has a real one:

- Every scalar sourced from `$(secret:…)` is added to a **taint set by node
  identity** (`NodeSet map[*yaml.Node]struct{}`) — structural, not a
  per-field allowlist you can forget to extend.
- `Settings.Dump()` deep-copies the resolved tree and replaces every tainted
  scalar with `«redacted»`, producing a safe-to-share config dump for
  `--print-config`, debug logs, and error context.

**Why it matters:** "fail loud with the offending key path" (a stated principle)
is unsafe without redaction — the diagnostic that names the key must not print
the secret. This is arguably a **must-have** to honor the existing §8 rule.
→ new _redaction.md_ or a section in _templating.md_ / _errors.md_.

### 1.3 Discriminated / polymorphic config binding

Both projects need "a config block whose concrete type is chosen by one of its
own fields," and the current binding model has no general answer:

- [T] ships `PolymorphicSettings[I any]` — `NewPolymorphicSettings[I](discriminator)`,
  `.Register(name, factory)`, `.SetDefault(name)`, `.Decode(Settings) (I, error)`
  — a reusable typed discriminator with per-variant defaults and strict
  unknown-field rejection, listing known variants on error.
- [C] needs exactly this in three places: `vault.type` (`file`/`env`),
  `apis[].kind` (jira/cds/gerrit/…), `toolsets[].kind` — each selecting a
  **strict per-kind sub-schema** for the remaining keys.

The current spec only solves the *vault* case (driver name → `VaultConf`, opaque
extra keys handed to `Configure`'s `decode`). cds-ai-agent shows the pattern is
general and app-facing, not vault-specific.

**Why it matters:** this is the single biggest schema-binding feature the
originating project [C] needs and the current spec does not offer.
→ _schema-and-binding.md_ (as a first-class binding mechanism, generalizing the
vault driver-selection pattern).

### 1.4 A strict load-time validation contract

cds-ai-agent's central UX principle: **"a config that loads without error is
guaranteed to instantiate."** It achieves this with:

- `KnownFields(true)` / `DisallowUnknownFields()` **everywhere** — typos are
  never silent (matches [T]'s `strictDecode`, but [T] only applies it to the
  `secrets` block and driver options, not the app schema).
- **Eager cross-reference validation**: unknown tool/kind/toolset names,
  inheritance cycles, "granted tool with no candidate," one-per-kind invariants
  — all checked at load, not at call time.
- Per-section `Validate()` returning **actionable** messages that name what is
  allowed (`type is required ("file" or "env")`).

The current spec has a "validate" lifecycle step ([config-loading.md](config-loading.md)
§5.5) but defers all of it to the unwritten _schema-and-binding.md_. **Decide how
much of this the SDK owns** (generic: unknown-key rejection, `required`, type
checks; cross-reference validation may be app-provided hooks). → _schema-and-binding.md_.

### 1.5 More secret resolver backends

The spec has one concrete driver (KeePass) and vaguely "cloud managers later."
The source projects show a broader, mostly *cheaper* set that real deployments
actually use:

| Driver | Seen | What it does | Why it matters |
|--------|------|--------------|----------------|
| **env-vault** | [C] | Maps `namespace/key` → env var by a deterministic rule (`bitbucket/token` → `CDS_SECRET_BITBUCKET_TOKEN`). Unlock is a no-op. | The **CI story**: same config, file vault in dev ↔ env-var vault in prod. Distinct from the `$(env:…)` *token* — this is a whole backend behind `secret:`. |
| **exec** | [T] | Shells out (`command: [pass, show]`), secret name as final arg, reads stdout; non-zero exit → `ErrNotFound`. | One driver bridges to `pass`/`sops`/`vault`/anything. Huge reach for ~30 lines. |
| **encrypted-file (JSON)** | [C] | Argon2id KEK → wrapped master key → XChaCha20-Poly1305 over a JSON blob; upgradeable KDF params stored in-file; atomic `0600`. | A dependency-free alternative to KeePass; simpler wire format; the originating project chose *this*, not KeePass. |
| **none** | [T] | Every `$(secret:…)` is a hard not-found. | Lets a secret-free config declare intent explicitly. |

**Why it matters:** cds-ai-agent [C] runs on the **env-vault** backend in CI —
that path must exist for flexconf to serve its originating use case. → new
`flexvault/driver/*` entries + a note in [vault-drivers.md](vault-drivers.md) §10
and _resolvers.md_.

### 1.6 `$(env:NAME:-default)` fallback syntax

[T]'s grammar supports an env-only default: `$(env:PORT:-8080)`. The current
token summary ([overview.md](overview.md) §4) has no defaulting; a missing env
var would presumably be a hard error. **Decide** whether `:-` (env-only, default
taken verbatim, no defaulting for `secret:`/`config:`) is in scope. Small, common,
worth an explicit yes/no. → _templating.md_.

---

## 2. Medium-impact / design-shaping

### 2.1 Template on the parsed node tree, not raw text

[T] substitutes into the **`*yaml.Node` tree**, never the raw bytes. Consequences
worth making normative in _templating.md_:

- An injected value can only ever become a **scalar's content** — it can never
  introduce a mapping, sequence, tag, alias, or new document (injection-safe).
- Substituted scalars are left **untagged** (`Tag=""`), so `$(env:PORT)` decodes
  to `int`, `"true"` to `bool` — native Go types, no wrapper types.
- Resolved values are **not re-scanned** (a `$(...)` *inside* a secret is inert).
- **Keys are never templated** — only values are walked.

This is an implementation strategy, but it dictates observable behavior; capture
it as a decision. → _templating.md_.

### 2.2 Defaults as a pre-populated struct instance

The current schema example uses a struct-tag default (`default=30s`,
[overview.md](overview.md) §2). [T] takes a different route: you pass a
**pre-populated instance of your own config struct**; the file overrides only the
keys it names (per-key merge for free, and defaults are type-checked, can't
drift from the schema). Both models are viable; _schema-and-binding.md_ should
pick one (or support both). Note [T] also exposes `Defaults(v any)` and applies
the default tree *under* the loaded tree at decode time.

### 2.3 A deferred-decode `Settings` object + `Dump()`

[T] splits loading into `Load(cfg, out, …)` (full pipeline + decode) and
`LoadFile(cfg, …) (*Settings, …)` (stops before decode). The `Settings` holds the
resolved tree + taint set and offers `Decode(out)`, `Dump()`, `Marshal/Unmarshal`.
Useful for `--print-config`, manual/partial decode, and nested capture. The
current API is `Load(name, &dst)` only ([config-loading.md](config-loading.md)
§4). Consider exposing the intermediate object. → _api.md_ (ties to 1.2).

### 2.4 App-settings-directory model

[T] resolves **one directory** for the whole app and derives everything from it:

- `settings.New(appName, …)` → dir precedence `WithPath` → `<APP>_CONFIG` env →
  `os.UserConfigDir()/<app>` (`~/.config/<app>/`).
- `<APP>_CONFIG` names a **directory, not a file**; `config.yaml` and
  `secrets.kdbx` therefore **relocate together** — the deliberate "single
  resolution point."

The current spec instead takes an explicit `dirs …` list into `New`
([config-loading.md](config-loading.md) §4) and **defers** XDG discovery
([config-loading.md](config-loading.md) §7). The app-name-derived directory is a
nicer default UX and is what both the config file and (in the standalone CLI) the
vault registry already gesture at. **Reconcile** the "explicit dirs" model with
the "one app dir" model. → [config-loading.md](config-loading.md) §7.

### 2.5 A config-file generator CLI: `settings init` / `path`

[T] ships a second Cobra group (`cli/settings`): `init [--force]` renders the
app's declared defaults to `config.yaml` (refuses to clobber; writes `0600`;
round-trips back through `Load`) and `path` prints the location. The current CLI
spec covers only the `secret` group and lists config-loading commands as out of
scope ([cli.md](cli.md) §12). This is a low-cost, high-value onboarding command;
consider promoting it. → [cli.md](cli.md).

### 2.6 Agent: absolute max-lifetime + memory hardening

The current agent spec has an **idle** timeout only ([cli.md](cli.md) §6.5). [T]
adds:

- `MaxLifetime` (default 30m) — an absolute cap independent of activity, so a
  busy agent still relocks eventually.
- `Harden()` after unlock: `RLIMIT_CORE=0`, `PR_SET_DUMPABLE=0`,
  `Mlockall(MCL_CURRENT|MCL_FUTURE)` (best-effort, Linux, build-tagged) — called
  *after* unlock so the memory-hard KDF's transient allocation isn't forced under
  mlock.

The spec currently lists mlock as "future hardening, noted, not required"
([cli.md](cli.md) §8). [T] shows it's cheap and already done. Consider promoting
`MaxLifetime` to the spec and `Harden()` from "future" to "SHOULD." → [cli.md](cli.md)
§6.5, §8.

### 2.7 Testability injection surface

[T] makes every external dependency injectable so tests never touch the real
FS/env/store/terminal: `WithFS(fs.FS)`, `WithEnv(Env)`, `WithSecretResolver`,
`WithSecretStore`, and a `PromptPassword` hook on the KeePass driver. The current
spec has `WithPrompter` / `NewMapPrompter` ([prompter.md](prompter.md)) but no
`WithFS`/`WithEnv` on the Loader. Worth adding for the same testability the spec
already values. → _api.md_ / [config-loading.md](config-loading.md).

---

## 3. Decision points where source projects diverged from the spec

Not gaps so much as places the current spec made a call the implementations
didn't — flagged so the divergence is intentional, not accidental.

### 3.1 In-config `secrets:` block vs. external vault registry

[T] configures the secret backend with a `secrets:` block **inside the app's own
config**, templated **env-only first** and then **stripped** before the app
decodes (bootstrap ordering: secrets can't reference secrets). The current spec
deliberately moves all vault definitions to an external registry at
`~/.config/flexconf/vaults.yaml` and **forbids** any inline definition
([vaults.md](vaults.md) §2). [vault-drivers.md](vault-drivers.md) §2.1 does gesture
at an "integrated mode," but [vaults.md](vaults.md) then bans it. **Confirm** the
registry-only stance — [T]'s in-config block is the more ergonomic single-file
experience the originating project [C] actually uses. This is the sharpest
design disagreement between spec and implementations.

### 3.2 Env-var overrides of arbitrary config keys

cds-ai-agent [C] explicitly *lacks* generic env-var overrides and its explorer
flagged it as "a gap the SDK could fill." The current spec firmly rejects env as
a config layer ([config-loading.md](config-loading.md) §1). The `$(env:…)` token
covers *author-chosen* overrides, but not koanf-style `APP_FOO_BAR` blanket
overrides. **Confirm** this remains a deliberate no (the token model is the
answer) rather than an oversight.

### 3.3 Secret ≠ final credential

cds-ai-agent [C] resolves a secret string by key, then does **token exchange**
on top (CDS signin-key → cached per-endpoint JWT, refreshed from `exp`). The spec
correctly keeps `Get` returning a single string ([vault-drivers.md](vault-drivers.md)
§7) and defers caching/rotation (§11). Just a reminder that the deferred
caching/rotation model must not assume "secret == the credential the app uses" —
exchange/refresh legitimately lives above the SDK.

---

## 4. What the spec already has that the implementations do not

For balance — the current spec is ahead of both codebases here, so these are
*not* gaps:

- **Named vault registry with a `vault:` token qualifier** and multiple vaults
  addressable from one config ([vaults.md](vaults.md) §5) — neither project does
  multi-vault.
- **`VaultID` = name + fingerprint of resolved config** ([vaults.md](vaults.md)
  §6) — [T] keys the agent by app only.
- **Directory layering** of the *same* file across ordered dirs
  ([config-loading.md](config-loading.md) §3) — [T] only has includes + per-key
  default merge; [C] has neither.
- **Prompter as a process-wide singleton** with a declare/consume credential
  protocol ([prompter.md](prompter.md)) — richer than [T]'s single
  `PromptPassword` hook.

---

## 5. Suggested triage order

1. **1.2 redaction** and **1.4 validation contract** — they make existing stated
   principles ("fail loud with key paths," "no secrets in errors") actually
   safe/true. Nearly must-have.
2. **1.3 polymorphic binding** and **1.5 env-vault + exec drivers** — the two
   features the originating project [C] concretely needs.
3. **1.1 `config:` includes** and **3.1 in-config secrets block** — resolve the
   composition/registry story together; they interact.
4. Everything in §2 — quality-of-life, decide alongside the relevant TODO spec.
