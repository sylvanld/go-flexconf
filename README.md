# flexconf

A small Go toolkit for application configuration: a **settings** package that
resolves where an app keeps its config, a **secrets** store backed by a
password-protected KeePass (`.kdbx`) database, an **agent** that holds an
unlocked store for a few minutes so a single unlock covers repeated CLI calls,
and a **secretcli** factory that drops the whole secret manager into any app's
own [cobra](https://github.com/spf13/cobra) command tree.

## Packages

### `settings`

Resolves an application's settings directory.

```go
cfg, err := settings.New("flexconf")            // ~/.config/flexconf/
cfg, err := settings.New("flexconf",            // explicit override
	settings.WithPath("/etc/flexconf"))
```

- Default path comes from `os.UserConfigDir()` → `<user-config-dir>/<app_name>`
  (`~/.config/<app_name>/` on Linux, honoring `XDG_CONFIG_HOME`).
- `WithPath("")` is a no-op, so an unset override never clobbers the default.

| Member | Description |
| --- | --- |
| `New(appName, ...Option)` | Build settings; errors with `ErrEmptyAppName` on empty name. |
| `DefaultPath(appName)` | Package-level default path resolver. |
| `AppName()` / `Path()` | The app name and the resolved settings directory. |
| `DefaultPath()` / `IsDefault()` | The default path, and whether it is in use. |
| `File(name)` | Path to a file inside the settings directory. |
| `EnsureDir()` | `MkdirAll` the settings directory (`0o755`). |

### `secrets`

A `Store` façade over a pluggable `Driver`.

```go
store := secrets.NewStore(driver)

err          := store.SetValue("api/token", "s3cr3t")
value, err   := store.GetValue("api/token")   // *string
ok, err      := store.Has("api/token")
all, err     := store.List()
err           = store.Delete("api/token")
```

- The store lazily calls `Driver.Unlock()` before the first operation.
- `Set` stamps `UpdatedAt`, preserving `CreatedAt` for existing secrets.
- Missing keys return `secrets.ErrNotFound` (use `errors.Is`).

#### KeePass driver

Stores secrets as entries in a `.kdbx` file: key → entry title, value →
protected password, timestamps → entry creation/modification times.

```go
driver := secrets.NewKeepassDriver(cfg.File("secrets.kdbx"))
store  := secrets.NewStore(driver)
```

- `Unlock()` prompts for the database password on the terminal (no echo). Inject
  `driver.PromptPassword` to supply it another way (e.g. in tests).
- If the file does not exist, it is created as a new empty database protected by
  the entered password.
- Writes are atomic (temp file + rename).
- `ReadOnly` mode (optional): opens for reading only and, after unlock, **drops
  the master credentials** from memory (so the master key is not retained);
  `Set`/`Delete` then return `secrets.ErrReadOnly`, and a missing file is an error
  rather than being created. Useful when you want a driver that provably cannot
  write and does not hold the master key.

### `agent`

A background process that holds an already-unlocked `secrets.Driver` and serves
its operations over a per-user Unix socket, plus a `Client` that *is* a
`secrets.Driver` forwarding calls to it. Because both sides speak `Driver`, the
CLI just does `secrets.NewStore(agentClient)` and the agent is transparent — and
because the agent serves writes too, a single unlock covers every command.

```go
sock := agent.SocketPath("example")      // $XDG_RUNTIME_DIR/example/agent.sock
srv  := agent.NewServer(driver, sock)    // driver already Unlock()-ed (read-write)
srv.IdleTTL, srv.MaxLifetime = 5*time.Minute, 30*time.Minute
go srv.Serve()

client := agent.NewClient(sock)          // implements secrets.Driver
store  := secrets.NewStore(client)       // get/set/list/delete all go over the socket
```

- Reads and writes both go through the agent. To serve writes the agent must keep
  the master credentials in memory for its lifetime (needed to re-encrypt the
  file).
- Expiry: idle timeout (resets on use) **and** an absolute lifetime cap; either
  firing drops the driver, removes the socket, and exits. `Client.Lock()` stops
  it early.
- Access control: socket dir `0700`, socket `0600`, and on Linux the peer's uid
  is verified via `SO_PEERCRED` (same-user only). `agent.Harden()` best-effort
  disables core dumps and locks memory out of swap.

#### Security note

The agent trades a little safety for convenience: decrypted secrets **and the
master key** live in memory for the TTL window instead of milliseconds. It
defends against *other* users (peer-uid check, private socket) and against
secrets reaching disk (no core dumps, `mlockall`), and it keeps the window short.
It does **not** — and cannot — defend against code running as **you** (same-user
`ptrace`, keylogging) or root; and Go strings can't be reliably wiped, so a short
TTL, not memory scrubbing, is the real mitigation. On a shared or higher-stakes
host, prefer an OS keyring (Secret Service / `gpg-agent`) over this bespoke agent.
(If you would rather the agent never hold the master key, run it with the
`secrets.KeepassDriver` `ReadOnly` mode and keep writes out of the agent — the
opposite convenience/safety trade.)

### `secretcli`

A factory that builds the whole secret manager as a cobra command tree, ready to
mount as a sub-command of any application's CLI. It wires `settings` + `secrets`
+ `agent` together for you, so the KeePass file, socket, and agent log all live
under the app's own config directory.

```go
cfg, _ := settings.New("myapp")             // ~/.config/myapp/
root := &cobra.Command{Use: "myapp"}
root.AddCommand(secretcli.New(cfg))         // adds "myapp secrets ..."
root.Execute()
```

The factory takes the app's resolved `*settings.Settings`; options adapt it:

| Option | Effect |
| --- | --- |
| `WithName("vault")` | Rename the top-level command (default `secrets`). |
| `WithKdbxPath(path)` | Relocate the store (default `<settings-dir>/secrets.kdbx`). |
| `WithTimeouts(idle, max)` | Change the agent idle timeout / absolute lifetime. |

The command tree:

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
    └── run     [--idle --max]   run the agent in the foreground
```

- Every command that touches secrets goes through the agent. If none is running,
  one is **started implicitly** — you are prompted for the password once, then it
  is cached until the agent locks.
- `agent unlock` starts it explicitly; `agent lock` stops it; `agent run` is the
  foreground process the detached unlock spawns (rarely run by hand).
- Starting the agent prompts in your shell, then spawns a detached process,
  re-invoking this binary at `… agent run` and passing the password over an
  inherited pipe (never argv/env). The command path is reconstructed at runtime,
  so it works wherever the tree is mounted in the host app.

## Example app

[`cmd/example`](cmd/example/main.go) is a minimal cobra app that mounts
`secretcli.New(cfg)` under `example secrets`:

```console
$ example secrets set api/token s3cr3t   # no agent yet → implicit unlock (prompts once)
Password for ~/.config/example/secrets.kdbx:
agent unlocked (socket ~/.local/run/example/agent.sock)
stored "api/token"

$ example secrets get api/token          # agent already running — no prompt
s3cr3t

$ example secrets list                   # keys with timestamps (when available)
KEY        CREATED              UPDATED
api/token  2026-07-17 19:27:06  2026-07-17 19:27:06

$ example secrets agent status
agent running (socket ...)

$ example secrets agent lock             # stop the agent, drop cached secrets
```

Set `EXAMPLE_CONFIG=/some/dir` to point it at a scratch config directory
(otherwise it defaults to `~/.config/example/`).

## Development

```console
go build ./...
go vet ./...
go test ./...
```

> **Note:** if `/tmp` is mounted `noexec`, run tests with a writable-and-exec
> temp dir, e.g. `GOTMPDIR="$HOME/.cache/gotmp" go test ./...`.
