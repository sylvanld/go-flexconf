# CLI & Secret Agent (`flexcli`)

- **Status:** 📝 Draft
- **Scope:** the `flexcli` package — a mountable Cobra command group `secret`
  (`init`/`unlock`/`lock`/`get`/`set`/`list`/`vaults`) — its two entry points (**embedded**
  in an app's CLI; and a **standalone `flexconf` binary** for global/personal
  vaults), both backed by the same vault registry ([vaults.md](vaults.md)), and
  the background **secret agent** that
  holds an unlocked vault in memory (ssh-agent style) with an idle auto-lock.
  Non-secret config-loading commands, shell completion, and non-Unix transports
  are out of scope (§12).

`flexcli` turns the `flexvault` Manager (see [vault-drivers.md](vault-drivers.md))
into a set of ergonomic terminal commands. It has **two entry points that share
the exact same command group** (§4):

1. **Embedded** — an application mounts the `secret` group into its own
   [Cobra](https://github.com/spf13/cobra) CLI; vault definitions come from the
   vault registry ([vaults.md](vaults.md)), with the app optionally pinning a
   default vault (`myapp secret get artifactory/token`).
2. **Standalone** — the module ships a `flexconf` binary that mounts the same
   group against the same registry (default: the user's global default vault), for
   managing personal vaults independent of any app
   (`flexconf secret set personal/github-token`).

Because each CLI invocation is a separate short-lived process, unlocking once per
command would re-prompt for the master password every time. `flexcli` solves this
the way `ssh-agent` / `gpg-agent` do: the first `unlock` spawns a **detached agent
process** that holds the *unlocked* vault in memory and serves subsequent
`get`/`set`/`list` over a private socket, auto-locking after an idle period (§6).

## 1. Package & binary placement

`flexcli` is a top-level package of the module, layered **above** `flexvault`; the
standalone binary lives at `cmd/flexconf`:

```
cmd/flexconf → flexcli → flexvault → flexprompt   (+ flexcli → github.com/spf13/cobra)
```

- `flexcli` imports `flexvault` (Manager, registration, decoders, `Initializer`),
  `flexprompt` (the CLI prompter), and Cobra. Nothing imports `flexcli`.
- It is **optional**: an app that only needs programmatic secret access uses
  `flexvault` directly and never pulls in Cobra.
- `cmd/flexconf` is a thin `main` that wires `flexcli` to the global config (§5).

See [overview.md](overview.md) "Module layout".

## 2. Configuration model: where the vault config comes from

Both entry points read vault definitions from the **same single source** — the
vault **registry** (`~/.config/flexconf/vaults.yaml`, layered per
[vaults.md](vaults.md) §3). Neither reads vault config from an application's own
`config.yaml`: an app's config holds app settings and `$(secret:…)` tokens only,
and the registry is the **sole** home for vault definitions (there is no
"integrated mode" in which an app bundles vault config).

The two entry points therefore differ only in:

| | Embedded (`myapp secret …`) | Standalone (`flexconf secret …`) |
|---|---|---|
| Binary / mount | the `secret` group mounted into the app's own CLI | the shipped `flexconf` binary |
| Vault **config** source | the vault registry ([vaults.md](vaults.md)) | the **same** vault registry |
| Which vault is default | the app MAY pin one via `Options.DefaultVault`; else the registry's `default:` | the registry's `default:` |
| Vault **secret** (master pw) | prompted at unlock (never in config) | prompted at unlock (never in config) |

In both, the root `--vault <name>` flag (§4) selects a named vault, overriding the
default. A vault definition is always a static registry entry: it MUST NOT contain
any templating tokens (`$(...)`) — see the static-definitions rule in
[vaults.md](vaults.md) §2.3.

## 3. Embedded entry point

The application constructs one `flexcli.App`, mounts its command group, and —
critically — lets it intercept agent re-execs at the top of `main`:

```go
func main() {
    app := flexcli.New(flexcli.Options{
        DefaultVault: "myapp",   // optional: pin a registry vault as this app's default
        // IdleTimeout defaults to 2 * time.Minute
    })

    // MUST be the first thing in main: if THIS process was spawned as the
    // agent, run the agent loop and exit; otherwise return immediately.
    app.RunAgentIfRequested()

    root := &cobra.Command{Use: "myapp"}
    root.AddCommand(app.SecretCommand())              // adds the "secret" group
    _ = root.Execute()
}
```

```go
package flexcli

type Options struct {
    // DefaultVault pins which registry vault is used when the --vault flag is not
    // given. Empty → the registry's own default: (vaults.md §4). The named vault
    // (and its driver + non-secret config) is always read from the registry
    // (vaults.md); an app never supplies vault config here.
    DefaultVault string

    // VaultID overrides the socket/agent identity for the selected vault. Empty →
    // derived deterministically from the vault NAME + a fingerprint of its
    // resolved config (§6.3, vaults.md §6). Distinct vaults get distinct agents.
    VaultID string

    // IdleTimeout is how long the agent stays unlocked with no request before it
    // auto-locks and exits. Zero → 2 * time.Minute. Negative → never idle-locks
    // (discouraged; documented escape hatch).
    IdleTimeout time.Duration

    // Prompter overrides the credential prompter used by init/unlock. Nil → a
    // flexprompt CLI prompter (masked secret input). The agent NEVER prompts.
    Prompter flexprompt.Prompter
}

// New returns an App bound to opts.
func New(opts Options) *App

// RunAgentIfRequested MUST be called at the very start of main(). If the current
// process was spawned by flexcli as an agent, it runs the agent to completion and
// calls os.Exit; otherwise it returns immediately and does nothing.
func (a *App) RunAgentIfRequested()

// SecretCommand returns the "secret" Cobra command group to mount under the
// app's root command.
func (a *App) SecretCommand() *cobra.Command
```

> **Naming.** The canonical command group is `secret` and the terms "agent" /
> "unlock" / "lock" mirror `ssh-agent` deliberately, so the mental model
> transfers. `secrets` is registered as a Cobra **alias** of `secret`, so both
> `myapp secret get …` and `myapp secrets get …` work; because Cobra lists an
> alias only on the command's own help line (not as a separate entry in the
> parent's command list), this adds no duplicate group to `--help`. The group
> name is overridable via `Options` (non-normative) if it collides.

## 4. The `secret` command group

Both entry points expose the identical group. All commands share the agent (§6).
Errors go to stderr, requested secret data to stdout; exit codes follow the
scheme in §9 (`0` success / `1` generic / `2` usage / `3` locked).

**Vault selection — the `--vault` flag.** `--vault <name>` is a **persistent flag
at the CLI root**, inherited by every `secret` subcommand
(`get`/`set`/`list`/`unlock`/`lock`/`status`). It names a vault in the registry
([vaults.md](vaults.md)) and **overrides** the default vault
(`Options.DefaultVault`, else the registry's `default:`). It is the **single**
mechanism for choosing a vault — there is no positional vault argument on any
subcommand. `myapp --vault work secret get deploy/key` and
`flexconf --vault work secret unlock` are the forms.

### `secret init`

Create a **new, empty** vault (requires a driver implementing `flexvault.Initializer`,
i.e. `Capabilities().Creatable`; otherwise fails with `ErrUnsupported`).

- Builds and `Configure`s the driver, calls `Manager.Create`: runs
  `InitCredentials()` and prompts **in the terminal** for the setup secrets — for
  KeePass a new master password entered **twice** (`PromptRequest.Confirm`).
- MUST fail if the vault already exists (no clobber); `--force` is **not**
  provided for `init` in v1 (deleting a vault is a destructive, explicit act left
  to the user).
- Does not leave an agent running; the user runs `unlock` next.
- Primary use is standalone (`flexconf secret init`) to bootstrap a personal
  KeePass file, but it works embedded too when the app's driver is `Creatable`.

### `secret unlock`

Targets the vault selected by `--vault` (§4), else the default vault. There is no
positional vault argument. `flexconf --vault work secret unlock` and
`flexconf secret unlock` (default) are the standalone forms.

1. Resolve the selected (or default) vault to a driver + non-secret config **from
   the registry**, build the driver (`flexvault.New(...)`) and `Configure` it —
   **in the foreground process** (has the TTY).
2. Call `driver.Credentials()` and run the CLI prompter's `Dispatch` to collect
   the answers **in the terminal** (master password masked).
3. Ensure an agent is running for this `VaultID` (§6.2 spawn): if none, spawn one
   and wait for its socket.
4. Send `unlock{answers}` to the agent, keyed to its `VaultID`. The agent builds
   its *own* Manager, reading the vault's non-secret config **from the registry it
   re-reads itself** (§6.2) — the config is not forwarded — then `Unlock`s with the
   supplied answers; on success it holds the vault unlocked and starts its idle
   timer.
5. Wipe the answers from the foreground process and report success (and, by
   default, the idle timeout, e.g. `unlocked; agent will lock after 2m idle`).

Re-running `unlock` while an agent already holds the vault is a no-op that
succeeds (`--force` re-prompts and re-unlocks). Retry-on-`ErrAuth` follows the
Manager policy (vault-drivers §5); on `ErrPromptCancelled` it aborts without
spawning/keeping an agent.

### `secret lock`

- Connect to the agent for this `VaultID` and request graceful shutdown: the
  agent calls `Manager.Lock()` (clearing key material, vault-drivers §2) and
  exits, removing its socket.
- If no agent is running, it is a **no-op** that reports "no agent running" and
  exits `0`.
- If graceful shutdown cannot be delivered (socket present but unresponsive), fall
  back to terminating the agent by its recorded PID (§6.2), then remove the stale
  socket. Prefer graceful so the agent can wipe memory first.

### `secret get <namespace/key>`

- Validate the address (exactly two non-empty segments, vault-drivers §6).
- If an agent is running: send `get{addr}`; print the secret value to stdout.
- If **no agent is running**: see §4.1 (auto-unlock), then retry once.
- A trailing newline is added by default for readability; `--raw` suppresses it
  (safe for piping). Secret values MUST NOT be logged.
- **Multi-line values.** A secret may itself contain newlines (a PEM key, a
  multi-line token). The default trailing newline is then ambiguous and
  line-oriented shell parsing (`read`, `$(...)`) truncates at the first newline;
  callers handling possibly-multi-line secrets MUST use `--raw` and consume the
  bytes verbatim. `--raw` emits the exact stored value with nothing added or
  stripped, so `get --raw | set` round-trips losslessly.

### `secret set <namespace/key>`

- The value is read from **stdin**, never from an argv flag, so it does not land
  in shell history or the process table (`echo -n s | myapp secret set a/b`).
- Requires a writable vault: if `Capabilities().Writable` is false the agent
  returns `ErrReadOnly` and the command fails without prompting.
- If no agent is running: auto-unlock (§4.1), then retry once. `Set` relies on the
  agent retaining the composite key for the session (vault-drivers §2), which it
  does because it stays unlocked.

### `secret list [namespace]`

- No argument → prints the namespaces. With a namespace → prints the key names in
  it (vault-drivers §6). One name per line for easy piping.
- If no agent is running: auto-unlock (§4.1), then retry once.

### `secret vaults`

Show the **resolved vault registry** — which registry files were consulted, in
what order, and the merged result — so a user can see *why* a vault name maps to
a given backend. It reads only the registry ([vaults.md](vaults.md) §2–§4); it
does **not** build drivers, contact the agent, or unlock anything.

- Prints, in order:
  1. the **registry files** consulted (§5.1, [vaults.md](vaults.md) §3), each
     marked `[ok]` / `[missing]` and annotated with its source (the well-known
     file, or its position in `FLEXCONF_VAULTS`);
  2. the resolved **`default:`** and which file set it;
  3. each **vault**: name, `driver`, its config keys, and the file it was defined
     in — noting when a later file **overrode** an earlier definition of the same
     name (whole-entry replacement, [vaults.md](vaults.md) §3).
- `--format yaml` emits the merged registry as a machine-readable YAML document
  (the plain resolved registry, suitable to save back as a `vaults.yaml`);
  provenance annotations appear only in the default human format. (Other output
  formats are out of scope for now; they can be added later.)
- **Dump-only by default:** the command is informational and **always exits 0**.
  Problems it notices — a `default:` not present in the merged `vaults:` map, a
  `driver` that is not registered, a file named in `FLEXCONF_VAULTS` that could
  not be read, or an empty registry — are reported as `note:` lines but do
  **not** fail the command.
- **`--validate` opts into enforcement.** With `--validate`, those same problems
  become **errors** and the command exits **non-zero** — making it usable as a CI
  check on a registry. Without the flag the checks are informational only. (These
  conditions also fail loudly when a token actually resolves against the registry
  at load time, per [vaults.md](vaults.md) §4; `--validate` just brings that
  check forward without resolving anything.)
- Output is safe to share/paste: the registry never contains credentials
  ([vaults.md](vaults.md) §2.1), so there is nothing to redact.

```console
$ flexconf secret vaults
registry files (in order):
  1. ~/.config/flexconf/vaults.yaml   [ok]
  2. ./project.vaults.yaml            [ok]   (FLEXCONF_VAULTS #2)

default: personal   (set by #1)

vaults:
  personal   driver=keepass  path=~/.local/share/flexconf/personal.kdbx   (from #1)
  work       driver=keepass  path=~/work/secrets.kdbx                     (from #2, overrides #1)
```

### 4.1 Auto-unlock behavior for get/set/list

When `get`/`set`/`list` find no running agent (vault locked):

- **Interactive TTY:** prompt `Vault is locked. Unlock now? [y/N]`. On yes, run
  the `unlock` flow (spawn agent + prompt for credentials), then retry the
  original request exactly once. On no, exit non-zero with a locked error.
- **Non-interactive (no TTY, or `--no-unlock`):** do **not** prompt; fail with a
  clear error directing the user to run `secret unlock` (or to run in a TTY).
  This prevents scripts/daemons from hanging (mirrors the `flexprompt` "no
  default CLI prompter" rule, [prompter.md](prompter.md) §2).
- `--unlock` forces the unlock attempt without asking; `--no-unlock` forces the
  failure path. A missing answer defaults to **No**.

## 5. Standalone `flexconf` binary & global config

`cmd/flexconf` is a minimal `main` that mounts the same `secret` group against a
**global vault config**:

```go
func main() {
    root := &cobra.Command{Use: "flexconf"}

    opts, err := flexcli.GlobalOptions() // env + defaults; vault defs from the registry
    if err != nil { /* report and exit */ }

    app := flexcli.New(opts)
    app.RunAgentIfRequested()            // first, as always
    root.AddCommand(app.SecretCommand()) // registers the --vault persistent root flag (§4)
    _ = root.Execute()
}
```

```go
// GlobalOptions builds Options for the standalone flexconf CLI (and for any app
// that wants to expose the user's global vault). Vault DEFINITIONS always come
// from the vault registry (vaults.md); GlobalOptions itself only carries process
// options (idle timeout, prompter). Which vault is used is the registry's
// default: (vaults.md §4), overridden at runtime by the --vault root flag (§4).
//
// As an escape hatch for running WITHOUT a registry file, --driver plus driver
// flags (e.g. --path), or the corresponding FLEXCONF_* env vars, synthesize an
// anonymous ad-hoc vault for one-off use (§5.1, CLI precedence flags > env >
// registry). The agent reconstructs a registry-defined vault by re-reading the
// registry; an ad-hoc vault is reconstructed from the same flags/env forwarded
// through the re-exec marker.
func GlobalOptions() (Options, error)
```

### 5.1 Global config file

The global vault definitions are the **vault registry** specified normatively in
[vaults.md](vaults.md); this section only describes how the standalone CLI reads
it.

- **Location:** `$XDG_CONFIG_HOME/flexconf/vaults.yaml` (default
  `~/.config/flexconf/vaults.yaml`); the file is YAML. See
  [vaults.md](vaults.md) §2.2. (CLI-only preferences, if any, stay in a separate
  `config.yaml`; vault definitions live only in the registry.)
- **Named vaults** so a user can keep several; each is a driver + its non-secret
  config, with a top-level `default:`. Master passwords are **never** stored
  here. Layering across registry files follows [vaults.md](vaults.md) §3.

```yaml
default: personal
vaults:
  personal:
    driver: keepass
    path: ~/.local/share/flexconf/personal.kdbx   # static; no $(...) tokens (vaults.md §2.3)
    readonly: false
  work:
    driver: keepass
    path: ~/work/secrets.kdbx
    readonly: true
```

- **Selection:** the `--vault work` root flag (§4) (or `FLEXCONF_VAULT=work`),
  else the registry's `default:` name.
- **Ad-hoc vault without a registry file:** `--driver` and driver flags (e.g.
  `--path`), or `FLEXCONF_*` env vars, synthesize an **anonymous ad-hoc vault** so
  `flexconf` runs with zero registry file — enough to `init`/`unlock` a one-off
  KeePass file. Precedence is **flags > env > registry**; when a named registry
  vault is used, the registry is authoritative.
- **Defaults:** driver `keepass`; secret file under
  `$XDG_DATA_HOME/flexconf/<vault>.kdbx` when no path is given.

### 5.2 Typical standalone bootstrap

```console
$ flexconf secret init                    # create ~/.local/share/flexconf/personal.kdbx
New KeePass master password: ****
Confirm master password:     ****
created vault "personal"

$ flexconf secret unlock
KeePass master password: ****
unlocked; agent will lock after 2m idle

$ echo -n 'ghp_xxx' | flexconf secret set personal/github-token
ok
$ flexconf --vault work secret get deploy/key
```

Each profile gets its own `VaultID`, hence its own agent — `flexconf` (personal),
`flexconf --vault work`, and `myapp` never share an unlocked vault.

## 6. The agent

### 6.1 What it is

A detached child process that:

- constructs and holds **one** unlocked `flexvault.Manager` for a given `VaultID`;
- listens on a private Unix domain socket and serves
  `get`/`set`/`list`/`lock`/`status` requests;
- resets an idle timer on every served request and, after `IdleTimeout` with no
  activity, calls `Manager.Lock()` and exits;
- exits (after `Lock`) on `lock`, on `SIGTERM`/`SIGINT`, and on socket loss.

The agent **never prompts**: it only ever receives already-collected `answers`
from a foreground `unlock`. `init` does **not** use the agent (it neither unlocks
nor stays resident).

### 6.2 Spawning — self-exec, no second binary

`flexcli` does not ship a separate agent binary. The foreground process
re-executes **itself** (`os.Args[0]`) with a reserved marker in the environment
(e.g. `FLEXCLI_AGENT=<vaultID>` plus the socket path), fully detached from the
controlling terminal (new session; stdio → `/dev/null`). The re-exec'd process
hits `App.RunAgentIfRequested()` at the top of `main`, sees the marker, and runs
the agent loop. This is why `RunAgentIfRequested()` MUST be first in `main`.

The agent reconstructs its vault's non-secret config by **re-reading the registry
itself** (`vaults.yaml`, [vaults.md](vaults.md)) for the `VaultID`'s vault — a
durable file, so reproduction is automatic and no vault config is forwarded over
the socket. (For an anonymous **ad-hoc** vault (§5.1) there is no registry entry,
so the defining `--driver`/`--path`/`FLEXCONF_*` values are carried through the
re-exec marker instead.)

Spawn is race-safe: the spawner takes an exclusive `flock` on a per-`VaultID` lock
file around "check socket → spawn → wait for socket". If two commands race, the
loser finds the socket already bound and simply connects. A second agent for the
same `VaultID` MUST fail to bind and exit.

**Startup-failure reporting.** The foreground `unlock` waits for the socket to
appear with a bounded timeout. If the detached agent dies during startup (e.g. the
runtime dir is unwritable), it writes a one-line fatal message to
`agent-<VaultID>.err` (next to the socket) **before** exiting; the foreground
reads that file and reports it. On timeout with **no** error file present, the
foreground reports a generic `agent failed to start`. The `.err` file is removed
on the next successful start.

### 6.3 Socket location, identity, and cleanup

- **Location:** under a user-private runtime dir — `$XDG_RUNTIME_DIR/flexconf/`
  when set, else `$TMPDIR/flexconf-$UID/` — created `0700`. Socket mode `0600`,
  path `agent-<VaultID>.sock`; PID recorded in `agent-<VaultID>.pid`.
- **VaultID:** `Options.VaultID` if set, else `"<name>-<fp>"` where `fp` is the
  **first 8 hex characters of `SHA-256(canonical(resolvedVaultConf))`** and
  `canonical` is the resolved non-secret `VaultConf` serialized as YAML with keys
  sorted (secret material excluded). E.g. vault `work` → `work-3f9a1c02`, socket
  `agent-work-3f9a1c02.sock`. The name makes sharing an agent explicit
  (`--vault work` targets the `work` agent); the fingerprint keeps a same-named
  but differently-resolved vault (e.g. a project-local override) on its own agent.
  MUST NOT include secret material. See [vaults.md](vaults.md) §6. (This
  supersedes the earlier config-hash-only default.)
- **Stale sockets:** on startup the agent unlinks a pre-existing socket only after
  confirming nothing answers (connect → refused). On clean exit it removes its
  socket and PID file.

### 6.4 Transport & protocol

- **Transport:** Unix domain socket, same-user only. The agent MUST verify the
  peer UID (e.g. `SO_PEERCRED`) matches its own and reject others, in addition to
  filesystem permissions.
- **Framing:** length-prefixed messages, one request → one response. The wire
  encoding is an unexported implementation detail (JSON in v1) — clients only ever
  use the Cobra commands.
- **Message set (conceptual):**

  | Request | Payload | Response |
  |---------|---------|----------|
  | `unlock` | `answers` map only (config re-read from registry, §6.2) | ok / `ErrAuth` |
  | `get`    | `addr`                        | value / `ErrNotFound` / `ErrLocked` |
  | `set`    | `addr` + value                | ok / `ErrReadOnly` / `ErrLocked` |
  | `list`   | `namespace`                   | `[]string` / `ErrLocked` |
  | `status` | —                             | unlocked?, `VaultID`, idle-remaining, `Capabilities` |
  | `lock`   | —                             | ok, then the agent exits |

- Every request except `status` resets the idle timer. Errors carry codes mapping
  back to the `flexvault` sentinels so the CLI can `errors.Is` them.
- The agent **serializes** requests — it handles one at a time, matching the
  single-owner write model ([vault-drivers.md](vault-drivers.md) §10a). Concurrent
  client connections are accepted but processed sequentially, so a `set` never
  races another `set`/`get` on the same backend.

### 6.5 Idle auto-lock

- Default `IdleTimeout` is **2 minutes**; configurable via `Options.IdleTimeout`,
  overridable at unlock time with `--idle <dur>`, or via the
  `FLEXCONF_IDLE_TIMEOUT` env var (a Go duration string, e.g. `5m`). Precedence:
  `--idle` flag > `FLEXCONF_IDLE_TIMEOUT` > `Options.IdleTimeout` > the 2m default.
- The timer resets on each served `get`/`set`/`list`/`unlock`. On expiry the agent
  calls `Manager.Lock()` (clears key material) and exits, removing its socket. A
  later `get` then follows the auto-unlock path (§4.1).
- `secret status` reports remaining idle time without resetting it.

## 7. Multiple vaults

A single `secret` **command** targets **one** vault per invocation, selected via
the `--vault` root flag (§4), defaulting to the registry's default vault
([vaults.md](vaults.md) §4). Each named vault gets its own `VaultID`, hence its
own agent (§6.3), so vaults coexist without colliding.

Config **loading** (as opposed to the CLI) already addresses *multiple* vaults
in one pass: each `$(secret:vault:...)` token selects its vault by name
([vaults.md](vaults.md) §5), resolving against as many agents as it references.
A multi-vault-in-one-**command** CLI UX (e.g. bulk operations across vaults)
remains deferred.

## 8. Normative security requirements

- The agent socket MUST be user-private: `0700` parent dir, `0600` socket, **and**
  a peer-UID check; connections from other UIDs MUST be rejected.
- Secret values and credentials MUST NOT be logged, echoed, or placed in argv, in
  the environment of long-lived processes, or in error messages — on either side
  of the socket. `secret get` writes only the requested value to stdout; `secret
  set` reads the value from stdin, never argv.
- Master-password entry MUST be masked (via the `flexprompt` CLI prompter,
  `Secret: true`); `init` uses double-entry (`Confirm: true`) for new passwords.
- Plaintext credentials forwarded to the agent MUST be wiped in the foreground
  process after sending; the agent MUST follow the `flexvault` plaintext-vs-
  derived-material rule (discard plaintext after key derivation; clear all on
  `Lock`, vault-drivers §2).
- The agent MUST auto-lock on idle timeout and on termination signals, calling
  `Manager.Lock()` so key material is cleared before exit.
- `secret lock` SHOULD prefer graceful shutdown over `SIGKILL`; PID-kill is the
  fallback for an unresponsive agent only.
- `secret init` MUST NOT overwrite an existing vault.
- **Future hardening (noted, not required in v1):** `mlock` the agent's secret
  pages to keep them out of swap; move secret material to `[]byte` (ties to the
  `string`-in-v1 decision, vault-drivers §7).

## 9. Errors & exit codes

- The CLI maps agent errors back to the `flexvault` sentinels (`ErrLocked`,
  `ErrNotFound`, `ErrReadOnly`, `ErrAuth`, `ErrUnsupported`) and the `flexprompt`
  sentinels (`ErrPromptCancelled`, `ErrNoPrompter`) for accurate messages and
  testable `errors.Is` behavior.
- Exit codes: **`0`** success; **`1`** generic failure; **`2`** usage error
  (Cobra default); **`3`** locked / not-unlocked (so scripts can branch and run
  `unlock`, then retry). No separate not-found code in v1 (a missing secret is a
  generic `1`). These are stable and mirrored in _errors.md_.

## 10. Typical embedded session

```console
$ myapp secret unlock
KeePass master password (secrets.kdbx): ****
unlocked; agent will lock after 2m idle

$ myapp secret list
artifactory
database

$ myapp secret get artifactory/token
dckr_pat_xxx…                       # value only, to stdout

$ echo -n 's3cr3t' | myapp secret set database/password
ok

$ myapp secret lock
locked; agent stopped
```

Inside another program the same values resolve transparently via
`$(secret:artifactory/token)` (see [overview.md](overview.md) §4) — the resolver
uses the same running agent if present, or unlocks per the loader's policy.

## 11. Resolved decisions

- **Two entry points, one command group** — embedded (`app.SecretCommand()`) and
  standalone (`cmd/flexconf`, via `GlobalOptions`) build the identical `secret`
  group, both backed by the **vault registry** (no app-bundled vault config); they
  differ only in binary/mount and which vault is default (§2–§5).
- **Vault selection** — `--vault <name>` persistent root flag overrides the
  default vault; no positional vault argument (§4, Decision).
- **Global config** — user-level vault **registry** at
  `~/.config/flexconf/vaults.yaml` ([vaults.md](vaults.md)); precedence
  flags > env > registry files > defaults; secrets never stored (§5).
- **Vault creation** — `secret init` via `flexvault.Initializer` + double-entry
  new password (§4, vault-drivers §4).
- **Agent** — self-exec detached process, per-`VaultID` private socket, 2-minute
  idle auto-lock, auto-unlock prompt on TTY only (§4.1, §6).
- **Registry inspection** — `secret vaults` dumps the resolved registry and file
  provenance (dump-only, always exits 0); `--validate` opts into enforcement and
  a non-zero exit for CI use (§4). This resolves the registry-inspection item
  deferred in [vaults.md](vaults.md) §8.

## 12. Out of scope / deferred

- **Config-loading commands** (dumping/validating the app's own config) — later.
- **Non-Unix transports** — v1 targets Unix domain sockets; Windows named pipes
  (or AF_UNIX on modern Windows) are deferred.
- **Shell completion, man pages** — standard Cobra features, not specified here.
- **Resolver ↔ agent integration** — how the `secret:` resolver discovers and
  reuses a running agent during a normal `Load` is deferred to _resolvers.md_.
- **Vault deletion / rekey** — not in v1 (`init` never clobbers).
- **Multi-vault-in-one-command UX** — see §7 and vault-drivers §12.
