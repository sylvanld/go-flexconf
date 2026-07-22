---
tags:
  - specs
---

# Candidate Features (gap analysis)

- **Status:** 🗒️ Notes (non-normative)
- **Purpose:** A gap analysis of the spec set against two real codebases: the
  prior/alternate implementation at `~/tools/flexconf` (the most complete existing
  attempt) and `~/sylvan/ai-tools/cds-ai-agent` (the project whose needs
  originated flexconf). It is kept as a record of *what was decided* and a
  shortlist of *post-v1 candidates*. Nothing here is normative — the accepted
  specs are.

Legend for "seen in": **[T]** = `~/tools/flexconf`, **[C]** = `cds-ai-agent`.

> **State:** every high-impact gap below has been resolved into an accepted spec
> (§1). What remains is a shortlist of **post-v1 candidates** — additive features
> that are explicitly *not* in v1 scope (§2) — plus a note on where source
> projects diverged and the divergence was deliberately kept (§3).

---

## 1. High-impact gaps — all resolved

Each of these was load-bearing in a source project and is now settled in a spec.

| Gap | Seen | Resolution |
|-----|------|------------|
| **`config:` include/splice token** | [T] | **In v1.** Whole-value splice, `.yaml`/`.yml`, literal path, containment + cycle + depth guards ([templating.md](templating.md) §7). |
| **Discriminated / polymorphic binding** | [T],[C] | **In v1**, generalized beyond the vault case ([variants.md](variants.md); [schema-and-binding.md](schema-and-binding.md) §6). Covers `vault.driver`, `apis[].kind`, `toolsets[].kind`. |
| **Strict load-time validation contract** | [C] | **In v1.** Always-strict unknown-key rejection at every level, `required` on the merged tree, bottom-up `Validate()` hooks ([schema-and-binding.md](schema-and-binding.md) §5). |
| **Node-tree templating** (not raw text) | [T] | **In v1**, made normative — scalar-content-only injection, untagged results, inert resolved values, keys never templated ([templating.md](templating.md) §2). |
| **Defaults as a pre-populated instance** | [T] | **In v1.** Defaults are a pre-populated struct; the file overrides only present keys; tag `default=` deferred as possible sugar ([schema-and-binding.md](schema-and-binding.md) §3). |
| **Secret redaction / no-secrets-in-diagnostics** | [T] | **Resolved by scope.** v1 has **no** config-dump feature, so there is no taint set / `Dump()` / `«redacted»` format. The one obligation — never print a secret-origin value in an **error** — is normative ([errors.md](errors.md) §1, §4). |
| **`$(env:NAME:-default)` fallback** | [T] | **Rejected.** A missing env var is always a hard error; no scheme has defaulting syntax. Supply fallbacks via config-file defaults ([templating.md](templating.md) §10, [resolvers.md](resolvers.md) §3). |

---

## 2. Post-v1 candidates (not in v1 scope)

These are genuine, additive ideas deliberately **out of v1**. None blocks v1, and
each could be added later without breaking the v1 surface.

### 2.1 More secret-resolver backends

v1 ships the KeePass driver ([vault-drivers.md](vault-drivers.md)). These are the
most-wanted additions, all as new `flexvault/driver/*` packages:

| Driver | Seen | What it does | Why it matters |
|--------|------|--------------|----------------|
| **env-vault** | [C] | Maps `namespace/key` → env var deterministically (`bitbucket/token` → `CDS_SECRET_BITBUCKET_TOKEN`); unlock is a no-op. | The **CI story**: same config, file vault in dev ↔ env-var vault in prod. |
| **exec** | [T] | Shells out (`pass`/`sops`/`vault`/…), secret name as final arg, reads stdout; non-zero exit → `ErrNotFound`. | One driver bridges to many backends for little code. |
| **encrypted-file (JSON)** | [C] | Argon2id KEK → wrapped master key → XChaCha20-Poly1305 over a JSON blob; atomic `0600`. | Dependency-free alternative to KeePass; the originating project chose *this*. |
| **none** | [T] | Every `$(secret:…)` is a hard not-found. | Lets a secret-free config declare intent explicitly. |

*(env-vault is the highest priority — it is cds-ai-agent's CI path.)*

### 2.2 Agent: absolute max-lifetime + memory hardening

[T] adds `MaxLifetime` (absolute relock cap, default 30m) on top of the idle
timeout, and `Harden()` after unlock (`RLIMIT_CORE=0`, `PR_SET_DUMPABLE=0`,
`Mlockall`, best-effort/Linux). v1 has the idle timeout only and lists mlock as
"future hardening" ([cli.md](cli.md) §6.5, §8). Cheap and proven; candidate to
promote.

### 2.3 A config-file generator CLI: `settings init` / `path`

[T] ships a second Cobra group that renders declared defaults to `config.yaml`
(refuses to clobber, `0600`, round-trips) and prints the location. v1 has no
config-printing/generating command at all ([cli.md](cli.md) §12). Low-cost
onboarding win for a later release.

### 2.4 App-name-derived config directory

[T] resolves **one** directory (`WithPath` → `<APP>_CONFIG` → `~/.config/<app>/`)
so `config.yaml` and the vault file relocate together. v1 takes an explicit
`dirs` list and defers discovery ([config-loading.md](config-loading.md) §7). A
nicer default UX; deferred, not rejected.

### 2.5 A deferred-decode `Settings` object

[T] exposes an intermediate resolved-tree object (`Decode`, `Dump`,
`Marshal/Unmarshal`) alongside a one-shot `Load`. v1's only entry point is
`Load(name, &dst)`; the object is **not** in v1 ([api.md](api.md) §6). Ties to a
future config-dump feature if one is ever added.

*(Note: `WithFS`/`WithEnv` — the testability-injection ask from the same review —
**are** in v1, so that part is done, not a candidate: [api.md](api.md) §1.)*

---

## 3. Deliberate divergences from the source projects

Places v1 made a different call than an implementation, kept on purpose:

- **External vault registry, not an in-config `secrets:` block.** [T] configures
  the backend inside the app's own config; v1 moves all vault definitions to
  `~/.config/flexconf/vaults.yaml` and forbids inline definitions
  ([vault-registry.md](vault-registry.md) §2). This is a **confirmed** v1 stance —
  the registry keeps *what a value pulls* decoupled from *which backend serves it*.
- **No blanket env-var overrides of arbitrary keys.** v1 rejects env as a config
  layer ([config-loading.md](config-loading.md) §1); author-chosen `$(env:…)`
  tokens are the answer, not koanf-style `APP_FOO_BAR` overrides. **Confirmed** —
  the token model is intentional.
- **`Get` returns a single string; token exchange lives above the SDK.** [C] does
  signin-key → JWT exchange on top of a resolved secret. v1 keeps `Get` returning
  one string ([vault-drivers.md](vault-drivers.md) §7); exchange/refresh is an
  application concern, above flexconf.
