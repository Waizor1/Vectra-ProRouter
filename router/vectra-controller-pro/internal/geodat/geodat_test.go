package geodat

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// Encoders for a synthetic geoip.dat (the wire format the package reads).
func pbVarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbBytes(field int, v []byte) []byte {
	b := pbVarint(uint64(field<<3 | 2))
	b = append(b, pbVarint(uint64(len(v)))...)
	return append(b, v...)
}

func pbUint(field int, v uint64) []byte {
	return append(pbVarint(uint64(field<<3)), pbVarint(v)...)
}

func cidr(p string) []byte {
	pf := netip.MustParsePrefix(p)
	return append(pbBytes(1, pf.Addr().AsSlice()), pbUint(2, uint64(pf.Bits()))...)
}

func entry(code string, reverse bool, cidrs ...string) []byte {
	e := pbBytes(1, []byte(code))
	for _, c := range cidrs {
		e = append(e, pbBytes(2, cidr(c))...)
	}
	if reverse {
		e = append(e, pbUint(3, 1)...)
	}
	return pbBytes(1, e)
}

func TestParseCategories(t *testing.T) {
	var file []byte
	file = append(file, entry("PRIVATE", false, "10.0.0.0/8", "fc00::/7")...)
	file = append(file, entry("DIRECT", false, "5.8.0.0/16", "77.88.8.8/32", "2a02:6b8::/32")...)
	file = append(file, entry("NOTRU", true, "1.1.1.0/24")...)
	file = append(file, entry("direct", false, "9.9.9.0/24")...) // a second DIRECT: xray reads the first
	got, err := Parse(file, "direct", "Private")
	if err != nil {
		t.Fatal(err)
	}
	d := got["DIRECT"]
	if len(d.V4) != 2 || d.V4[0] != netip.MustParsePrefix("5.8.0.0/16") || d.V4[1] != netip.MustParsePrefix("77.88.8.8/32") {
		t.Fatalf("DIRECT v4 = %v", d.V4)
	}
	if len(d.V6) != 1 || d.V6[0] != netip.MustParsePrefix("2a02:6b8::/32") {
		t.Fatalf("DIRECT v6 = %v", d.V6)
	}
	if p := got["PRIVATE"]; len(p.V4) != 1 || len(p.V6) != 1 {
		t.Fatalf("PRIVATE = %+v", p)
	}
	if _, ok := got["NOTRU"]; ok {
		t.Fatal("a category not asked for was decoded")
	}
	if _, err := Parse(file, "NOTRU"); !errors.Is(err, ErrReverse) {
		t.Fatalf("reverse-match category: err %v, want ErrReverse", err)
	}
	if _, err := Parse(file[:len(file)-3], "DIRECT", "NOTRU"); err == nil {
		t.Fatal("a truncated file must be refused")
	}
}

// The real geoip.dat the package ships, when the build cache has it: the
// DIRECT category the fleet's route policy sends straight out.
func TestRealGeoIP(t *testing.T) {
	p := filepath.Join("..", "..", "dist", "cache", "geo", "geoip.dat")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no cached geoip.dat")
	}
	got, err := Load(p, "DIRECT", "RU")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got["DIRECT"].V4); n < 10000 {
		t.Fatalf("DIRECT has %d v4 prefixes, want thousands", n)
	}
	if n := len(got["RU"].V4); n < 5000 {
		t.Fatalf("RU has %d v4 prefixes", n)
	}
}

// Codes names every category, geoip.dat's and geosite.dat's alike (a
// geosite entry is its code, then domains in field 2).
func TestCodesListsEveryCategory(t *testing.T) {
	var file []byte
	file = append(file, entry("private", false, "10.0.0.0/8")...)
	file = append(file, entry("DIRECT", true, "5.8.0.0/16")...)
	site := append(pbBytes(1, []byte("russia-outside")), pbBytes(2, append(pbUint(1, 2), pbBytes(2, []byte("example.org"))...))...)
	file = append(file, pbBytes(1, site)...)
	got, err := Codes(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PRIVATE", "DIRECT", "RUSSIA-OUTSIDE"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	if _, err := Codes(file[:len(file)-2]); err == nil {
		t.Fatal("a truncated file must be refused")
	}
	if got, err := Codes(nil); err != nil || len(got) != 0 {
		t.Fatalf("an empty file: %v %v", got, err)
	}
}
