# flexconf

> **Flexible configuration & secret management for Go** — declare your config
> as plain Go types, and let the people who *write* the config decide where
> each value comes from: a file, an environment variable, or a vault. ✨

[![Documentation](https://img.shields.io/badge/docs-sylvanld.github.io%2Fgo--flexconf-blue?logo=materialformkdocs&logoColor=white)](https://sylvanld.github.io/go-flexconf/)
[![Go Reference](https://pkg.go.dev/badge/github.com/sylvanld/go-flexconf.svg)](https://pkg.go.dev/github.com/sylvanld/go-flexconf)

```yaml
service: api
timeout: 10s
url: https://$(env:HOST)/api            # 🌱 from the environment
token: $(secret:artifactory/token)      # 🔐 from an encrypted vault
```

Your application code never changes — it just sees a `string`. Tokens are
resolved at load time; `secret:` lookups go through a pluggable **vault
driver** selected at runtime by the operator, not the application.

## ✨ Highlights

- 🗂️ **Layered configuration** — stack config directories; maps deep-merge,
  scalars override, defaults live in Go.
- 🏷️ **Typed schema & binding** — plain Go structs with `flexconf` tags;
  binding is all-or-nothing.
- 🧩 **Templating tokens** — `$(env:…)`, `$(file:…)`, `$(config:…)`,
  `$(secret:…)`, embeddable in literal text.
- 🔐 **Vault-backed secrets** — secrets never live in config files; operators
  own the vault registry.
- 🕵️ **Secret agent & CLI** — an ssh-agent-style daemon holds the unlocked
  vault in memory and auto-locks on idle.

## 🚀 Quick start

Install the SDK:

```console
$ go get github.com/sylvanld/go-flexconf
```

Declare your configuration as a plain Go struct and load it from one or more
config directories — later directories override earlier ones:

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

Write the config file — any value may pull from the environment or from a
vault via `$(…)` tokens, resolved at load time:

```yaml
# ./config/config.yaml
service: api
timeout: 10s
token: $(secret:artifactory/token)
url: https://$(env:HOST)/api
```

Finally, create a vault and store the secret using the shipped CLI — your app
then resolves `$(secret:…)` through a background agent, no code changes:

```console
$ go install github.com/sylvanld/go-flexconf/cmd/flexconf@latest
$ flexconf secret init && flexconf secret unlock
$ echo -n 'tok' | flexconf secret set artifactory/token
```

For the full four-step walkthrough (including secrets), see
**[Get started](https://sylvanld.github.io/go-flexconf/quickstart/)**.

## 📚 Documentation

Full documentation lives at **<https://sylvanld.github.io/go-flexconf/>**:

| | |
|---|---|
| 🏠 [Overview](https://sylvanld.github.io/go-flexconf/) | What flexconf is and how it works. |
| 🚀 [Get started](https://sylvanld.github.io/go-flexconf/quickstart/) | Minimal setup in four steps. |
| 📖 [User guide](https://sylvanld.github.io/go-flexconf/user-guide/) | One practical page per package or topic. |
| 📜 [Specs](https://sylvanld.github.io/go-flexconf/specs/) | The normative source of truth. |
| 🗺️ [Roadmap](https://sylvanld.github.io/go-flexconf/roadmap/) | The delivery plan. |

The site is built with [Zensical](https://zensical.org/) from the
[`docs/`](docs/) directory — run `make docs-serve` to preview locally.

## 🧭 Status

**v0.1.0 implemented**, spec-first: the [specs](docs/specs/index.md) drive the
implementation and the [roadmap](docs/roadmap/index.md) tracks delivery. See
[`AGENTS.md`](AGENTS.md) for how to work in this repository, or `make help`
for all targets.
