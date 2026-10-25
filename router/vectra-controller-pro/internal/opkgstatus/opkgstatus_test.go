package opkgstatus

import "testing"

func TestVersion(t *testing.T) {
	status := "Package: xray-core\nVersion: 26.7.28-r1\nStatus: install ok installed\n\n" +
		"Package: half\nVersion: 1\nStatus: install ok unpacked\n\n" +
		"Package: last\nVersion: 2\r\nStatus: install user installed"
	for name, want := range map[string]string{"xray-core": "26.7.28-r1", "half": "", "last": "2", "none": ""} {
		if got := Version([]byte(status), name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	for v, want := range map[string]string{"26.10.21-r1": "26.10.21", "26.10.21-1": "26.10.21", "26.10.21": "26.10.21"} {
		if got := Upstream(v); got != want {
			t.Errorf("Upstream(%s) = %s", v, got)
		}
	}
}

func TestAtLeast(t *testing.T) {
	for _, tc := range []struct {
		v, min string
		want   bool
	}{
		{"26.10.21", "26.9.8", true}, {"26.9.8", "26.9.8", true}, {"26.7.28", "26.9.8", false},
		{"26.9.10-1", "26.9.9-2", true}, {"26.9.9-2", "26.9.9-2", true}, {"26.9.9-1", "26.9.9-2", false},
		{"26.8.10-r1", "26.9.9-2", false}, {"26.10.21-r1", "26.10.21", true},
	} {
		if got := AtLeast(tc.v, tc.min); got != tc.want {
			t.Errorf("AtLeast(%s, %s) = %v", tc.v, tc.min, got)
		}
	}
}
