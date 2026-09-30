package sites

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// RFC 3492 section 7.1, the sample strings whose code points are unambiguous
// (the RFC's mixed-case annotation aside: the encoder writes lowercase, and
// the comparison ignores case). Cross-checked against Python's "punycode"
// codec, an independent implementation of the same RFC.
var rfc3492Samples = []struct{ name, unicode, puny string }{
	{"(B) Chinese (simplified)", "他们为什么不说中文", "ihqwcrb4cv8a8dqg056pqjye"},
	{"(C) Chinese (traditional)", "他們爲什麽不說中文", "ihqwctvzc91f659drss3x8bo0yb"},
	{"(D) Czech", "Pročprostěnemluvíčesky", "Proprostnemluvesky-uyb24dma41a"},
	{"(I) Russian", "почемужеонинеговорятпорусски", "b1abfaaepdrnnbgefbadotcwatmq2g4l"},
	{"(J) Spanish", "PorquénopuedensimplementehablarenEspañol", "PorqunopuedensimplementehablarenEspaol-fmd56a"},
	{"(L) 3<nen>B<gumi><kinpachi><sensei>", "3年B組金八先生", "3B-ww4c5e180e575a65lsy2b"},
	{"(O) <hitotsu><yane><no><shita>2", "ひとつ屋根の下2", "2-u9tlzr9756bt3uc0v"},
	{"(P) Maji<de>Koi<suru>5<byou><mae>", "MajiでKoiする5秒前", "MajiKoi5-783gue6qz075azm5e"},
	{"(Q) <pafii>de<runba>", "パフィーdeルンバ", "de-jg4avhby1noc0d"},
	{"(R) <sono><supiido><de>", "そのスピードで", "d9juau41awczczp"},
}

func TestPunycodeRFC3492Samples(t *testing.T) {
	for _, s := range rfc3492Samples {
		got, err := punycodeEncode(s.unicode)
		if err != nil || !strings.EqualFold(got, s.puny) {
			t.Errorf("%s: encode = %q, %v; want %q", s.name, got, err, s.puny)
		}
		back, err := punycodeDecode(s.puny)
		if err != nil || back != s.unicode {
			t.Errorf("%s: decode(%q) = %q, %v; want %q", s.name, s.puny, back, err, s.unicode)
		}
	}
}

func TestPunycodeDecodeRefusesWhatNoEncoderWrites(t *testing.T) {
	for _, in := range []string{
		"-abc",           // a delimiter at 0 is not consumed, and "-" is not a digit
		"ab!c",           // not a digit
		"99999999999999", // a number that never ends: truncated, or past 32 bits
		"aé-b",           // a non-basic code point among the literal ones
	} {
		if out, err := punycodeDecode(in); err == nil {
			t.Errorf("decode(%q) = %q, want an error", in, out)
		}
	}
	// Valid punycode that is no valid LABEL is the label check's to refuse
	// (TestParseRefusesGarbage): all literal, or a control character.
	if out, err := punycodeDecode("abc-"); err != nil || out != "abc" {
		t.Errorf("decode(abc-) = %q, %v", out, err)
	}
	if out, err := punycodeDecode("a"); err != nil || out != "\u0080" {
		t.Errorf("decode(a) = %q, %v", out, err)
	}
}

func TestParseNormalizesWhatAPersonPastes(t *testing.T) {
	for _, tc := range []struct {
		in, display, ascii string // ascii "" = an address or a CIDR
	}{
		// Domains, and what surrounds them in a URL.
		{"sberbank.ru", "sberbank.ru", "sberbank.ru"},
		{"  SberBank.RU \t", "sberbank.ru", "sberbank.ru"},
		{"https://www.sberbank.ru/ru/person?utm=1#top", "sberbank.ru", "sberbank.ru"},
		{"HTTPS://Online.Sberbank.ru", "online.sberbank.ru", "online.sberbank.ru"},
		{"http://user:secret@online.sberbank.ru:8443/login", "online.sberbank.ru", "online.sberbank.ru"},
		{"user@example.org", "example.org", "example.org"},
		{"//example.org/path", "example.org", "example.org"},
		{"example.org/path?q", "example.org", "example.org"},
		{"example.org:443", "example.org", "example.org"},
		{"example.org:", "example.org", "example.org"},
		{"example.org.", "example.org", "example.org"},
		{"*.example.org", "example.org", "example.org"},
		{".example.org", "example.org", "example.org"},
		{"*.www.example.org", "example.org", "example.org"},
		{"www.example.org", "example.org", "example.org"},
		{"www.com", "www.com", "www.com"}, // a site of its own, not all of .com
		{"www2.example.org", "www2.example.org", "www2.example.org"},
		// IDN: kept in Unicode, rendered in punycode.
		{"госуслуги.рф", "госуслуги.рф", "xn--c1aapkosapc.xn--p1ai"},
		{"https://ГОСУСЛУГИ.РФ/help", "госуслуги.рф", "xn--c1aapkosapc.xn--p1ai"},
		{"XN--C1AAPKOSAPC.XN--P1AI", "госуслуги.рф", "xn--c1aapkosapc.xn--p1ai"},
		{"xn--c1aapkosapc.рф", "госуслуги.рф", "xn--c1aapkosapc.xn--p1ai"},
		{"www.сбербанк.рф", "сбербанк.рф", "xn--80abap1arsf.xn--p1ai"},
		{"пример.испытание", "пример.испытание", "xn--e1afmkfd.xn--80akhbyknj4f"},
		{"münchen.de", "münchen.de", "xn--mnchen-3ya.de"},
		{"日本語。jp", "日本語.jp", "xn--wgv71a119e.jp"},     // ideographic full stop
		{"ｅｘａｍｐｌｅ．ｃｏｍ", "example.com", "example.com"}, // full-width input
		// A single label is a whole top-level domain, as the provider uses them.
		{"рф", "рф", "xn--p1ai"},
		{"RU", "ru", "ru"},
		// Addresses and CIDRs.
		{"1.2.3.4", "1.2.3.4", ""},
		{"1.2.3.4:80", "1.2.3.4", ""},
		{"http://1.2.3.4/path", "1.2.3.4", ""},
		{"1.2.3.0/24", "1.2.3.0/24", ""},
		{"1.2.3.4/24", "1.2.3.0/24", ""},
		{"1.2.3.4/32", "1.2.3.4", ""},
		{"0.0.0.0/0", "0.0.0.0/0", ""},
		{"2001:DB8::1", "2001:db8::1", ""},
		{"[2001:db8::1]", "2001:db8::1", ""},
		{"[2001:db8::1]:443", "2001:db8::1", ""},
		{"https://[2001:db8::1]:8443/x", "2001:db8::1", ""},
		{"2001:db8::/32", "2001:db8::/32", ""},
		{"2001:db8::7/32", "2001:db8::/32", ""},
		{"[2001:db8::]/32", "2001:db8::/32", ""},
		{"2001:db8::1/128", "2001:db8::1", ""},
		{"::ffff:1.2.3.4", "1.2.3.4", ""},
		{"::ffff:1.2.3.0/120", "1.2.3.0/24", ""},
	} {
		s, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if s.Display != tc.display || s.Domain != (tc.ascii != "") || s.ASCII != tc.ascii {
			t.Errorf("Parse(%q) = %+v; want display %q ascii %q", tc.in, s, tc.display, tc.ascii)
		}
		if !s.Domain && !s.Prefix.IsValid() {
			t.Errorf("Parse(%q): an address without a prefix", tc.in)
		}
		// Canonical is a fixed point: what is kept parses to itself.
		if again, err := Parse(s.Display); err != nil || again != s {
			t.Errorf("Parse(%q) is not a fixed point: %+v, %v", s.Display, again, err)
		}
	}
}

func TestParseRefusesGarbage(t *testing.T) {
	for _, in := range []string{
		"", "   ", "\t",
		"exa mple.com", "exa\u00A0mple.com", "exa\x00mple.com", "a\u200Bb.com", "\xff.com",
		"http://", "https://:443", "https://", "ht tp://x.com", "1http://x.com",
		"foo..com", "..example.com", "example.com..", ".", "*", "*.", "-", "-foo.com", "foo-.com",
		"ex_ample.com", "i❤.ws", "@", "user@", "exa$mple.com", "\"quoted\".com",
		"1.2.3", "256.1.1.1", "12345", "01.02.03.04", "0x7f.1",
		"1.2.3.4/33", "1.2.3.4/", "1.2.3.4/-1", "1.2.3.4/+8", "1.2.3.4/24/x", "1.2.3.4/24?x", "2001:db8::/129",
		"fe80::1%eth0", "fe80::%eth0/64",
		"example.com:abc", "example.com:99999", "example.com:0", "example.com:-1",
		"[2001:db8::1", "[1.2.3.4]:80", "[2001:db8::1]x", "[2001:db8::1]:x", "[example.com]",
		"javascript:alert(1)",
		strings.Repeat("a", 64) + ".com",
		strings.Repeat("а", 60) + ".рф",   // 66 characters in punycode
		strings.Repeat("a.", 127) + "com", // 257 characters
		strings.Repeat("x", maxEntryLen+1),
		"xn--.com", "xn--abc-d.com", // an empty and a control-character A-label
		"xn--wca.de", // decodes to "Ü": not what the encoder writes for a lowercase name
		"xn--ü.com",  // xn-- only ever starts a punycode label
		"xn--mnchen-3ya-.de",
	} {
		if s, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted garbage as %+v", in, s)
		}
	}
}

func TestCoversKnowsWhereASiteEnds(t *testing.T) {
	p := func(s string) Site {
		t.Helper()
		out, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"sberbank.ru", "sberbank.ru", true},
		{"sberbank.ru", "online.sberbank.ru", true},
		{"online.sberbank.ru", "sberbank.ru", false},
		{"sberbank.ru", "notsberbank.ru", false}, // a suffix, not a subdomain
		{"рф", "госуслуги.рф", true},
		{"1.2.3.0/24", "1.2.3.4", true},
		{"1.2.3.0/24", "1.2.3.0/25", true},
		{"1.2.3.0/25", "1.2.3.0/24", false},
		{"1.2.3.4", "1.2.3.0/24", false},
		{"0.0.0.0/0", "2001:db8::1", false}, // another family
		{"1.2.3.4", "1.2.3.4", true},
		{"example.org", "1.2.3.4", false},
	} {
		if got := p(tc.a).Covers(p(tc.b)); got != tc.want {
			t.Errorf("%s covers %s = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if p(tc.a).Covers(p(tc.b)) && p(tc.a).Display != p(tc.b).Display && p(tc.a).Specificity() >= p(tc.b).Specificity() {
			t.Errorf("%s covers %s but is not less specific", tc.a, tc.b)
		}
	}
	if m := p("госуслуги.рф").Matcher(); m != "domain:xn--c1aapkosapc.xn--p1ai" {
		t.Errorf("matcher = %q", m)
	}
	if m := p("1.2.3.4/24").Matcher(); m != "1.2.3.0/24" {
		t.Errorf("matcher = %q", m)
	}
}

func TestNormalizeListsKeepsOneOfEachAndNamesTheFirstRefusal(t *testing.T) {
	d, p, err := NormalizeLists(
		[]string{"https://www.sberbank.ru/", "госуслуги.рф", "SBERBANK.RU", "1.2.3.4/24", "sberbank.ru:443"},
		[]string{"example.org", "exa mple.org"},
	)
	if err == nil {
		t.Fatalf("an invalid proxy entry was accepted: %v %v", d, p)
	}
	var le *ListError
	if !errors.As(err, &le) || le.List != "proxy" || le.Index != 1 {
		t.Fatalf("err = %v, want proxy[1]", err)
	}

	d, p, err = NormalizeLists(
		[]string{"https://www.sberbank.ru/", "госуслуги.рф", "SBERBANK.RU", "1.2.3.4/24", "sberbank.ru:443"},
		[]string{"example.org", "*.example.org"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d, ","); got != "sberbank.ru,госуслуги.рф,1.2.3.0/24" {
		t.Errorf("direct = %s", got)
	}
	if got := strings.Join(p, ","); got != "example.org" {
		t.Errorf("proxy = %s", got)
	}

	// The detail names the first refused entry, as sent.
	_, _, err = NormalizeLists([]string{"ok.ru", "exa mple..com", "bad..com"}, []string{})
	if err == nil || !strings.HasPrefix(err.Error(), `direct[1] "exa mple..com": `) {
		t.Errorf("err = %v", err)
	}

	// One site, two ways — even in two spellings.
	_, _, err = NormalizeLists([]string{"example.org"}, []string{"https://www.example.org/x"})
	if !errors.As(err, &le) || le.List != "proxy" || le.Index != 0 || !strings.Contains(err.Error(), "direct list too") {
		t.Errorf("err = %v", err)
	}

	// At most Max each — counted after duplicates are dropped.
	var many, dups []string
	for i := 0; i <= Max; i++ {
		many = append(many, fmt.Sprintf("s%d.example", i))
		dups = append(dups, "same.example")
	}
	if _, _, err := NormalizeLists(many, nil); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at most %d", Max)) {
		t.Errorf("%d sites accepted: %v", len(many), err)
	}
	if d, _, err := NormalizeLists(many[:Max], dups); err != nil || len(d) != Max {
		t.Errorf("exactly %d sites, and %d copies of one: %v", Max, len(dups), err)
	}

	// Empty is empty, not nil-vs-empty noise.
	d, p, err = NormalizeLists(nil, []string{})
	if err != nil || len(d) != 0 || len(p) != 0 {
		t.Errorf("empty lists = %v %v %v", d, p, err)
	}
}

func TestListErrorQuotesABoundedEntry(t *testing.T) {
	_, _, err := NormalizeLists([]string{strings.Repeat("я", 500) + " x"}, nil)
	if err == nil || len(err.Error()) > 400 {
		t.Fatalf("err = %d bytes: %v", len(err.Error()), err)
	}
}
