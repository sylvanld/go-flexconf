# `cli/secrets` (`secretcli`) — embeddable command tree

Package `cli/secrets` (import path `github.com/sylvanld/flexconf/cli/secrets`,
package name `secrets`, referred to as **secretcli**) is a factory that builds
the whole secret manager as a [cobra](https://github.com/spf13/cobra) command
tree, ready to mount as a sub-command of any application's CLI. It wires
[`settings`](settings.md) + [`secrets`](secrets.md) + [`agent`](agent.md)
together so the KeePass file, socket, and agent log all live under the app's own
config directory.

## Factory

### `New(cfg *settings.Settings, opts ...Option) *cobra.Command`

Returns the top-level command (default `Use: "secrets"`) with sub-commands
attached. Defaults derived from `cfg`:

- **kdbx path**: `cfg.File("secrets.kdbx")`.
- **socket**: `agent.SocketPath(cfg.AppName())`.
- **idle timeout**: 5m; **max lifetime**: 30m.
- **password-fd env var name**: derived from the app name (below).

The root command sets `SilenceUsage: true` (errors don't dump usage).

### Options

| Option | Effect |
| --- | --- |
| `WithName(name)` | Rename the top-level command (default `secrets`). Empty name ignored. |
| `WithKdbxPath(path)` | Relocate the KeePass store. Empty path ignored. |
| `WithTimeouts(idle, max)` | Override agent idle/max durations. Non-positive values leave the default. |

## Command tree

```
<name>                       (default: secrets)
├── get <key>                read a secret
├── list                     list secrets with created/updated timestamps
├── set <key> <value>        write a secret
├── delete <key>             delete a secret
└── agent                    manage the unlock agent
    ├── unlock  [--idle --max]   start the agent (prompts once)
    ├── lock                     stop the agent, drop cached secrets
    ├── status                   show whether an agent is running
    └── run     [--idle --max --kdbx]  run the agent in the foreground
```

Every secret-touching command goes through the agent. `store()` obtains a
`secrets.Store` backed by an `agent.Client`, starting an agent first if none is
running (implicit unlock — the password prompt appears once).

## Secret commands

| Command | Args | Output | Errors |
| --- | --- | --- | --- |
| `get <key>` | exactly 1 | prints the value + newline to **stdout** | `no secret found for key "<key>"` on `ErrNotFound`; `reading "<key>": <err>` otherwise |
| `list` | none | table `KEY / CREATED / UPDATED` (tab-aligned) to **stdout**, sorted by key ascending | propagates list errors |
| `set <key> <value>` | exactly 2 | `stored "<key>"` to **stderr** | propagates set errors |
| `delete <key>` | exactly 1 | `deleted "<key>"` to **stderr** | `no secret found for key "<key>"` on `ErrNotFound`; else propagates |

Timestamps in `list` are rendered as local time `2006-01-02 15:04:05`, or `-`
when nil/zero (`formatTime`).

Note the stdout/stderr split: machine-readable output (`get` value, `list` table)
goes to stdout; status messages (`stored`, `deleted`, prompts, agent notices) go
to stderr — so `get` can be captured cleanly in a shell substitution.

## Agent commands

### `agent status`

Prints `agent running (socket <path>)` or `no agent running (socket <path>)` to
**stdout**. Never starts an agent.

### `agent unlock [--idle d] [--max d]`

- If an agent is already running, prints `agent already running` (stderr) and
  returns.
- Otherwise starts a detached agent (`startAgentDetached`), prompting once.
- `--idle`/`--max` default to the configured timeouts.

### `agent lock`

- If no agent is running, prints `no agent running` (stderr) and returns.
- Otherwise sends `lock`; on success prints `agent locked` (stderr).

### `agent run [--idle d] [--max d] [--kdbx path]`

Runs the agent in the **foreground**. Normally invoked by the detached process
that `unlock` spawns, rarely by hand. The hidden `--kdbx` flag lets the parent
hand the child the exact store path regardless of how the app resolves settings.

`runAgent` behaviour:

1. Builds a **read-write** `KeepassDriver` for the store path.
2. If launched via the detached path, reads the password from the inherited pipe
   (`passwordFromPipe`) and installs it as `PromptPassword`; otherwise prompts on
   the terminal.
3. `Unlock`s the driver (fails here on a wrong password).
4. Calls `agent.Harden()` **after** unlock (so the memory-hard KDF's large
   transient allocation isn't forced under `mlock`); a hardening failure is
   logged as a warning, not fatal.
5. `Listen`s (bind-before-announce), installs SIGINT/SIGTERM → `Stop`, prints
   `agent listening on <sock> (idle …, max …)` to stderr, and `Serve`s until
   idle/max/lock/signal.

## Detached start (`startAgentDetached`)

How an agent is launched from an interactive command:

1. Prompt for the password on the terminal (no echo), reading from stdin.
2. Create an `os.Pipe`; the read end is passed to the child as **fd 3**
   (`ExtraFiles`), and the child is told its number via the per-app env var
   (`<APP>_SECRETS_AGENT_PW_FD=3`). The password travels over this inherited
   pipe — **never** via argv or the environment.
3. Re-invoke **this same binary** (`os.Executable()`) at the `agent run`
   sub-command. The argv is reconstructed from `runCmd.CommandPath()`
   (`childRunArgs`), so it routes correctly wherever the tree is mounted in the
   host app; `--idle`, `--max`, and `--kdbx` are passed explicitly.
4. Redirect the child's stdout/stderr to `<settings-dir>/agent.log`
   (`0o600`, truncated) so a startup failure is diagnosable.
5. Detach into its own session (`Setsid` on unix; no-op elsewhere) so it survives
   the parent shell exiting.
6. Write the password into the pipe and close it.
7. Reap the child in the background; then poll the socket up to 200×50ms (~10s):
   - Socket answers → print `agent unlocked (socket <path>)` (stderr) and return.
   - Child exits first → return `agent did not start: agent process exited (…)`
     with the tail of the agent log.
   - Timeout → `agent did not start: timed out waiting for agent socket` with the
     log tail.

### Password-fd env var name (`envFDName`)

Uppercases the app name and replaces every non-`[A-Z0-9]` rune with `_`, then
appends `_SECRETS_AGENT_PW_FD`. Empty result falls back to `APP`. E.g. `my-app`
→ `MY_APP_SECRETS_AGENT_PW_FD`.

## Platform notes

- `detachAttr()` returns `Setsid: true` on unix builds, an empty
  `SysProcAttr{}` elsewhere (the agent still runs but isn't reparented into its
  own session).

## Security properties (inherited)

- The master password is only ever passed to the child over an inherited pipe,
  keeping it out of `ps`/argv and the environment.
- The running agent applies the same peer-uid check, private socket, and
  hardening as the [`agent`](agent.md) package.
- Because the served driver is read-write, the agent holds the master key for its
  lifetime; the idle/max timeouts bound that window.
