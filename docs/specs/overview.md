---
tags:
  - specs
  - configuration
  - secrets
---

# Overview

- **Status:** 📝 Draft
- **Audience:** Contributors to flexconf and developers integrating the SDK.

This spec describes the overall architecture of flexconf, the core concepts,
and the vocabulary used across all other specs. It is intentionally
high-level; details belong in the topic-specific specs linked below.

## 1. Goal

flexconf lets a Go application:

1. **Declare** the shape of its configuration once, as Go types.
2. **Load** that configuration from one or more config directories with
   sensible layering.
3. **Resolve** templating tokens in config values transparently — pulling from
   the environment or from secret backends — without the application code
   needing to know which values are secrets.

The distinguishing idea: **the config author, not the application developer,
decides what is a secret.** A developer says "I need a field `Token string`".
An operator writing the config file writes
`token: $(secret:artifactory/token)` — or `token: hardcoded-for-dev` in a
throwaway environment. The application is unchanged either way.

## 2. Core concepts

### Config schema

The application-provided description of its configuration, expressed as Go
types (structs with tags). flexconf binds loaded values into an instance of
these types. The `flexconf:"..."` struct tag mirrors the semantics of the
standard `yaml:"..."` tag (field name, then comma-separated options); it exists
as a format-agnostic abstraction so the same struct can back other config
formats later without re-tagging. Details: _schema-and-binding.md_.

```go
type Config struct {
    Service  string `flexconf:"service"`
    Timeout  time.Duration `flexconf:"timeout,default=30s"`
    Artifactory struct {
        URL   string `flexconf:"url"`
        Token string `flexconf:"token"` // may resolve from a secret, but the type doesn't care
    } `flexconf:"artifactory"`
}
```

### Config directory / layer

Configuration is loaded from one or more **config directories**. A Loader is
configured with an ordered list of them; each directory is a **layer**. When
more than one directory provides the same file, the layers are merged into a
single tree (a later directory overrides an earlier one, per-key). A single
directory is the common case; multiple directories enable base/override layering
(e.g. a shipped default dir beneath a site-local one). Details:
_config-loading.md_.

### Config file

Within the directories, an individual configuration is selected **by file name**
— `config.yaml`, `agents.yaml`, … — and bound into a Go struct. One Loader can
load several kinds of config for the same app by name (see Loader). Environment
variables are **not** a config layer; they feed `$(env:...)` tokens (see
Resolver), not the directory merge. Details: _config-loading.md_.

### Templating token

A placeholder embedded in a config **value** with the grammar
`$(scheme:path)`, e.g. `$(env:HOME)`, `$(secret:artifactory/token)`. Tokens are
resolved after layers are merged and before binding. Details: [templating.md](templating.md).

### Resolver

The component that resolves a token for a given **scheme**. `env` and `secret`
are built in; custom schemes can be registered. A resolver maps a `path` to a
concrete value. Details: [resolvers.md](resolvers.md).

### Vault driver

The pluggable backend behind the `secret:` scheme. A driver knows how to fetch
a secret by path from a specific system (HashiCorp Vault, AWS/GCP/Azure secret
managers, an encrypted file, …). The active driver is **selected at runtime**
via a driver-registry pattern, so swapping backends requires no code change in
the loading path. Details: _vault-drivers.md_.

### Loader

The top-level orchestrator, configured with the ordered config directories. Its
`Load(name, &dst)` method ties everything together for one named file: read that
file from each layer → merge by precedence → resolve tokens (via resolvers,
which may call vault drivers) → bind into `dst` → validate. The same Loader
loads several config kinds by calling `Load` with different names and structs
(`Load("config.yaml", &cfg)`, `Load("agents.yaml", &agents)`). Details:
_config-loading.md_ and _api.md_.

## 3. How the pieces fit together

```
   Load("config.yaml", &cfg)
                        ┌─────────────────────────────────────────────────┐
   config dirs   ─────▶ │                  Loader                          │
   (ordered layers)     │                                                  │
                        │  1. read <name> from each layer (directory)      │
                        │  2. merge by precedence  →  raw value tree       │
                        │  3. resolve $(scheme:path) tokens ───────────────┼──▶ Resolvers
                        │       • env:    → environment                    │        │
                        │       • secret: → Vault Driver ──────────────────┼──▶ Vault / AWS / file / …
                        │       • custom: → registered resolver            │        │
                        │  4. bind resolved tree → &cfg (the struct)       │◀───────┘
                        │  5. validate (required, types, custom rules)     │
                        └────────────────────────┬────────────────────────┘
                                                 ▼
                                     typed, validated config value
```

Resolution order matters: tokens are resolved on the *merged* tree, so a value
introduced by a higher-precedence layer can itself contain a token. Nesting and
escaping rules are defined in [templating.md](templating.md).

## 4. Token grammar summary

Normative grammar lives in [templating.md](templating.md); summarized here for
orientation.

- A token has the form `$(scheme:path)`.
- `scheme` selects a resolver; `path` is opaque to flexconf and interpreted by
  that resolver.
- Built-in schemes:
  - `env:NAME` — value of environment variable `NAME`. A missing variable is a
    hard error; there is **no** `:-default` syntax ([templating.md](templating.md) §10).
  - `secret:[vault:]namespace/key` — value fetched for that two-level secret
    address (see _vault-drivers.md_ §6). An optional leading `vault:` segment
    selects a named vault from the registry; omitting it uses the default vault
    (see _vault-registry.md_ §4–§5).
  - `file:path` — verbatim contents of a file (relative to the containing config
    file); non-secret (see [resolvers.md](resolvers.md) §4).
  - `config:path.yaml` — **structural include**: splices another YAML file's tree
    in as the whole value (no deep-merge); a different composition axis from
    directory layering ([templating.md](templating.md) §7).
- Scalar tokens (`env`/`secret`/`file`) may appear anywhere inside a string value
  and be combined with literal text: `url: https://$(env:HOST):$(env:PORT)/api`.
  `config:` must be a **whole value**. Templating operates on the parsed node
  tree (values only, never keys; resolved values are inert), and a whole-value
  token is left untagged so its type is inferred from the resolved content
  ([templating.md](templating.md) §2, §6).

## 5. Vault driver selection summary

Normative rules live in _vault-drivers.md_.

- Drivers implement a common interface and register under a name.
- Vaults are **named** in a registry (`map[name]VaultConf`) and referenced by
  name; each name maps to a driver + its non-secret config. Definitions are
  layered across files and code, with a designated **default vault**. Details:
  _vault-registry.md_.
- The `secret:` resolver delegates to whichever vault the token selects (the
  named one, else the default). Config authors never name the *backend* in
  tokens — only an optional logical *vault* name and the secret address
  (`[vault:]namespace/key`).

## 6. Module layout

The module (`go.mod` root `github.com/sylvanld/go-flexconf`) is organized around **three core packages** plus
an optional CLI package. The split follows the dependency direction —
`flexprompt` at the bottom — so there are no import cycles.

| Package | Owns | Imports |
|---------|------|---------|
| **`flexprompt`** | Credential/​input collection: the `Prompter` interface, `PromptRequest`, the process-wide singleton (`SetPrompter`/`GetPrompter`), built-in prompters (`NewCLIPrompter`, `NewMapPrompter`, `NewEnvPrompter`), and prompt errors. The module's **leaf** package. | (stdlib only) |
| **`flexvault`** | Secret backends and their lifecycle: the `VaultDriver` interface, the `Manager` (unlock/get/set/list), `Capabilities`, the sentinel errors, driver registration (`Register`/`New`), the config decoders (`MapDecoder`/`EnvDecoder`), and concrete drivers under `flexvault/driver/*` (e.g. `flexvault/driver/keepass`). | `flexprompt` |
| **`flexconf`** | Config loading: schema declaration and binding, config directories and layering, the templating engine, resolvers (`env`, `secret`, `file`), and the top-level `Loader`. The `secret:` resolver reaches vaults through the background agent (an internal proxy over `internal/agent`), driving a `flexvault.Manager` ([resolvers.md](resolvers.md) §5). Re-exports `RunAgentIfRequested`. | `flexvault`, `flexprompt`, `internal/agent` |
| **`internal/agent`** *(internal)* | The **secret agent runtime**: the socket protocol, client (`Dial`), server loop (`Serve`), self-exec spawning (`RunAgentIfRequested`), `VaultID` derivation, socket/PID/lock-file handling, idle auto-lock, and the internal agent-proxy `VaultDriver`. Holds one unlocked vault in memory (ssh-agent style). Not part of the public API; shared by `flexconf` and `flexcli` ([resolvers.md](resolvers.md) §5.5). | `flexvault`, `flexprompt` |
| **`flexcli`** *(optional)* | A mountable Cobra `secret` command group (`init`/`unlock`/`lock`/`get`/`set`/`list`) that **drives** the `internal/agent` runtime. Mounted **embedded** in an app's CLI or via the shipped **`cmd/flexconf`** binary; both read vault definitions from the vault registry ([vault-registry.md](vault-registry.md)). Not needed for programmatic use. | `flexvault`, `flexprompt`, `internal/agent`, Cobra |

Rules:

- **Dependencies point downward only:** `cmd/flexconf → flexcli → internal/agent
  → flexvault → flexprompt`, and `flexconf → internal/agent` (for the `secret:`
  resolver). `flexprompt` MUST NOT import the others; `flexvault` MUST NOT import
  `flexconf`, `flexcli`, or `internal/agent`; `internal/agent` MUST NOT import
  `flexconf` or `flexcli`; nothing imports `flexcli`. The agent runtime is a
  **module-internal** package so neither `flexconf` nor `flexcli` needs to import
  the other to share it ([resolvers.md](resolvers.md) §5.5).
- An application using only secret management (no config loading) can depend on
  `flexvault` (+ `flexprompt`) alone, without pulling in the config loader or
  Cobra.
- Concrete drivers live in their own sub-packages (`flexvault/driver/keepass`)
  and register with `flexvault` from `init()`, so importing a driver for its
  side effect (`_ "…/flexvault/driver/keepass"`) is all that's needed to make it
  selectable by name.

Details of the `flexvault` types are in [vault-drivers.md](vault-drivers.md);
the `flexprompt` `Prompter` in [prompter.md](prompter.md); `flexcli` in
[cli.md](cli.md).

## 7. Glossary

| Term | Meaning |
|------|---------|
| **Config schema** | App-provided Go types describing the expected configuration. |
| **Config directory (layer)** | A directory holding config files; the Loader is configured with an ordered list of them, each a layer merged by precedence. |
| **Config file** | An individual config selected by name (`config.yaml`, `agents.yaml`) within the layers and bound into a struct. |
| **Layering** | Merging the same-named file across config directories by precedence into one tree (later directory overrides earlier). |
| **Token** | `$(scheme:path)` placeholder inside a config value. |
| **Scheme** | The prefix of a token selecting a resolver (`env`, `secret`, …). |
| **Resolver** | Component that turns a token's `path` into a value. |
| **Vault driver** | Pluggable backend behind the `secret:` scheme (`flexvault.VaultDriver`). Formerly "secret driver". See _vault-drivers.md_. |
| **Manager** | `flexvault.Manager`: wraps one vault driver; drives its lifecycle, owns the credential dispatch, and exposes `Unlock`/`Get`/`Set`/`List`. See _vault-drivers.md_. |
| **Prompter** | Abstraction for collecting credentials in one interaction (CLI, GUI, env, tests). Lives in the `flexprompt` package as a process-wide singleton (`SetPrompter`/`GetPrompter`); the Manager passes it the driver's declared requests. See _prompter.md_. |
| **Vault config vs. vault secret** | A vault's non-secret settings (path, URL) vs. its credentials (password, token) — sourced and handled separately. See _vault-drivers.md_ §2.1. |
| **Secret address** | A two-level `namespace/key` identifier for a secret (e.g. `artifactory/token`). See _vault-drivers.md_ §6. |
| **Vault registry** | A layered map of vault `name → VaultConf` (driver + non-secret config) that names and configures vaults. See _vault-registry.md_. |
| **Default vault** | The vault a `$(secret:...)` token resolves against when no `vault:` segment is given; set by the registry's `default:` key. See _vault-registry.md_ §4. |
| **Vault reference** | The optional `vault:` segment in a token (`$(secret:vault:namespace/key)`) selecting a named vault. See _vault-registry.md_ §5. |
| **Loader** | Orchestrator configured with ordered config dirs; `Load(name, &dst)`: read → merge → resolve → bind → validate for that named file. |
| **Binding** | Mapping the resolved value tree onto the config schema types. |
| **Secret agent** | Long-lived, ssh-agent-style background process (spawned by `flexcli`) that holds one unlocked vault in memory and serves `get`/`set`/`list` over a private socket, auto-locking after an idle timeout. See _cli.md_. |
| **Global vault** | A user-level vault defined in the user's registry (`~/.config/flexconf/vaults.yaml`), independent of any app. See _vault-registry.md_ §2 and _cli.md_ §5. |

## 8. Open questions

Tracked here until resolved into the appropriate spec:

- Whether resolution is eager (at load) only, or optionally lazy/on-access for
  secrets that should rotate.
- Caching and TTL semantics for vault drivers.

Resolve these in the topic specs and remove them from this list.

**Resolved** (kept for reference):

- Behavior on partial failure — some tokens unresolved — `Load` fails loud and
  early, see [config-loading.md](config-loading.md) §3, §5.