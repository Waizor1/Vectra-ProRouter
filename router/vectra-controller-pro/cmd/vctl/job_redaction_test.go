package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
)

// subToken is the bearer material providers put in the subscription URL.
//
// A job result is the one payload that travels BOTH ways that matter: it is
// stored in the panel database, and it is journalled into
// /etc/vectra-controller-pro/state.json, which
// openwrt/files/lib/upgrade/keep.d/vectra-controller-pro preserves into every
// sysupgrade backup. The router fetches the provider document itself precisely
// so the panel never holds provider secrets — a leaked URL in an error message
// undoes that.
const subToken = "BEARER-9c2a77f1-DO-NOT-LEAK"

// deadProviderDaemon returns a daemon whose subscription URL carries subToken
// and points at a provider that is not listening, so the very first fetch fails
// with the *url.Error that used to carry the whole URL.
func deadProviderDaemon(t *testing.T, dir string, panel *panelStub) (*daemon, string) {
	t.Helper()
	provider := newProviderStub(t, providerEntry(t))
	d := newTestDaemon(t, dir, panel, provider)

	secret := provider.URL + "/sub/" + subToken + "?token=" + subToken
	if err := os.WriteFile(filepath.Join(dir, "operator.json"), operatorConfigPointingAt(t, secret), 0o600); err != nil {
		t.Fatal(err)
	}
	provider.Close() // connection refused on the next fetch
	return d, secret
}

func refreshJob() controlplane.Job {
	return controlplane.Job{ID: "j-refresh", Type: "refresh_xray_subscriptions", State: "queued"}
}

// The failure result the panel stores must not contain the token.
func TestJobFailureResultSentToPanelDoesNotLeakSubscriptionURL(t *testing.T) {
	dir := t.TempDir()
	panel := newPanelStub(t, nil)
	d, secret := deadProviderDaemon(t, dir, panel)
	d.st.RouterID = "r-e2e"

	_ = d.jobRefreshSubscriptions(context.Background(), refreshJob())

	results := panel.resultsFor("j-refresh")
	if len(results) == 0 {
		t.Fatal("no job result reached the panel")
	}
	var sawFailure bool
	for _, r := range results {
		if r.Status != "failure" {
			continue
		}
		sawFailure = true
		msg, _ := r.Result["error"].(string)
		if msg == "" {
			t.Fatalf("failure result carried no error text: %+v", r.Result)
		}
		if strings.Contains(msg, subToken) {
			t.Fatalf("bearer token leaked into the panel job result:\n  %s", msg)
		}
		if strings.Contains(msg, secret) {
			t.Fatalf("the full subscription URL leaked into the panel job result:\n  %s", msg)
		}
		// It still has to be actionable.
		if !strings.Contains(msg, "refresh") {
			t.Errorf("failure result lost its context: %s", msg)
		}
	}
	if !sawFailure {
		t.Fatalf("expected a failure result, got %+v", results)
	}
}

// The journalled copy on disk must not contain it either. The panel is dead
// here, so finishJob leaves PendingJobResult in state.json — exactly the
// situation a sysupgrade backup would capture.
func TestJournalledJobResultDoesNotLeakSubscriptionURL(t *testing.T) {
	dir := t.TempDir()
	panel := newPanelStub(t, nil)
	d, secret := deadProviderDaemon(t, dir, panel)
	d.st.RouterID = "r-e2e"
	panel.Close() // job-result submission fails, so the journal entry survives

	_ = d.jobRefreshSubscriptions(context.Background(), refreshJob())

	if d.st.PendingJobResult == nil {
		t.Fatal("expected the failed submission to leave a journalled result")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	if strings.Contains(string(raw), subToken) {
		t.Fatalf("bearer token leaked into state.json (kept across sysupgrade):\n%s", raw)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the full subscription URL leaked into state.json:\n%s", raw)
	}
}

// finishJob is the choke point: anything a job hands it gets scrubbed, whatever
// produced the string. run_terminal_command stdout is the obvious other source.
func TestFinishJobScrubsSubscriptionURLFromAnyResultValue(t *testing.T) {
	dir := t.TempDir()
	panel := newPanelStub(t, nil)
	d, secret := deadProviderDaemon(t, dir, panel)
	d.st.RouterID = "r-e2e"

	cfg, err := d.loadDesiredConfig()
	if err != nil {
		t.Fatalf("load desired config: %v", err)
	}
	d.desired = cfg

	job := controlplane.Job{ID: "j-shell", Type: "run_terminal_command"}
	err = d.finishJob(context.Background(), job, "success", "", "", map[string]interface{}{
		"stdout":  "curl -sS " + secret + "\n",
		"nested":  map[string]interface{}{"cmd": "wget " + secret},
		"list":    []interface{}{"first", secret},
		"strings": []string{secret},
		"headers": map[string]string{"url": secret},
		"count":   3,
	})
	if err != nil {
		t.Fatalf("finishJob: %v", err)
	}

	results := panel.resultsFor("j-shell")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	for k, v := range results[0].Result {
		if s := renderValue(v); strings.Contains(s, subToken) {
			t.Errorf("result[%q] leaked the bearer token: %s", k, s)
		}
	}
	// Non-string values must survive untouched.
	if got, ok := results[0].Result["count"].(float64); !ok || got != 3 {
		t.Errorf("scrubbing must not disturb non-string values: %#v", results[0].Result["count"])
	}
}

func renderValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		var b strings.Builder
		for _, e := range t {
			b.WriteString(renderValue(e))
			b.WriteByte('\n')
		}
		return b.String()
	case map[string]interface{}:
		var b strings.Builder
		for _, e := range t {
			b.WriteString(renderValue(e))
			b.WriteByte('\n')
		}
		return b.String()
	default:
		return ""
	}
}

// A terminal command's answer is in the shape the panel parses
// (routerTerminalResultPayloadSchema): without its required fields the panel
// dropped the whole payload and showed every answer from vctl as empty.
func TestTerminalAnswerHasThePanelsShape(t *testing.T) {
	dir := t.TempDir()
	panel := newPanelStub(t, nil)
	d, _ := deadProviderDaemon(t, dir, panel)
	d.st.RouterID = "r-e2e"
	job := controlplane.Job{ID: "j-term", Type: "run_terminal_command",
		Payload: map[string]interface{}{"command": "echo out; echo err >&2; exit 3", "timeoutSeconds": float64(20)}}
	if err := d.jobRunTerminal(context.Background(), job); err != nil {
		t.Fatalf("jobRunTerminal: %v", err)
	}
	results := panel.resultsFor("j-term")
	if len(results) != 1 {
		t.Fatalf("%d results, want 1", len(results))
	}
	r := results[0].Result
	for _, k := range []string{"startedAt", "completedAt"} {
		s, _ := r[k].(string)
		if _, err := time.Parse(time.RFC3339, s); err != nil || !strings.HasSuffix(s, "Z") {
			t.Errorf("%s = %q, want an RFC 3339 UTC time", k, s)
		}
	}
	if r["command"] != "echo out; echo err >&2; exit 3" || r["timeoutSeconds"] != float64(20) ||
		r["exitCode"] != float64(3) || r["stdout"] != "out\n" || r["stderr"] != "err\n" || r["timedOut"] != false {
		t.Fatalf("answer = %#v", r)
	}
	if _, ok := r["durationMs"].(float64); !ok {
		t.Fatalf("no durationMs: %#v", r)
	}
}
