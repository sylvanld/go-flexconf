//go:build linux

package agent

import "golang.org/x/sys/unix"

// Harden makes a best effort to keep secret material off disk for the current
// process: it disables core dumps and tries to lock memory out of swap. It is
// intended to be called once by the agent process at startup. Failures are
// returned for logging but are not fatal (mlockall in particular may be denied
// without sufficient RLIMIT_MEMLOCK).
func Harden() error {
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE); err != nil {
		return err
	}
	return nil
}
