---
icon: lucide/vault
tags:
  - reference
  - vault
  - secrets
---

# Vault registry

Vaults are **named** in an operator-owned registry and referenced by name
everywhere else. An application never defines a vault — it only writes
`$(secret:…)` tokens; the operator decides which backend serves them.

Normative spec: [vault-registry.md](../specs/vault-registry.md).

## The registry file

```yaml
# ~/.config/flexconf/vaults.yaml
default: personal          # the vault unqualified $(secret:…) tokens use
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

- A definition is a registered **driver name** plus that driver's own
  non-secret settings, handed verbatim to its `Configure`.
- **Credentials are never stored here** — they are prompted at unlock.
- Definitions are **static**: no `$(...)` token of any kind is allowed
  (`$(secret:)` would be circular; `$(env:)` would make a shared file behave
  differently per context). Violations fail the load.
- Location: `$XDG_CONFIG_HOME/flexconf/vaults.yaml`
  (default `~/.config/flexconf/vaults.yaml`).

### Path rules

- A leading `~/` always expands to the user's home (centrally — every driver
  sees an expanded path).
- A **relative** `path:` resolves against the directory of the registry file
  that defined it, not the working directory.

## Layering — `FLEXCONF_VAULTS`

The effective registry is an ordered list of files, later file wins:

- `FLEXCONF_VAULTS` **unset**: the single well-known file (missing → empty
  registry).
- `FLEXCONF_VAULTS` **set** (`:`-separated on Unix): the **exhaustive**
  ordered list, applied left to right. This replaces discovery entirely — the
  well-known file participates only if listed.

Override semantics: the `vaults:` map merges by name, but each name's
`VaultConf` is replaced **wholesale** (never deep-merged — you can't end up
with one file's `driver` and another's `path`). The last `default:` wins.

## Default vault & token references

```
$(secret:[<vault>:]<namespace>/<key>)
```

| Token | Vault |
|-------|-------|
| `$(secret:artifactory/token)` | the registry's `default:` |
| `$(secret:work:deploy/key)` | `work` |

- Keeping tokens **unqualified is recommended** — the config stays portable
  and the operator picks the backend per environment via `default:`.
- An unqualified token with **no default configured** fails loudly at load.
- An unknown vault name is a load-time error listing the known names.
- Vault names match exactly and case-sensitively; they cannot contain `/` or `:`.

## Vault identity (`VaultID`)

Each named, resolved vault gets a deterministic identity
`<name>-<fp>` (e.g. `work-3f9a1c02`), where `fp` is the first 8 hex chars of
SHA-256 over the canonical (keys-sorted) serialization of the resolved
non-secret config. The background agent keys unlocked vaults by `VaultID`:

- same name + same resolved config → **shared** agent (intentional);
- same name + different config (e.g. a project-local override) → distinct
  agents, so one context's unlocked vault is never served to another.
