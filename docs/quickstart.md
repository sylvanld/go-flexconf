# Quickstart

This guide walks through wiring `flexconf` into a Go application: resolving where
config lives, loading a templated YAML file into a typed struct, generating a
default config file for a fresh install, and pulling secrets in without ever
writing them to disk in plaintext.

Full reference: [`docs/specs/settings.md`](specs/settings.md) and
[`docs/specs/secrets.md`](specs/secrets.md).

## Install

```sh
go get github.com/sylvanld/go-flexconf
```

Three concepts, all reachable from the one `github.com/sylvanld/go-flexconf` import:

| Concept | Type | What it is |
| --- | --- | --- |
| **App context** | `flexconf.AppConfig` | *Where* config lives — the app name and its config dir (`~/.config/<app>/`). Built with `flexconf.NewAppConfig`. |
| **Loaded settings** | `flexconf.Settings` | *What* was loaded — a block located, templated, and secret-resolved, decoded into your struct on demand. Also carries a block's *defaults*, via `flexconf.Defaults`. |
| **Secrets** | `secrets.Store` | *How* secrets are stored — a store over pluggable drivers (keepass, agent, …). |

Two ready-made cobra command trees mount into your CLI: `cli/settings`
(`settings init` — write the default config file) and `cli/secrets`
(`secrets set/get/list` — manage the secrets your config references).

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

`cfg.File("config.yaml")` → `~/.config/myapp/config.yaml`.

The directory resolves by this precedence:

1. `flexconf.WithAppPath("/some/dir")` — explicit, for tests and one-offs.
2. **`MYAPP_CONFIG`** — the `<APP>_CONFIG` environment variable (uppercased app
   name, non-alphanumerics → `_`). `NewAppConfig` reads it for you; you write no
   `os.Getenv`.
3. `~/.config/myapp/` — the platform default.

`<APP>_CONFIG` names the config **directory**, never a file. The main config file
is always `config.yaml` inside it, so pointing the variable elsewhere relocates
the whole directory as a unit — `config.yaml` and `secrets.kdbx` move together.
That is why the directory is resolved here, once: an override only some
components honoured would leave you reading config from one place and secrets
from another.

## 3. Load

```go
var c Config
if err := flexconf.Load(cfg, &c); err != nil {
	return err
}
```

`Load` runs **locate → read → parse → template → decode**:

1. **Locate** — `config.yaml` inside the app's config directory (see [Choosing
   the config file](#choosing-the-config-file)).
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

## 5. Defaults, and generating that file

Hand-writing the file above is fine for you; it is a poor first experience for
everyone else. `flexconf` can write it, from defaults you declare **as a
pre-populated value of your own config type** — not as struct tags or a separate
YAML blob, both of which are a second copy of your schema that drifts from the
first one silently. A Go value can't drift: it's type-checked against the struct
it defaults.

```go
func defaultConfig() any {
	c := &Config{Debug: false, Channels: []string{"desktop"}}
	c.HTTP.BaseURL = "https://api.example.com"
	c.HTTP.Port = 8080
	return c
}
```

### Defaults apply on load, per key

Pass a pre-populated struct to `Load` and the file overrides only the keys it
names — YAML decoding leaves everything it doesn't mention untouched:

```go
c := defaultConfig().(*Config)
if err := flexconf.Load(cfg, c); err != nil {   // config.yaml overrides per key
	return err
}
```

A `config.yaml` containing just `http: {port: 9000}` yields port `9000` and the
default base URL — no merge code, no `if x == ""` fixups.

### Writing the default file: `settings init`

Mount the settings command tree and users get an `init` that renders those same
defaults to the file `Load` reads:

```go
import settingscli "github.com/sylvanld/go-flexconf/cli/settings"

root.AddCommand(settingscli.New(cfg, defaultConfig)) // adds "myapp settings ..."
```

```sh
$ myapp settings init
wrote /home/u/.config/myapp/config.yaml

$ myapp settings init          # never clobbers silently
error: /home/u/.config/myapp/config.yaml already exists (use --force to overwrite)

$ myapp settings path
/home/u/.config/myapp/config.yaml
```

`defaults` is a `func() any`, not a value, so each call renders defaults
untouched by anything that decoded over them earlier. The file is written `0600`
— a config file is a natural home for credentials.

What `init` writes always round-trips back through `Load` to the same values.

### Defaults for `flexconf.Settings` blocks

A block you decode lazily (see [section 8](#8-blocks-whose-shape-you-dont-know-up-front))
is a `flexconf.Settings`, so it needs its default supplied as one.
`flexconf.Defaults` wraps a pre-populated value:

```go
type Config struct {
	Name string            `yaml:"name"`
	HTTP flexconf.Settings `yaml:"http"`
}

func defaultConfig() any {
	return &Config{
		Name: "myapp",
		HTTP: flexconf.Defaults(&HTTPConfig{BaseURL: "https://api.example.com", Port: 8080}),
	}
}
```

The same per-key merge applies: the loaded block decodes *over* the default, so
`http: {port: 9000}` in the file keeps the default base URL. That holds even
though the block is captured raw — `Settings` keeps its default alongside the
loaded tree rather than replacing it.

## 6. Secrets

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
import clisecrets "github.com/sylvanld/go-flexconf/cli/secrets"

root.AddCommand(clisecrets.New(cfg)) // adds "myapp secrets ..."
```

```sh
myapp secrets set api/token s3cr3t
myapp secrets get api/token
myapp secrets list
```

## 7. Redaction

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

## 8. Blocks whose shape you don't know up front

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

// Register the variants once. The discriminator field name is explicit — there
// is no default; each block names its own selector.
var vaults = flexconf.NewPolymorphicSettings[Vault]("type").
	Register("keepass", func() Vault { return &KeepassVault{} }).
	Register("env",     func() Vault { return &EnvVault{} })

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

### Defaults for polymorphic blocks

A factory returns a *fresh* target each time, so a factory that returns a
**pre-populated** value declares that variant's defaults — the block decodes over
it, key by key. `SetDefault` additionally names the variant a block resolves to
when it omits the discriminator (or is missing entirely):

```go
var vaults = flexconf.NewPolymorphicSettings[Vault]("type").
	Register("keepass", func() Vault { return &KeepassVault{Path: "secrets.kdbx", ReadOnly: true} }).
	Register("env",     func() Vault { return &EnvVault{Prefix: "MYAPP_"} }).
	SetDefault("keepass")
```

Now `vault: {path: /custom.kdbx}` resolves to a `*KeepassVault` with
`ReadOnly: true` inherited. `SetDefault` **panics** on a name you never
registered — that's a wiring bug, caught at startup rather than at the first
load. Without a `SetDefault`, a block missing its discriminator stays a fatal
error, as before.

For `settings init`, `DefaultSettings()` renders the default variant *including*
the discriminator that selects it, so the generated block is complete and
re-loadable rather than an empty stub:

```go
func defaultConfig() any {
	vaultDefaults, err := vaults.DefaultSettings()
	if err != nil {
		panic(err) // a registry wiring bug, not a runtime condition
	}
	return &Config{Vault: vaultDefaults}
}
```

```yaml
vault:
    type: keepass        # spliced back in — variant structs never declare it
    path: secrets.kdbx
    readonly: true
```

## Choosing the config file

`Load` locates the config file with this precedence:

1. `flexconf.WithConfigFile("/path/to/config.yaml")` — explicit override, for
   tests or a `--config` flag.
2. `cfg.File("config.yaml")` — always that name, inside the config directory.

There is no environment variable at this level. `<APP>_CONFIG` moves the
**directory** (step 2 of [Build the app context](#2-build-the-app-context)), and
`config.yaml` is always the file inside it:

```sh
MYAPP_CONFIG=/etc/myapp   →  /etc/myapp/config.yaml
                             /etc/myapp/secrets.kdbx
```

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

	"github.com/sylvanld/go-flexconf"
	clisecrets "github.com/sylvanld/go-flexconf/cli/secrets"
	settingscli "github.com/sylvanld/go-flexconf/cli/settings"
)

type Config struct {
	HTTP struct {
		BaseURL string `yaml:"base_url"`
		Port    int    `yaml:"port"`
		Token   string `yaml:"token"`
	} `yaml:"http"`
	Debug bool `yaml:"debug"`
}

// defaultConfig returns a fresh, fully-populated config: what "settings init"
// writes, and the baseline the config file is decoded over.
func defaultConfig() any {
	c := &Config{}
	c.HTTP.BaseURL = "https://api.example.com"
	c.HTTP.Port = 8080
	return c
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
	root.AddCommand(clisecrets.New(cfg))                 // "myapp secrets ..."
	root.AddCommand(settingscli.New(cfg, defaultConfig)) // "myapp settings ..."

	root.RunE = func(*cobra.Command, []string) error {
		c := defaultConfig().(*Config) // start from the defaults
		if err := flexconf.Load(cfg, c); err != nil {
			return err
		}
		fmt.Printf("listening on :%d → %s\n", c.HTTP.Port, c.HTTP.BaseURL)
		return nil
	}
	return root.Execute()
}
```
