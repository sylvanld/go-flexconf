//go:build unix

package secrets

import "syscall"

// detachAttr detaches the spawned agent into its own session so it survives the
// parent shell exiting.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
