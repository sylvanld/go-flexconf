// Package agent implements the flexconf secret-agent runtime: an ssh-agent
// style detached process holding one unlocked flexvault.Manager in memory,
// serving get/set/list/lock/status over a user-private Unix socket with an
// idle auto-lock. It also provides the client (Dial), self-exec spawning
// (RunAgentIfRequested / EnsureAgent), and the agent-proxy VaultDriver the
// flexconf secret: resolver uses.
//
// This package is module-internal: it is shared by flexconf and flexcli but
// is not part of the public API.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Environment markers used by the self-exec spawn (§cli.md 6.2).
const (
	envMarker  = "FLEXCLI_AGENT"         // = VaultID: this process is an agent
	envVault   = "FLEXCLI_AGENT_VAULT"   // target vault name (registry key)
	envIdle    = "FLEXCONF_IDLE_TIMEOUT" // idle auto-lock duration (Go string)
	envRuntime = "FLEXCONF_RUNTIME_DIR"  // test override for the runtime dir
)

// runtimeDir returns the user-private directory holding agent sockets:
// $FLEXCONF_RUNTIME_DIR (tests) > $XDG_RUNTIME_DIR/flexconf > $TMPDIR/flexconf-$UID.
// It is created 0700 on first use.
func runtimeDir() (string, error) {
	var dir string
	if d := os.Getenv(envRuntime); d != "" {
		dir = d
	} else if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		dir = filepath.Join(d, "flexconf")
	} else {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("flexconf-%d", os.Getuid()))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("agent: creating runtime dir: %w", err)
	}
	return dir, nil
}

func socketPath(vaultID string) (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-"+vaultID+".sock"), nil
}

func pidPath(vaultID string) (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-"+vaultID+".pid"), nil
}

func errPath(vaultID string) (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-"+vaultID+".err"), nil
}

func lockPath(vaultID string) (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-"+vaultID+".lock"), nil
}
