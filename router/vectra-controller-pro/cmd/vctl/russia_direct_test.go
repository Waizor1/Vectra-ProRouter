package main

import (
	"testing"

	"vectra-controller-pro/internal/localctl"
)

// Russian sites go direct only where the provider sends them through its
// Russian bridge: a document without BL-RU (PassWall's, native) keeps its
// splice key, so an upgrade re-renders nothing there.
func TestSpliceOptionsSendRussiaDirectOnlyWhereTheProviderHasItsBridge(t *testing.T) {
	withBridge := []byte(`{"outbounds":[{"tag":"DIRECT","protocol":"freedom"}],"routing":{"rules":[],"balancers":[{"tag":"BL-RU","selector":["b"]}]}}`)
	without := []byte(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`)
	if o, _ := spliceOptionsFor(withBridge, localctl.Overrides{}, true); !o.RussiaDirect {
		t.Fatal("the provider's bridge is not bypassed")
	}
	if o, _ := spliceOptionsFor(withBridge, localctl.Overrides{}, false); o.RussiaDirect {
		t.Fatal("switched off, still direct")
	}
	if o, _ := spliceOptionsFor(without, localctl.Overrides{}, true); o.RussiaDirect {
		t.Fatal("a document without BL-RU got the option (its key would change)")
	}
}

// The owner's country per service reaches the splice as it is kept.
func TestSpliceOptionsCarryTheServiceCountries(t *testing.T) {
	doc := []byte(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`)
	o, _ := spliceOptionsFor(doc, localctl.Overrides{Services: map[string]string{"tiktok": "DE"}}, true)
	if o.Services["tiktok"] != "DE" || len(o.Services) != 1 {
		t.Fatalf("services %v", o.Services)
	}
}
