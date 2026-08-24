package passwall

import (
	"context"
	"fmt"
	"strings"
)

/*
A subscription refresh is the one job on this agent that can report success
while destroying the router's entire proxy configuration.

`subscribe.lua start all` exits 0 no matter what happens inside it. Its errors
go to the PassWall log and nowhere else, so the two known ways a refresh
destroys a router both look identical to a clean run from the exit code:

  - the script dies before it ever reaches the network (measured on
    yuranrod-msk 2026-08-24: user_agent=passwall2 selects a branch that
    concatenates api.get_version(), which is nil on that build, and the whole
    subscription aborts at subscribe.lua:1970); or
  - the provider refuses the client and answers with a single placeholder node
    at 0.0.0.0 remarked "App not supported", which PassWall imports like any
    other node — replacing every real node with it.

Either way the router keeps working as a plain router while all five slots
point at nodes that no longer exist and traffic silently leaves direct. The
panel, meanwhile, records the job as succeeded and nobody is told.

So the exit code is not the result. This module measures the result: what
nodes the router actually holds afterwards, and what the log says the
subscription actually did.
*/

// PasswallLogPath is the only place the subscription transcript is written.
const PasswallLogPath = "/tmp/log/passwall2.log"

// placeholderNodeAddress is the address the provider hands back when it
// refuses to serve a client.
const placeholderNodeAddress = "0.0.0.0"

// subscriptionLogStartMarker begins one subscription run in the log. Only the
// lines after the LAST occurrence describe the run we just triggered — the log
// survives across refreshes within a boot, and judging a fresh run by an old
// day's traceback would fail healthy routers.
const subscriptionLogStartMarker = "Start subscribing"

// Signatures of a subscription that did not complete. The Lua ones cover the
// crash class as a class, not just the one concatenation bug: any nil deref in
// subscribe.lua aborts the import exactly the same way.
var subscriptionLogErrorSignatures = []string{
	"attempt to concatenate",
	"attempt to index",
	"attempt to call",
	"attempt to perform arithmetic",
	"stack traceback",
	"App not supported",
}

// maxSubscriptionLogErrors caps what travels back to the panel in the job
// result. A traceback is a dozen lines and only the first few identify it.
const maxSubscriptionLogErrors = 8

const (
	// SubscriptionRefreshFailurePlaceholder means the provider answered but
	// refused this client. Remediation is on the provider or the request
	// headers, never on the node list.
	SubscriptionRefreshFailurePlaceholder = "placeholder_nodes"
	// SubscriptionRefreshFailureNoNodes means the refresh left the router with
	// nothing to route through, whatever the cause.
	SubscriptionRefreshFailureNoNodes = "no_nodes"
	// SubscriptionRefreshFailureScriptError means subscribe.lua died partway.
	SubscriptionRefreshFailureScriptError = "script_error"
)

// SubscriptionRefreshOutcome is the measured result of a refresh, as opposed to
// the exit status of the script that performed it.
type SubscriptionRefreshOutcome struct {
	OK      bool   `json:"ok"`
	Failure string `json:"failure,omitempty"`

	// Measured distinguishes "the router holds zero nodes" from "we could not
	// read the node list". Only the first is a verdict.
	Measured         bool `json:"measured"`
	NodesBefore      int  `json:"nodesBefore"`
	NodesAfter       int  `json:"nodesAfter"`
	PlaceholderNodes int  `json:"placeholderNodes"`

	LogErrors []string `json:"logErrors,omitempty"`
	// Detail records why a measurement was unavailable. It never by itself
	// makes the outcome a failure.
	Detail string `json:"detail,omitempty"`
}

// FailureMessage renders the verdict for the panel's job error field.
func (o SubscriptionRefreshOutcome) FailureMessage() string {
	switch o.Failure {
	case SubscriptionRefreshFailurePlaceholder:
		return fmt.Sprintf(
			"subscription refused this client: %d of %d imported nodes are provider placeholders (%s)",
			o.PlaceholderNodes, o.NodesAfter, placeholderNodeAddress,
		)
	case SubscriptionRefreshFailureNoNodes:
		if o.NodesBefore > 0 {
			return fmt.Sprintf("subscription refresh wiped the node list (%d nodes before, none after)", o.NodesBefore)
		}
		return "subscription refresh imported no nodes"
	case SubscriptionRefreshFailureScriptError:
		if len(o.LogErrors) > 0 {
			return "subscription script failed: " + o.LogErrors[0]
		}
		return "subscription script failed"
	case "":
		return ""
	default:
		return "subscription refresh failed: " + o.Failure
	}
}

// CountSubscriptionNodes reports how many node sections the router holds and how
// many of those are provider placeholders.
func CountSubscriptionNodes(ctx context.Context, backend UCIBackend) (total int, placeholders int, err error) {
	if backend == nil {
		backend = ExecBackend{}
	}
	lines, err := backend.Show(ctx, "passwall2")
	if err != nil {
		return 0, 0, fmt.Errorf("read passwall2 config: %w", err)
	}
	sections, err := ParseUCILines(lines)
	if err != nil {
		return 0, 0, fmt.Errorf("parse passwall2 config: %w", err)
	}
	for _, section := range sections {
		if section.Type != "nodes" {
			continue
		}
		total++
		if strings.TrimSpace(optionString(section, "address")) == placeholderNodeAddress {
			placeholders++
		}
	}
	return total, placeholders, nil
}

// VerifySubscriptionRefresh judges a completed refresh by what it left behind.
//
// Pass a negative nodesBefore when the pre-refresh count is unknown; it is used
// only to phrase the verdict, never to reach one.
//
// It never returns an error. A refresh that cannot be measured is reported as
// unmeasured and left passing: this decides whether a router's job is marked
// failed, and inventing failures from a missing log would be its own outage.
// Condemnation needs evidence; absence of evidence is not it.
func VerifySubscriptionRefresh(ctx context.Context, backend UCIBackend, nodesBefore int) SubscriptionRefreshOutcome {
	outcome := SubscriptionRefreshOutcome{OK: true, NodesBefore: nodesBefore}

	total, placeholders, err := CountSubscriptionNodes(ctx, backend)
	if err != nil {
		outcome.Detail = "node count unavailable: " + err.Error()
	} else {
		outcome.Measured = true
		outcome.NodesAfter = total
		outcome.PlaceholderNodes = placeholders
	}

	logErrors, logErr := scanSubscriptionLog(ctx, backend)
	if logErr != nil {
		outcome.Detail = strings.TrimSpace(outcome.Detail + " subscription log unavailable: " + logErr.Error())
	}
	outcome.LogErrors = logErrors

	switch {
	case outcome.Measured && placeholders > 0:
		outcome.OK = false
		outcome.Failure = SubscriptionRefreshFailurePlaceholder
	case outcome.Measured && total == 0:
		outcome.OK = false
		outcome.Failure = SubscriptionRefreshFailureNoNodes
	case len(logErrors) > 0:
		outcome.OK = false
		outcome.Failure = SubscriptionRefreshFailureScriptError
	}

	return outcome
}

// scanSubscriptionLog returns the error lines belonging to the most recent
// subscription run only.
func scanSubscriptionLog(ctx context.Context, backend UCIBackend) ([]string, error) {
	if backend == nil {
		backend = ExecBackend{}
	}
	result, err := backend.Run(ctx, "tail", "-n", "200", PasswallLogPath)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(result.Stdout, "\n")
	start := 0
	for index, line := range lines {
		if strings.Contains(line, subscriptionLogStartMarker) {
			start = index
		}
	}

	errors := []string{}
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, signature := range subscriptionLogErrorSignatures {
			if strings.Contains(trimmed, signature) {
				errors = append(errors, trimmed)
				break
			}
		}
		if len(errors) >= maxSubscriptionLogErrors {
			break
		}
	}
	if len(errors) == 0 {
		return nil, nil
	}
	return errors, nil
}
