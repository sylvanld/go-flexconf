//go:build !unix

package secretcli

import "syscall"

// detachAttr is a no-op on platforms without Setsid; the agent still runs but is
// not reparented into its own session.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
