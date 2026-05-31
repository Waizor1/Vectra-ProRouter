package main

import (
	"fmt"
	"os"

	"vectra-controller-pro/internal/config"
)

func cmdValidate(args []string) error {
	fs := newFlagSet("validate")
	cfgPath := fs.String("config", "", "path to operator config JSON")
	verbose := fs.Bool("v", false, "print defaults diff")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ok: %s (schema=%d, nodes=%d, rules=%d, subs=%d)\n",
		*cfgPath, c.Schema, len(c.Nodes), len(c.Routing.Rules), len(c.Subscriptions))
	if *verbose {
		diffs := config.DefaultsDiff(c)
		if len(diffs) == 0 {
			fmt.Fprintln(os.Stderr, "(no defaults applied)")
		} else {
			fmt.Fprintln(os.Stderr, "defaults applied:")
			for _, d := range diffs {
				fmt.Fprintln(os.Stderr, "  -", d)
			}
		}
	}
	return nil
}
