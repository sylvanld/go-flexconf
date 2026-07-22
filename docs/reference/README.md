---
tags:
  - reference
---

# Reference

User-facing documentation for the flexconf SDK, one page per package or
topic. The [specs](../specs/README.md) remain the normative source of truth;
these pages are the practical guide.

| Page | Covers |
|------|--------|
| [flexconf loader](flexconf.md) | `New`/`Load`, config directories as layers, merge semantics, load lifecycle, errors. |
| [Schema & binding](schema.md) | The `flexconf` struct tag, defaults, supported types, strict validation, `Validate()` hook. |
| [Templating & resolvers](templating.md) | `$(scheme:path)` tokens, escaping, `env:`/`file:`/`config:`, custom resolvers, static loaders. |
| [Variants & registry](variants.md) | Polymorphic config: discriminators, selectors, `Resolve`/`Get`. |
| [flexprompt](flexprompt.md) | Credential collection: the `Prompter` singleton and built-ins. |
| [flexvault](flexvault.md) | Vault drivers, the `Manager` lifecycle, addressing, the KeePass driver. |
| [Vault registry](vaults.md) | `vaults.yaml`, layering via `FLEXCONF_VAULTS`, the default vault, `VaultID`. |
| [Secret resolution](secrets.md) | The `secret:` scheme, agent vs in-process policies, redaction. |
| [CLI](cli.md) | The `secret` command group, the standalone `flexconf` binary, the agent. |

## End-to-end example

```go
package main

import (
    "log"
    "time"

    "github.com/sylvanld/go-flexconf/flexconf"
    "github.com/sylvanld/go-flexconf/flexprompt"
    _ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass"
)

type Config struct {
    Service string        `flexconf:"service,required"`
    Timeout time.Duration `flexconf:"timeout"`
    Token   string        `flexconf:"token"` // may be $(secret:…) — the type doesn't care
}

func main() {
    flexconf.RunAgentIfRequested()                    // enable agent-backed secrets
    flexprompt.SetPrompter(flexprompt.NewCLIPrompter())

    cfg := Config{Timeout: 30 * time.Second}          // defaults live in Go
    if err := flexconf.New("/etc/myapp", "./config").Load("config.yaml", &cfg); err != nil {
        log.Fatal(err)
    }
}
```

```yaml
# ./config/config.yaml
service: api
token: $(secret:artifactory/token)   # resolved via the operator's vault registry
```
