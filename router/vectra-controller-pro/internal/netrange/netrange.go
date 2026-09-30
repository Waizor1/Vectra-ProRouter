// Package netrange turns address lists into the fewest address ranges: what
// an nft interval set holds. Each element of such a set costs the kernel
// ~210 bytes and the loading nft process ~1.5 KB, so a list is merged before
// it is loaded — the fleet's geoip:DIRECT is 35,572 prefixes and 14,807
// ranges.
package netrange

import (
	"math"
	"net/netip"
	"sort"
)

// Range is an inclusive address range of one family.
type Range struct {
	From, To netip.Addr
}

// FromPrefix is the range a prefix covers.
func FromPrefix(p netip.Prefix) Range {
	p = p.Masked()
	a := p.Addr()
	b := a.As16()
	hostBits := a.BitLen() - p.Bits()
	off := 16 - a.BitLen()/8 // v4 lives in the last 4 bytes of As16
	for i := 15; i >= off && hostBits > 0; i-- {
		n := hostBits
		if n > 8 {
			n = 8
		}
		b[i] |= byte(1<<n - 1)
		hostBits -= n
	}
	to := netip.AddrFrom16(b)
	if a.Is4() {
		to = to.Unmap()
	}
	return Range{From: a, To: to}
}

// Merge sorts the prefixes and merges overlapping and adjacent ones. The
// result is ordered, disjoint and never adjacent; the families stay apart
// (v4 ranges first).
func Merge(ps []netip.Prefix) []Range {
	rs := make([]Range, 0, len(ps))
	for _, p := range ps {
		if p.IsValid() {
			rs = append(rs, FromPrefix(p))
		}
	}
	return mergeRanges(rs)
}

func mergeRanges(rs []Range) []Range {
	sort.Slice(rs, func(i, j int) bool {
		if c := rs[i].From.Compare(rs[j].From); c != 0 {
			return c < 0
		}
		return rs[i].To.Compare(rs[j].To) < 0
	})
	out := rs[:0]
	for _, r := range rs {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.From.Is4() == r.From.Is4() {
				next := last.To.Next()
				if r.From.Compare(last.To) <= 0 || (next.IsValid() && r.From == next) {
					if r.To.Compare(last.To) > 0 {
						last.To = r.To
					}
					continue
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// Subtract removes every address of the excluded prefixes from rs (merged
// ranges, as Merge returns them).
func Subtract(rs []Range, excl []netip.Prefix) []Range {
	if len(excl) == 0 {
		return rs
	}
	cut := Merge(append([]netip.Prefix(nil), excl...))
	var out []Range
	for _, r := range rs {
		pieces := []Range{r}
		for _, x := range cut {
			if x.From.Is4() != r.From.Is4() {
				continue
			}
			var next []Range
			for _, p := range pieces {
				if x.To.Compare(p.From) < 0 || x.From.Compare(p.To) > 0 {
					next = append(next, p)
					continue
				}
				if x.From.Compare(p.From) > 0 {
					next = append(next, Range{From: p.From, To: x.From.Prev()})
				}
				if x.To.Compare(p.To) < 0 {
					next = append(next, Range{From: x.To.Next(), To: p.To})
				}
			}
			pieces = next
		}
		out = append(out, pieces...)
	}
	return out
}

// Size is how many addresses r covers (approximate beyond 2^53).
func Size(r Range) float64 {
	return addrFloat(r.To) - addrFloat(r.From) + 1
}

func addrFloat(a netip.Addr) float64 {
	b := a.As16()
	if a.Is4() {
		return float64(uint32(b[12])<<24 | uint32(b[13])<<16 | uint32(b[14])<<8 | uint32(b[15]))
	}
	var v float64
	for _, c := range b {
		v = v*256 + float64(c)
	}
	return v
}

// Largest keeps the n ranges covering the most addresses (all of them when n
// covers the list), back in address order, and reports the share of the
// list's addresses they cover (1 when nothing was dropped).
func Largest(rs []Range, n int) ([]Range, float64) {
	if n >= len(rs) {
		return rs, 1
	}
	if n <= 0 {
		return nil, 0
	}
	idx := make([]int, len(rs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return Size(rs[idx[a]]) > Size(rs[idx[b]]) })
	var total, kept float64
	for _, r := range rs {
		total += Size(r)
	}
	keep := idx[:n]
	sort.Ints(keep)
	out := make([]Range, 0, n)
	for _, i := range keep {
		out = append(out, rs[i])
		kept += Size(rs[i])
	}
	if total == 0 || math.IsInf(total, 0) {
		return out, 0
	}
	return out, kept / total
}

// String is r as an nft set element: an address, a prefix when r is exactly
// one, or "from-to".
func (r Range) String() string {
	if r.From == r.To {
		return r.From.String()
	}
	for bits := r.From.BitLen(); bits >= 0; bits-- {
		p := netip.PrefixFrom(r.From, bits)
		if p.Masked().Addr() != r.From {
			break
		}
		if pr := FromPrefix(p); pr.To == r.To {
			return p.String()
		} else if pr.To.Compare(r.To) > 0 {
			break
		}
	}
	return r.From.String() + "-" + r.To.String()
}
