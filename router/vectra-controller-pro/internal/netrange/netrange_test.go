package netrange

import (
	"net/netip"
	"strings"
	"testing"
)

func pfx(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func join(rs []Range) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, r.String())
	}
	return strings.Join(parts, " ")
}

func TestFromPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.0/8":      "10.0.0.0-10.255.255.255",
		"77.88.8.8/32":    "77.88.8.8-77.88.8.8",
		"0.0.0.0/0":       "0.0.0.0-255.255.255.255",
		"5.8.0.1/16":      "5.8.0.0-5.8.255.255",
		"2a02:6b8::/32":   "2a02:6b8::-2a02:6b8:ffff:ffff:ffff:ffff:ffff:ffff",
		"2001:db8::1/128": "2001:db8::1-2001:db8::1",
	} {
		r := FromPrefix(netip.MustParsePrefix(in))
		if got := r.From.String() + "-" + r.To.String(); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestMergeAdjacentOverlappingAndFamilies(t *testing.T) {
	got := join(Merge(pfx(
		"10.0.1.0/24", "10.0.0.0/24", // adjacent: one /23
		"10.0.0.128/25", // inside
		"10.0.3.0/24",   // a gap at 10.0.2.0/24
		"2a02:6b8::/32", "2a02:6b9::/32",
		"77.88.8.8/32",
	)))
	want := "10.0.0.0/23 10.0.3.0/24 77.88.8.8 2a02:6b8::/31"
	if got != want {
		t.Fatalf("merged %q, want %q", got, want)
	}
}

func TestSubtract(t *testing.T) {
	rs := Merge(pfx("198.16.0.0/12", "8.8.8.0/24"))
	got := join(Subtract(rs, pfx("198.18.0.0/15", "8.8.8.8/32")))
	want := "8.8.8.0/29 8.8.8.9-8.8.8.255 198.16.0.0/15 198.20.0.0-198.31.255.255"
	if got != want {
		t.Fatalf("subtracted %q, want %q", got, want)
	}
	if got := join(Subtract(rs, nil)); got != join(rs) {
		t.Fatal("subtracting nothing changed the ranges")
	}
}

func TestLargestKeepsTheBiggestInOrder(t *testing.T) {
	rs := Merge(pfx("1.0.0.0/24", "2.0.0.0/16", "3.0.0.0/30", "4.0.0.0/20"))
	kept, share := Largest(rs, 2)
	if got := join(kept); got != "2.0.0.0/16 4.0.0.0/20" {
		t.Fatalf("kept %q", got)
	}
	want := float64(65536+4096) / float64(256+65536+4+4096)
	if share < want-1e-9 || share > want+1e-9 {
		t.Fatalf("share %v, want %v", share, want)
	}
	if all, s := Largest(rs, 10); len(all) != 4 || s != 1 {
		t.Fatalf("n >= len: %d ranges, share %v", len(all), s)
	}
	if none, s := Largest(rs, 0); none != nil || s != 0 {
		t.Fatal("n = 0 must keep nothing")
	}
}

func TestStringForms(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{"1.2.3.4", "1.2.3.4", "1.2.3.4"},
		{"1.2.3.0", "1.2.3.255", "1.2.3.0/24"},
		{"1.2.3.1", "1.2.3.255", "1.2.3.1-1.2.3.255"},
		{"1.2.0.0", "1.2.2.255", "1.2.0.0-1.2.2.255"},
		{"0.0.0.0", "255.255.255.255", "0.0.0.0/0"},
	} {
		r := Range{From: netip.MustParseAddr(tc.from), To: netip.MustParseAddr(tc.to)}
		if got := r.String(); got != tc.want {
			t.Errorf("%s-%s: %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}
