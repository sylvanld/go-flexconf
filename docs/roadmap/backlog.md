---
tags:
  - roadmap
---

# Backlog

Additive ideas deliberately left out of v0.1.0. None blocks the current
surface, and each can be added later without breaking it. Items graduate from
here into a versioned roadmap page when scheduled.

## More secret-resolver backends

v0.1.0 ships the KeePass driver ([vault-drivers.md](../specs/vault-drivers.md)).
These are the most-wanted additions, all as new `flexvault/driver/*` packages:

| Driver | What it does | Why it matters |
|--------|--------------|----------------|
| **env-vault** | Maps `namespace/key` → env var deterministically (`bitbucket/token` → `CDS_SECRET_BITBUCKET_TOKEN`); unlock is a no-op. | The **CI story**: same config, file vault in dev ↔ env-var vault in prod. Highest priority — it is the originating project's CI path. |
| **hashicorp-vault** | Talks to a [HashiCorp Vault](https://www.vaultproject.io/) server (KV secrets engine), mapping `namespace/key` onto a secret path; auth via token or the server's configured method. | The team/server-side story: centrally managed secrets, policies, and audit — no local vault file to distribute. |

## Agent: absolute max-lifetime + memory hardening

A `MaxLifetime` (absolute relock cap, default 30m) on top of the idle timeout,
and a `Harden()` step after unlock (`RLIMIT_CORE=0`, `PR_SET_DUMPABLE=0`,
`Mlockall`, best-effort/Linux). v0.1.0 has the idle timeout only and lists
mlock as "future hardening" ([cli.md](../specs/cli.md) §6.5, §8). Cheap and
proven in a prior implementation; candidate to promote.

## A config-file generator CLI: `settings init` / `path`

A second Cobra group that renders declared defaults to `config.yaml` (refuses
to clobber, `0600`, round-trips) and prints the location. v0.1.0 has no
config-printing/generating command at all ([cli.md](../specs/cli.md) §12).
Low-cost onboarding win for a later release.

## App-name-derived config directory

Resolve **one** directory (`WithPath` → `<APP>_CONFIG` → `~/.config/<app>/`)
so `config.yaml` and the vault file relocate together. v0.1.0 takes an
explicit `dirs` list and defers discovery
([config-loading.md](../specs/config-loading.md) §7). A nicer default UX;
deferred, not rejected.

## A deferred-decode `Settings` object

An intermediate resolved-tree object (`Decode`, `Dump`, `Marshal/Unmarshal`)
alongside the one-shot `Load`. v0.1.0's only entry point is `Load(name, &dst)`
([api.md](../specs/api.md) §6). Ties to a future config-dump feature if one is
ever added.
