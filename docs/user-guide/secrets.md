---
icon: lucide/key-round
tags:
  - reference
  - secrets
---

# Secret resolution

The `secret:` scheme unifies config and secrets: a config value written as
`$(secret:artifactory/token)` resolves at load time from a vault, and the
application code never knows the value was a secret.

Normative specs: [resolvers.md](../specs/resolvers.md) §5,
[vault-registry.md](../specs/vault-registry.md), [errors.md](../specs/errors.md).

## Token form

```yaml
token: $(secret:artifactory/token)     # default vault
key:   $(secret:work:deploy/key)       # named vault "work"
url:   https://$(secret:net/host)/api  # embeddable like any scalar token
```

The optional leading `vault:` segment selects a named vault from the
[registry](vaults.md); without it the registry's `default:` is used. The
address is always two levels: `namespace/key`.

## How resolution reaches a vault

### `PolicyAgent` (default)

The resolver proxies through the **background agent** — the ssh-agent-style
process holding one unlocked vault in memory per `VaultID`:

1. If an agent is running for the vault, `get` is served with **no prompt** —
   a vault unlocked once (by `secret unlock` or an earlier Load) serves every
   later read for free.
2. If not, the resolver collects the vault's credentials via the process-wide
   `flexprompt` prompter (one interaction), spawns a detached agent, forwards
   the unlock, and retries. The agent stays resident and idle-locks after
   its timeout.

Agent spawning is **self-exec**: library consumers MUST call

```go
func main() {
    flexconf.RunAgentIfRequested() // first thing in main
    ...
}
```

If the entry point is not wired and no agent is running, resolution fails
with `ErrAgentUnavailable` (never hangs, never silently downgrades).

### `PolicyInProcess`

```go
ld := flexconf.New(dir).With(flexconf.WithSecretPolicy(flexconf.PolicyInProcess))
defer ld.Close()
```

The vault's real driver is unlocked **in-process** via the process prompter —
no agent. Each vault unlocks once per Loader (cached; `Close()` locks them).
Use this for one-shot CI jobs and short-lived batches.

## Prompting

Install a prompter once at startup (see [flexprompt](flexprompt.md)):

```go
flexprompt.SetPrompter(flexprompt.NewCLIPrompter())   // interactive
flexprompt.SetPrompter(flexprompt.NewEnvPrompter("FLEXCONF_")) // CI
```

Within one `Load`, unlocks are serialized and each referenced vault prompts
**at most once**; later tokens for the same vault reuse the unlocked manager.

## Redaction

Every resolved `secret:` value is flagged secret-origin. Any error message —
e.g. a bind-time type mismatch — names the field and key path but **omits the
value**. Non-secret values (including `file:` contents) may appear in
messages.

## Errors

`secret:` resolution surfaces the `flexvault` sentinels (`ErrNotFound`,
`ErrLocked`, `ErrAuth`, …), `flexprompt.ErrPromptCancelled` (terminal, no
retry), and `flexconf.ErrAgentUnavailable`. All match with `errors.Is` and
are wrapped with the offending key path and vault name.
