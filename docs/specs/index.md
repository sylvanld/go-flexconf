---
tags:
  - specs
  - configuration
  - secrets
---

# flexconf — Specifications

**flexconf** is a Go SDK for flexible **configuration** and **secret**
management, unifying the two so that config files can transparently reference
secrets and environment values.

## In one paragraph

An application declares the structure of its configuration (as Go types) and
lets flexconf load it from one or more config directories. Config files may contain
templating tokens such as `$(env:FOO)` or `$(secret:artifactory/token)`. The
person authoring the config — not the application developer — decides which
values are pulled from the environment or from a secret backend. flexconf
resolves those tokens at load time, delegating `secret:` lookups to a
pluggable **vault driver** (Vault, cloud secret managers, files, …) selected
dynamically at runtime.

## Design principles

- **Declare structure, not plumbing.** Apps describe *what* config they need;
  flexconf handles *how* and *where* it comes from.
- **Secrets are a source, not a special field.** Whether a value is a secret is
  a property of the config *authoring*, expressed via templating tokens — not
  something baked into the app's type definitions.
- **Drivers are pluggable.** The secret backend is chosen at runtime via a
  driver pattern; adding a backend must not require changes to config-loading
  code.
- **Fail loud, fail early.** Missing keys, unresolvable tokens, and type
  mismatches are detected at load time with clear errors.

## Packages

The module has **three core packages** plus an optional CLI package, layered so
dependencies point one way (no cycles). Listed bottom-up (leaf first):

- **`flexprompt`** — credential/input collection: the `Prompter` singleton
  ([prompter.md](prompter.md)). Leaf package; imports none of the others.
- **`flexvault`** — secret backends: `VaultDriver`, `Manager`, drivers (KeePass, …).
- **`flexconf`** — config loading (schema, sources, templating, resolvers, loader).
- **`internal/agent`** *(internal)* — the ssh-agent-style secret agent runtime
  that holds an unlocked vault in memory; shared by `flexconf` (the `secret:`
  resolver) and `flexcli` ([resolvers.md](resolvers.md) §5.5).
- **`flexcli`** *(optional)* — a mountable Cobra `secret` command group that drives
  the `internal/agent` runtime.

See [overview.md](overview.md) "Module layout" for the normative description.

## Spec index

| Spec | Status | Summary |
|------|--------|---------|
| [overview.md](overview.md) | ✅&nbsp;Accepted | Architecture, core concepts, glossary, and how the pieces fit together. |
| [config-loading.md](config-loading.md) | ✅&nbsp;Accepted | Config directories as layers, the `Loader` and `Load(name, &struct)`, merge precedence, formats, and the load lifecycle. |
| [schema-and-binding.md](schema-and-binding.md) | ✅&nbsp;Accepted | How apps declare config structure (the `flexconf` tag), how defaults (pre-populated instance) and values bind to Go types, strict validation, and the `Validate()` hook. |
| [variants.md](variants.md) | ✅&nbsp;Accepted | Polymorphic config: choosing a concrete variant by a discriminator key, the `Registry[V]` that binds and holds every instance, selectors, and selector-based resolution (exactly-one-or-fail). |
| [templating.md](templating.md) | ✅&nbsp;Accepted | Token grammar (`$(scheme:path)`), the node-tree substitution model, escaping (`$$(`), no-nesting rule, resolved-scalar typing, and the `$(config:path)` include/splice token. |
| [prompter.md](prompter.md) | ✅&nbsp;Accepted | The `flexprompt` package: the `Prompter` interface, `PromptRequest`, the process-wide singleton, built-in prompters, and prompter errors. |
| [vault-drivers.md](vault-drivers.md) | ✅&nbsp;Accepted | The `VaultDriver` interface, the `Manager` (unlock/get/set/list) and its dispatch to the `Prompter`, `namespace/key` addressing, and the KeePass driver. |
| [vault-registry.md](vault-registry.md) | ✅&nbsp;Accepted | The named vault registry, layering, the default vault, and vault references in tokens (`$(secret:[vault:]namespace/key)`). |
| [cli.md](cli.md) | ✅&nbsp;Accepted | The `flexcli` Cobra `secret` command group (incl. `secret vaults` registry inspection) and the background secret agent (ssh-agent style) with idle auto-lock. |
| [resolvers.md](resolvers.md) | ✅&nbsp;Accepted | The `Resolver` interface and registration, built-in schemes (`env`, `secret`, `file`), and how the `secret:` scheme reaches a vault through the background agent (spawning one like the CLI when none is running). |
| [api.md](api.md) | ✅&nbsp;Accepted | Public Go API surface of the SDK, gathered per package (semantics owned by the topic specs). |
| [errors.md](errors.md) | ✅&nbsp;Accepted | Error taxonomy, wrapping, secret-origin redaction, and the `errors.Is` contract. |
| [missing.md](missing.md) | 🗒️&nbsp;Notes | Non-normative gap analysis: what landed in the specs and the post-v1 candidate list. |

> All v1 specs are accepted. [missing.md](missing.md) is non-normative notes.
> When you add a spec, create the file, update this table, and register it in the
> Zensical nav (`docs/zensical.toml`).
