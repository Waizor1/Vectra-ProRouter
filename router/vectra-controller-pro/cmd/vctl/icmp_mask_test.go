package main

import (
	"context"
	"errors"
	"testing"

	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/vault"
)

// The ICMP mask follows the table in place: relaxed only once a table that
// refuses QUIC is loaded, back when the owner keeps QUIC, untouched by a
// load that failed, put back when the data plane is unloaded.
func TestTheICMPMaskFollowsTheLoadedTable(t *testing.T) {
	d, _, _ := shutdownDaemon(t)
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(`{"fakedns":[{"ipPool":"198.18.0.0/15","poolSize":65535}]}`)); err != nil {
		t.Fatal(err)
	}
	d.directLoad = func(context.Context, string) error { return nil }
	var loaded []firewall.Spec
	d.applyRuleset = func(_ string, s firewall.Spec) error { loaded = append(loaded, s); return nil }
	var follows []bool
	restores := 0
	oldF, oldR := icmpMaskFollow, icmpMaskRestore
	t.Cleanup(func() { icmpMaskFollow, icmpMaskRestore = oldF, oldR })
	icmpMaskFollow = func(relax bool) (string, error) { follows = append(follows, relax); return "", nil }
	icmpMaskRestore = func() (string, error) { restores++; return "", nil }
	ctx := context.Background()

	d.programFirewallWithin(ctx, d.desired, 0)
	if len(loaded) != 1 || !firewall.RelaxesICMP(loaded[0]) || len(follows) != 1 || !follows[0] {
		t.Fatalf("a table refusing QUIC (pools %v): follows %v", loaded[0].CarriedV4, follows)
	}

	d.cfg.QUICThrough = true
	d.programFirewallWithin(ctx, d.desired, 0)
	if len(follows) != 2 || follows[1] {
		t.Fatalf("QUIC kept: follows %v, want the mask back", follows)
	}

	d.applyRuleset = func(string, firewall.Spec) error { return errors.New("nft: syntax") }
	d.programFirewallWithin(ctx, d.desired, 0)
	if len(follows) != 2 {
		t.Fatalf("a failed load moved the mask: follows %v", follows)
	}

	d.unloadDataPlane(ctx, d.desired)
	if restores != 1 {
		t.Fatalf("unloaded: %d restores, want 1", restores)
	}
}
