---
tags:
  - specs
  - errors
  - configuration
  - secrets
---

# Errors

- **Status:** ✅ Accepted
- **Scope:** the **error taxonomy** for the whole module — the sentinel errors
  each package exposes, how errors are **wrapped** with diagnostic context (file,
  layer, key path), the **secret-origin redaction** rule that keeps secret values
  out of messages, and the `errors.Is`/`errors.As` contract callers rely on. Each
  sentinel is *owned* by the topic spec that defines the behaviour behind it; this
  spec collects them in one place so the taxonomy is visible as a whole and stays
  consistent. Where a message's exact wording matters, the owning spec is the
  authority.

## 1. Principles

- **Fail loud, fail early** ([specs index](index.md)). Every error surfaces at
  `Load` (or at CLI command time), never as a silent default or a deferred
  surprise. There is no lenient/best-effort mode.
- **Match with `errors.Is`.** All exported sentinels are package-level `error`
  values created with `errors.New`. Callers MUST test them with `errors.Is`, not
  by string comparison — the human-readable message is not part of the contract,
  the sentinel identity is.
- **Wrap, don't replace.** A failure at any lifecycle stage
  ([config-loading.md](config-loading.md) §5) wraps its stage's sentinel with
  **diagnostic context** — the offending file/layer and the **key path** — using
  `fmt.Errorf("…: %w", …)`, so `errors.Is` still finds the sentinel and
  `errors.Unwrap` reaches the cause.
- **Secret-origin redaction (normative).** A resolved scalar that originated from
  a `secret:` token is flagged as **secret-origin** ([resolvers.md](resolvers.md)
  §5). Any error message MUST name the field/key path but MUST **omit the value**
  when it is secret-origin. This is the *only* redaction obligation in v1: v1 has
  no config-dump feature (§4), so there is no separate redaction/dump format to
  define. Non-secret values (including `file:` contents) MAY appear in messages.

## 2. Sentinel taxonomy

The sentinels below are the complete v1 set. Each row names the owning spec; the
`errors.Is` identity and the package it lives in are the stable contract.

### 2.1 `flexconf` — loading, templating, resolving, binding

| Sentinel | Meaning | Owner |
|----------|---------|-------|
| `ErrConfigNotFound` | No configured layer provides the requested `name`. | [config-loading.md](config-loading.md) §3, §6 |
| `ErrUnsupportedFormat` | `name` has a non-`.yaml`/`.yml` extension. | [config-loading.md](config-loading.md) §2, §6 |
| `ErrInvalidName` | `name` contains a path separator or `..`. | [config-loading.md](config-loading.md) §2, §6 |
| `ErrBadToken` | Malformed template token (no `:`, unterminated `$(`). | [templating.md](templating.md) §9 |
| `ErrIncludeEmbedded` | `$(config:…)` used somewhere other than as a whole value. | [templating.md](templating.md) §7, §9 |
| `ErrIncludeExtension` | Include path does not end in `.yaml`/`.yml`. | [templating.md](templating.md) §7, §9 |
| `ErrIncludeEscape` | Include path escapes the config directory. | [templating.md](templating.md) §7, §9 |
| `ErrIncludeCycle` | Include cycle detected (message names the full chain). | [templating.md](templating.md) §7, §9 |
| `ErrIncludeTooDeep` | Include nesting exceeds `maxIncludeDepth = 16`. | [templating.md](templating.md) §7, §9 |
| `ErrUnknownScheme` | No resolver registered for a token's scheme. | [resolvers.md](resolvers.md) §1, §7 |
| `ErrEnvNotSet` | `$(env:NAME)` references an unset variable (hard error). | [resolvers.md](resolvers.md) §3, §7 |
| `ErrFileNotFound` | `$(file:…)` target is missing/unreadable. | [resolvers.md](resolvers.md) §4, §7 |
| `ErrAgentUnavailable` | `PolicyAgent` selected but the process can neither reach nor host an agent. | [resolvers.md](resolvers.md) §5.4, §7 |
| `ErrUnknownField` | A resolved key has no matching struct field (strict, every level). | [schema-and-binding.md](schema-and-binding.md) §5, §9 |
| `ErrMissingRequired` | A `required` key is absent from the merged tree. | [schema-and-binding.md](schema-and-binding.md) §5, §9 |
| `ErrTypeMismatch` | A value does not fit its target field type. | [schema-and-binding.md](schema-and-binding.md) §4, §9 |
| `ErrInvalidTag` | Malformed `flexconf` struct tag (a programming error). | [schema-and-binding.md](schema-and-binding.md) §2, §9 |
| `ErrVariantNotFound` | No configured variant matches the requested family + selectors. | [variants.md](variants.md) §7 |
| `ErrVariantAmbiguous` | More than one configured variant matches the selectors. | [variants.md](variants.md) §7 |

`ErrUnknownScheme` is produced by the resolver layer but is also relevant to
templating (an unknown scheme is caught as tokens are dispatched); its identity
lives in `flexconf` and is owned by [resolvers.md](resolvers.md).

### 2.2 `flexvault` — vault backends

The vault sentinels are defined and documented in
[vault-drivers.md](vault-drivers.md) §8; they surface up through the `secret:`
resolver ([resolvers.md](resolvers.md) §7):

| Sentinel | Meaning |
|----------|---------|
| `ErrNotFound` | The secret address does not exist in the vault. |
| `ErrLocked` | The vault is not unlocked. |
| `ErrAuth` | Authentication/unlock failed (wrong master password, token). |
| `ErrUnsupported` | The driver does not support the requested capability. |

The exact set and their per-driver semantics are normative in
[vault-drivers.md](vault-drivers.md) §8; this table is a pointer, not a second
definition.

### 2.3 `flexprompt` — credential collection

| Sentinel | Meaning | Owner |
|----------|---------|-------|
| `ErrPromptCancelled` | The user/driver cancelled a prompt; terminal, no retry. | [prompter.md](prompter.md) §5 |

## 3. Wrapping & diagnostic context

- The Loader wraps a stage error with the **offending file/layer** and the
  **key path** of the value under work
  ([config-loading.md](config-loading.md) §6). A resolver error is additionally
  wrapped with the scheme and the token's `path`
  ([resolvers.md](resolvers.md) §1) — with the secret-origin redaction of §1
  applied to any value.
- A `Validate()` hook's returned error is wrapped with the field/key path of the
  value it ran on ([schema-and-binding.md](schema-and-binding.md) §9); the hook's
  own message is preserved verbatim (it is expected to be actionable).
- Wrapping uses `%w`, so `errors.Is(err, flexconf.ErrTypeMismatch)` and friends
  work through the whole chain, and `errors.As` can reach a driver-specific error
  type where one is defined.

## 4. What is deliberately absent in v1

- **No config-dump / `--print-config` and therefore no taint/`«redacted»` dump
  format.** v1 offers no built-in way to print a whole resolved config, so the
  only place a secret could leak is an error message — covered by the
  secret-origin redaction rule (§1). A structural taint set and a `Dump()` that
  rewrites secret scalars to `«redacted»` are **not** in v1; there is no
  `redaction.md`. (Earlier drafts referenced one; it is dropped, not deferred.)
- **No error-code enum / i18n.** Errors are Go sentinels plus wrapped context;
  there is no numeric code space or localized message catalogue.

## 5. Resolved decisions

- **One taxonomy, owned per topic.** Sentinels live in the package whose behaviour
  they describe; this spec is the consolidated index and the authority on the
  cross-cutting rules (wrapping, `errors.Is`, redaction).
- **`errors.Is` is the contract**; messages are not.
- **Secret-origin redaction is the sole redaction obligation** — enforced in
  every message, made unambiguous by the absence of any config-dump feature (§4).
- **No `redaction.md`, no dump format, no error codes in v1** (§4).
