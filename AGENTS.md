# AGENTS.md

Instructions for AI agents (and humans) working in this repository.

## What this project is

**flexconf** is a Go SDK for flexible configuration and secret management. See
[`docs/specs/README.md`](docs/specs/README.md) for the project summary and the
index of all specifications.

## Golden rule: spec first

This project is developed **spec-first**. Specifications live in
`docs/specs/*.md` and are the source of truth. Code follows the spec, not the
other way around.

Before writing or changing any code:

1. **Read the relevant spec(s)** in `docs/specs/`. Start from
   [`docs/specs/README.md`](docs/specs/README.md), which indexes every spec.
2. **If the behavior isn't specified, stop and write/update the spec first.**
   Get it agreed before implementing.
3. **If the spec is wrong or incomplete, fix the spec** in the same change (or a
   preceding one) — never silently diverge from it in code.

When code and spec disagree, the spec wins. Reconcile them explicitly.

## Working on specs

- Keep [`docs/specs/README.md`](docs/specs/README.md) up to date: every spec
  file must have an entry in its index, with a one-line summary.
- One spec file per coherent topic (e.g. config loading, templating, secret
  drivers). Prefer splitting over one giant file.
- Each spec should state its **status** (`Draft`, `Accepted`, `Superseded`) at
  the top.
- Use concrete, testable statements. Prefer examples and small code snippets
  over prose. Use RFC 2119 keywords (MUST / SHOULD / MAY) for normative rules.
- Cross-link related specs with relative Markdown links.
- Keep terminology consistent with the glossary in the overview spec. If you
  introduce a new term, define it there.

## Working on code (once specs exist)

- Match the design and vocabulary defined in the specs exactly (type names,
  package names, token syntax, etc.).
- Follow standard Go conventions: `gofmt`, idiomatic error handling, small
  focused packages, table-driven tests.
- Every behavior described as normative (MUST/SHOULD) in a spec should have a
  corresponding test.
- Reference the relevant spec in code comments where a non-obvious rule comes
  from a spec decision.

## Package layout

The module is organized into **three core packages** plus an optional CLI package
(see [`docs/specs/overview.md`](docs/specs/overview.md) "Module layout" for the
normative description). Dependencies point downward only —
`cmd/flexconf → flexcli → flexvault → flexprompt` — so there are no import
cycles. Note that `flexcli` imports `flexvault` (and `flexprompt`) **directly**;
it does **not** import `flexconf` (the CLI manages secrets, not app config):

- **`flexprompt`** — credential/input collection: the `Prompter` interface,
  the process-wide singleton (`SetPrompter`/`GetPrompter`), and built-in
  prompters. Leaf package; imports none of the others.
- **`flexvault`** — secret backends: `VaultDriver`, `Manager`,
  `Capabilities`, sentinel errors, driver registration, config decoders, and
  concrete drivers under `flexvault/driver/*` (e.g. `flexvault/driver/keepass`).
  Imports `flexprompt`.
- **`flexconf`** — config loading: schema/binding, sources/layering, templating,
  resolvers, and the `Loader`. Imports `flexvault` and `flexprompt`.
- **`flexcli`** *(optional)* — a mountable Cobra `secret` command group and the
  background secret agent. Imports `flexvault`, `flexprompt`, and Cobra; nothing
  imports it.

When adding a type, put it in the package that owns its concern, and never add
an import that points upward.

## Documentation

The docs site is built with [Zensical](https://zensical.org/) from the `docs/`
directory (specs live under `docs/specs/`). Common tasks are wrapped in the
`Makefile`:

- `make docs-serve` — serve the docs locally with live reload at
  `http://127.0.0.1:10001` (override the port with `DOCS_PORT=...`).
- `make docs-build` — build the static site into `docs/build` (runs
  `zensical build --clean`). Use this to check the docs compile before pushing.
- `make docs-clean` — remove the generated `docs/build` output.
- `make help` — list all available targets.

All docs commands run through `uv run --isolated`, so `uv` must be installed;
dependencies (including Zensical) are pinned in `docs/pyproject.toml` /
`docs/uv.lock`. On push to `main`, `.github/workflows/docs.yml` builds and
deploys the site to GitHub Pages. `docs/build` is generated output and must not
be committed.

## Conventions

- Module path / import root: TBD (to be defined when `go.mod` is created). The
  three packages above are sub-packages of that root.
- Public API surface should be reviewed against the API spec before release.
- Never commit real secrets, tokens, or credentials — not even in tests or spec
  examples. Use obviously fake placeholders (e.g. `artifactory/token`).
