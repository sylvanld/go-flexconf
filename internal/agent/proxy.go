package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

// ErrAgentUnavailable reports that no agent is running and this process
// cannot host one. The flexconf secret: resolver re-exports it.
var ErrAgentUnavailable = errors.New("flexconf: no agent and process cannot host one")

// entryPointWired reports whether RunAgentIfRequested is reachable in this
// process (set when it runs and returns as a no-op).
var entryPointWired = false

func init() {
	// RunAgentIfRequested flips this when actually invoked; init keeps the
	// zero default explicit.
	entryPointWired = false
}

// MarkEntryPointWired records that RunAgentIfRequested was called in main, so
// self-exec spawning is possible.
func MarkEntryPointWired() { entryPointWired = true }

// ProxyDriver is a flexvault.VaultDriver that proxies to the background agent
// for a target vault, spawning and unlocking one (like the CLI's auto-unlock)
// when none is running. It is not a publicly registered driver.
type ProxyDriver struct {
	vaultName string
	vaultID   string
	conf      vaultreg.VaultConf
	realDrv   flexvault.VaultDriver // built for Credentials() declaration only
	client    *Client
	unlocked  bool
}

// NewProxy builds a proxy for the named vault (empty = registry default),
// resolving it against the effective registry.
func NewProxy(vaultName string) (*ProxyDriver, error) {
	reg, err := vaultreg.Load()
	if err != nil {
		return nil, err
	}
	name, conf, err := reg.Resolve(vaultName)
	if err != nil {
		return nil, err
	}
	drv, err := conf.BuildDriver()
	if err != nil {
		return nil, err
	}
	return &ProxyDriver{
		vaultName: name,
		vaultID:   vaultreg.VaultID(name, conf),
		conf:      conf,
		realDrv:   drv,
	}, nil
}

// VaultID exposes the proxy's target agent identity.
func (p *ProxyDriver) VaultID() string { return p.vaultID }

func (p *ProxyDriver) Name() string { return "agent-proxy" }

// Configure is a no-op: the proxy is configured at construction from the
// registry (it performs no backend I/O of its own).
func (p *ProxyDriver) Configure(decode func(any) error) error { return nil }

// Credentials forwards the REAL driver's declaration so a missing agent can
// be spawned and unlocked in one interaction.
func (p *ProxyDriver) Credentials() []flexprompt.PromptRequest {
	if Running(p.vaultID) {
		return nil // agent already unlocked: nothing to collect
	}
	return p.realDrv.Credentials()
}

// Unlock ensures an agent is running for the target VaultID and forwards the
// collected answers to it. It never opens the backend in-process.
func (p *ProxyDriver) Unlock(_ context.Context, answers map[string]string) error {
	if !Running(p.vaultID) {
		if !entryPointWired {
			return fmt.Errorf("agent: entry point not wired — call flexconf.RunAgentIfRequested() first in main, or use PolicyInProcess: %w", ErrAgentUnavailable)
		}
		if err := Spawn(p.vaultID, p.vaultName); err != nil {
			return err
		}
	}
	client, err := Dial(p.vaultID)
	if err != nil {
		return err
	}
	if err := client.Unlock(answers); err != nil {
		client.Close()
		return err
	}
	p.client = client
	p.unlocked = true
	return nil
}

func (p *ProxyDriver) Capabilities() flexvault.Capabilities {
	if p.client != nil {
		if st, err := p.client.Status(); err == nil {
			return flexvault.Capabilities{Writable: st.Writable}
		}
	}
	return p.realDrv.Capabilities()
}

func (p *ProxyDriver) Get(_ context.Context, addr string) (string, error) {
	client, err := p.activeClient()
	if err != nil {
		return "", err
	}
	return client.Get(addr)
}

func (p *ProxyDriver) Set(_ context.Context, addr, value string) error {
	client, err := p.activeClient()
	if err != nil {
		return err
	}
	return client.Set(addr, value)
}

func (p *ProxyDriver) List(_ context.Context, ns string) ([]string, error) {
	client, err := p.activeClient()
	if err != nil {
		return nil, err
	}
	return client.List(ns)
}

func (p *ProxyDriver) activeClient() (*Client, error) {
	if p.client != nil {
		return p.client, nil
	}
	client, err := Dial(p.vaultID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", flexvault.ErrLocked, err)
	}
	p.client = client
	return client, nil
}

// Lock closes the proxy's connection. The agent itself stays resident (its
// idle timer owns its lifetime); locking the agent is an explicit CLI action.
func (p *ProxyDriver) Lock() error {
	if p.client != nil {
		p.client.Close()
		p.client = nil
	}
	p.unlocked = false
	return nil
}
