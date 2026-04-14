package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"vectra-controller-agent/internal/passwall"
)

type fakeLogRunner struct {
	results map[string]passwall.CommandResult
	errs    map[string]error
}

func (f fakeLogRunner) Run(_ context.Context, name string, args ...string) (passwall.CommandResult, error) {
	command := name
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}

	result := f.results[command]
	err := f.errs[command]
	return result, err
}

func TestParseCollectRouterLogsJobClampsAndDefaults(t *testing.T) {
	request := parseCollectRouterLogsJob(map[string]interface{}{
		"source": "unknown",
		"lines":  12,
	})

	if request.Source != "all" {
		t.Fatalf("expected fallback source all, got %q", request.Source)
	}
	if request.Lines != minRouterLogLines {
		t.Fatalf("expected lines to clamp to %d, got %d", minRouterLogLines, request.Lines)
	}
}

func TestBuildRouterLogCommandsSelectsSingleSource(t *testing.T) {
	commands := buildRouterLogCommands(routerLogCollectionRequest{
		Source: "controller",
		Lines:  120,
	})

	if len(commands) != 1 {
		t.Fatalf("expected one command, got %d", len(commands))
	}
	if commands[0].ID != "controller" {
		t.Fatalf("expected controller command, got %q", commands[0].ID)
	}
	if !strings.Contains(commands[0].Command, "tail -n 120") {
		t.Fatalf("expected controller command to keep requested lines, got %q", commands[0].Command)
	}
}

func TestCollectRouterLogsBuildsStructuredSnapshots(t *testing.T) {
	command := "sh -c logread | grep -E 'vectra-controller|vectra' | tail -n 80"
	runner := fakeLogRunner{
		results: map[string]passwall.CommandResult{
			command: {
				Command: command,
				Stdout:  "controller ok",
			},
		},
		errs: map[string]error{},
	}

	snapshots, stdout, stderr, err := collectRouterLogs(
		context.Background(),
		runner,
		routerLogCollectionRequest{Source: "controller", Lines: 80},
	)
	if err != nil {
		t.Fatalf("collect logs returned error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %d", len(snapshots))
	}
	if stdout == "" {
		t.Fatal("expected aggregated stdout")
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if snapshots[0]["id"] != "controller" {
		t.Fatalf("expected controller snapshot, got %#v", snapshots[0])
	}
}

func TestCollectRouterLogsReturnsPartialFailurePayload(t *testing.T) {
	command := "sh -c logread -e 'dnsmasq' | tail -n 60"
	runner := fakeLogRunner{
		results: map[string]passwall.CommandResult{
			command: {
				Command: command,
				Stdout:  "dnsmasq failed",
				Stderr:  "permission denied",
			},
		},
		errs: map[string]error{
			command: errors.New("logread failed"),
		},
	}

	snapshots, _, stderr, err := collectRouterLogs(
		context.Background(),
		runner,
		routerLogCollectionRequest{Source: "dnsmasq", Lines: 60},
	)
	if err == nil {
		t.Fatal("expected collection error")
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %d", len(snapshots))
	}
	if !strings.Contains(stderr, "permission denied") {
		t.Fatalf("expected stderr to include command failure, got %q", stderr)
	}
}
