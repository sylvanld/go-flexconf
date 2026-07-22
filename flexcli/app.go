// Package flexcli provides a mountable Cobra `secret` command group
// (init/unlock/lock/get/set/list/status/vaults) driving the flexconf secret
// agent. It has two entry points sharing the same group: embedded in an
// application's CLI, and the standalone cmd/flexconf binary. Vault
// definitions always come from the vault registry (vaults.yaml).
package flexcli

import (
	"errors"
	"os"
	"time"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
	"github.com/sylvanld/go-flexconf/internal/agent"
)

// Options carries an App's process options. Vault definitions are never
// supplied here — they come from the vault registry.
type Options struct {
	// DefaultVault pins which registry vault is used when the --vault flag is
	// not given. Empty → the registry's own default:.
	DefaultVault string

	// VaultID overrides the socket/agent identity for the selected vault.
	// Empty → derived from the vault name + config fingerprint.
	VaultID string

	// IdleTimeout is how long the agent stays unlocked with no request before
	// it auto-locks and exits. Zero → 2 minutes. Negative → never (discouraged).
	IdleTimeout time.Duration

	// Prompter overrides the credential prompter used by init/unlock. Nil → a
	// flexprompt CLI prompter (masked secret input). The agent never prompts.
	Prompter flexprompt.Prompter
}

// App binds the secret command group to its options.
type App struct {
	opts Options
}

// New returns an App bound to opts.
func New(opts Options) *App { return &App{opts: opts} }

// RunAgentIfRequested MUST be called at the very start of main(). If the
// current process was spawned by flexcli as an agent, it runs the agent to
// completion and exits; otherwise it returns immediately and does nothing.
func (a *App) RunAgentIfRequested() {
	if a.opts.IdleTimeout != 0 {
		os.Setenv("FLEXCONF_IDLE_TIMEOUT", a.opts.IdleTimeout.String())
	}
	agent.RunAgentIfRequested()
}

// prompter returns the App's credential prompter (default: CLI).
func (a *App) prompter() flexprompt.Prompter {
	if a.opts.Prompter != nil {
		return a.opts.Prompter
	}
	return flexprompt.NewCLIPrompter()
}

// GlobalOptions builds Options for the standalone flexconf CLI (and any app
// exposing the user's global vaults): process options from the environment,
// vault definitions from the registry. FLEXCONF_VAULT selects the default
// vault; FLEXCONF_IDLE_TIMEOUT tunes the idle auto-lock.
func GlobalOptions() (Options, error) {
	opts := Options{DefaultVault: os.Getenv("FLEXCONF_VAULT")}
	if s := os.Getenv("FLEXCONF_IDLE_TIMEOUT"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return opts, errors.New("flexcli: invalid FLEXCONF_IDLE_TIMEOUT: " + s)
		}
		opts.IdleTimeout = d
	}
	return opts, nil
}

// Exit codes: 0 success, 1 generic failure, 2 usage (Cobra), 3 locked.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
	ExitLocked  = 3
)

// ExitCode maps a command error to the CLI's stable exit-code scheme.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, flexvault.ErrLocked):
		return ExitLocked
	default:
		return ExitFailure
	}
}
