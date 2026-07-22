---
tags:
  - roadmap
---

# Implementation Roadmap

This roadmap decomposes the v1 implementation into an ordered list of pull
requests. Each PR is a self-contained feature: code + tests + user-facing
documentation land together. Ordering follows the module's dependency
direction ([overview.md](specs/overview.md) §6) — leaf packages first, so
every PR builds and tests green on its own.

Conventions applied to every PR:

- **Tests are mandatory** — no PR merges without tests covering its feature.
- **User-facing API is documented** under `docs/reference/`, one page per
  package/topic, registered in the Zensical nav.
- **Specs are authoritative** — implementation follows `docs/specs/*`; any
  deviation discovered while coding is called out in the PR description.

## PR list

| # | Branch | Feature | Status |
|---|--------|---------|--------|
| 1 | `docs/implementation-roadmap` | This roadmap. | ✅ |
| 2 | `feat/flexprompt` | `flexprompt` leaf package: `Prompter`, `PromptRequest`, process-wide singleton (`SetPrompter`/`GetPrompter`), `NewCLIPrompter`/`NewMapPrompter`/`NewEnvPrompter`/`PrompterFunc`, sentinel errors ([prompter.md](specs/prompter.md)). Reference page `docs/reference/flexprompt.md`. | ✅ |
| 3 | `feat/flexvault-core` | `flexvault` core: `VaultDriver` + `Initializer` interfaces, `Capabilities`, sentinel errors, `Manager` (lifecycle enforcement, dispatch, retry, address validation, serialization), driver registration (`Register`/`New`), `MapDecoder`/`EnvDecoder` ([vault-drivers.md](specs/vault-drivers.md) §1–§9). Reference page `docs/reference/flexvault.md`. | ✅ |
| 4 | `feat/keepass-driver` | `flexvault/driver/keepass`: KeePass driver over gokeepasslib — Configure/Credentials/Unlock/Get/Set/List/Lock, `Initializer`, group/entry ↔ namespace/key mapping ([vault-drivers.md](specs/vault-drivers.md) §10). Reference page section. | ✅ |
| 5 | `feat/variant-engine` | `internal/variant`: generic `Registry[V]` — variant registration, discriminator, selectors, subset matching, exactly-one resolution, duplicate detection ([variants.md](specs/variants.md) §2–§7). Internal; public re-export lands with PR 8. | ⬜ |
| 6 | `feat/loader-core` | `flexconf` loader core: `New(dirs...)`, `Load(name, &dst)`, options plumbing, YAML read, layer merge (§3), per-file shape validation, reflection binder (`flexconf` tag, defaults-by-instance, `required`, strict unknown keys, coercion incl. `time.Duration`/`time.Time`/`TextUnmarshaler`, `Validate()` hook, all-or-nothing), sentinel errors ([config-loading.md](specs/config-loading.md), [schema-and-binding.md](specs/schema-and-binding.md)). No token resolution yet (static pipeline). Reference pages `docs/reference/flexconf.md`, `docs/reference/schema.md`. | ✅ |
| 7 | `feat/templating` | Templating engine: token grammar, `$$(` escaping, node-tree substitution, typed whole-value vs string mixed scalars, `Resolver` interface, `RegisterResolver`/`WithResolver`/`WithResolvers`, `env:` (+`WithEnv`), `file:` (+`WithFS`), `$(config:)` includes (cycle detection, depth cap, containment), secret-origin flagging plumbing ([templating.md](specs/templating.md), [resolvers.md](specs/resolvers.md) §1–§4). Reference page `docs/reference/templating.md`. | ✅ |
| 8 | `feat/variants-binding` | Variant locations in binding (`V`, `[]V`, `map[string]V`), selector derivation, process-wide registry, `flexconf` re-exports (`Registry[V]` alias, `RegisterVariant`, `Resolve`, `Get`, `Select`, `WithDiscriminator`, `WithRegistry`) ([variants.md](specs/variants.md)). Reference page `docs/reference/variants.md`. | ⬜ |
| 9 | `feat/vault-registry` | Vault registry: env-derived layer list (`FLEXCONF_VAULTS` / well-known file), static-loader front-end, whole-entry replacement, `default:` vault, `~`/relative path normalization, `VaultID` derivation ([vault-registry.md](specs/vault-registry.md)). Reference page `docs/reference/vaults.md`. | ⬜ |
| 10 | `feat/agent-runtime` | `internal/agent`: socket protocol (length-prefixed JSON), server loop with idle auto-lock, client `Dial`, self-exec spawn + `RunAgentIfRequested`, lock/PID/err files, peer-UID check, agent-proxy `VaultDriver` ([cli.md](specs/cli.md) §6, [resolvers.md](specs/resolvers.md) §5.2, §5.5). | ⬜ |
| 11 | `feat/secret-resolver` | `secret:` resolver: `[vault:]namespace/key` parsing, registry lookup, `PolicyAgent` (via agent proxy) and `PolicyInProcess`, `WithSecretPolicy`, `flexconf.RunAgentIfRequested` re-export, secret-origin redaction in errors ([resolvers.md](specs/resolvers.md) §5, [errors.md](specs/errors.md)). Reference page `docs/reference/secrets.md`. | ⬜ |
| 12 | `feat/flexcli` | `flexcli` Cobra `secret` group (`init`/`unlock`/`lock`/`get`/`set`/`list`/`vaults`, `--vault` root flag, auto-unlock, exit codes) + `cmd/flexconf` standalone binary ([cli.md](specs/cli.md)). Reference page `docs/reference/cli.md`. | ⬜ |
| 13 | `docs/reference-polish` | Docs polish: reference index page, nav registration for all reference pages, README quick-start refresh. | ⬜ |

## Sequencing notes

- **Dependency direction** matches [overview.md](specs/overview.md) §6:
  `flexprompt` → `flexvault` → (`internal/variant`, `flexconf`) →
  `internal/agent` → `flexcli` → `cmd/flexconf`.
- PR 6 lands the Loader with an effectively empty resolver set; PR 7 turns on
  the default resolvers minus `secret:`, which arrives in PR 11 once the
  registry (PR 9) and agent (PR 10) exist.
- External dependencies are introduced where first needed:
  `golang.org/x/term` (PR 2), `gopkg.in/yaml.v3` (PR 6),
  `github.com/tobischo/gokeepasslib` (PR 4), `github.com/spf13/cobra` (PR 12).
- Statuses in this table are updated as PRs merge.
