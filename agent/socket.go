package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// SocketPath returns the agent socket path for appName. It prefers
// $XDG_RUNTIME_DIR/<appName>/agent.sock — a private per-user 0700 tmpfs, which
// avoids the symlink/TOCTOU hazards of a shared /tmp — and otherwise falls back
// to <os.TempDir()>/<appName>-<uid>/agent.sock.
func SocketPath(appName string) string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, appName, "agent.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", appName, os.Getuid()), "agent.sock")
}
