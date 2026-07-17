//go:build !linux

package agent

// Harden is a no-op on platforms without the Linux hardening syscalls.
func Harden() error { return nil }
