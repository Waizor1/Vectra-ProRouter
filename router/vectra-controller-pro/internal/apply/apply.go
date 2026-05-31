// Package apply is the xray-direct analogue of the legacy agent's
// passwall/apply: it turns an operator's desired config (an internal/config
// Config, schema 1, delivered by the panel as JSON) into a live Xray runtime.
// The flow is filesystem-only and deterministic — render -> persist desired
// config -> write xray.json — and returns an ApplyResult shaped like the
// agent's so job-result reporting stays compatible. The daemon owns the
// supervisor and decides whether to reload based on result.Changed.
package apply

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine"
)

// Operation is a single visible step of an apply (mirrors the agent shape).
type Operation struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

// ApplyResult is the outcome of an apply, reported back to the panel.
type ApplyResult struct {
	Noop          bool        `json:"noop"`
	Changed       bool        `json:"changed"`
	DesiredDigest string      `json:"desiredDigest"`
	AppliedDigest string      `json:"appliedDigest"`
	Operations    []Operation `json:"operations"`
	XrayBytes     int         `json:"xrayBytes"`
}

// Applier renders + persists desired xray configs.
type Applier struct {
	engine     coreengine.Engine
	configPath string                 // where the operator desired config is persisted
	writeXray  func(data []byte) error // atomic writer for the rendered xray.json
}

// New builds an Applier. writeXray is typically supervisor.Process.WriteXrayConfig.
func New(engine coreengine.Engine, configPath string, writeXray func([]byte) error) *Applier {
	return &Applier{engine: engine, configPath: configPath, writeXray: writeXray}
}

// Digest returns the canonical digest of a desired config.
func Digest(cfg *config.Config) (string, error) {
	canonical, err := config.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Apply decodes raw desired-config JSON, and if it differs from currentDigest
// (or the rendered file is missing), renders it to Xray JSON and persists both
// the desired config and the rendered config. It does NOT restart Xray — the
// caller reloads the supervisor when result.Changed is true.
func (a *Applier) Apply(ctx context.Context, raw json.RawMessage, currentDigest string, renderExists bool) (ApplyResult, error) {
	res := ApplyResult{}
	cfg, err := config.Read(bytes.NewReader(raw), "desired-revision")
	if err != nil {
		return res, fmt.Errorf("decode desired config: %w", err)
	}
	desiredDigest, err := Digest(cfg)
	if err != nil {
		return res, fmt.Errorf("digest: %w", err)
	}
	res.DesiredDigest = desiredDigest

	if desiredDigest == currentDigest && renderExists {
		res.Noop = true
		res.AppliedDigest = currentDigest
		return res, nil
	}

	xrayBytes, err := a.engine.Render(ctx, cfg)
	if err != nil {
		return res, fmt.Errorf("render xray config: %w", err)
	}
	res.Operations = append(res.Operations, Operation{Kind: "render", Description: "rendered Xray JSON from desired config"})

	if a.configPath != "" {
		if err := config.Save(a.configPath, cfg); err != nil {
			return res, fmt.Errorf("persist desired config: %w", err)
		}
		res.Operations = append(res.Operations, Operation{Kind: "persist_config", Description: "persisted operator desired config"})
	}

	if a.writeXray == nil {
		return res, fmt.Errorf("apply: no xray writer configured")
	}
	if err := a.writeXray(xrayBytes); err != nil {
		return res, fmt.Errorf("write xray.json: %w", err)
	}
	res.Operations = append(res.Operations, Operation{Kind: "write_xray", Description: "wrote rendered xray.json"})

	res.Changed = true
	res.AppliedDigest = desiredDigest
	res.XrayBytes = len(xrayBytes)
	return res, nil
}

// FileExists is a small helper for the daemon to decide renderExists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
