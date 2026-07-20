# flexconf

A Go toolkit for application configuration. Its centerpiece is a **config
loader** that turns a templated YAML file into your own typed struct, resolving
`$(env:…)`, `$(secret:…)` and `$(config:…)` references against a parsed node
tree — never raw text — and tracking secret-sourced values so any config dump
redacts them.

Around it sit the pieces that make that practical: a **settings** package that
resolves where an app keeps its config, a **secrets** store backed by a
password-protected KeePass (`.kdbx`) database, an **agent** that holds an
unlocked store for a few minutes so a single unlock covers repeated CLI calls,
and two ready-made [cobra](https://github.com/spf13/cobra) command trees that
drop the secret manager and the config-file generator into any app's own CLI.

```sh
go get github.com/sylvanld/go-flexconf
```

**New here? Start with the [quickstart](docs/quickstart.md).** Per-component
behaviour specs live in [`docs/specs/`](docs/specs/README.md).

## The loader

The root package `github.com/sylvanld/go-flexconf` is a flat façade over
`internal/loader`, so one import reaches everything.

```go
cfg, err := flexconf.NewAppConfig("myapp")   // ~/.config/myapp/
var c Config
err = flexconf.Load(cfg, &c)                 // locate → read → parse → template → decode
```

```yaml
# ~/.config/myapp/config.yaml
http:
  base_url: $(env:BASE_URL:-https://api.example.com)
  port: $(env:PORT:-8080)          # decodes into an int
  token: $(secret:api/token)
logging: $(config:logging.yaml)    # splices in another file
```

`Config` is a plain struct with `yaml` tags and native Go types — no wrapper
types. Templating happens on the parsed tree, so an injected value can never
change the document's structure.

| Member | Description |
| --- | --- |
| `NewAppConfig(name, ...)` / `WithAppPath(p)` | Build the app context (re-exports `settings.New` / `WithPath`). |
| `Load(cfg, out, ...Option)` | Full pipeline, decoding into `out`. |
| `LoadFile(cfg, ...Option)` | Same, stopping before decode; returns a `Settings`. |
| `Settings` | A located, templated, secret-resolved block, decoded on demand. |
| `Defaults(v)` | A `Settings` whose fallback tree is a pre-populated value. |
| `PolymorphicSettings[I]` / `NewPolymorphicSettings` | Pick a block's concrete type from a discriminator field. |
| `RegisterSecretDriver(name, factory)` | Add a `$(secret:…)` backend. |
| `Redacted` | What a secret-sourced scalar renders as in a dump. |

Options: `WithConfigFile`, `WithEnv`, `WithFS`, `WithSecretStore`,
`WithSecretResolver` — every input is injectable, so tests never touch the real
filesystem, environment, or secret store.

### Choosing the config file

1. `flexconf.WithConfigFile("/path/config.yaml")` — explicit override.
2. `cfg.File("config.yaml")` — always that name, under the config directory.

The config **directory** is what moves, and it is resolved once, by
`NewAppConfig`: `WithAppPath` → `<APP>_CONFIG` (uppercased app name,
non-alphanumerics → `_`) → `~/.config/<app>/`. So `<APP>_CONFIG` names a
directory, never a file, and everything derived from it relocates as a unit:

```sh
MYAPP_CONFIG=/etc/myapp   →  /etc/myapp/config.yaml
                             /etc/myapp/secrets.kdbx
```

### Defaults

Declare defaults as a pre-populated value of your own config type — it is
type-checked against the struct it defaults, so it cannot drift the way a
parallel YAML blob or a second set of struct tags does. Pass it to `Load` and
the file overrides only the keys it names:

```go
c := defaultConfig().(*Config)
err := flexconf.Load(cfg, c)   // config.yaml wins per key
```

Lazily-decoded blocks carry their default via `flexconf.Defaults(&HTTPConfig{…})`;
polymorphic ones via a factory returning a pre-populated variant.

### Redaction

Secret-sourced values are tainted through the whole pipeline, structurally
rather than by field allowlist, so you cannot forget to mark a new secret field:

```go
loaded, err := flexconf.LoadFile(cfg)
dump, _ := loaded.Dump()       // token: "«redacted»"
err = loaded.Decode(&c)
```

### Where `$(secret:…)` resolves

In precedence order: an injected `*secrets.Store` (`WithSecretStore`), a
`secrets:` block in the config file (templated env-only, and stripped before
your struct decodes), or — with neither — the zero-config **agent → read-only
keepass** default. Built-in drivers: `agent`, `keepass`, `env`, `exec`, `none`.

## Packages

### `settings`

Resolves an application's settings directory. `AppConfig` is the *app context*
(where config lives), as distinct from `flexconf.Settings` (what was loaded).

```go
cfg, err := settings.New("flexconf")            // WithPath → FLEXCONF_CONFIG → ~/.config/flexconf/
cfg, err := settings.New("flexconf",            // explicit override
	settings.WithPath("/etc/flexconf"))
```

- Resolution order: `WithPath` → `<APP>_CONFIG` → `DefaultPath(appName)`.
- Default path comes from `os.UserConfigDir()` → `<user-config-dir>/<app_name>`
  (`~/.config/<app_name>/` on Linux, honoring `XDG_CONFIG_HOME`).
- `WithPath("")` and an empty `<APP>_CONFIG` are no-ops, so an unset override
  never clobbers the resolution below it.
- The directory is resolved **exactly once**, here. Nothing else re-derives it
  from the environment, which is what keeps `config.yaml` and `secrets.kdbx`
  moving together.

| Member | Description |
| --- | --- |
| `New(appName, ...Option)` | Build an `*AppConfig`; errors with `ErrEmptyAppName` on empty name. |
| `WithPath(p)` / `WithEnv(env)` | Override the directory; inject the environment the `<APP>_CONFIG` lookup reads. |
| `EnvVar(appName)` | The variable naming the config dir — `MY_APP_CONFIG` for `my-app`. |
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

## Command trees

Two factories return a `*cobra.Command` ready to mount as a sub-command of any
application's CLI. Each takes the app's resolved `*settings.AppConfig`, so the
config file, KeePass file, socket, and agent log all live under the app's own
config directory.

```go
import (
	clisecrets  "github.com/sylvanld/go-flexconf/cli/secrets"
	settingscli "github.com/sylvanld/go-flexconf/cli/settings"
)

cfg, _ := settings.New("myapp")                       // ~/.config/myapp/
root := &cobra.Command{Use: "myapp"}
root.AddCommand(clisecrets.New(cfg))                  // adds "myapp secrets ..."
root.AddCommand(settingscli.New(cfg, defaultConfig))  // adds "myapp settings ..."
root.Execute()
```

Both packages are named after their directory (`cli/secrets` is `package
secrets`, `cli/settings` is `package settings`), so import them under an alias
when the `secrets` or `settings` library packages are also in scope.

### `cli/settings` — writing the config file

The other half of the loading story: the loader reads a config file, this writes
the first one. `init` renders the application's declared defaults — a fresh,
fully-populated instance of its own config struct, supplied as a `func() any` —
to the file the loader reads, so a new install starts from a complete, valid,
re-loadable document. Building the defaults per invocation keeps them immune to
mutation by anything that decoded over them earlier.

```
<name>                       (default: settings)
├── init [--force]           write the default config file (refuses to clobber)
└── path                     print the config file path
```

| Option | Effect |
| --- | --- |
| `WithName("config")` | Rename the top-level command (default `settings`). |
| `WithConfigPath(path)` | Relocate the file written by `init` (default `<settings-dir>/config.yaml`). |

The file is written `0600` — a config file is a natural home for credentials,
and `$(secret:…)` templating means one may hold resolved values. What `init`
writes always round-trips back through `Load` to the same values.

### `cli/secrets` — the secret manager

Wires `settings` + `secrets` + `agent` together into a full secret manager.

| Option | Effect |
| --- | --- |
| `WithName("vault")` | Rename the top-level command (default `secrets`). |
| `WithKdbxPath(path)` | Relocate the store (default `<settings-dir>/secrets.kdbx`). |
| `WithTimeouts(idle, max)` | Change the agent idle timeout / absolute lifetime. |

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
`clisecrets.New(cfg)` under `example secrets` and `settingscli.New(cfg, defaultConfig)`
under `example settings`. It also shows how to declare defaults for a nested
`flexconf.Settings` block and for a polymorphic `vault` block whose shape is
chosen by its own `type` field.

`settings init` writes the app's declared defaults to the file the loader reads,
so a fresh install starts from a complete config rather than a blank one:

```console
$ example settings init
wrote ~/.config/example/config.yaml

$ example settings init                  # non-destructive by default
error: ~/.config/example/config.yaml already exists (use --force to overwrite)

$ cat ~/.config/example/config.yaml
name: example
http:
    base_url: https://api.example.com
    timeout: 30
    retries: 3
vault:
    type: keepass
    path: secrets.kdbx
    readonly: true
```

And the secret manager:

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
