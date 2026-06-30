//go:build linux

package netmark

import "testing"

func TestControlInstalledOnLinux(t *testing.T) {
	if Control(0x564354) == nil {
		t.Fatal("expected a socket control function on linux when a fwmark is set")
	}
}
