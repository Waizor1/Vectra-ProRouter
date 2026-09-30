package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultMetricsServer is where the router splices xray's metrics endpoint
// (internal/coreengine/xray: SpliceOptions.MetricsListen).
const DefaultMetricsServer = "127.0.0.1:10086"

// Observation is xray's view of one outbound, from /debug/vars "observatory".
// burstObservatory reports health_ping statistics and no timestamps; the
// classic observatory reports timestamps and a single delay.
type Observation struct {
	Alive bool `json:"alive"`
	// DelayMs is the average probe round trip; 0 for a dead node, and also
	// for a sub-millisecond one.
	DelayMs    int64       `json:"delay"`
	LastSeen   int64       `json:"last_seen_time"`
	LastTry    int64       `json:"last_try_time"`
	HealthPing *HealthPing `json:"health_ping"`
}

// HealthPing is burstObservatory's per-node statistics, durations in ns.
type HealthPing struct {
	All       int64 `json:"all"`
	Fail      int64 `json:"fail"`
	Deviation int64 `json:"deviation"`
	Average   int64 `json:"average"`
	Max       int64 `json:"max"`
	Min       int64 `json:"min"`
}

// Traffic is one outbound's byte counters since xray started.
type Traffic struct {
	Uplink   int64 `json:"uplink"`
	Downlink int64 `json:"downlink"`
}

// Metrics is the part of /debug/vars the router uses. The rest (memstats,
// cmdline) is skipped by the decoder.
type Metrics struct {
	Observatory map[string]Observation `json:"observatory"`
	Stats       struct {
		Outbound map[string]Traffic `json:"outbound"`
	} `json:"stats"`
}

// maxMetricsBytes bounds the read: /debug/vars is ~10 KB (memstats dominates).
const maxMetricsBytes = 2 << 20

// FetchMetrics reads xray's metrics endpoint.
func FetchMetrics(ctx context.Context, server string) (*Metrics, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server+"/debug/vars", nil)
	if err != nil {
		return nil, err
	}
	// A private client: no proxy from the environment, no keep-alive pool
	// outliving a short-lived rpcd process.
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xray metrics: http %d", resp.StatusCode)
	}
	var m Metrics
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetricsBytes)).Decode(&m); err != nil {
		return nil, fmt.Errorf("xray metrics: %w", err)
	}
	return &m, nil
}
