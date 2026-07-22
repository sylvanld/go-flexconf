package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// RunAgentIfRequested MUST be called at the very start of main() (or
// TestMain). If this process was spawned as an agent (the self-exec marker is
// present), it runs the agent loop to completion and exits; otherwise it
// returns immediately and does nothing.
func RunAgentIfRequested() {
	vaultID := os.Getenv(envMarker)
	if vaultID == "" {
		MarkEntryPointWired()
		return
	}
	vaultName := os.Getenv(envVault)
	if err := Serve(vaultID, vaultName); err != nil {
		// Startup failures are reported through the .err file so the
		// foreground can surface them (the agent is fully detached).
		if ep, perr := errPath(vaultID); perr == nil {
			os.WriteFile(ep, []byte(err.Error()+"\n"), 0o600)
		}
		os.Exit(1)
	}
	os.Exit(0)
}

// Spawn starts a detached agent for (vaultID, vaultName) by re-executing the
// current binary with the self-exec marker, then waits for its socket. It is
// race-safe: an exclusive flock around check→spawn→wait means a losing racer
// just finds the socket already bound.
func Spawn(vaultID, vaultName string) error {
	lp, err := lockPath(vaultID)
	if err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("agent: opening spawn lock: %w", err)
	}
	defer lockFile.Close()
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("agent: acquiring spawn lock: %w", err)
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)

	if Running(vaultID) {
		return nil // lost the race — an agent is already up
	}

	// Remove a stale error file from a previous failed start.
	ep, err := errPath(vaultID)
	if err == nil {
		os.Remove(ep)
	}

	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(),
		envMarker+"="+vaultID,
		envVault+"="+vaultName,
	)
	// Fully detach: new session, stdio to /dev/null.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("agent: spawning agent: %w", err)
	}
	// The agent outlives us; release the process handle.
	go cmd.Wait() //nolint:errcheck

	// Wait for the socket with a bounded timeout, surfacing the .err file.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if Running(vaultID) {
			return nil
		}
		if data, err := os.ReadFile(ep); err == nil && len(data) > 0 {
			return fmt.Errorf("agent: startup failed: %s", string(data))
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("agent: agent failed to start (timeout waiting for socket)")
}
