package netmark

import "testing"

// A zero fwmark means "do not mark"; callers must dial normally. This holds on
// every platform.
func TestControlNilWhenFwmarkZero(t *testing.T) {
	if Control(0) != nil {
		t.Fatal("fwmark 0 must not install a socket control function")
	}
}
