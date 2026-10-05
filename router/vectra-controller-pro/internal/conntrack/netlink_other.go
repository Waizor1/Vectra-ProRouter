//go:build !linux

package conntrack

// readNetlink: ctnetlink is Linux's.
func readNetlink() ([]Entry, error) { return nil, errNoNetlink }

// deleteEntries: ctnetlink is Linux's.
func deleteEntries([]Entry) (int, error) { return 0, errNoNetlink }
