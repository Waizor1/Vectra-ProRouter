//go:build !linux

package conntrack

// readNetlink: ctnetlink is Linux's.
func readNetlink() ([]Entry, error) { return nil, errNoNetlink }
