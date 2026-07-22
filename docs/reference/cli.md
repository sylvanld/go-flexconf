---
tags:
  - reference
  - cli
  - secrets
---

# CLI — `flexcli` & the `flexconf` binary

`flexcli` provides a mountable Cobra **`secret`** command group (alias
`secrets`) driving the background secret agent. Two entry points share the
identical group:

- **Embedded** — mount it into your app's CLI (`myapp secret get …`).
- **Standalone** — the shipped `cmd/flexconf` binary (`flexconf secret get …`).

Both read vault definitions from the [vault registry](vaults.md) only.

Normative spec: [cli.md](../specs/cli.md).

## Embedding

```go
func main() {
    app := flexcli.New(flexcli.Options{
        DefaultVault: "myapp",          // optional registry-vault pin
        // IdleTimeout defaults to 2m; Prompter defaults to the CLI prompter
    })
    app.RunAgentIfRequested()           // MUST be first in main (agent re-execs)

    root := &cobra.Command{Use: "myapp"}
    root.AddCommand(app.SecretCommand())
    if err := root.Execute(); err != nil {
        os.Exit(flexcli.ExitCode(err))  // 0 ok / 1 generic / 2 usage / 3 locked
    }
}
```

## Commands

All subcommands accept the persistent `--vault <name>` flag (else
`FLEXCONF_VAULT`, else the app's `DefaultVault`, else the registry's
`default:`).

| Command | Behaviour |
|---------|-----------|
| `secret init` | Create a new, empty vault (driver must be `Creatable`). Never overwrites. Double-entry master password. |
| `secret unlock [--force]` | Collect credentials in the foreground (masked), spawn a detached agent, forward the unlock, wipe the answers. Re-running while unlocked is a no-op; `--force` re-prompts. |
| `secret lock` | Graceful agent shutdown (clears key material). No agent → "no agent running", exit 0. |
| `secret get <ns/key> [--raw]` | Print the value to stdout (`--raw`: exact bytes, mandatory for multi-line secrets; `get --raw \| set` round-trips). |
| `secret set <ns/key>` | Value read from **stdin**, never argv: `echo -n s \| myapp secret set a/b`. Requires a writable vault. |
| `secret list [namespace]` | Namespaces, or key names within one; one per line. |
| `secret status` | Agent state + remaining idle time (does not reset the timer). |
| `secret vaults [--format yaml] [--validate]` | Dump the resolved registry with file provenance. Dump-only always exits 0; `--validate` turns the `note:` problems into errors for CI. |

### Auto-unlock (`get`/`set`/`list`)

When no agent is running:

- **Interactive TTY:** asks `Vault is locked. Unlock now? [y/N]`, then retries
  once.
- **Non-interactive or `--no-unlock`:** fails with exit code **3** and
  guidance to run `secret unlock` — scripts never hang.
- `--unlock` forces the unlock attempt without asking.

## The agent

The first `unlock` spawns a detached, self-exec'd agent that holds the
unlocked vault in memory and serves requests over a user-private Unix socket
(`$XDG_RUNTIME_DIR/flexconf/agent-<VaultID>.sock`, 0600, peer-UID-checked).
It auto-locks after the idle timeout (default **2 minutes**;
`FLEXCONF_IDLE_TIMEOUT`, e.g. `5m`). Each named vault gets its own `VaultID`
and agent, so vaults never collide. The agent itself never prompts.

## Typical session

```console
$ flexconf secret init
New KeePass master password: ****
created vault "personal"

$ flexconf secret unlock
KeePass master password: ****
unlocked; agent will lock after idle timeout

$ echo -n 'ghp_xxx' | flexconf secret set personal/github-token
ok
$ flexconf secret get personal/github-token
ghp_xxx
$ flexconf --vault work secret get deploy/key
```

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | generic failure (incl. secret not found) |
| 2 | usage error (Cobra) |
| 3 | vault locked / not unlocked (scripts can branch, run `unlock`, retry) |
