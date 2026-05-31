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

func loadSpec(cfgPath string) (firewall.Spec, error) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return firewall.Spec{}, err
	}
	if c.Inbounds.Tproxy == nil {
		return firewall.Spec{}, fmt.Errorf("config has no inbounds.tproxy; firewall not applicable")
	}
	s := firewall.DefaultSpec(c.Inbounds.Tproxy.Port, c.Inbounds.Tproxy.FwMark)
	return s, nil
}

func firewallRender(args []string) error {
	fs := newFlagSet("firewall render")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	out := fs.String("out", "-", "output file; '-' for stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	s, err := loadSpec(*cfgPath)
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
	s, err := loadSpec(*cfgPath)
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
	s, err := loadSpec(*cfgPath)
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

func firewallRouting(args []string) error {
	fs := newFlagSet("firewall routing")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	s, err := loadSpec(*cfgPath)
	if err != nil {
		return err
	}
	for _, c := range firewall.RoutingCommands(s) {
		fmt.Println(c)
	}
	return nil
}
