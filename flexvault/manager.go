package flexvault

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sylvanld/go-flexconf/flexprompt"
)

// Manager wraps a single VaultDriver, drives its lifecycle, enforces state,
// and serializes access. It is safe for concurrent use.
type Manager struct {
	mu         sync.Mutex
	driver     VaultDriver
	prompter   flexprompt.Prompter // optional override; nil → process-wide
	retries    int
	configured bool
	unlocked   bool
}

// Option tunes a Manager.
type Option func(*Manager)

// WithUnlockRetries caps the ErrAuth re-attempts on Unlock (default 3).
func WithUnlockRetries(n int) Option {
	return func(m *Manager) { m.retries = n }
}

// WithPrompter overrides the process-wide prompter for this Manager only
// (useful for tests and for vaults needing a dedicated prompter).
func WithPrompter(p flexprompt.Prompter) Option {
	return func(m *Manager) { m.prompter = p }
}

// NewManager returns a Manager driving the given driver. Credentials are
// collected via the process-wide flexprompt.GetPrompter() unless overridden
// with WithPrompter. The vault starts locked.
func NewManager(driver VaultDriver, opts ...Option) *Manager {
	m := &Manager{driver: driver, retries: 3}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *Manager) activePrompter() flexprompt.Prompter {
	if m.prompter != nil {
		return m.prompter
	}
	return flexprompt.GetPrompter() // read at dispatch time, not construction
}

// Configure loads the driver's non-secret settings.
func (m *Manager) Configure(decode func(target any) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.driver.Configure(decode); err != nil {
		return err
	}
	m.configured = true
	return nil
}

// Unlock runs Credentials → Dispatch → driver.Unlock. When the vault is
// already unlocked it is a no-op that succeeds without re-prompting. On
// ErrAuth it re-runs Dispatch → Unlock up to the retry cap; on
// flexprompt.ErrPromptCancelled it stops immediately.
func (m *Manager) Unlock(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.configured {
		return fmt.Errorf("flexvault: unlock: %w", ErrNotConfigured)
	}
	if m.unlocked {
		return nil
	}
	return m.unlockLocked(ctx)
}

// unlockLocked implements the dispatch/retry loop; m.mu must be held.
func (m *Manager) unlockLocked(ctx context.Context) error {
	reqs := m.driver.Credentials()
	attempts := m.retries
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		answers, err := m.activePrompter().Dispatch(ctx, reqs)
		if err != nil {
			return err // includes ErrPromptCancelled: terminal, no retry
		}
		err = m.driver.Unlock(ctx, answers)
		clear(answers)
		if err == nil {
			m.unlocked = true
			return nil
		}
		if !errors.Is(err, ErrAuth) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// Open is a convenience running Configure → Unlock.
func (m *Manager) Open(ctx context.Context, decode func(target any) error) error {
	if err := m.Configure(decode); err != nil {
		return err
	}
	return m.Unlock(ctx)
}

// Create initializes a brand-new empty vault (Configure must have run; then
// InitCredentials → Dispatch → Init). It requires the driver to implement
// Initializer; otherwise it returns ErrUnsupported. It does NOT leave the
// vault unlocked — call Unlock (or Open) afterward.
func (m *Manager) Create(ctx context.Context, decode func(target any) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	init, ok := m.driver.(Initializer)
	if !ok {
		return fmt.Errorf("flexvault: create: driver %q cannot create vaults: %w", m.driver.Name(), ErrUnsupported)
	}
	if err := m.driver.Configure(decode); err != nil {
		return err
	}
	m.configured = true
	answers, err := m.activePrompter().Dispatch(ctx, init.InitCredentials())
	if err != nil {
		return err
	}
	defer clear(answers)
	return init.Init(ctx, answers)
}

// guard enforces the lifecycle order for access methods; m.mu must be held.
func (m *Manager) guard(op string) error {
	if !m.configured {
		return fmt.Errorf("flexvault: %s: %w", op, ErrNotConfigured)
	}
	if !m.unlocked {
		return fmt.Errorf("flexvault: %s: %w", op, ErrLocked)
	}
	return nil
}

// Get retrieves the secret at addr ("namespace/key").
func (m *Manager) Get(ctx context.Context, addr string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard("get"); err != nil {
		return "", err
	}
	ns, key, err := ParseAddress(addr)
	if err != nil {
		return "", err
	}
	return m.driver.Get(ctx, ns+"/"+key)
}

// Set stores value at addr (create or overwrite).
func (m *Manager) Set(ctx context.Context, addr string, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard("set"); err != nil {
		return err
	}
	ns, key, err := ParseAddress(addr)
	if err != nil {
		return err
	}
	return m.driver.Set(ctx, ns+"/"+key, value)
}

// List enumerates namespaces (namespace == "") or the keys in one namespace.
func (m *Manager) List(ctx context.Context, namespace string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard("list"); err != nil {
		return nil, err
	}
	return m.driver.List(ctx, namespace)
}

// Capabilities reports the driver's capabilities (zero value before Configure).
func (m *Manager) Capabilities() Capabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.driver.Capabilities()
}

// Lock clears the unlocked state and calls the driver's Lock.
func (m *Manager) Lock() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unlocked = false
	return m.driver.Lock()
}

// IsUnlocked reports whether the vault is currently unlocked.
func (m *Manager) IsUnlocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unlocked
}
