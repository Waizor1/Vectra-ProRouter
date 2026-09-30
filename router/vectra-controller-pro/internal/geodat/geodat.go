// Package geodat reads the address lists of Xray's geoip.dat.
//
// The file is a protobuf GeoIPList: entries (field 1) of GeoIP — a
// country_code (1), CIDRs (2: ip bytes 1, prefix 2) and reverse_match (3).
// vctl needs a category's addresses to route them in the kernel (see
// cmd/vctl/direct_bypass.go); the format is small enough to read without a
// protobuf dependency, and only the categories asked for are decoded.
package geodat

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

// Category is one geoip category's addresses.
type Category struct {
	V4, V6 []netip.Prefix
}

// ErrReverse is returned for a category that matches everything EXCEPT its
// list (reverse_match): it cannot be expressed as a set of addresses.
var ErrReverse = errors.New("geodat: reverse-match category")

// Load reads the named categories (case-insensitive) from a geoip.dat file.
// A category the file does not have is absent from the map.
func Load(path string, codes ...string) (map[string]Category, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b, codes...)
}

// Parse is Load on the file's bytes.
func Parse(b []byte, codes ...string) (map[string]Category, error) {
	want := make(map[string]bool, len(codes))
	for _, c := range codes {
		want[strings.ToUpper(c)] = true
	}
	out := map[string]Category{}
	r := reader{b: b}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return nil, err
		}
		if field != 1 || wire != 2 {
			if err := r.skip(wire); err != nil {
				return nil, err
			}
			continue
		}
		entry, err := r.bytes()
		if err != nil {
			return nil, err
		}
		code, err := entryCode(entry)
		if err != nil {
			return nil, err
		}
		code = strings.ToUpper(code)
		if _, dup := out[code]; dup || !want[code] {
			continue // a code twice: xray reads the first, and so do we
		}
		cat, err := decodeEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("geodat: %s: %w", code, err)
		}
		out[code] = cat
	}
	return out, nil
}

// Codes lists the categories a geo file has, upper-cased: geoip.dat's and
// geosite.dat's alike — both are lists of entries (field 1) named by their
// field 1. Nothing but the names is decoded.
func Codes(b []byte) ([]string, error) {
	var out []string
	r := reader{b: b}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return nil, err
		}
		if field != 1 || wire != 2 {
			if err := r.skip(wire); err != nil {
				return nil, err
			}
			continue
		}
		entry, err := r.bytes()
		if err != nil {
			return nil, err
		}
		code, err := entryCode(entry)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.ToUpper(code))
	}
	return out, nil
}

func entryCode(entry []byte) (string, error) {
	r := reader{b: entry}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return "", err
		}
		if field == 1 && wire == 2 {
			v, err := r.bytes()
			return string(v), err
		}
		if err := r.skip(wire); err != nil {
			return "", err
		}
	}
	return "", nil
}

func decodeEntry(entry []byte) (Category, error) {
	var cat Category
	r := reader{b: entry}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return cat, err
		}
		switch {
		case field == 2 && wire == 2:
			cidr, err := r.bytes()
			if err != nil {
				return cat, err
			}
			p, err := decodeCIDR(cidr)
			if err != nil {
				return cat, err
			}
			if p.Addr().Is4() {
				cat.V4 = append(cat.V4, p)
			} else {
				cat.V6 = append(cat.V6, p)
			}
		case field == 3 && wire == 0:
			v, err := r.varint()
			if err != nil {
				return cat, err
			}
			if v != 0 {
				return cat, ErrReverse
			}
		default:
			if err := r.skip(wire); err != nil {
				return cat, err
			}
		}
	}
	return cat, nil
}

func decodeCIDR(b []byte) (netip.Prefix, error) {
	var ip []byte
	var bits uint64
	r := reader{b: b}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return netip.Prefix{}, err
		}
		switch {
		case field == 1 && wire == 2:
			if ip, err = r.bytes(); err != nil {
				return netip.Prefix{}, err
			}
		case field == 2 && wire == 0:
			if bits, err = r.varint(); err != nil {
				return netip.Prefix{}, err
			}
		default:
			if err := r.skip(wire); err != nil {
				return netip.Prefix{}, err
			}
		}
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Prefix{}, fmt.Errorf("an address of %d bytes", len(ip))
	}
	if int(bits) > addr.BitLen() {
		return netip.Prefix{}, fmt.Errorf("prefix /%d on %s", bits, addr)
	}
	return netip.PrefixFrom(addr, int(bits)).Masked(), nil
}

// reader walks protobuf wire format.
type reader struct {
	b []byte
	i int
}

var errTruncated = errors.New("geodat: truncated")

func (r *reader) done() bool { return r.i >= len(r.b) }

func (r *reader) varint() (uint64, error) {
	var v uint64
	for s := uint(0); s < 64; s += 7 {
		if r.i >= len(r.b) {
			return 0, errTruncated
		}
		c := r.b[r.i]
		r.i++
		v |= uint64(c&0x7f) << s
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, errors.New("geodat: varint overflow")
}

func (r *reader) key() (field int, wire int, err error) {
	k, err := r.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(k >> 3), int(k & 7), nil
}

func (r *reader) bytes() ([]byte, error) {
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.b)-r.i) {
		return nil, errTruncated
	}
	v := r.b[r.i : r.i+int(n)]
	r.i += int(n)
	return v, nil
}

func (r *reader) skip(wire int) error {
	switch wire {
	case 0:
		_, err := r.varint()
		return err
	case 1:
		r.i += 8
	case 2:
		_, err := r.bytes()
		return err
	case 5:
		r.i += 4
	default:
		return fmt.Errorf("geodat: wire type %d", wire)
	}
	if r.i > len(r.b) {
		return errTruncated
	}
	return nil
}
