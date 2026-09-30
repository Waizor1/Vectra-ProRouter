package uci

import (
	"reflect"
	"testing"
)

const wireless = `
config wifi-device 'radio0'
	option type 'mac80211'
	option band '2g'
	option disabled '1'

config wifi-iface
	option device 'radio0'
	option mode 'ap'
	option ssid "Open\"Wrt"   # a comment
	option encryption none

config wifi-device radio1
	option hwmode '11a'

config wifi-iface 'guest'
	option device 'radio1'
	option mode 'ap'
	option key 'it'\''s "fine"'
	list maclist '00:11:22:33:44:55'
	list maclist '66:77:88:99:aa:bb'

config wifi-iface
	option mode 'sta'
`

func TestParseReadsSectionsTheWayUCIDoes(t *testing.T) {
	f, err := Parse(wireless)
	if err != nil {
		t.Fatal(err)
	}
	ifaces := f.OfType("wifi-iface")
	if len(ifaces) != 3 || len(f.OfType("wifi-device")) != 2 {
		t.Fatalf("sections = %+v", f.Sections)
	}
	if got := []string{ifaces[0].Ref(), ifaces[1].Ref(), ifaces[2].Ref()}; !reflect.DeepEqual(got, []string{"@wifi-iface[0]", "guest", "@wifi-iface[2]"}) {
		t.Errorf("refs = %v", got)
	}
	if ifaces[0].Get("ssid") != `Open"Wrt` || ifaces[0].Get("encryption") != "none" {
		t.Errorf("first iface = %+v", ifaces[0].Options)
	}
	if ifaces[1].Get("key") != `it's "fine"` {
		t.Errorf("joined quoting = %q", ifaces[1].Get("key"))
	}
	if got := ifaces[1].Lists["maclist"]; len(got) != 2 || got[1] != "66:77:88:99:aa:bb" {
		t.Errorf("list = %v", got)
	}
	if r := f.Named("radio1"); r == nil || r.Get("hwmode") != "11a" || r.Ref() != "radio1" {
		t.Errorf("radio1 = %+v", r)
	}
}

func TestParseRefusesWhatUCIWouldRefuse(t *testing.T) {
	for name, src := range map[string]string{
		"an option outside a section": "option ssid x\n",
		"an unknown statement":        "config a 'b'\n\tssid = x\n",
		"an option without a value":   "config a 'b'\n\toption ssid\n",
		"an unterminated quote":       "config a 'b\n",
		"an unterminated dquote":      "config a \"b\n",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestStatementsFollowUCIQuoting(t *testing.T) {
	stmts, err := Statements("option a 'x y'\"z\"w  # c\noption b \"l1\\\nl2\" \\# \n")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"option", "a", "x yzw"}, {"option", "b", "l1l2", "#"}}
	if !reflect.DeepEqual(stmts, want) {
		t.Fatalf("statements = %q, want %q", stmts, want)
	}
}
