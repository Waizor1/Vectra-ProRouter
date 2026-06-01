// Package api is a typed client for Xray's gRPC API surface
// (StatsService, HandlerService, ObservatoryService, LoggerService).
//
// For v0.1 alpha the concrete implementation shells out to the `xray api ...`
// subcommands. The Client interface is stable so v0.2 can swap in a native
// google.golang.org/grpc client without changing call sites.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Client is the stable surface other packages depend on.
type Client interface {
	// Stats
	StatQuery(ctx context.Context, pattern string, reset bool) ([]Stat, error)
	StatGet(ctx context.Context, name string, reset bool) (Stat, error)
	SystemStats(ctx context.Context) (SystemStats, error)

	// HandlerService — hot add/remove of outbounds and inbounds.
	AddOutbound(ctx context.Context, outboundJSON []byte) error
	RemoveOutbound(ctx context.Context, tag string) error
	AddInbound(ctx context.Context, inboundJSON []byte) error
	RemoveInbound(ctx context.Context, tag string) error

	// Logger — restart access log + level change at runtime.
	RestartLogger(ctx context.Context) error

	// Observatory (not yet implemented via CLI shell-out — returns ErrNotImplemented).
	Observatory(ctx context.Context, tag string) (ObservatoryStatus, error)

	// Health: round-trips a no-op to ensure the API is reachable.
	Ping(ctx context.Context) error
}

// Stat is one named counter from StatsService.
type Stat struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// SystemStats is xray's internal system info (NumGoroutine, etc.)
type SystemStats struct {
	NumGoroutine uint32 `json:"numGoroutine"`
	NumGC        uint32 `json:"numGc"`
	Alloc        uint64 `json:"alloc"`
	TotalAlloc   uint64 `json:"totalAlloc"`
	Sys          uint64 `json:"sys"`
	Mallocs      uint64 `json:"mallocs"`
	Frees        uint64 `json:"frees"`
	LiveConnections int32 `json:"liveConnections"`
	Uptime       uint32 `json:"uptime"`
}

// ObservatoryStatus is per-outbound health.
type ObservatoryStatus struct {
	OutboundTag string        `json:"outboundTag"`
	Alive       bool          `json:"alive"`
	Delay       time.Duration `json:"delay"`
	LastSeen    time.Time     `json:"lastSeen"`
	LastTryTime time.Time     `json:"lastTryTime"`
	LastErrors  []string      `json:"lastErrors,omitempty"`
}

// ErrNotImplemented is returned for capabilities not yet wired in v0.1 alpha
// (specifically: native ObservatoryService access; v0.2 will fix).
var ErrNotImplemented = fmt.Errorf("api: not implemented in v0.1 alpha (use vctl/v0.2 native gRPC)")

// CLIClient is the shell-out implementation that calls `xray api ...`.
type CLIClient struct {
	Binary string // path to xray binary; default "xray"
	Server string // host:port of the API inbound; default "127.0.0.1:10085"
	// CommandTimeout caps individual `xray api ...` calls.
	CommandTimeout time.Duration
}

// NewCLIClient returns a CLIClient with sensible defaults.
func NewCLIClient(binary, server string) *CLIClient {
	c := &CLIClient{Binary: binary, Server: server, CommandTimeout: 5 * time.Second}
	if c.Binary == "" {
		c.Binary = "xray"
	}
	if c.Server == "" {
		c.Server = "127.0.0.1:10085"
	}
	return c
}

func (c *CLIClient) exec(ctx context.Context, args ...string) ([]byte, error) {
	if c.CommandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.CommandTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Binary, append([]string{"api"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("xray api %s: %w (out=%q)", strings.Join(args, " "), err, truncate(string(out), 200))
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Ping does a no-op stats query — cheap and existence-checking.
func (c *CLIClient) Ping(ctx context.Context) error {
	_, err := c.exec(ctx, "stats", "-server="+c.Server, "-name=", "-runtime=false")
	if err != nil {
		// If the API tag isn't configured, this fails — that's still a useful signal.
		return err
	}
	return nil
}

// StatGet returns one named counter.
func (c *CLIClient) StatGet(ctx context.Context, name string, reset bool) (Stat, error) {
	args := []string{"stats", "-server=" + c.Server, "-name=" + name}
	if reset {
		args = append(args, "-reset=true")
	}
	out, err := c.exec(ctx, args...)
	if err != nil {
		return Stat{}, err
	}
	return parseSingleStat(out, name)
}

// StatQuery returns all stats matching a regex pattern.
func (c *CLIClient) StatQuery(ctx context.Context, pattern string, reset bool) ([]Stat, error) {
	args := []string{"statsquery", "-server=" + c.Server, "-pattern=" + pattern}
	if reset {
		args = append(args, "-reset=true")
	}
	out, err := c.exec(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseStatList(out)
}

// SystemStats returns xray's system counters.
func (c *CLIClient) SystemStats(ctx context.Context) (SystemStats, error) {
	out, err := c.exec(ctx, "statssys", "-server="+c.Server)
	if err != nil {
		return SystemStats{}, err
	}
	var s SystemStats
	if err := json.Unmarshal(out, &s); err != nil {
		return SystemStats{}, fmt.Errorf("parse statssys: %w", err)
	}
	return s, nil
}

// AddOutbound sends an "ado" with an outbound JSON document.
// xray expects the JSON on stdin; we pipe it through.
func (c *CLIClient) AddOutbound(ctx context.Context, outboundJSON []byte) error {
	if c.CommandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.CommandTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Binary, "api", "ado", "-server="+c.Server, "-")
	cmd.Stdin = strings.NewReader(string(outboundJSON))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray api ado: %w (%s)", err, truncate(string(out), 200))
	}
	return nil
}

// RemoveOutbound removes an outbound by tag.
func (c *CLIClient) RemoveOutbound(ctx context.Context, tag string) error {
	_, err := c.exec(ctx, "rmo", "-server="+c.Server, "-t="+tag)
	return err
}

// AddInbound mirrors AddOutbound for inbounds.
func (c *CLIClient) AddInbound(ctx context.Context, inboundJSON []byte) error {
	if c.CommandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.CommandTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Binary, "api", "adi", "-server="+c.Server, "-")
	cmd.Stdin = strings.NewReader(string(inboundJSON))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray api adi: %w (%s)", err, truncate(string(out), 200))
	}
	return nil
}

// RemoveInbound removes an inbound by tag.
func (c *CLIClient) RemoveInbound(ctx context.Context, tag string) error {
	_, err := c.exec(ctx, "rmi", "-server="+c.Server, "-t="+tag)
	return err
}

// RestartLogger restarts xray's logger (e.g., to rotate access log).
func (c *CLIClient) RestartLogger(ctx context.Context) error {
	_, err := c.exec(ctx, "restartlogger", "-server="+c.Server)
	return err
}

// Observatory: deliberately not implemented via CLI in v0.1 alpha.
// Native gRPC client will be added in v0.2 with proper ObservatoryService support.
func (c *CLIClient) Observatory(ctx context.Context, tag string) (ObservatoryStatus, error) {
	return ObservatoryStatus{}, ErrNotImplemented
}

// parseSingleStat handles `xray api stats` plain-text output:
//
//	stat: <name> <value>
//
// Some xray builds emit JSON; we tolerate both.
func parseSingleStat(out []byte, name string) (Stat, error) {
	t := strings.TrimSpace(string(out))
	if t == "" {
		return Stat{Name: name, Value: 0}, nil
	}
	// JSON path: {"stat":{"name":"...","value":"..."}}
	if t[0] == '{' {
		var wrap struct {
			Stat struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"stat"`
		}
		if err := json.Unmarshal([]byte(t), &wrap); err == nil {
			v, _ := coerceInt(wrap.Stat.Value)
			return Stat{Name: wrap.Stat.Name, Value: v}, nil
		}
	}
	// Plain text fallback
	parts := strings.Fields(t)
	if len(parts) >= 2 {
		v, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		return Stat{Name: name, Value: v}, nil
	}
	return Stat{}, fmt.Errorf("unparseable stat output: %q", truncate(t, 80))
}

func parseStatList(out []byte) ([]Stat, error) {
	t := strings.TrimSpace(string(out))
	if t == "" {
		return nil, nil
	}
	if t[0] == '{' {
		var wrap struct {
			Stat []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"stat"`
		}
		if err := json.Unmarshal([]byte(t), &wrap); err != nil {
			return nil, err
		}
		out := make([]Stat, 0, len(wrap.Stat))
		for _, s := range wrap.Stat {
			v, _ := coerceInt(s.Value)
			out = append(out, Stat{Name: s.Name, Value: v})
		}
		return out, nil
	}
	// Plain text fallback: "name value\nname value\n..."
	var stats []Stat
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		stats = append(stats, Stat{Name: parts[0], Value: v})
	}
	return stats, nil
}

func coerceInt(v any) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	}
	return 0, fmt.Errorf("not int")
}

var _ Client = (*CLIClient)(nil)
