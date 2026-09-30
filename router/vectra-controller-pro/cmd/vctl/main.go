// vctl — Vectra Controller Pro CLI.
//
// All commands run locally (no router contact required for v0.1 alpha).
// See README for usage.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"vectra-controller-pro/internal/logging"
)

// Version is overridden at build time via -ldflags "-X main.Version=...".
var (
	Version   = "0.1.0-alpha"
	BuildDate = "dev"
	Commit    = "dev"
)

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands []command

func register(c command) { commands = append(commands, c) }

func init() {
	register(command{name: "version", summary: "Print version info", run: cmdVersion})
	register(command{name: "render", summary: "Render Xray JSON from operator config", run: cmdRender})
	register(command{name: "validate", summary: "Validate an operator config file", run: cmdValidate})
	register(command{name: "subscribe", summary: "Subscription tools (fetch|parse)", run: cmdSubscribe})
	register(command{name: "supervise", summary: "Run Xray under supervisor (long-running)", run: cmdSupervise})
	register(command{name: "firewall", summary: "Generate or apply nftables ruleset", run: cmdFirewall})
	register(command{name: "geo", summary: "Geo data tools (update|verify)", run: cmdGeo})
	register(command{name: "doctor", summary: "Diagnostic checks (env, deps, perms)", run: cmdDoctor})
	register(command{name: "api", summary: "Xray gRPC API tools (stats|observatory|handler)", run: cmdAPI})
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" || name == "help" {
		usage()
		return
	}
	for _, c := range commands {
		if c.name == name {
			if err := c.run(os.Args[2:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintf(os.Stderr, "vctl %s: %v\n", c.name, err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "vctl: unknown command %q\n\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "Vectra Controller Pro v"+Version)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Usage: vctl <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", c.name, c.summary)
	}
}

func cmdVersion(_ []string) error {
	fmt.Printf("vctl %s (commit=%s built=%s)\n", Version, Commit, BuildDate)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("vctl "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// Helper for subcommands that don't print before logging is configured.
func setupLogging(level string) {
	logging.SetDefault(logging.New(level, os.Stderr, "text"))
}
