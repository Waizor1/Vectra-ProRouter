//go:build linux

package controlplane

import "syscall"

// setSocketMark stamps SO_MARK on every socket the control-plane dialer opens,
// so the nftables output chain can `return` on it before the TPROXY rules and
// the router never loses its panel link to the proxy data plane.
//
// Stdlib syscall only — the module has no external dependencies and must keep
// cross-compiling to linux/arm64 without a vendor tree.
func setSocketMark(mark int) func(network, address string, c syscall.RawConn) error {
	if mark <= 0 {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var setErr error
		if err := c.Control(func(fd uintptr) {
			setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, mark)
		}); err != nil {
			return err
		}
		return setErr
	}
}
