package sites

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Punycode, RFC 3492 — the encoding IDNA puts after "xn--". The standard
// library has none; this is the RFC's own algorithm (section 6), without the
// optional mixed-case annotation (domains are lowercased before encoding).

const (
	pcBase        = 36
	pcTMin        = 1
	pcTMax        = 26
	pcSkew        = 38
	pcDamp        = 700
	pcInitialBias = 72
	pcInitialN    = 128
	// pcMaxInt is the RFC's maxint: the arithmetic below is done in int64 and
	// held to 32 bits, so the result does not depend on the platform's int.
	pcMaxInt = 1<<31 - 1
)

var (
	errPunycodeOverflow = errors.New("punycode: overflow")
	errPunycodeInput    = errors.New("punycode: bad input")
)

func pcAdapt(delta, numPoints int64, first bool) int64 {
	if first {
		delta /= pcDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := int64(0)
	for delta > ((pcBase-pcTMin)*pcTMax)/2 {
		delta /= pcBase - pcTMin
		k += pcBase
	}
	return k + (pcBase-pcTMin+1)*delta/(delta+pcSkew)
}

// pcThreshold is t for the digit at position k (RFC 3492, 6.2/6.3).
func pcThreshold(k, bias int64) int64 {
	switch t := k - bias; {
	case t < pcTMin:
		return pcTMin
	case t > pcTMax:
		return pcTMax
	default:
		return t
	}
}

func pcEncodeDigit(d int64) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

func pcDecodeDigit(c byte) (int64, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int64(c-'0') + 26, true
	case c >= 'a' && c <= 'z':
		return int64(c - 'a'), true
	case c >= 'A' && c <= 'Z':
		return int64(c - 'A'), true
	}
	return 0, false
}

// punycodeEncode encodes s (RFC 3492 6.3): its basic (ASCII) code points
// first, a "-" when there are any, then the rest as generalized
// variable-length integers.
func punycodeEncode(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errPunycodeInput
	}
	input := []rune(s)
	var out strings.Builder
	for _, r := range input {
		if r < 0x80 {
			out.WriteByte(byte(r))
		}
	}
	b := int64(out.Len())
	h := b
	if b > 0 {
		out.WriteByte('-')
	}
	n, delta, bias := int64(pcInitialN), int64(0), int64(pcInitialBias)
	for h < int64(len(input)) {
		m := int64(pcMaxInt)
		for _, r := range input {
			if c := int64(r); c >= n && c < m {
				m = c
			}
		}
		if m-n > (pcMaxInt-delta)/(h+1) {
			return "", errPunycodeOverflow
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range input {
			c := int64(r)
			if c < n {
				if delta++; delta > pcMaxInt {
					return "", errPunycodeOverflow
				}
			}
			if c != n {
				continue
			}
			q := delta
			for k := int64(pcBase); ; k += pcBase {
				t := pcThreshold(k, bias)
				if q < t {
					break
				}
				out.WriteByte(pcEncodeDigit(t + (q-t)%(pcBase-t)))
				q = (q - t) / (pcBase - t)
			}
			out.WriteByte(pcEncodeDigit(q))
			bias = pcAdapt(delta, h+1, h == b)
			delta = 0
			h++
		}
		delta++
		n++
	}
	return out.String(), nil
}

// punycodeDecode is the inverse (RFC 3492 6.2). It fails on anything the
// encoder could not have produced: a non-basic byte, a bad digit, a code
// point that is basic, a surrogate or past U+10FFFF, an overflow.
func punycodeDecode(s string) (string, error) {
	var output []rune
	in := s
	// Everything before the LAST delimiter is literal — when that is at
	// least one code point; a delimiter at position 0 is not consumed.
	if d := strings.LastIndexByte(s, '-'); d > 0 {
		for i := 0; i < d; i++ {
			if s[i] >= 0x80 {
				return "", errPunycodeInput
			}
			output = append(output, rune(s[i]))
		}
		in = s[d+1:]
	}
	n, i, bias := int64(pcInitialN), int64(0), int64(pcInitialBias)
	for len(in) > 0 {
		oldi, w := i, int64(1)
		for k := int64(pcBase); ; k += pcBase {
			if len(in) == 0 {
				return "", errPunycodeInput
			}
			digit, ok := pcDecodeDigit(in[0])
			in = in[1:]
			if !ok {
				return "", errPunycodeInput
			}
			if digit > (pcMaxInt-i)/w {
				return "", errPunycodeOverflow
			}
			i += digit * w
			t := pcThreshold(k, bias)
			if digit < t {
				break
			}
			if w > pcMaxInt/(pcBase-t) {
				return "", errPunycodeOverflow
			}
			w *= pcBase - t
		}
		length := int64(len(output)) + 1
		bias = pcAdapt(i-oldi, length, oldi == 0)
		if i/length > pcMaxInt-n {
			return "", errPunycodeOverflow
		}
		n += i / length
		i %= length
		if n < 0x80 || n > utf8.MaxRune || (n >= 0xD800 && n <= 0xDFFF) {
			return "", errPunycodeInput
		}
		output = append(output, 0)
		copy(output[i+1:], output[i:])
		output[i] = rune(n)
		i++
	}
	return string(output), nil
}
