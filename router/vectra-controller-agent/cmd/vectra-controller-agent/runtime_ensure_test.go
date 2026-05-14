package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-agent/internal/controlplane"
)

func TestRunEnsurePasswallRuntimeJobRepairsKnownLowStorageRuntimeGaps(t *testing.T) {
	backend := &fakeCommandRunner{}

	payload, results, err := runEnsurePasswallRuntimeJob(
		context.Background(),
		backend,
		map[string]interface{}{
			"actions": []interface{}{
				ensureRuntimeActionCompactGeodata,
				ensureRuntimeActionDNSMasqFull,
			},
		},
		controlplane.RouterInventory{},
	)
	if err != nil {
		t.Fatalf("runEnsurePasswallRuntimeJob returned error: %v", err)
	}

	if got, want := payload["ok"], true; got != want {
		t.Fatalf("payload ok = %v, want %v", got, want)
	}
	if len(results) != 3 {
		t.Fatalf("expected geodata, dnsmasq-full and restart commands, got %d: %#v", len(results), results)
	}
	if len(backend.calls) != 3 {
		t.Fatalf("backend calls = %#v, want three commands", backend.calls)
	}
	if !strings.Contains(backend.calls[0], defaultCompactGeoIPURL) ||
		!strings.Contains(backend.calls[0], defaultCompactGeoSiteURL) {
		t.Fatalf("compact geodata command did not use compact defaults: %q", backend.calls[0])
	}
	if !strings.Contains(backend.calls[1], "opkg download dnsmasq-full") ||
		!strings.Contains(backend.calls[1], "refusing to remove base dnsmasq") ||
		!strings.Contains(backend.calls[1], "dhcp backup was restored") {
		t.Fatalf("dnsmasq-full command must preflight and stage package before removal, got %q", backend.calls[1])
	}
	if got, want := backend.calls[2], "sh -c "+passwallPostInstallRecoveryCommand; got != want {
		t.Fatalf("restart command = %q, want %q", got, want)
	}
}

func TestEnsureRuntimeActionsDefaultsAndDeduplicates(t *testing.T) {
	if got, want := ensureRuntimeActions(nil), []string{ensureRuntimeActionCompactGeodata, ensureRuntimeActionDNSMasqFull}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default actions = %#v, want %#v", got, want)
	}

	got := ensureRuntimeActions(map[string]interface{}{
		"actions": []interface{}{
			ensureRuntimeActionDNSMasqFull,
			ensureRuntimeActionDNSMasqFull,
			ensureRuntimeActionCompactGeodata,
			"",
		},
	})
	want := []string{ensureRuntimeActionDNSMasqFull, ensureRuntimeActionCompactGeodata}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deduped actions = %#v, want %#v", got, want)
	}
}

func TestRunEnsurePasswallRuntimeJobRejectsUnsupportedActions(t *testing.T) {
	backend := &fakeCommandRunner{}

	payload, _, err := runEnsurePasswallRuntimeJob(
		context.Background(),
		backend,
		map[string]interface{}{
			"actions": []interface{}{"install_everything"},
		},
		controlplane.RouterInventory{},
	)
	if err == nil {
		t.Fatalf("expected unsupported action to fail")
	}
	if got, want := payload["ok"], false; got != want {
		t.Fatalf("payload ok = %v, want %v", got, want)
	}
	if len(backend.calls) != 0 {
		t.Fatalf("unsupported action should not execute commands, got %#v", backend.calls)
	}
}
