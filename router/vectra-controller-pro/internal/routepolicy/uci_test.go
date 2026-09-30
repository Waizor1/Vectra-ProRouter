package routepolicy

import (
	"reflect"
	"testing"
)

func TestParseUCI(t *testing.T) {
	text := `
config global 'vectra_global'
	option node 'myshunt'
	option enabled '0'

config shunt_rules 'WorldProxy'
	option remarks 'WorldProxy'
	option network 'tcp,udp'
	option domain_list 'geosite:META
domain:example.org'
	list ip_list '1.2.3.0/24'
	list ip_list "5.6.7.8"

config nodes 'kKUp9qPl'
	option remarks 'it'\''s here'
	option port 50055 # a comment

config subscribe_list
	option remark 'sub'
config subscribe_list
	option remark 'sub2'
`
	secs, err := ParseUCI(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 5 {
		t.Fatalf("%d sections, want 5", len(secs))
	}
	if secs[0].Type != "global" || secs[0].Name != "vectra_global" || secs[0].Get("node") != "myshunt" {
		t.Fatalf("section 0: %+v", secs[0])
	}
	wp := secs[1]
	if wp.Get("domain_list") != "geosite:META\ndomain:example.org" {
		t.Fatalf("a multi-line value: %q", wp.Get("domain_list"))
	}
	if !reflect.DeepEqual(wp.Lists["ip_list"], []string{"1.2.3.0/24", "5.6.7.8"}) {
		t.Fatalf("lists: %v", wp.Lists["ip_list"])
	}
	if secs[2].Get("remarks") != "it's here" || secs[2].Get("port") != "50055" {
		t.Fatalf("quoting/comment: %+v", secs[2].Options)
	}
	if !secs[3].Anonymous || secs[3].Name != "@subscribe_list[0]" || secs[4].Name != "@subscribe_list[1]" {
		t.Fatalf("anonymous sections: %q %q", secs[3].Name, secs[4].Name)
	}
	for _, bad := range []string{"option x 'y'", "config", "config a b c d", "option 'unterminated", "frobnicate x"} {
		if _, err := ParseUCI("config t 'n'\n" + bad); err == nil && bad != "option x 'y'" {
			t.Errorf("%q: no error", bad)
		}
	}
	if _, err := ParseUCI("option x 'y'"); err == nil {
		t.Error("an option outside a section must be an error")
	}
}
