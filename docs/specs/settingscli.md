# `cli/settings` (`settingscli`) — embeddable settings command tree

Package `cli/settings` (import path `github.com/sylvanld/go-flexconf/cli/settings`,
package name `settings`, referred to as **settingscli**) is a factory that builds
a settings command tree as a [cobra](https://github.com/spf13/cobra) command,
ready to mount as a sub-command of any application's CLI.

It is the other half of the loading story. [`flexconf`](settings.md) reads a
config file; this writes the first one. `init` renders the application's declared
[defaults](settings.md#defaults) — a pre-populated instance of its own config
struct — to the path `flexconf` would load, so a fresh install starts from a
complete, valid, re-loadable document instead of a blank file and a spec to read.

## Factory

### `New(cfg *settings.AppConfig, defaults func() any, opts ...Option) *cobra.Command`

Returns the top-level command (default `Use: "settings"`) with sub-commands
attached.

- `cfg` supplies the settings directory, so the file written is the file loaded.
- `defaults` returns a **fresh**, fully-populated config value. It is a `func`
  rather than a shared value so each invocation renders defaults untouched by
  anything that decoded over them earlier. A `nil` `defaults` makes `init` fail
  with a clear error rather than writing an empty document.

Defaults derived from `cfg`:

- **config path**: `cfg.File("config.yaml")` — matching the loader's own default
  (pipeline step 1, precedence rule 3).

The root command sets `SilenceUsage: true` (errors don't dump usage).

### Options

| Option | Effect |
| --- | --- |
| `WithName(name)` | Rename the top-level command (default `settings`). Empty name ignored. |
| `WithConfigPath(path)` | Relocate the config file. Empty path ignored. Must match what the app passes to `flexconf.WithConfigFile`, or `init` writes a file the loader does not read. |

## Command tree

```
<name>                       (default: settings)
├── init [--force|-f]        write the default configuration file
└── path                     print the path of the configuration file
```

### `init`

1. Unless `--force`, `stat` the target and **refuse to overwrite** an existing
   file. Re-running `init` can never silently discard a configuration someone
   has edited — the destructive path is opt-in.
2. `yaml.Marshal(defaults())`.
3. `MkdirAll` the settings directory (the first-run case it exists to serve).
4. Write with mode **`0600`**: a config file is a natural home for credentials,
   and `$(secret:…)` templating means one may hold resolved values.
5. Print `wrote <path>` to the command's stdout.

The existence check happens *before* rendering, so a bad default surfaces as its
own error rather than hiding behind an "already exists".

Errors:

| Condition | Behaviour |
| --- | --- |
| File exists, no `--force` | `<path> already exists (use --force to overwrite)`; file left untouched. |
| `stat` fails for another reason | Propagated — an unreadable target is not treated as absent. |
| No `defaults` declared | `no default configuration declared for this application`. |
| Marshal / mkdir / write failure | Propagated with context. |

### `path`

Prints the resolved config file path, so scripts and humans can find what `init`
wrote without re-deriving the platform's config dir.

## Invariants

- **What `init` writes, the loader reads.** The rendered document must round-trip
  through `flexconf.Load` back to the same values. This is the property the whole
  command exists for and is covered by a test.
- **`init` is non-destructive by default.** Without `--force`, an existing file is
  never modified.
- **The command owns no schema.** It marshals an opaque `any`; the application
  declares what its defaults are.

## Example

```go
cfg, _ := settings.New("example")

root := &cobra.Command{Use: "example"}
root.AddCommand(settingscli.New(cfg, defaultConfig))
root.Execute()
```

```console
$ example settings init
wrote /home/u/.config/example/config.yaml

$ example settings init
error: /home/u/.config/example/config.yaml already exists (use --force to overwrite)
```

Produces a complete config, including the polymorphic `vault` block rendered with
the discriminator that selects it:

```yaml
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
