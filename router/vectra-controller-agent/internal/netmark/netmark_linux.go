//go:build linux

// Package netmark provides the shared SO_MARK dial-control hook used to stamp
// the agent's own sockets so their packets are matched by the nftables
// carve-out that excludes them from the PassWall2 transparent proxy. Both the
// control-plane check-in client and the recovery panel-reachability probe use
// it so they measure the SAME direct egress path; the reachability probes that
// must test the proxied client path simply pass fwmark 0 and dial normally.
package netmark

import "syscall"

// Control returns a net.Dialer Control hook that stamps SO_MARK on the socket.
// Packets carrying this firewall mark are matched by an nftables carve-out that
// excludes them from the PassWall2 transparent proxy, so the marked traffic
// ALWAYS egresses directly — even when the active shunt routes the catch-all
// (including, on some boards, the control-plane path) through a possibly-dead
// proxy. This is the network-level guarantee that the control plane can never
// be stranded by PassWall configuration.
//
// A zero fwmark disables marking (returns nil), so the caller dials normally.
func Control(fwmark uint) func(network, address string, c syscall.RawConn) error {
	if fwmark == 0 {
		return nil
	}
	return func(network, address string, c syscall.RawConn) error {
		var setErr error
		controlErr := c.Control(func(fd uintptr) {
			setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(fwmark))
		})
		if controlErr != nil {
			return controlErr
		}
		return setErr
	}
}
