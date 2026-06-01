package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

func cmdRender(args []string) error {
	fs := newFlagSet("render")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	out := fs.String("out", "-", "output file; '-' for stdout")
	pretty := fs.Bool("pretty", true, "pretty-print output (default true)")
	caps := fs.Bool("caps", false, "instead of rendering, print engine capabilities")
	if err := fs.Parse(args); err != nil {
		return err
	}
	eng := xray.New()
	if *caps {
		c := eng.Capabilities()
		b, _ := json.MarshalIndent(c, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if err := eng.Validate(context.Background(), c); err != nil {
		return fmt.Errorf("engine validate: %w", err)
	}
	data, err := eng.Render(context.Background(), c)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if !*pretty {
		// Re-marshal compact.
		var tmp any
		if err := json.Unmarshal(data, &tmp); err == nil {
			data, _ = json.Marshal(tmp)
		}
	}
	if *out == "-" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(*out, append(data, '\n'), 0o600)
}
