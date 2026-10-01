package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/setup"
)

func connectWifiFixture(t *testing.T) *wizardRouter {
	t.Helper()
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	oldEnv, oldApply := connectWifiEnv, connectWifiApply
	connectWifiEnv = func() setup.Env { return w.env }
	t.Cleanup(func() { connectWifiEnv, connectWifiApply = oldEnv, oldApply })
	return w
}

func TestConnectWifiRejectsInvalidParamsBeforeMutation(t *testing.T) {
	w := connectWifiFixture(t)
	for _, raw := range []string{`{}`, `null`, `[]`, `{"ssid":"x","password":"short"}`, `{"ssid":"x","password":"ключключ"}`, `{"ssid":"x","password":"fixture-key","extra":true}`, `{"ssid":"x","password":"fixture-key"}{}`} {
		code, ok := connectApplyWifi(context.Background(), w.cfg, json.RawMessage(raw))
		if code != "invalid_params" || ok {
			t.Fatalf("invalid input accepted: %s %v", code, ok)
		}
	}
	if w.commands() != "" {
		t.Fatal("invalid input mutated router")
	}
	if connectWifiParamsValid(strings.Repeat("я", 17), "fixture-key") || connectWifiParamsValid("x\n", "fixture-key") {
		t.Fatal("invalid SSID accepted")
	}
}

func TestConnectWifiFinalVerificationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		state, code string
		ok          bool
	}{
		{setup.ApplyOK, "applied", true}, {setup.ApplyRolledBack, "rolled_back", false},
		{setup.ApplyFailed, "apply_failed", false}, {setup.ApplyPartial, "unverified", false}, {setup.ApplyUnverified, "unverified", false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			w := connectWifiFixture(t)
			called := false
			connectWifiApply = func(ctx context.Context, env setup.Env) setup.ApplyState {
				called = true
				if !strings.Contains(w.commands(), "uci commit wireless") {
					t.Fatal("verified before commit")
				}
				if l, err := setup.LockWifi(env); err == nil {
					l.Close()
					t.Fatal("verification lost WiFi lock")
				}
				if st, err := os.Stat(env.WifiJob); err != nil || st.Mode().Perm() != 0600 {
					t.Fatal("rollback job not private")
				}
				os.Remove(env.WifiJob)
				return setup.ApplyState{State: tc.state, Radios: map[string]bool{"radio0": true, "radio1": true}, Detail: "fixture-key"}
			}
			code, ok := connectApplyWifi(context.Background(), w.cfg, json.RawMessage(`{"ssid":"Fixture","password":"fixture-key"}`))
			if !called || code != tc.code || ok != tc.ok {
				t.Fatalf("outcome %s %v verified %v", code, ok, called)
			}
		})
	}
}

func TestConnectWifiPreservesPendingAndInterruptedJobs(t *testing.T) {
	for _, job := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "interrupted"}[job], func(t *testing.T) {
			w := connectWifiFixture(t)
			path := filepath.Join(w.env.UCISaveDir, "wireless")
			want := "busy"
			if job {
				path = w.env.WifiJob
				want = "interrupted"
			}
			w.write(t, path, "fixture owner's pending data")
			code, ok := connectApplyWifi(context.Background(), w.cfg, json.RawMessage(`{"ssid":"Fixture","password":"fixture-key"}`))
			if code != want || ok || w.commands() != "" {
				t.Fatalf("outcome %s %v", code, ok)
			}
			if raw, _ := os.ReadFile(path); string(raw) != "fixture owner's pending data" {
				t.Fatal("owner data overwritten")
			}
		})
	}
}

func TestConnectWifiCancelledCommitStillFinishesVerification(t *testing.T) {
	w := connectWifiFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.onRun = func(cmd string) {
		if cmd == "uci commit wireless" {
			cancel()
		}
	}
	verified := false
	connectWifiApply = func(ctx context.Context, env setup.Env) setup.ApplyState {
		verified = true
		if ctx.Err() != nil {
			t.Fatal("committed change verification cancelled")
		}
		os.Remove(env.WifiJob)
		return setup.ApplyState{State: setup.ApplyOK, Radios: map[string]bool{"radio0": true}}
	}
	code, ok := connectApplyWifi(ctx, w.cfg, json.RawMessage(`{"ssid":"Fixture","password":"fixture-key"}`))
	if code != "interrupted" || ok || !verified {
		t.Fatalf("outcome %s %v verified %v", code, ok, verified)
	}
}

func TestConnectWifiStageFailureDoesNotLeakOrApply(t *testing.T) {
	w := connectWifiFixture(t)
	w.fail = func(cmd string) bool { return strings.HasPrefix(cmd, "uci set ") }
	connectWifiApply = func(context.Context, setup.Env) setup.ApplyState {
		t.Fatal("failed stage verified")
		return setup.ApplyState{}
	}
	code, ok := connectApplyWifi(context.Background(), w.cfg, json.RawMessage(`{"ssid":"Fixture","password":"fixture-key"}`))
	if code != "apply_failed" || ok {
		t.Fatalf("outcome %s %v", code, ok)
	}
	if _, err := os.Stat(w.env.WifiJob); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential job remains after stage failure")
	}
}

func TestConnectWifiUsesRealVerifiedHelper(t *testing.T) {
	w := connectWifiFixture(t)
	w.ubus = map[string]string{"call network.wireless status": `{"radio1":{"up":true,"interfaces":[{"ifname":"wlan1","config":{"mode":"ap"}}]}}`}
	code, ok := connectApplyWifi(context.Background(), w.cfg, json.RawMessage(`{"ssid":"Fixture","password":"fixture-key"}`))
	if code != "applied" || !ok {
		t.Fatalf("outcome %s %v", code, ok)
	}
	if !strings.Contains(w.commands(), "wifi reload") {
		t.Fatal("did not use actual restart helper")
	}
	if _, err := os.Stat(w.env.WifiJob); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential-bearing rollback job remains")
	}
	if a := setup.LoadApply(w.env); a == nil || a.State != setup.ApplyOK {
		t.Fatal("no final verified state")
	}
}
