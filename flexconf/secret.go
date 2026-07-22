package flexconf

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sylvanld/go-flexconf/flexvault"
	"github.com/sylvanld/go-flexconf/internal/agent"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

// SecretPolicy chooses how the secret: resolver reaches vaults.
type SecretPolicy int

const (
	// PolicyAgent (default) proxies through the background agent, spawning
	// one via the process prompter if none is running. Requires the self-exec
	// entry point: call flexconf.RunAgentIfRequested() first in main.
	PolicyAgent SecretPolicy = iota

	// PolicyInProcess builds a flexvault.Manager around the vault's REAL
	// driver and unlocks it in-process via the process prompter; no agent is
	// spawned and unlocked material lives only for this Loader (locked by
	// Close).
	PolicyInProcess
)

// ErrAgentUnavailable reports that PolicyAgent is selected but no agent is
// running and this process cannot host one (entry point not wired). Wire
// flexconf.RunAgentIfRequested() first in main, or select PolicyInProcess.
var ErrAgentUnavailable = agent.ErrAgentUnavailable

// WithSecretPolicy chooses how the secret: resolver reaches vaults
// (default PolicyAgent).
func WithSecretPolicy(p SecretPolicy) Option {
	return func(o *loaderOptions) { o.secretPolicy = p }
}

// RunAgentIfRequested MUST be called at the very start of main() by library
// consumers that want agent-backed secret resolution (PolicyAgent). If the
// current process was spawned as an agent it runs the agent loop and exits;
// otherwise it returns immediately and does nothing.
func RunAgentIfRequested() {
	agent.RunAgentIfRequested()
}

// secretState caches unlocked vault Managers per Loader (keyed by resolved
// vault name) and serializes unlocks, so one Load prompts at most once per
// referenced vault. Shared across With-derived Loader copies.
type secretState struct {
	mu       sync.Mutex
	managers map[string]*flexvault.Manager
}

// resolveSecret resolves a $(secret:[vault:]namespace/key) token.
func (l *Loader) resolveSecret(path string) (string, error) {
	vaultName, addr, err := vaultreg.ParseRef(path)
	if err != nil {
		return "", err
	}
	if l.secrets == nil {
		return "", errors.New("flexconf: secret resolution unavailable on this Loader")
	}
	l.secrets.mu.Lock()
	defer l.secrets.mu.Unlock() // serializes vault unlocks (one Dispatch at a time)

	mgr, err := l.secretManager(vaultName)
	if err != nil {
		return "", err
	}
	value, err := mgr.Get(context.Background(), addr)
	if err != nil {
		return "", fmt.Errorf("vault %q: %w", displayVault(vaultName), err)
	}
	return value, nil
}

func displayVault(name string) string {
	if name == "" {
		return "(default)"
	}
	return name
}

// secretManager returns (building and unlocking on first use) the Manager for
// a referenced vault. Callers hold secrets.mu.
func (l *Loader) secretManager(vaultName string) (*flexvault.Manager, error) {
	if l.secrets.managers == nil {
		l.secrets.managers = map[string]*flexvault.Manager{}
	}
	// Resolve the name first so the cache key is the effective vault name
	// (the default vault shares its entry with explicit references to it).
	reg, err := vaultreg.Load()
	if err != nil {
		return nil, err
	}
	name, conf, err := reg.Resolve(vaultName)
	if err != nil {
		return nil, err
	}
	if mgr, ok := l.secrets.managers[name]; ok {
		return mgr, nil
	}

	var mgr *flexvault.Manager
	switch l.opts.secretPolicy {
	case PolicyInProcess:
		drv, err := conf.BuildDriver()
		if err != nil {
			return nil, err
		}
		mgr = flexvault.NewManager(drv)
		if err := mgr.Configure(flexvault.MapDecoder(conf.Config)); err != nil {
			return nil, err
		}
	default: // PolicyAgent
		proxy, err := agent.NewProxy(name)
		if err != nil {
			return nil, err
		}
		mgr = flexvault.NewManager(proxy)
		if err := mgr.Configure(flexvault.MapDecoder(nil)); err != nil {
			return nil, err
		}
	}
	if err := mgr.Unlock(context.Background()); err != nil {
		return nil, err
	}
	l.secrets.managers[name] = mgr
	return mgr, nil
}

// Close locks every vault Manager this Loader unlocked (PolicyInProcess) and
// drops its connections (PolicyAgent). Safe to call multiple times.
func (l *Loader) Close() error {
	if l.secrets == nil {
		return nil
	}
	l.secrets.mu.Lock()
	defer l.secrets.mu.Unlock()
	var firstErr error
	for name, mgr := range l.secrets.managers {
		if err := mgr.Lock(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("flexconf: locking vault %q: %w", name, err)
		}
		delete(l.secrets.managers, name)
	}
	return firstErr
}
