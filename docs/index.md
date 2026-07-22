---
icon: lucide/rocket
---

# Get started

**flexconf** is a Go SDK for flexible **configuration** and **secret**
management. Your application declares its configuration as Go types; flexconf
loads it from one or more config directories, resolving templating tokens such
as `$(env:FOO)` or `$(secret:artifactory/token)` along the way. The person
authoring the config — not the application — decides which values come from the
environment or from a secret backend.

This page walks through a minimal setup in four steps. The
[reference](reference/README.md) covers every topic in depth; the
[specs](specs/README.md) are the normative source of truth.

## 1. Install

```console
$ go get github.com/sylvanld/go-flexconf
```

To manage vaults from the command line, also install the standalone binary:

```console
$ go install github.com/sylvanld/go-flexconf/cmd/flexconf@latest
```

## 2. Declare your configuration

Configuration is a plain Go struct with `flexconf` tags. Defaults live in Go —
pre-populate the struct before loading:

```go
package main

import (
    "log"
    "time"

    "github.com/sylvanld/go-flexconf/flexconf"
    "github.com/sylvanld/go-flexconf/flexprompt"
    _ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass" // secret backend
)

type Config struct {
    Service string        `flexconf:"service,required"`
    Timeout time.Duration `flexconf:"timeout"`
    Token   string        `flexconf:"token"` // may be $(secret:…) — the type doesn't care
}

func main() {
    flexconf.RunAgentIfRequested()                    // MUST be first in main: enables agent-backed secrets
    flexprompt.SetPrompter(flexprompt.NewCLIPrompter())

    cfg := Config{Timeout: 30 * time.Second}          // defaults live in Go
    if err := flexconf.New("/etc/myapp", "./config").Load("config.yaml", &cfg); err != nil {
        log.Fatal(err)
    }

    log.Printf("service=%s timeout=%s", cfg.Service, cfg.Timeout)
}
```

`flexconf.New` takes **config directories as layers**, ordered lowest → highest
precedence: maps deep-merge by key, scalars and sequences are replaced
wholesale. Binding is all-or-nothing — on any error your struct is left exactly
as passed. See [flexconf loader](reference/flexconf.md) and
[schema & binding](reference/schema.md).

## 3. Write the config file

```yaml
# ./config/config.yaml
service: api
timeout: 10s
token: $(secret:artifactory/token)   # resolved via the operator's vault registry
url: https://$(env:HOST)/api         # tokens can be embedded in literal text
```

Values may contain `$(scheme:path)` tokens, resolved at load time:

| Token                     | Resolves to                                                  |
| ------------------------- | ------------------------------------------------------------ |
| `$(env:NAME)`             | Environment variable (missing is a hard error).              |
| `$(file:path)`            | Verbatim file contents, relative to the config file.         |
| `$(config:other.yaml)`    | Structural include: splices another YAML tree in place.      |
| `$(secret:namespace/key)` | Secret from the default vault (or `$(secret:vault:ns/key)`). |

See [templating & resolvers](reference/templating.md) for the full grammar,
escaping, and custom resolvers.

## 4. Set up secrets

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

`unlock` spawns a detached, ssh-agent-style background agent that holds the
unlocked vault in memory and auto-locks after an idle timeout (default 2
minutes). Your application then resolves `$(secret:…)` tokens through the
agent without ever prompting — or, when no agent is running, prompts via the
`flexprompt` prompter you configured in step 2.

See the [vault registry](reference/vaults.md),
[secret resolution](reference/secrets.md), and the [CLI](reference/cli.md) —
including how to mount the same `secret` command group into your own app's CLI
with `flexcli`.

## Where to go next

- [Reference index](reference/README.md) — one practical page per package or
  topic.
- [Variants & registry](reference/variants.md) — polymorphic config with
  discriminators.
- [Specs](specs/README.md) — the normative behaviour, spec-first.
- [Roadmap](roadmap.md) — the delivery plan.
