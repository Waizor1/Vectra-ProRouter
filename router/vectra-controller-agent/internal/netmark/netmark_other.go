//go:build !linux

package netmark

import "syscall"

// Control is a no-op on non-Linux platforms; SO_MARK is Linux-only. The agent
// only runs on OpenWrt/Linux in production — this keeps the package building and
// unit-testable on developer machines (e.g. darwin).
func Control(_ uint) func(network, address string, c syscall.RawConn) error {
	return nil
}
