// Command example demonstrates embedding the flexconf command trees in an
// application's own cobra CLI. It resolves the app's settings directory, then
// mounts the secret manager as the "secrets" sub-command and the settings
// manager as "settings":
//
//	example settings init
//	example settings path
//	example secrets set api/token s3cr3t
//	example secrets get api/token
//	example secrets list
//	example secrets agent status
//	example secrets agent lock
//
// Point it at a scratch config dir with EXAMPLE_CONFIG=/some/dir (handy for
// testing); unset, it defaults to ~/.config/example/. The variable names the
// config *directory* — config.yaml and secrets.kdbx both move with it.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	flexconf "github.com/sylvanld/go-flexconf"
	"github.com/sylvanld/go-flexconf/cli/secrets"
	settingscli "github.com/sylvanld/go-flexconf/cli/settings"
	"github.com/sylvanld/go-flexconf/settings"
)

const appName = "example"

// config is the application's schema. The vault block is polymorphic: its shape
// depends on its own `type` field, so it is captured as a Settings and resolved
// by the registry below.
type config struct {
	Name  string            `yaml:"name"`
	HTTP  flexconf.Settings `yaml:"http"`
	Vault flexconf.Settings `yaml:"vault"`
}

type httpConfig struct {
	BaseURL string `yaml:"base_url"`
	Timeout int    `yaml:"timeout"`
	Retries int    `yaml:"retries"`
}

// vault is the interface every vault variant satisfies.
type vault interface{ where() string }

type keepassVault struct {
	Path     string `yaml:"path"`
	ReadOnly bool   `yaml:"readonly"`
}

func (k *keepassVault) where() string { return k.Path }

type envVault struct {
	Prefix string `yaml:"prefix"`
}

func (e *envVault) where() string { return e.Prefix }

// vaults registers the variants. Each factory returns a pre-populated value, so
// a config naming only some keys inherits the rest as that variant's defaults;
// SetDefault picks the variant a block omitting `type` resolves to.
var vaults = flexconf.NewPolymorphicSettings[vault]("type").
	Register("keepass", func() vault { return &keepassVault{Path: "secrets.kdbx", ReadOnly: true} }).
	Register("env", func() vault { return &envVault{Prefix: "EXAMPLE_"} }).
	SetDefault("keepass")

// defaultConfig returns a fresh, fully-populated config — what `settings init`
// renders to config.yaml. It is a func so each call yields untouched defaults.
func defaultConfig() any {
	vaultDefaults, err := vaults.DefaultSettings()
	if err != nil {
		panic(err) // a registry wiring bug, not a runtime condition
	}
	return &config{
		Name: appName,
		HTTP: flexconf.Defaults(&httpConfig{
			BaseURL: "https://api.example.com",
			Timeout: 30,
			Retries: 3,
		}),
		Vault: vaultDefaults,
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// EXAMPLE_CONFIG is honoured by settings.New itself — no os.Getenv here.
	cfg, err := settings.New(appName)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDir(); err != nil {
		return err
	}

	root := &cobra.Command{
		Use:           appName,
		Short:         "Example app embedding the flexconf secret manager",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	// Mount the reusable command trees as "example secrets ..." / "settings ...".
	root.AddCommand(secrets.New(cfg))
	root.AddCommand(settingscli.New(cfg, defaultConfig))

	return root.Execute()
}
