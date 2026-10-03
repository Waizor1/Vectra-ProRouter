package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"vectra-controller-pro/internal/localctl"
)

// `vctl retire-passwall [--now]`: PassWall2 off the router now, for an
// operator. The daemon does it (retire_passwall.go), on its loop, the same
// way it does it by itself once the window is over: this only asks, and
// prints the answer.
func init() {
	register(command{name: "retire-passwall", summary: "remove PassWall2 once Vectra carries the traffic for good (--now: without waiting out the day)", run: cmdRetirePassWall})
}

const retireUsage = `usage: vctl retire-passwall [--now]
  Removes PassWall2 from the router (its configuration kept in
  /etc/vectra-controller-pro/backup), as vctl does by itself once Vectra has
  carried the traffic, switched on for good, for a day (UCI
  passwall_retire_after). Where a person removed PassWall2's packages, it
  tidies what they left the same way: the configuration backed up and taken,
  the takeover's notes that it is owed back gone. Afterwards "vectra off"
  leaves the router on plain internet. Refused while a trial runs, with
  route_source 'passwall', while vctl carries no traffic, or with UCI
  retire_passwall '0'.
  --now   do not wait out the day; every other condition still holds`

// Seams: tests stand the daemon in.
var (
	retireCall           = localctl.Call
	retireOut  io.Writer = os.Stdout
)

func cmdRetirePassWall(args []string) error {
	fs := newFlagSet("retire-passwall")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, retireUsage) }
	now := fs.Bool("now", false, "do not wait out the stability window")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q (vctl retire-passwall [--now])", strings.Join(fs.Args(), " "))
	}
	cfg := rpcdConfig()
	ctx, cancel := context.WithTimeout(context.Background(), retireReplyWait+time.Minute)
	defer cancel()
	resp, err := retireCall(ctx, cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpRetirePassWall, Now: *now})
	switch {
	case errors.Is(err, localctl.ErrDaemonDown):
		return errors.New("vctl does not run: PassWall2 goes only while Vectra carries the router's traffic (vectra status)")
	case err != nil:
		return err
	case resp.Code == "pending":
		fmt.Fprintln(retireOut, "PassWall2's removal is still going on in vctl: logread -e vctl")
		return nil
	case !resp.OK:
		fmt.Fprintln(retireOut, resp.Detail)
		return fmt.Errorf("%s: %s", resp.Code, resp.Detail)
	}
	fmt.Fprintln(retireOut, resp.Detail)
	return nil
}
