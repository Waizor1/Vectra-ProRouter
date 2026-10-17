package geodat

import "strings"

// Entry is one category of a geo file as it is stored: the record of a
// GeoIP or GeoSite message (the file's field 1), kept byte for byte, so a
// category moves from an upstream file into Vectra's without being decoded
// and encoded again.
type Entry struct {
	Code string // upper case
	Raw  []byte
}

// Select returns the categories of a geo file named by codes
// (case-insensitive), in the file's order; no codes selects every category.
// A code the file has twice keeps its first entry, as xray reads it. A code
// the file does not have is absent from the result.
func Select(b []byte, codes ...string) ([]Entry, error) {
	want := make(map[string]bool, len(codes))
	for _, c := range codes {
		want[strings.ToUpper(c)] = true
	}
	var out []Entry
	seen := map[string]bool{}
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
		if seen[code] || (len(want) > 0 && !want[code]) {
			continue
		}
		seen[code] = true
		out = append(out, Entry{Code: code, Raw: entry})
	}
	return out, nil
}

// Merge joins selections from several files into one list: a code an
// earlier list already has is dropped from a later one — the first source
// wins.
func Merge(lists ...[]Entry) []Entry {
	var out []Entry
	seen := map[string]bool{}
	for _, l := range lists {
		for _, e := range l {
			if seen[e.Code] {
				continue
			}
			seen[e.Code] = true
			out = append(out, e)
		}
	}
	return out
}

// Encode writes entries as a geo file: each record as the list's field 1,
// in order — a GeoIPList for GeoIP records, a GeoSiteList for GeoSite ones.
func Encode(entries []Entry) []byte {
	n := 0
	for _, e := range entries {
		n += 1 + 10 + len(e.Raw)
	}
	b := make([]byte, 0, n)
	for _, e := range entries {
		b = append(b, 1<<3|2)
		b = appendVarint(b, uint64(len(e.Raw)))
		b = append(b, e.Raw...)
	}
	return b
}

// Items counts the category's rules: its CIDRs (geoip) or domains (geosite),
// both field 2 of the entry.
func (e Entry) Items() (int, error) {
	n := 0
	r := reader{b: e.Raw}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return 0, err
		}
		if field == 2 && wire == 2 {
			n++
		}
		if err := r.skip(wire); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
