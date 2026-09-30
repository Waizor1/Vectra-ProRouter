package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/config"
)

// The panel may lock the router UI from the operator config it delivers. The
// decoder refuses unknown fields, so without this field such a config would
// fail every apply job.
func TestTheOperatorConfigCarriesThePanelsUILock(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "panel", "operator-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["ui"] = map[string]interface{}{"lock": true}
	locked, _ := json.Marshal(doc)
	c, err := config.Unmarshal(locked)
	if err != nil || c.UI == nil || !c.UI.Lock {
		t.Fatalf("ui.lock: %+v %v", c, err)
	}
	if c, err := config.Unmarshal(raw); err != nil || c.UI != nil {
		t.Fatalf("no ui block: %+v %v", c, err)
	}
	doc["ui"] = map[string]interface{}{"lock": true, "theme": "dark"}
	unknown, _ := json.Marshal(doc)
	if _, err := config.Unmarshal(unknown); err == nil {
		t.Fatal("an unknown field inside ui was accepted")
	}
}
