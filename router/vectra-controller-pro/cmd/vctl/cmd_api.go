package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"vectra-controller-pro/internal/api"
)

func cmdAPI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("api: subcommand required: ping | stats | statquery | sys | add-outbound | rm-outbound | observatory | logger-restart")
	}
	switch args[0] {
	case "ping":
		return apiSimple(args[1:], func(c api.Client, ctx context.Context) error { return c.Ping(ctx) }, "ok")
	case "stats":
		return apiStat(args[1:])
	case "statquery":
		return apiStatQuery(args[1:])
	case "sys":
		return apiSys(args[1:])
	case "add-outbound":
		return apiAddOutbound(args[1:])
	case "rm-outbound":
		return apiRmOutbound(args[1:])
	case "observatory":
		return apiObservatory(args[1:])
	case "logger-restart":
		return apiSimple(args[1:], func(c api.Client, ctx context.Context) error { return c.RestartLogger(ctx) }, "restarted")
	default:
		return fmt.Errorf("api: unknown subcommand %q", args[0])
	}
}

// Shared flag set for API subcommands.
func apiClientFromFlags(args []string) (api.Client, []string, error) {
	fs := newFlagSet("api")
	binary := fs.String("binary", "xray", "path to xray binary")
	server := fs.String("server", "127.0.0.1:10085", "API server host:port")
	timeout := fs.Int("timeout", 5, "per-call timeout (seconds)")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	c := api.NewCLIClient(*binary, *server)
	c.CommandTimeout = time.Duration(*timeout) * time.Second
	return c, fs.Args(), nil
}

func apiSimple(args []string, fn func(api.Client, context.Context) error, ok string) error {
	c, _, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	if err := fn(c, context.Background()); err != nil {
		return err
	}
	fmt.Println(ok)
	return nil
}

func apiStat(args []string) error {
	c, rest, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("api stats: <name> required (e.g. 'outbound>>>node-world>>>traffic>>>uplink')")
	}
	s, err := c.StatGet(context.Background(), rest[0], false)
	if err != nil {
		return err
	}
	return printJSON(s)
}

func apiStatQuery(args []string) error {
	c, rest, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	pattern := ""
	if len(rest) > 0 {
		pattern = rest[0]
	}
	list, err := c.StatQuery(context.Background(), pattern, false)
	if err != nil {
		return err
	}
	return printJSON(list)
}

func apiSys(args []string) error {
	c, _, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	s, err := c.SystemStats(context.Background())
	if err != nil {
		return err
	}
	return printJSON(s)
}

func apiAddOutbound(args []string) error {
	c, rest, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	src := "-"
	if len(rest) > 0 {
		src = rest[0]
	}
	var data []byte
	if src == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(src)
	}
	if err != nil {
		return err
	}
	if err := c.AddOutbound(context.Background(), data); err != nil {
		return err
	}
	fmt.Println("added")
	return nil
}

func apiRmOutbound(args []string) error {
	c, rest, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("api rm-outbound: <tag> required")
	}
	if err := c.RemoveOutbound(context.Background(), rest[0]); err != nil {
		return err
	}
	fmt.Println("removed")
	return nil
}

func apiObservatory(args []string) error {
	c, rest, err := apiClientFromFlags(args)
	if err != nil {
		return err
	}
	tag := ""
	if len(rest) > 0 {
		tag = rest[0]
	}
	o, err := c.Observatory(context.Background(), tag)
	if errors.Is(err, api.ErrNotImplemented) {
		fmt.Fprintln(os.Stderr, "observatory access via CLI shell-out is not implemented in v0.1 alpha")
		fmt.Fprintln(os.Stderr, "native gRPC ObservatoryService access ships in v0.2")
		return err
	}
	if err != nil {
		return err
	}
	return printJSON(o)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
