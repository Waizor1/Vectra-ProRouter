package geodat

import (
	"bytes"
	"slices"
	"testing"
)

func siteEntry(code string, domains ...string) []byte {
	e := pbBytes(1, []byte(code))
	for _, d := range domains {
		e = append(e, pbBytes(2, append(pbUint(1, 2), pbBytes(2, []byte(d))...))...)
	}
	return pbBytes(1, e)
}

func codesOf(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Code)
	}
	return out
}

func TestSelectByCode(t *testing.T) {
	var file []byte
	file = append(file, entry("PRIVATE", false, "10.0.0.0/8")...)
	file = append(file, entry("ru", false, "5.8.0.0/16", "2a02:6b8::/32")...)
	file = append(file, entry("CN", false, "1.0.1.0/24")...)
	file = append(file, entry("TELEGRAM", false, "91.108.4.0/22")...)
	file = append(file, entry("Ru", false, "9.9.9.0/24")...) // a second RU: the first is the one

	got, err := Select(file, "telegram", "RU", "NOSUCH")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"RU", "TELEGRAM"}; !slices.Equal(codesOf(got), want) {
		t.Fatalf("codes = %v, want %v (file order, upper case)", codesOf(got), want)
	}
	if n, err := got[0].Items(); err != nil || n != 2 {
		t.Fatalf("RU items = %d %v, want the first RU's 2", n, err)
	}

	// The written file reads back with the package's own readers.
	out := Encode(got)
	if codes, err := Codes(out); err != nil || !slices.Equal(codes, []string{"RU", "TELEGRAM"}) {
		t.Fatalf("Codes(Encode) = %v %v", codes, err)
	}
	cats, err := Parse(out, "ru", "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(cats["RU"].V4) != 1 || len(cats["RU"].V6) != 1 || len(cats["TELEGRAM"].V4) != 1 {
		t.Fatalf("Parse(Encode) = %+v", cats)
	}
	if cats["RU"].V4[0].String() != "5.8.0.0/16" {
		t.Fatalf("RU = %v, want the first RU's addresses", cats["RU"].V4)
	}
}

func TestSelectEveryCodeKeepsRecordsVerbatim(t *testing.T) {
	var file []byte
	file = append(file, siteEntry("ANIME", "anilibria.tv", "shikimori.one")...)
	file = append(file, siteEntry("youtube", "youtube.com")...)
	got, err := Select(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ANIME", "YOUTUBE"}; !slices.Equal(codesOf(got), want) {
		t.Fatalf("codes = %v, want %v", codesOf(got), want)
	}
	// A whole file selected and written again is the same bytes: the code's
	// case inside the record is the upstream's own.
	if out := Encode(got); !bytes.Equal(out, file) {
		t.Fatalf("Encode(Select(file)) differs from the file")
	}
	if n, _ := got[0].Items(); n != 2 {
		t.Fatalf("ANIME items = %d, want 2", n)
	}
}

func TestMergeFirstSourceWins(t *testing.T) {
	a, err := Select(append(entry("DIRECT", false, "5.8.0.0/16"), entry("PRIVATE", false, "10.0.0.0/8")...))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Select(append(entry("private", false, "192.168.0.0/16", "172.16.0.0/12"), entry("RU", false, "5.8.0.0/16")...))
	if err != nil {
		t.Fatal(err)
	}
	m := Merge(a, b)
	if want := []string{"DIRECT", "PRIVATE", "RU"}; !slices.Equal(codesOf(m), want) {
		t.Fatalf("codes = %v, want %v", codesOf(m), want)
	}
	cats, err := Parse(Encode(m), "PRIVATE")
	if err != nil {
		t.Fatal(err)
	}
	if p := cats["PRIVATE"].V4; len(p) != 1 || p[0].String() != "10.0.0.0/8" {
		t.Fatalf("PRIVATE = %v, want the first source's", p)
	}
}

func TestSelectRefusesBrokenFiles(t *testing.T) {
	file := append(entry("RU", false, "5.8.0.0/16"), entry("TELEGRAM", false, "91.108.4.0/22")...)
	if _, err := Select(file[:len(file)-3], "RU"); err == nil {
		t.Fatal("a truncated file must be refused, even past the codes asked for")
	}
	if got, err := Select(nil); err != nil || len(got) != 0 {
		t.Fatalf("an empty file: %v %v", got, err)
	}
	if _, err := (Entry{Code: "X", Raw: []byte{0x12, 0x05, 'a'}}).Items(); err == nil {
		t.Fatal("a truncated record must be refused")
	}
}
