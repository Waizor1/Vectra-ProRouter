package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/firewall"
)

func cmdFirewall(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("firewall: subcommand required: render | apply | revert | routing")
	}
	switch args[0] {
	case "render":
		return firewallRender(args[1:])
	case "apply":
		return firewallApply(args[1:])
	case "revert":
		return firewallRevert(args[1:])
	case "routing":
		return firewallRouting(args[1:])
	default:
		return fmt.Errorf("firewall: unknown subcommand %q", args[0])
	}
}

// loadSpec builds the ruleset the CLI renders and applies. refuseIPv6 renders
// the daemon's refusal of the LAN's IPv6 (Spec.RefuseIPv6, the daemon's
// default unless UCI ipv6 '1'); without it the CLI carries IPv6, as the
// stand's ipv6 modes need.
func loadSpec(cfgPath string, refuseIPv6 bool, lanDevs string) (firewall.Spec, error) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return firewall.Spec{}, err
	}
	if c.Inbounds.Tproxy == nil {
		return firewall.Spec{}, fmt.Errorf("config has no inbounds.tproxy; firewall not applicable")
	}
	s := firewall.DefaultSpec(c.Inbounds.Tproxy.Port, c.Inbounds.Tproxy.FwMark)
	// Keep in lockstep with the daemon's firewallSpecFromConfig so `vctl firewall
	// render/apply` reflects the operator's kill-switch setting (was silently
	// ignored, misleading operators validating the ruleset offline).
	s.KillSwitch = c.Inbounds.Tproxy.KillSwitch
	s.RefuseIPv6 = refuseIPv6
	if refuseIPv6 && lanDevs != "" {
		s.LANDevices = strings.Split(lanDevs, ",")
	}
	return s, nil
}

func firewallRender(args []string) error {
	fs := newFlagSet("firewall render")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	out := fs.String("out", "-", "output file; '-' for stdout")
	refuse := fs.Bool("refuse-ipv6", false, "refuse the LAN's IPv6 to the outside, as the daemon does")
	lan := fs.String("lan-devices", "", "with -refuse-ipv6: the LAN's devices, comma-separated (IPv6 into them is left to the router's firewall)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	s, err := loadSpec(*cfgPath, *refuse, *lan)
	if err != nil {
		return err
	}
	text, err := firewall.Render(s)
	if err != nil {
		return err
	}
	if *out == "-" {
		fmt.Print(text)
		return nil
	}
	return os.WriteFile(*out, []byte(text), 0o644)
}

func firewallApply(args []string) error {
	fs := newFlagSet("firewall apply")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	yes := fs.Bool("yes", false, "actually run nft (default off — print-only)")
	refuse := fs.Bool("refuse-ipv6", false, "refuse the LAN's IPv6 to the outside, as the daemon does")
	lan := fs.String("lan-devices", "", "with -refuse-ipv6: the LAN's devices, comma-separated (IPv6 into them is left to the router's firewall)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("firewall apply only works on Linux (you're on %s); use 'firewall render' instead", runtime.GOOS)
	}
	s, err := loadSpec(*cfgPath, *refuse, *lan)
	if err != nil {
		return err
	}
	text, err := firewall.Render(s)
	if err != nil {
		return err
	}
	if !*yes {
		fmt.Println("# (dry-run; pass --yes to apply)")
		fmt.Print(text)
		return nil
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft -f -: %w (%s)", err, out)
	}
	fmt.Println("applied")
	return nil
}

func firewallRevert(args []string) error {
	fs := newFlagSet("firewall revert")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	yes := fs.Bool("yes", false, "actually run commands")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	s, err := loadSpec(*cfgPath, false, "")
	if err != nil {
		return err
	}
	cmds := firewall.RevertCommands(s)
	if !*yes {
		fmt.Println("# (dry-run; pass --yes to apply)")
		for _, c := range cmds {
			fmt.Println(c)
		}
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("firewall revert only works on Linux")
	}
	for _, c := range cmds {
		parts := strings.Fields(c)
		cmd := exec.Command(parts[0], parts[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v (%s)\n", c, err, strings.TrimSpace(string(out)))
		} else {
			fmt.Fprintf(os.Stderr, "  %s: ok\n", c)
		}
	}
	return nil
}

// firewallRouting installs the policy route that makes TPROXY deliverable.
//
// Without `ip rule add fwmark <mark> lookup <table>` plus the matching local
// route, the prerouting TPROXY rules mark packets that the kernel then has
// nowhere to deliver: xray's tproxy inbound never sees a connection and the
// data plane is silently dead. This used to be print-only with no -yes flag
// while `apply` executed on -yes, so following the obvious pattern
// (`apply -yes` then `routing -yes`) failed with "flag provided but not
// defined" — and an operator who missed that line in the output got a proxy
// that looked applied and carried nothing.
func firewallRouting(args []string) error {
	fs := newFlagSet("firewall routing")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	yes := fs.Bool("yes", false, "actually run commands")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	s, err := loadSpec(*cfgPath, false, "")
	if err != nil {
		return err
	}
	cmds := firewall.RoutingCommands(s)
	if !*yes {
		fmt.Println("# (dry-run; pass --yes to apply)")
		for _, c := range cmds {
			fmt.Println(c)
		}
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("firewall routing only works on Linux (you're on %s)", runtime.GOOS)
	}
	// "RTNETLINK answers: File exists" on a re-run is benign, so a failing
	// command is reported and skipped rather than aborting the rest.
	for _, c := range cmds {
		parts := strings.Fields(c)
		cmd := exec.Command(parts[0], parts[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v (%s)\n", c, err, strings.TrimSpace(string(out)))
		} else {
			fmt.Fprintf(os.Stderr, "  %s: ok\n", c)
		}
	}
	return nil
}
