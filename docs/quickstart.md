# Quickstart

This guide walks through wiring `flexconf` into a Go application: resolving where
config lives, loading a templated YAML file into a typed struct, and pulling
secrets in without ever writing them to disk in plaintext.

Full reference: [`docs/specs/settings.md`](specs/settings.md) and
[`docs/specs/secrets.md`](specs/secrets.md).

## Install

```sh
go get github.com/sylvanld/flexconf
```

Three concepts, all reachable from the one `github.com/sylvanld/flexconf` import:

| Concept | Type | What it is |
| --- | --- | --- |
| **App context** | `flexconf.AppConfig` | *Where* config lives — the app name and its config dir (`~/.config/<app>/`). Built with `flexconf.NewAppConfig`. |
| **Loaded settings** | `flexconf.Settings` | *What* was loaded — a block located, templated, and secret-resolved, decoded into your struct on demand. |
| **Secrets** | `secrets.Store` | *How* secrets are stored — a store over pluggable drivers (keepass, agent, …). |

You import one path and reach the loader as `flexconf.Load`. The implementation
lives in `internal/loader`; the root package is a thin re-export.

## 1. Define your config struct

Model your config as a plain struct with `yaml` tags using **native Go types** —
`int`, `bool`, `float64`, `string`, slices, maps, and nested/arbitrary structs.
Templated values resolve like any hand-written YAML scalar, so `$(env:PORT)`
decodes into an `int`, `$(env:DEBUG)` into a `bool`, and so on.

```go
type Config struct {
	HTTP struct {
		BaseURL string `yaml:"base_url"`
		Port    int    `yaml:"port"`
		Token   string `yaml:"token"`
	} `yaml:"http"`
	Debug    bool     `yaml:"debug"`
	Channels []string `yaml:"channels"`
}
```

No wrapper types. A list must be written as a list (`channels: [a, b]`), a number
as a number — the struct is exactly what you'd write for any `yaml.Unmarshal`.

> **Durations:** Go's `time.Duration` does not parse `"30s"` from YAML. If you
> want human-friendly durations, either store an int (`timeout_seconds: 30`) or
> declare a small type with an `UnmarshalYAML` that calls `time.ParseDuration` —
> that's a normal Go type in *your* package, not something flexconf imposes.

## 2. Build the app context

`flexconf.NewAppConfig` gives you an `AppConfig`: the app's name and its config
directory — `~/.config/<app>/` by default (honoring `XDG_CONFIG_HOME`; the
platform config dir elsewhere).

```go
cfg, err := flexconf.NewAppConfig("myapp")
if err != nil {
	return err
}
```

`cfg.File("config.yaml")` → `~/.config/myapp/config.yaml`. Override the
directory in tests or one-offs with
`flexconf.NewAppConfig("myapp", flexconf.WithAppPath("/some/dir"))`.

## 3. Load

```go
var c Config
if err := flexconf.Load(cfg, &c); err != nil {
	return err
}
```

`Load` runs **locate → read → parse → template → decode**:

1. **Locate** — `~/.config/myapp/config.yaml`, unless the `MYAPP_CONFIG`
   environment variable points somewhere else (see [Choosing the config
   file](#choosing-the-config-file)).
2. **Read + parse** the YAML into a node tree.
3. **Template** every `$(…)` reference on that tree (not on raw text — an
   injected value can never change the document's structure).
4. **Decode** the result into `&c`.

## 4. Write a config file

`~/.config/myapp/config.yaml`:

```yaml
http:
  base_url: $(env:BASE_URL:-https://api.example.com)
  port: $(env:PORT:-8080)
  token: $(secret:api/token)
debug: $(env:DEBUG:-false)
channels:
  - telegram
  - desktop

# Optional: pull in another file (relative to this one).
logging: $(config:logging.yaml)
```

Reference syntax:

| Reference | Resolves to |
| --- | --- |
| `$(env:NAME)` | environment variable `NAME` (errors if unset) |
| `$(env:NAME:-default)` | `NAME`, or `default` when unset |
| `$(secret:name)` | secret `name` from the secret store (see below) |
| `$(config:file.yaml)` | the parsed, recursively-templated contents of `file.yaml` |
| `$$(` | a literal `$(` (escape) |

References may be embedded in text (`$(env:HOME)/.cache/myapp`) — except
`$(config:…)`, which must be the whole value since it splices in structure.

## 5. Secrets

`$(secret:…)` resolves through the `secrets` package. There are three ways the
backing store is chosen, in precedence order.

### Zero config (default)

With no `secrets:` block and no injected store, `flexconf` uses the same
**agent → keepass** stack the CLI manages: if a running secret agent is found at
the app's socket it's used; otherwise it falls back to reading
`~/.config/myapp/secrets.kdbx` (read-only). Nothing to configure — just store a
secret (next section) and reference it.

### A `secrets:` block in the config

Select and configure the driver from the config file itself. The block is
templated **env-only** (so it can't depend on a secret to bootstrap the secret
store) and is stripped from the tree before your struct decodes — your `Config`
does **not** declare a `secrets` field.

```yaml
secrets:
  driver: keepass
  keepass:
    path: ~/.config/myapp/secrets.kdbx
    readonly: true

token: $(secret:api/token)
```

Built-in drivers:

| `driver:` | Backing store |
| --- | --- |
| `agent` | secret agent, falling back to read-only keepass (the default) |
| `keepass` | a KeePass `.kdbx` file (`path:`, `readonly:`) |
| `env` | environment variables — `secret:api/token` reads `$API_TOKEN` |
| `exec` | shells out: `command: [pass, show]` → `pass show api/token` |
| `none` | no secrets; any `$(secret:…)` is a fatal error |

Register your own with `flexconf.RegisterSecretDriver("name", factory)`.

### An injected store (dependency injection)

When your app already built a `*secrets.Store` (e.g. after prompting for a
master password once), hand it to the loader — it wins over both the block and
the default:

```go
err := flexconf.Load(cfg, &c, flexconf.WithSecretStore(store))
```

### Managing the secret file

Mount the reusable secret-manager CLI in your own command tree so users can
create the secrets your config references:

```go
import clisecrets "github.com/sylvanld/flexconf/cli/secrets"

root.AddCommand(clisecrets.New(cfg)) // adds "myapp secrets ..."
```

```sh
myapp secrets set api/token s3cr3t
myapp secrets get api/token
myapp secrets list
```

## 6. Redaction

Secret-sourced values are *tainted* through the whole pipeline, so a config dump
never leaks them. Use `LoadFile` + `Dump` wherever you echo config (a
`--print-config` flag, a debug log):

```go
loaded, err := flexconf.LoadFile(cfg)
if err != nil {
	return err
}
dump, _ := loaded.Dump()       // token: "«redacted»"
fmt.Println(string(dump))
if err := loaded.Decode(&c); err != nil {
	return err
}
```

Redaction is structural (the taint set), not a field allowlist — you can't
forget to mark a new secret field.

## 7. Blocks whose shape you don't know up front

Sometimes a parent struct can't statically declare a child block's shape —
either the owning subsystem should decode its own slice, or the shape depends on
a field *inside* the block. Declare the field as `flexconf.Settings`: `Load`
locates, templates, and secret-resolves it like everything else, but leaves it
undecoded until you ask.

**Deferred decode** — the parent doesn't know (or care about) the shape; the
subsystem decodes it later:

```go
type Config struct {
	Server ServerConfig      `yaml:"server"`
	Plugin flexconf.Settings `yaml:"plugin"` // captured raw
}

// Elsewhere — the plugin owns its schema:
var pc PluginConfig
if err := c.Plugin.Decode(&pc); err != nil {
	return err
}
```

**Polymorphic decode** — the block is one of several types, chosen by one of its
own fields. A `PolymorphicSettings` registry maps that field's value to the
concrete type:

```yaml
vault:
  type: keepass          # <- the discriminator picks the shape
  path: ~/.config/myapp/secrets.kdbx
  readonly: true
```

```go
type Vault interface{ Open() (*Store, error) }

type KeepassVault struct {
	Path     string `yaml:"path"`
	ReadOnly bool   `yaml:"readonly"`
}
type EnvVault struct {
	Prefix string `yaml:"prefix"`
}

// Register the variants once; the discriminator field is explicit (no default).
var vaults = flexconf.NewPolymorphicSettings[Vault]("type")

func init() {
	vaults.Register("keepass", func() Vault { return &KeepassVault{} })
	vaults.Register("env",     func() Vault { return &EnvVault{} })
}

type Config struct {
	Vault flexconf.Settings `yaml:"vault"`
}

// After Load:
v, err := vaults.Decode(c.Vault) // *KeepassVault or *EnvVault
```

The discriminator (`type` here — use whatever field fits your domain, e.g.
`engine`, `channel`) is stripped before the remaining keys decode **strictly**,
so a variant struct declares only its own fields and an unknown key is an error.
This is the same tagged-union mechanism the built-in `secrets:` block uses to
select its driver, made reusable for your own config.

## Choosing the config file

`Load` locates the config file with this precedence:

1. `flexconf.WithConfigFile("/path/to/config.yaml")` — explicit override.
2. The `<APP>_CONFIG` environment variable — for app name `myapp`, that's
   `MYAPP_CONFIG` (uppercased, non-alphanumerics → `_`). It names the config
   **file**, not the directory.
3. `cfg.File("config.yaml")` — the default under the settings directory.

## Testing

Every input is injectable, so tests never touch the real filesystem, environment,
or secret store:

```go
err := flexconf.Load(cfg, &c,
	flexconf.WithFS(fstest.MapFS{                       // in-memory config
		"config.yaml": {Data: []byte("token: $(secret:api/token)\n")},
	}),
	flexconf.WithConfigFile("config.yaml"),
	flexconf.WithEnv(flexconf.MapEnv{"HOME": "/home/u"}), // fixed environment
	flexconf.WithSecretResolver(myResolver),              // fixed secrets
)
```

`WithEnv` takes a `flexconf.MapEnv` (a `map[string]string`); `WithSecretResolver`
takes anything implementing `Secret(name string) (string, error)`.

## Putting it together

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sylvanld/flexconf"
	clisecrets "github.com/sylvanld/flexconf/cli/secrets"
)

type Config struct {
	HTTP struct {
		BaseURL string `yaml:"base_url"`
		Port    int    `yaml:"port"`
		Token   string `yaml:"token"`
	} `yaml:"http"`
	Debug bool `yaml:"debug"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := flexconf.NewAppConfig("myapp")
	if err != nil {
		return err
	}
	if err := cfg.EnsureDir(); err != nil {
		return err
	}

	root := &cobra.Command{Use: "myapp", SilenceUsage: true}
	root.AddCommand(clisecrets.New(cfg)) // "myapp secrets ..."

	root.RunE = func(*cobra.Command, []string) error {
		var c Config
		if err := flexconf.Load(cfg, &c); err != nil {
			return err
		}
		fmt.Printf("listening on :%d → %s\n", c.HTTP.Port, c.HTTP.BaseURL)
		return nil
	}
	return root.Execute()
}
```
