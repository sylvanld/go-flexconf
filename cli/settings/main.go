// Package settingscli provides a ready-made settings command tree that any
// application can embed as a sub-command of its own CLI.
//
// Its purpose is the other half of the loading story: flexconf reads a config
// file, and this writes the first one. `init` renders the application's declared
// defaults — a pre-populated instance of its own config struct — to the config
// file flexconf would load, so a fresh install starts from a complete, valid,
// re-loadable document instead of a blank file and a spec to read.
//
// Wire it into an application with the factory:
//
//	cfg, _ := settings.New("myapp")
//	root := &cobra.Command{Use: "myapp"}
//	root.AddCommand(settingscli.New(cfg, defaultConfig))  // adds "myapp settings ..."
//	root.Execute()
//
// defaultConfig is a func() any returning a fresh, fully-populated config value.
// Building it per invocation (rather than passing one shared value) keeps the
// defaults immune to mutation by anything that decoded over them earlier. Nested
// flexconf.Settings fields render via flexconf.Defaults, and polymorphic blocks
// via PolymorphicSettings.DefaultSettings, so a default config composes from the
// same pieces the loader decodes.
package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	flexconf "github.com/sylvanld/go-flexconf"
	"github.com/sylvanld/go-flexconf/settings"
)

// cli holds the resolved configuration shared by every sub-command.
type cli struct {
	cfg      *settings.AppConfig
	name     string
	file     string
	defaults func() any
}

// Option customizes the command tree built by New.
type Option func(*cli)

// WithName overrides the name of the top-level command (default "settings"), so
// an app can expose it as, e.g., "config".
func WithName(name string) Option {
	return func(c *cli) {
		if name != "" {
			c.name = name
		}
	}
}

// WithConfigPath overrides the config file written by init (default
// <settings-dir>/config.yaml). It should match what the app passes to
// flexconf.WithConfigFile, or init will write a file the loader does not read.
func WithConfigPath(path string) Option {
	return func(c *cli) {
		if path != "" {
			c.file = path
		}
	}
}

// New builds a settings command tree bound to cfg. defaults returns a fresh,
// fully-populated instance of the application's config struct — what init
// renders. Add the returned command to the application's root command (or run it
// as a root itself).
func New(cfg *settings.AppConfig, defaults func() any, opts ...Option) *cobra.Command {
	c := &cli{
		cfg:      cfg,
		name:     "settings",
		file:     cfg.File(flexconf.ConfigFileName),
		defaults: defaults,
	}
	for _, o := range opts {
		o(c)
	}

	root := &cobra.Command{
		Use:          c.name,
		Short:        "Inspect and initialize application settings",
		Long:         "Inspect and initialize application settings.\n\ninit writes the application's default configuration to the file flexconf\nloads, giving a fresh install a complete starting point.",
		SilenceUsage: true,
	}
	root.AddCommand(
		c.initCmd(),
		c.pathCmd(),
	)
	return root
}

// initCmd writes the application's default config to the settings directory.
func (c *cli) initCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the default configuration file",
		Long:  "Write the application's default configuration to the settings directory.\n\nRefuses to overwrite an existing file unless --force is given, so re-running\ninit can never silently discard a configuration someone has edited.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runInit(cmd, force)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing configuration file")
	return cmd
}

// runInit renders the defaults and writes them, refusing to clobber.
func (c *cli) runInit(cmd *cobra.Command, force bool) error {
	if c.defaults == nil {
		return errors.New("no default configuration declared for this application")
	}

	// Check before rendering so a bad default surfaces as its own error rather
	// than hiding behind an "already exists".
	if !force {
		switch _, err := os.Stat(c.file); {
		case err == nil:
			return fmt.Errorf("%s already exists (use --force to overwrite)", c.file)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("checking %s: %w", c.file, err)
		}
	}

	data, err := yaml.Marshal(c.defaults())
	if err != nil {
		return fmt.Errorf("rendering default configuration: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(c.file), 0o755); err != nil {
		return fmt.Errorf("creating settings directory: %w", err)
	}
	// 0600: a config file is a natural home for credentials, and the loader's
	// $(secret:…) templating means one may hold resolved values.
	if err := os.WriteFile(c.file, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", c.file, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", c.file)
	return nil
}

// pathCmd prints where the config file lives, so scripts and humans can find
// what init wrote without re-deriving the platform's config dir.
func (c *cli) pathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path of the configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), c.file)
			return nil
		},
	}
}
