# flexconf

**flexconf** is a Go SDK for flexible **configuration** and **secret**
management, unifying the two so that config files can transparently reference
secrets and environment values.

An application declares the structure of its configuration as Go types and lets
flexconf load it from one or more config directories. Config files may contain
templating tokens such as `$(env:FOO)` or `$(secret:artifactory/token)`; the
person authoring the config decides which values are pulled from the
environment or from a secret backend. flexconf resolves those tokens at load
time, delegating `secret:` lookups to a pluggable **vault driver** (Vault, cloud
secret managers, files, …) selected at runtime.

## Quick start

```go
import (
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
    flexconf.RunAgentIfRequested() // first in main: enables agent-backed secrets
    flexprompt.SetPrompter(flexprompt.NewCLIPrompter())

    cfg := Config{Timeout: 30 * time.Second}
    if err := flexconf.New("/etc/myapp", "./config").Load("config.yaml", &cfg); err != nil {
        log.Fatal(err)
    }
}
```

```yaml
# ./config/config.yaml
service: api
token: $(secret:artifactory/token)   # resolved via the operator's vault registry
url: https://$(env:HOST)/api
```

Manage vaults from the shipped CLI:

```console
$ flexconf secret init && flexconf secret unlock
$ echo -n 'tok' | flexconf secret set artifactory/token
```

## Packages

| Package | Purpose |
|---------|---------|
| `flexconf` | Config loading: layered directories, templating tokens, schema binding, variants. |
| `flexvault` (+ `flexvault/driver/keepass`) | Pluggable secret backends and the `Manager` lifecycle. |
| `flexprompt` | Credential collection (CLI/map/env prompters, process-wide singleton). |
| `flexcli` + `cmd/flexconf` | Mountable Cobra `secret` command group and the standalone binary, backed by an ssh-agent-style secret agent. |

## Status

**v1 implemented**, spec-first. The specifications in
[`docs/specs/`](docs/specs/) are the source of truth
([index](docs/specs/README.md)); the practical guide lives in
[`docs/reference/`](docs/reference/); the delivery plan in
[`docs/roadmap.md`](docs/roadmap.md). See [`AGENTS.md`](AGENTS.md) for how to
work in this repository.

## Documentation

The docs site is built with [Zensical](https://zensical.org/) from the `docs/`
directory. Run `make docs-serve` to preview locally, or `make help` to list all
targets.
