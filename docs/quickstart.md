---
icon: lucide/rocket
---

# Get started

A minimal FlexConf setup in **four steps**: install → declare → configure →
secrets. :rocket: New to FlexConf? Start with the [overview](index.md). For
depth, see the [user guide](user-guide/index.md); the [specs](specs/index.md)
are the normative source of truth.

## :one: Install

=== ":simple-go: Go SDK"

    ```console
    $ go get github.com/sylvanld/go-flexconf
    ```

=== ":material-console: CLI (optional)"

    To manage vaults from the command line, also install the standalone
    binary:

    ```console
    $ go install github.com/sylvanld/go-flexconf/cmd/flexconf@latest
    ```

## :two: Declare your configuration

Configuration is a plain Go struct with `flexconf` tags. Defaults live in Go —
pre-populate the struct before loading:

```go
package main

import (
    "log"
    "time"

    "github.com/sylvanld/go-flexconf/flexconf"
    "github.com/sylvanld/go-flexconf/flexprompt"
    _ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass" // (1)!
)

type Config struct {
    Service string        `flexconf:"service,required"`
    Timeout time.Duration `flexconf:"timeout"`
    Token   string        `flexconf:"token"` // (2)!
}

func main() {
    flexconf.RunAgentIfRequested() // (3)!
    flexprompt.SetPrompter(flexprompt.NewCLIPrompter())

    cfg := Config{Timeout: 30 * time.Second} // (4)!
    if err := flexconf.New("/etc/myapp", "./config").Load("config.yaml", &cfg); err != nil {
        log.Fatal(err)
    }

    log.Printf("service=%s timeout=%s", cfg.Service, cfg.Timeout)
}
```

1. Blank-import the secret backend(s) you want available.
2. May be `$(secret:…)` — the type doesn't care where the value comes from.
3. **Must be first in `main`**: enables agent-backed secret resolution.
4. Defaults live in Go — pre-populate before loading.

!!! note "Layers & binding"

    `flexconf.New` takes **config directories as layers**, ordered lowest →
    highest precedence: maps deep-merge by key, scalars and sequences are
    replaced wholesale. Binding is all-or-nothing — on any error your struct
    is left exactly as passed. See [loading configuration](user-guide/flexconf.md)
    and [schema & binding](user-guide/schema.md).

## :three: Write the config file

```yaml
# ./config/config.yaml
service: api
timeout: 10s
token: $(secret:artifactory/token)   # (1)!
url: https://$(env:HOST)/api         # (2)!
```

1. Resolved via the operator's vault registry — see step 4. :point_down:
2. Tokens can be embedded in literal text.

Values may contain `$(scheme:path)` tokens, resolved at load time:

| Token                     | Resolves to                                                  |
| ------------------------- | ------------------------------------------------------------ |
| `$(env:NAME)`             | :seedling: Environment variable (missing is a hard error).   |
| `$(file:path)`            | :page_facing_up: Verbatim file contents, relative to the config file. |
| `$(config:other.yaml)`    | :jigsaw: Structural include: splices another YAML tree in place. |
| `$(secret:namespace/key)` | :closed_lock_with_key: Secret from the default vault (or `$(secret:vault:ns/key)`). |

See [templating & resolvers](user-guide/templating.md) for the full grammar,
escaping, and custom resolvers.

## :four: Set up secrets

Secrets never live in config files — `$(secret:…)` tokens are looked up in a
**vault registry** owned by the operator:

```yaml
# ~/.config/flexconf/vaults.yaml
default: personal
vaults:
  personal:
    driver: keepass
    path: ~/.local/share/flexconf/personal.kdbx
```

Create, unlock, and populate the vault with the CLI:

```console
$ flexconf secret init
New KeePass master password: ****
created vault "personal"

$ flexconf secret unlock
KeePass master password: ****
unlocked; agent will lock after idle timeout

$ echo -n 'tok' | flexconf secret set artifactory/token
ok
```

!!! tip ":lock: The secret agent"

    `unlock` spawns a detached, **ssh-agent-style** background agent that
    holds the unlocked vault in memory and auto-locks after an idle timeout
    (default 2 minutes). Your application then resolves `$(secret:…)` tokens
    through the agent without ever prompting — or, when no agent is running,
    prompts via the `flexprompt` prompter you configured in step 2.

See the [vault registry](user-guide/vaults.md),
[secret resolution](user-guide/secrets.md), and the [CLI](user-guide/cli.md) —
including how to mount the same `secret` command group into your own app's CLI
with `flexcli`.

## :compass: Going further

That's the whole loop — typed config, layered files, vault-backed secrets.
:tada: To dive deeper into any part of it (templating grammar, variants,
custom resolvers, vault drivers, …), head to the
**[User guide](user-guide/index.md)**.
