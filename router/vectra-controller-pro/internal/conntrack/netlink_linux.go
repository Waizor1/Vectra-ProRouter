//go:build linux

package conntrack

import (
	"fmt"
	"syscall"
	"time"
)

// readNetlink dumps the table through a NETLINK_NETFILTER socket.
func readNetlink() ([]Entry, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_NETFILTER)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoNetlink, err)
	}
	defer syscall.Close(fd)
	sa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
	if err := syscall.Bind(fd, sa); err != nil {
		return nil, fmt.Errorf("%w: bind: %v", errNoNetlink, err)
	}
	tv := syscall.Timeval{Sec: 2}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return nil, fmt.Errorf("conntrack: netlink receive timeout: %w", err)
	}
	seq := uint32(time.Now().UnixNano())
	if err := syscall.Sendto(fd, dumpRequest(seq), 0, sa); err != nil {
		return nil, fmt.Errorf("%w: send: %v", errNoNetlink, err)
	}
	// A dump comes in messages of at most 32 KB.
	buf := make([]byte, 64<<10)
	var out []Entry
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("conntrack: netlink receive: %w", err)
		}
		done, err := parseDump(buf[:n], seq, func(e Entry) { out = append(out, e) })
		if err != nil {
			return nil, err
		}
		if done {
			return out, nil
		}
	}
}

// deleteEntries sends one IPCTNL_MSG_CT_DELETE per entry and reads each
// acknowledgement.
func deleteEntries(es []Entry) (int, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_NETFILTER)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errNoNetlink, err)
	}
	defer syscall.Close(fd)
	sa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
	if err := syscall.Bind(fd, sa); err != nil {
		return 0, fmt.Errorf("%w: bind: %v", errNoNetlink, err)
	}
	tv := syscall.Timeval{Sec: 1}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return 0, fmt.Errorf("conntrack: netlink receive timeout: %w", err)
	}
	buf := make([]byte, 4096)
	seq := uint32(time.Now().UnixNano())
	n := 0
	for _, e := range es {
		seq++
		if err := syscall.Sendto(fd, deleteRequest(seq, e), 0, sa); err != nil {
			return n, fmt.Errorf("conntrack: netlink delete: %w", err)
		}
		for {
			m, _, err := syscall.Recvfrom(fd, buf, 0)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				return n, fmt.Errorf("conntrack: netlink delete acknowledgement: %w", err)
			}
			errno, ok := ackErrno(buf[:m], seq)
			if !ok {
				continue
			}
			switch errno {
			case 0:
				n++
			case int(syscall.ENOENT):
			default:
				return n, fmt.Errorf("conntrack: the kernel refused a delete (errno %d)", errno)
			}
			break
		}
	}
	return n, nil
}
