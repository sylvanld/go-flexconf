//go:build linux

package agent

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeer rejects any connection whose peer uid differs from this process's
// uid, using SO_PEERCRED. This is the real access control — file permissions on
// the socket itself are not reliably enforced for connect.
func checkPeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("agent: not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}

	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credErr != nil {
		return credErr
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("agent: peer uid %d not permitted", cred.Uid)
	}
	return nil
}
