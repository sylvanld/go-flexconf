//go:build !linux

package agent

import "net"

// checkPeer is a no-op on platforms without SO_PEERCRED. Access is then gated
// only by the 0700 socket directory and 0600 socket permissions, a weaker
// guarantee documented in the README.
func checkPeer(net.Conn) error { return nil }
