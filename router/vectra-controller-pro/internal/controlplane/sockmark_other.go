//go:build !linux

package controlplane

import "syscall"

// setSocketMark is a no-op off Linux: SO_MARK does not exist there. The dev
// host has no TPROXY ruleset either, so there is nothing to escape.
func setSocketMark(int) func(network, address string, c syscall.RawConn) error {
	return nil
}
