package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/localctl"
)

// retireCLI stands the daemon in for `vctl retire-passwall`: what it was
// asked, and what it answers.
type retireCLI struct {
	asked  []localctl.SocketRequest
	answer localctl.SocketResponse
	err    error
	out    bytes.Buffer
}

func newRetireCLI(t *testing.T) *retireCLI {
	t.Helper()
	c := &retireCLI{}
	prevCall, prevOut := retireCall, retireOut
	retireCall = func(ctx context.Context, _ string, req localctl.SocketRequest) (localctl.SocketResponse, error) {
		c.asked = append(c.asked, req)
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) < retireReplyWait {
			t.Errorf("the call gives up before the daemon does: %v", time.Until(dl))
		}
		return c.answer, c.err
	}
	retireOut = &c.out
	t.Cleanup(func() { retireCall, retireOut = prevCall, prevOut })
	return c
}

// It asks the daemon — --now as it was typed — and prints the answer.
func TestRetirePassWallAsksTheDaemon(t *testing.T) {
	c := newRetireCLI(t)
	c.answer = localctl.SocketResponse{OK: true, Code: "passwall_retired", Detail: "PassWall2 retired: removed luci-app-passwall2"}
	if err := cmdRetirePassWall([]string{"--now"}); err != nil {
		t.Fatal(err)
	}
	if len(c.asked) != 1 || c.asked[0].Op != localctl.OpRetirePassWall || !c.asked[0].Now {
		t.Fatalf("asked %+v", c.asked)
	}
	if !strings.Contains(c.out.String(), "PassWall2 retired: removed luci-app-passwall2") {
		t.Fatalf("printed %q", c.out.String())
	}
	if err := cmdRetirePassWall(nil); err != nil || c.asked[1].Now {
		t.Fatalf("without --now: %v, asked %+v", err, c.asked[1])
	}
}

// A refusal, the wait and a failure are errors, with the daemon's words.
func TestRetirePassWallSaysWhyNot(t *testing.T) {
	for _, code := range []string{"refused", "waiting", "apply_failed", "busy"} {
		c := newRetireCLI(t)
		c.answer = localctl.SocketResponse{Code: code, Detail: "PassWall2 stays: a trial runs"}
		err := cmdRetirePassWall(nil)
		if err == nil || !strings.Contains(c.out.String()+err.Error(), "a trial runs") {
			t.Errorf("%s: err %v, printed %q", code, err, c.out.String())
		}
	}
}

// vctl not running: nothing carries the traffic, so nothing can go.
func TestRetirePassWallWithoutTheDaemon(t *testing.T) {
	c := newRetireCLI(t)
	c.err = fmt.Errorf("%w (dial: no such file)", localctl.ErrDaemonDown)
	err := cmdRetirePassWall([]string{"--now"})
	if err == nil || !strings.Contains(err.Error(), "vctl does not run") {
		t.Fatalf("err %v", err)
	}
}

// Still going past the wait: said, not a failure.
func TestRetirePassWallStillGoing(t *testing.T) {
	c := newRetireCLI(t)
	c.answer = localctl.SocketResponse{OK: true, Code: "pending"}
	if err := cmdRetirePassWall(nil); err != nil || !strings.Contains(c.out.String(), "logread -e vctl") {
		t.Fatalf("err %v, printed %q", err, c.out.String())
	}
}

func TestRetirePassWallRefusesStrayArguments(t *testing.T) {
	newRetireCLI(t)
	if err := cmdRetirePassWall([]string{"now"}); err == nil {
		t.Fatal("a stray argument accepted")
	}
}
