package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `vctl passwall-render` is the check before the first `vectra on`, on a
// router where vctl never ran: no agent.json (rendered from UCI as the init
// script does) and no operator config (the base one vctl routes by until the
// panel's arrives). On 2026-10-03 it failed on both.
func TestPassWallRenderWorksBeforeVctlEverRan(t *testing.T) {
	passwallRouter(t)
	dir := t.TempDir()
	agentJSON := `{"controlUrl":"https://api.vectra-pro.net","statePath":"` + filepath.Join(dir, "state.json") +
		`","xrayConfigPath":"` + filepath.Join(dir, "xray-desired.json") + `","xrayBinary":"` + writeFakeXray(t, dir) + `"}`
	prev := agentRenderer
	t.Cleanup(func() { agentRenderer = prev })
	agentRenderer = filepath.Join(dir, "render-xray-config.sh")
	if err := os.WriteFile(agentRenderer, []byte("#!/bin/sh\ncat > \"$1\" <<'J'\n"+agentJSON+"\nJ\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "render.json")
	if err := cmdPassWallRender([]string{"-config", filepath.Join(dir, "no-agent.json"), "-out", out}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Inbounds []struct {
			Tag  string `json:"tag"`
			Port int    `json:"port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Inbounds) == 0 || doc.Inbounds[0].Tag != "tproxy-in" || doc.Inbounds[0].Port != 12345 {
		t.Fatalf("the render's inbounds: %+v (%v)", doc.Inbounds, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "xray-desired.json")); !os.IsNotExist(err) {
		t.Fatal("the render wrote an operator config: the router would read as linked")
	}
	// A renderer that fails says so, and why.
	if err := os.WriteFile(agentRenderer, []byte("#!/bin/sh\necho 'uci: Entry not found' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = cmdPassWallRender([]string{"-config", filepath.Join(dir, "no-agent.json"), "-out", out})
	if err == nil || !strings.Contains(err.Error(), "Entry not found") {
		t.Fatalf("err = %v", err)
	}
}
