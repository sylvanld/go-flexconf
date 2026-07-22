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
| [overview.md](overview.md) | 📝&nbsp;Draft | Architecture, core concepts, glossary, and how the pieces fit together. |
| [config-loading.md](config-loading.md) | 📝&nbsp;Draft | Config directories as layers, the `Loader` and `Load(name, &struct)`, merge precedence, formats, and the load lifecycle. |
| _schema-and-binding.md_ | 🚧&nbsp;TODO | How apps declare config structure and how loaded values bind to Go types (tags, defaults, required, validation). |
| _templating.md_ | 🚧&nbsp;TODO | Token grammar (`$(scheme:path)`), escaping, nesting, and resolution order. |
| [prompter.md](prompter.md) | ✅&nbsp;Accepted | The `flexprompt` package: the `Prompter` interface, `PromptRequest`, the process-wide singleton, built-in prompters, and prompter errors. |
| [vault-drivers.md](vault-drivers.md) | ✅&nbsp;Accepted | The `VaultDriver` interface, the `Manager` (unlock/get/set/list) and its dispatch to the `Prompter`, `namespace/key` addressing, and the KeePass driver. |
| [vault-registry.md](vault-registry.md) | 📝&nbsp;Draft | The named vault registry, layering, the default vault, and vault references in tokens (`$(secret:[vault:]namespace/key)`). |
| [cli.md](cli.md) | 📝&nbsp;Draft | The `flexcli` Cobra `secret` command group (incl. `secret vaults` registry inspection) and the background secret agent (ssh-agent style) with idle auto-lock. |
| [resolvers.md](resolvers.md) | 📝&nbsp;Draft | The `Resolver` interface and registration, built-in schemes (`env`, `secret`, `file`), and how the `secret:` scheme reaches a vault through the background agent (spawning one like the CLI when none is running). |
| _api.md_ | 🚧&nbsp;TODO | Public Go API surface of the SDK. |
| _errors.md_ | 🚧&nbsp;TODO | Error taxonomy, wrapping, and diagnostics. |

> Rows in _italics_ are planned but not yet written. When you add a spec, create
> the file and update this table.
