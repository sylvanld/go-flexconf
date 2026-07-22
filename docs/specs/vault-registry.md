---
tags:
  - specs
  - vault
  - secrets
  - configuration
---

# Vault Registry & Vault References

- **Status:** ✅ Accepted
- **Scope:** how vaults are **named and configured** (the vault registry), how
  those definitions are **layered** across files, how a **default vault** is
  chosen, and how config values **reference** a vault in a token
  (`$(secret:[vault:]namespace/key)`). Also defines how a vault **name** maps to
  an agent identity (`VaultID`). The `VaultDriver`/`Manager` mechanics themselves
  live in [vault-drivers.md](vault-drivers.md); the CLI that drives them in
  [cli.md](cli.md).

## 1. Model

A **vault** definition is just a `driver` plus the **settings** that driver needs
to locate and open its backend — e.g. a file path, a server URL, a read-only
flag. These settings are plain configuration, not credentials: the secret used
to *unlock* the vault (a master password, a token) is never part of the
definition and is collected at unlock time, not stored (see
[vault-drivers.md](vault-drivers.md) §2.1). A driver may need no settings at
all, in which case a definition is just its `driver` name.

This spec adds one idea: vaults are **named** and defined in a **registry**, and
the rest of flexconf refers to a vault **by name**, never by inline definition.

Referencing vaults by name keeps *what* a config value pulls from a vault
decoupled from *which* backend serves it: a config value carries only a vault
name and a secret address, while the name → backend mapping lives once in the
registry. That indirection buys two things:

- **Backend-agnostic when using the default.** A config whose tokens are
  unqualified (`$(secret:artifactory/token)`) names no backend at all — it just
  targets the default vault (§4). The *same* config then runs against a personal
  KeePass file in development and a cloud secret manager in production with **no
  change to any config value**; only the registry's `default` differs. (Only in
  this default case is the config truly backend-unaware.)
- **Multiple backends from one config when wanted.** The same config MAY draw
  secrets from *several* vaults at once by qualifying tokens with a vault name —
  `$(secret:work:deploy/key)` and `$(secret:personal:github/pat)` side by side —
  each resolving through its own named vault (§5). Qualifying deliberately ties
  that value to a named vault.

The registry is an **operator/user concern, never an application concern.** An
application ships its config *schema* and default *values* and is agnostic about
whether any value is a secret; it neither defines nor bundles a vault. If an
app's own built-in default genuinely needs a secret, best practice is an
`$(env:...)` token, not a bundled vault.

## 2. Vault registry

### 2.1 Shape

The registry is a map of vault **name → `VaultConf`**, plus a **default** name:

```yaml
# ~/.config/flexconf/vaults.yaml
default: personal          # name of the default vault (see §4)
vaults:
  personal:
    driver: keepass
    path: ~/.local/share/flexconf/personal.kdbx
    readonly: false
  work:
    driver: keepass
    path: ~/work/secrets.kdbx
    readonly: true
```

A `VaultConf` is: a `driver` name (a registered `flexvault` driver, see
[vault-drivers.md](vault-drivers.md) §9) plus that driver's own settings keys
(`path`, `readonly`, `url`, …), if it has any. The extra keys are opaque to the
registry — they are handed verbatim to the driver's `Configure` `decode`
callback. **Credentials (master passwords, tokens) are never stored here** — they
are prompted at unlock ([vault-drivers.md](vault-drivers.md) §2.1).

### 2.2 Location & format

- **Well-known location:** `$XDG_CONFIG_HOME/flexconf/vaults.yaml` (i.e.
  `~/.config/flexconf/vaults.yaml`). The registry file is YAML. (Additional
  formats are deliberately out of scope for now; they can be added later.)
- This sits in flexconf's standard config directory (the same one the CLI uses,
  [cli.md](cli.md) §5.1). `vaults.yaml` is the **only** place vault definitions
  live — no other file defines vaults.

### 2.3 Normative static definitions

A `VaultConf` MUST be **static**: its values are read literally, and it MUST NOT
contain any templating tokens (`$(...)`) — neither `$(secret:...)` nor
`$(env:...)`. The registry is shared by every app and context on the machine, so
what the file says is exactly what every consumer gets. Keeping it static, with
no per-context resolution, makes a vault's location and settings unambiguous and
easy to troubleshoot.

- `$(secret:...)` could not work regardless: the registry is what *resolves*
  `secret:`, so a vault definition cannot depend on it.
- `$(env:...)` is deliberately excluded too: environment-dependent paths make a
  shared registry behave differently per shell/service and are hard to diagnose.

**Path resolution (normative).** For the common "path under the user's home" case,
a leading `~` (as `~/…`, i.e. the current user's home) **always expands** to the
user's home directory. This is done **centrally by the registry** when it resolves
a `VaultConf` — it is normative, not a per-driver option, so every driver sees an
already-expanded path and behaves identically. A **relative** `path:` is resolved
against the **directory of the `vaults.yaml` file that defined it** (not the
process working directory), so a vault's location is stable regardless of where a
command is run (§3). Beyond `~` expansion and relative-base resolution, values are
literal.

## 3. Layering

The effective registry is assembled from an **ordered list of registry files**;
a later file overrides an earlier one. The list is determined entirely by the
environment — never by application code:

- **`FLEXCONF_VAULTS` unset:** the single well-known file (§2.2) is used, if it
  exists. A missing file simply yields an empty registry.
- **`FLEXCONF_VAULTS` set:** it is the **exhaustive, ordered** list of registry
  files (OS-path-list separated, `:` on Unix), applied left to right. This
  **replaces** discovery entirely — the well-known file is used **only if it is
  named in the list**. This lets an operator deliberately *ignore* the
  well-known file even when it exists.

Override semantics (normative):

- **Whole-entry replacement, never deep-merge.** If two files both define vault
  `foo`, the later file's `VaultConf` *replaces* the earlier one entirely.
  Driver and config keys are never merged across files — this avoids a
  `VaultConf` with one file's `driver` and another file's `path`.
- The top-level `default:` (§4) follows the same rule: the last file that sets
  it wins.

This one mechanism covers the scopes people ask for — **global** (the well-known
file) and **per-project or per-deployment** (extra files via `FLEXCONF_VAULTS`).
"Per-app vs global vault" is therefore not a mode; it is which file in the list
defines the name. There is no built-in/implicit vault and no in-code vault
layer.

Because a relative `path:` resolves against **the directory of the file that
defined it** (§2.3), a per-project `FLEXCONF_VAULTS` file can use paths relative
to the project without depending on the current working directory; the winning
file for a name also fixes the base directory for that name's relative paths.

## 4. Default vault

- The registry MAY name a **default vault** via the top-level `default:` key
  (§2.1). Its value MUST be a name present in the effective `vaults:` map.
- An **unqualified** secret token — `$(secret:namespace/key)`, no vault
  segment — resolves against the default vault.
- Keeping app config unqualified is the **recommended default**: it keeps the
  config **portable**, because the config value names only the secret address
  and the operator decides which concrete vault is `default` per environment. A
  token should be qualified with an explicit vault name (§5) only when the config
  genuinely needs *more than one* vault at the same time.
- **Fail fast on the unspecified.** If a token is unqualified and **no** default
  vault is resolvable, it means nothing — the loader MUST fail loudly at load
  time with a clear error (per [README.md](README.md) "fail loud, fail early").
  It MUST NOT silently pick an arbitrary vault.

## 5. Vault references in tokens

The `secret:` scheme's `path` (see [overview.md](overview.md) §4) is extended
with an optional leading **vault** segment:

```
$(secret:[<vault>:]<namespace>/<key>)
```

Examples:

| Token | Vault | Address |
|-------|-------|---------|
| `$(secret:artifactory/token)` | *default* (§4) | `artifactory/token` |
| `$(secret:work:deploy/key)`   | `work`         | `deploy/key`        |
| `$(secret:personal:github/pat)` | `personal`   | `github/pat`        |

Parsing rules (normative):

- The resolver receives everything after `secret:`. If it contains a `:` before
  the first `/`, the segment up to that first `:` is the **vault name**; the
  remainder is the secret **address**. Otherwise there is no vault name and the
  **default vault** (§4) is used.
- The **vault name** MUST NOT contain `/` or `:`. It MUST match a name in the
  effective registry (§3) **exactly and case-sensitively** (no trimming, no
  case-folding — consistent with case-sensitive secret addresses,
  [vault-drivers.md](vault-drivers.md) §6); an unknown vault name is a load-time
  error.
- The **address** is a two-level `namespace/key` exactly as in
  [vault-drivers.md](vault-drivers.md) §6. `namespace` and `key` MUST NOT
  contain `:`. A secret resolves to a single string value; there is no
  field/sub-value addressing.
- `$(secret:...)` MUST NOT appear inside a `VaultConf` (§2.3).

The full normative token grammar (escaping, nesting, combining with literal
text) lives in [templating.md](templating.md); this section fixes only the vault-selection
part.

## 6. Vault identity & agents: `VaultID`

The background agent ([cli.md](cli.md) §6) keys an unlocked vault by `VaultID`.
With a named registry, identity is **derived from the name**, not from a hash of
inline config as an earlier draft proposed:

- `VaultID = "<name>-<fp>"`, where `fp` is the **first 8 hex characters of
  `SHA-256(canonical(resolvedVaultConf))`** and `canonical` is the resolved
  non-secret `VaultConf` serialized as YAML with keys sorted. E.g.
  `work-3f9a1c02`.
- The **name** is the human-facing handle (`flexconf --vault work secret unlock`
  targets the agent for `work`). Sharing an unlocked agent between processes is therefore
  **intentional and explicit**: two processes that reference the same resolved
  vault name share one agent on purpose.
- The **fingerprint** guards against the layering hazard in §3: if a
  higher-precedence file redefines `work` to a *different* `VaultConf`, its
  `VaultID` differs, so one context's unlocked `work` is never served to another
  context's (different) `work`. Same name + same resolved config → shared agent;
  same name + different resolved config → distinct agents.
- The fingerprint MUST be computed over the *non-secret* resolved config only and
  MUST NOT include secret material.

This **supersedes** the config-hash `VaultID` default in
[cli.md](cli.md) §6.3: the default `VaultID` is now name-derived as above.

## 7. Resolved decisions

- **Named registry** — vaults are defined once in a registry (map of
  name → `VaultConf`) and referenced by name everywhere else; the registry is an
  operator/user concern, never an application one (§1, §2).
- **Layering** — an ordered list of registry files, later overrides earlier,
  **whole-entry** replacement (never deep-merge). Sourced from the well-known
  file, or — when `FLEXCONF_VAULTS` is set — from its **exhaustive** list, which
  replaces discovery (§3). No built-in vault, no in-code vault layer.
- **Default vault** — top-level `default:` key; unqualified tokens use it;
  unqualified with no default is a hard error (§4).
- **Vault reference syntax** — `$(secret:[vault:]namespace/key)`, resolving to a
  single string value; unqualified → default vault; no field addressing (§5).
- **`VaultID`** — name + fingerprint of resolved config; supersedes the
  config-hash default (§6).
- **One prompter, not per vault** — there is no per-vault prompter or per-vault
  Manager option. The prompter is a **process-wide singleton** owned by the
  application ([prompter.md](prompter.md)); every vault that needs
  to prompt for a credential at unlock uses that same prompter. The registry
  names and configures vaults; it does not carry prompting behaviour.
- **Runs on the shared variant engine (matching only).** Name → `VaultConf`
  resolution reuses the `internal/variant` matching engine
  ([variants.md](variants.md) §10): the `driver` key is the discriminator, the map
  key is the `name` selector, and a driver's extra keys are its strict sub-schema.
  The registry's **own** rules are unchanged and stay outside that engine — file
  loading and location (§2.2), layering (§3), the static/no-tokens rule (§2.3), the
  default vault (§4), and the `VaultID` fingerprint (§6). The vault family is
  **registry-only**: instances come from operator files, not app config, and bind
  to no struct field.

This section **resolves** the "Multiple vaults" item deferred in
[vault-drivers.md](vault-drivers.md) §12 and [cli.md](cli.md) §7: a config can
now address more than one vault via the vault segment, selected per token.

## 8. Deferred

_None open._ The registry-inspection command — dump the effective registry and
show which file won each name — is specified as `secret vaults` in
[cli.md](cli.md) §4 (dump-only, with an opt-in `--validate` for CI).