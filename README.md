# flexconf

**flexconf** is a Go SDK for flexible **configuration** and **secret**
management, unifying the two so that config files can transparently reference
secrets and environment values.

An application declares the structure of its configuration as Go types and lets
flexconf load it from one or more config directories. Config files may contain
templating tokens such as `$(env:FOO)` or `$(secret:artifactory/token)`; the
person authoring the config decides which values are pulled from the
environment or from a secret backend. flexconf resolves those tokens at load
time, delegating `secret:` lookups to a pluggable **vault driver** (Vault, cloud
secret managers, files, …) selected at runtime.

## Status

Early, **spec-first** development. The specifications in
[`docs/specs/`](docs/specs/) are the source of truth; see
[`docs/specs/README.md`](docs/specs/README.md) for the index. Code follows the
spec — see [`AGENTS.md`](AGENTS.md) for how to work in this repository.

## Documentation

The docs site is built with [Zensical](https://zensical.org/) from the `docs/`
directory. Run `make docs-serve` to preview locally, or `make help` to list all
targets.
