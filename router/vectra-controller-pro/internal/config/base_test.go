package config_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"vectra-controller-pro/internal/config"
)

// The operator config vctl runs on before it is linked is the panel's, but
// for the subscription: proven against the panel's own output (the golden
// file, testdata/panel/README.md), not against a copy of its numbers.
func TestTheBaseConfigIsThePanelsWithoutTheSubscription(t *testing.T) {
	panel, err := config.Load(filepath.Join("testdata", "panel", "operator-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	panel.Subscriptions = nil
	panel.Instance.Name = "artem-lutfulin"
	config.ApplyDefaults(panel)

	base := config.Base("artem-lutfulin")
	if err := config.Validate(base); err != nil {
		t.Fatalf("the base config does not validate: %v", err)
	}
	if !reflect.DeepEqual(base, panel) {
		t.Fatalf("the base config drifted from the panel's:\n got %+v\nwant %+v", base, panel)
	}
	if len(base.Subscriptions) != 0 || base.UI != nil {
		t.Fatal("the base config names a subscription or a UI policy: those are the panel's to give")
	}
	// Each call its own: the daemon may change the one it holds.
	base.Inbounds.Tproxy.Port = 1
	if config.Base("x").Inbounds.Tproxy.Port != 12345 {
		t.Fatal("Base shares its inbound between calls")
	}
}
