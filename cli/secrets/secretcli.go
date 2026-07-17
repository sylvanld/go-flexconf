// Package secretcli provides a ready-made secret-management command tree that
// any application can embed as a sub-command of its own CLI.
//
// The command tree mirrors the standalone example CLI: get/list/set/delete for
// secrets, and an "agent" group (unlock/lock/status/run) that keeps a
// short-lived unlocked store so a single unlock covers repeated commands. Every
// command that touches secrets goes through the agent; if none is running one is
// started implicitly, prompting for the KeePass password once.
//
// Wire it into an application with the factory:
//
//	cfg, _ := settings.New("myapp")
//	root := &cobra.Command{Use: "myapp"}
//	root.AddCommand(secretcli.New(cfg))            // adds "myapp secrets ..."
//	root.Execute()
//
// The factory takes the app's resolved settings, so the KeePass file, socket,
// and agent log all live under the app's own configuration directory. Use the
// options to rename the command or relocate the store.
package secrets

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"forgejo.ovhcloud.tools/sylvan/flexconf/agent"
	"forgejo.ovhcloud.tools/sylvan/flexconf/secrets"
	"forgejo.ovhcloud.tools/sylvan/flexconf/settings"
)

// Defaults for a freshly built command tree.
const (
	defaultName        = "secrets"
	defaultKdbxName    = "secrets.kdbx"
	defaultIdleTTL     = 5 * time.Minute
	defaultMaxLifetime = 30 * time.Minute
)

// cli holds the resolved configuration shared by every sub-command.
type cli struct {
	cfg   *settings.Settings
	name  string
	kdbx  string
	idle  time.Duration
	max   time.Duration
	envFD string // inherited-fd env var name the detached agent reads its password from

	runCmd *cobra.Command // the "agent run" command, needed to spawn the detached agent
}

// Option customizes the command tree built by New.
type Option func(*cli)

// WithName overrides the name of the top-level command (default "secrets"), so
// an app can expose it as, e.g., "vault" or "creds".
func WithName(name string) Option {
	return func(c *cli) {
		if name != "" {
			c.name = name
		}
	}
}

// WithKdbxPath overrides the KeePass file location (default
// <settings-dir>/secrets.kdbx).
func WithKdbxPath(path string) Option {
	return func(c *cli) {
		if path != "" {
			c.kdbx = path
		}
	}
}

// WithTimeouts overrides the default agent idle timeout and absolute lifetime. A
// non-positive value leaves that default in place.
func WithTimeouts(idle, max time.Duration) Option {
	return func(c *cli) {
		if idle > 0 {
			c.idle = idle
		}
		if max > 0 {
			c.max = max
		}
	}
}

// New builds a secret-management command tree bound to cfg. Add the returned
// command to the application's root command (or run it as a root itself).
func New(cfg *settings.Settings, opts ...Option) *cobra.Command {
	c := &cli{
		cfg:   cfg,
		name:  defaultName,
		kdbx:  cfg.File(defaultKdbxName),
		idle:  defaultIdleTTL,
		max:   defaultMaxLifetime,
		envFD: envFDName(cfg.AppName()),
	}
	for _, o := range opts {
		o(c)
	}

	root := &cobra.Command{
		Use:          c.name,
		Short:        "Manage secrets in a password-protected KeePass store",
		Long:         "Manage secrets in a password-protected KeePass store.\n\nReads and writes go through a short-lived agent so a single unlock covers\nrepeated commands. If no agent is running, one is started implicitly.",
		SilenceUsage: true,
	}
	root.AddCommand(
		c.getCmd(),
		c.listCmd(),
		c.setCmd(),
		c.deleteCmd(),
		c.agentGroup(),
	)
	return root
}

// socket returns the per-user agent socket path for this app.
func (c *cli) socket() string { return agent.SocketPath(c.cfg.AppName()) }

// store returns a Store backed by the agent, starting one (an implicit unlock)
// if none is running.
func (c *cli) store() (*secrets.Store, error) {
	client, err := c.ensureAgent()
	if err != nil {
		return nil, err
	}
	return secrets.NewStore(client), nil
}

// ensureAgent returns a client for the running agent, starting one first if
// necessary (implicit unlock).
func (c *cli) ensureAgent() (*agent.Client, error) {
	client := agent.NewClient(c.socket())
	if client.IsRunning() {
		return client, nil
	}
	if err := c.startAgentDetached(c.idle, c.max); err != nil {
		return nil, err
	}
	return client, nil
}

// envFDName derives a per-app environment variable name for the inherited
// password pipe, e.g. "my-app" -> "MY_APP_SECRETS_AGENT_PW_FD".
func envFDName(appName string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(appName) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	name := b.String()
	if name == "" {
		name = "APP"
	}
	return name + "_SECRETS_AGENT_PW_FD"
}
