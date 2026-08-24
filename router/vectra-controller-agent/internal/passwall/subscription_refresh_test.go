package passwall

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type subscriptionRefreshBackend struct {
	lines   []string
	showErr error
	log     string
	logErr  error
}

func (b subscriptionRefreshBackend) Show(ctx context.Context, packageName string) ([]string, error) {
	if b.showErr != nil {
		return nil, b.showErr
	}
	return b.lines, nil
}

func (b subscriptionRefreshBackend) Batch(ctx context.Context, commands []string) error {
	return fmt.Errorf("unexpected batch: %v", commands)
}

func (b subscriptionRefreshBackend) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	if name != "tail" {
		return CommandResult{Command: command}, fmt.Errorf("unexpected command %s", command)
	}
	if b.logErr != nil {
		return CommandResult{Command: command}, b.logErr
	}
	return CommandResult{Command: command, Stdout: b.log}, nil
}

func nodeUCILines(nodes map[string]string) []string {
	lines := []string{}
	for id, address := range nodes {
		lines = append(lines,
			fmt.Sprintf("passwall2.%s=nodes", id),
			fmt.Sprintf("passwall2.%s.address='%s'", id, address),
			fmt.Sprintf("passwall2.%s.port='443'", id),
		)
	}
	return lines
}

const healthySubscriptionLog = `2026-08-24 19:44:33: Start subscribing...
2026-08-24 19:46:03:     - Successfully resolved the [BloopCat] node, number: 24
2026-08-24 19:46:07: Subscription complete...`

func TestVerifySubscriptionRefreshPassesOnHealthyImport(t *testing.T) {
	backend := subscriptionRefreshBackend{
		lines: nodeUCILines(map[string]string{"n1": "pl2.example.net", "n2": "ru19.example.net"}),
		log:   healthySubscriptionLog,
	}

	outcome := VerifySubscriptionRefresh(context.Background(), backend, 2)

	if !outcome.OK {
		t.Fatalf("expected healthy refresh to pass, got %+v", outcome)
	}
	if outcome.NodesAfter != 2 || !outcome.Measured {
		t.Fatalf("nodesAfter = %d measured = %v, want 2/true", outcome.NodesAfter, outcome.Measured)
	}
	if outcome.FailureMessage() != "" {
		t.Fatalf("FailureMessage() = %q, want empty", outcome.FailureMessage())
	}
}

// The yuranrod-msk shape: subscribe.lua died at the concatenation before it
// ever reached the network, the exit code was still 0, and the panel recorded
// the job as succeeded.
func TestVerifySubscriptionRefreshFailsOnScriptCrash(t *testing.T) {
	backend := subscriptionRefreshBackend{
		lines: nodeUCILines(map[string]string{"n1": "pl2.example.net"}),
		log: `2026-08-24 19:44:33: Start subscribing...
2026-08-24 19:44:33:   - /usr/share/passwall2/subscribe.lua:1970: attempt to concatenate a nil value
2026-08-24 19:44:33:   - stack traceback:`,
	}

	outcome := VerifySubscriptionRefresh(context.Background(), backend, 1)

	if outcome.OK {
		t.Fatalf("expected crashed subscription to fail, got %+v", outcome)
	}
	if outcome.Failure != SubscriptionRefreshFailureScriptError {
		t.Fatalf("failure = %q, want %q", outcome.Failure, SubscriptionRefreshFailureScriptError)
	}
	if !strings.Contains(outcome.FailureMessage(), "attempt to concatenate") {
		t.Fatalf("FailureMessage() = %q, want it to name the script error", outcome.FailureMessage())
	}
}

// A traceback from an earlier run in the same boot must not condemn the run we
// just performed.
func TestVerifySubscriptionRefreshIgnoresErrorsBeforeTheLatestRun(t *testing.T) {
	backend := subscriptionRefreshBackend{
		lines: nodeUCILines(map[string]string{"n1": "pl2.example.net"}),
		log: `2026-08-23 19:44:33: Start subscribing...
2026-08-23 19:44:33:   - /usr/share/passwall2/subscribe.lua:1970: attempt to concatenate a nil value
` + healthySubscriptionLog,
	}

	outcome := VerifySubscriptionRefresh(context.Background(), backend, 1)

	if !outcome.OK {
		t.Fatalf("stale traceback must not fail a fresh run, got %+v", outcome)
	}
}

func TestVerifySubscriptionRefreshFailsOnProviderPlaceholder(t *testing.T) {
	backend := subscriptionRefreshBackend{
		lines: nodeUCILines(map[string]string{"n1": "0.0.0.0"}),
		log:   healthySubscriptionLog,
	}

	outcome := VerifySubscriptionRefresh(context.Background(), backend, 20)

	if outcome.OK {
		t.Fatalf("expected placeholder import to fail, got %+v", outcome)
	}
	if outcome.Failure != SubscriptionRefreshFailurePlaceholder {
		t.Fatalf("failure = %q, want %q", outcome.Failure, SubscriptionRefreshFailurePlaceholder)
	}
	if outcome.PlaceholderNodes != 1 {
		t.Fatalf("placeholderNodes = %d, want 1", outcome.PlaceholderNodes)
	}
}

func TestVerifySubscriptionRefreshFailsWhenNodeListIsEmpty(t *testing.T) {
	backend := subscriptionRefreshBackend{lines: []string{}, log: healthySubscriptionLog}

	outcome := VerifySubscriptionRefresh(context.Background(), backend, 20)

	if outcome.OK {
		t.Fatalf("expected wiped node list to fail, got %+v", outcome)
	}
	if outcome.Failure != SubscriptionRefreshFailureNoNodes {
		t.Fatalf("failure = %q, want %q", outcome.Failure, SubscriptionRefreshFailureNoNodes)
	}
	if !strings.Contains(outcome.FailureMessage(), "20 nodes before") {
		t.Fatalf("FailureMessage() = %q, want it to name what was lost", outcome.FailureMessage())
	}
}

// Not being able to measure is not a verdict: a router whose config or log we
// cannot read must not be marked failed on that basis alone.
func TestVerifySubscriptionRefreshStaysPassingWhenUnmeasurable(t *testing.T) {
	backend := subscriptionRefreshBackend{
		showErr: fmt.Errorf("uci unavailable"),
		logErr:  fmt.Errorf("no such file"),
	}

	outcome := VerifySubscriptionRefresh(context.Background(), backend, -1)

	if !outcome.OK {
		t.Fatalf("unmeasurable refresh must stay passing, got %+v", outcome)
	}
	if outcome.Measured {
		t.Fatalf("measured = true, want false")
	}
	if outcome.Detail == "" {
		t.Fatalf("expected Detail to record why measurement failed")
	}
}

func TestCountSubscriptionNodesSeparatesPlaceholders(t *testing.T) {
	backend := subscriptionRefreshBackend{
		lines: nodeUCILines(map[string]string{
			"n1": "pl2.example.net",
			"n2": "0.0.0.0",
			"n3": "nl3.example.net",
		}),
	}

	total, placeholders, err := CountSubscriptionNodes(context.Background(), backend)
	if err != nil {
		t.Fatalf("CountSubscriptionNodes() error = %v", err)
	}
	if total != 3 || placeholders != 1 {
		t.Fatalf("total/placeholders = %d/%d, want 3/1", total, placeholders)
	}
}
