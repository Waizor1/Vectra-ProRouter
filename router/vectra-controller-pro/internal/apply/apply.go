// Package apply installs a PROVIDER-AUTHORED Xray document as the live config.
//
// Since the "consume provider JSON" pivot there is no rendering step: the
// provider's document is adopted wholesale and the ONLY mutation is replacing
// its inbounds array with the controller's tproxy inbound. Consequently:
//
//   - the digest is sha256 of the provider bytes directly. There is no
//     canonicalization pass — canonicalizing would mean parsing and
//     re-serializing, which is exactly the corruption we are avoiding.
//   - the provider bytes are persisted verbatim (config.SaveRaw), never
//     through config.Read, which uses DisallowUnknownFields and would hard-
//     reject a document full of Xray keys it has never heard of.
//   - `xray -test` GATES the write. A document Xray refuses is never
//     installed, so the previous good config stays live.
package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

// ErrRefused marks an apply `xray -test` refused: the previous render stays.
var ErrRefused = errors.New("xray -test refused the config")

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
	// DroppedInbounds records the provider inbounds the splice removed.
	DroppedInbounds []string `json:"droppedInbounds,omitempty"`
	// DroppedKeys and DroppedHosts are what of the provider's document the
	// router does not take, and left out (xray provider_guard.go): its
	// top-level keys, and dns.hosts entries over names the router resolves
	// directly.
	DroppedKeys  []string `json:"droppedKeys,omitempty"`
	DroppedHosts []string `json:"droppedHosts,omitempty"`
	// ProbeInterval is the observatory interval the render runs with when the
	// splice overrode it (0 = the provider's own), ProviderProbeInterval what
	// the provider asked for.
	ProbeInterval         time.Duration `json:"probeInterval,omitempty"`
	ProviderProbeInterval time.Duration `json:"providerProbeInterval,omitempty"`
}

// Validator gates the write. The daemon supplies a real `xray run -test`;
// tests supply a stub.
type Validator interface {
	Test(ctx context.Context, candidate []byte) error
}

// Applier splices + validates + persists a provider document.
type Applier struct {
	// Tproxy is the inbound spliced into every provider document. Required.
	Tproxy *config.TproxyInbound
	// ProviderPath is where the last-good provider document is stored VERBATIM.
	// Empty disables persistence (tests).
	ProviderPath string
	// SecretStorage selects authenticated at-rest storage for daemon-owned documents.
	SecretStorage bool
	// WriteXray atomically installs the spliced document (typically
	// supervisor.Process.WriteXrayConfig).
	WriteXray func(data []byte) error
	// Validate runs `xray -test`. Required — an unchecked config is never
	// installed.
	Validate Validator
	// AllowInsecureTLS disables the refuse-on-allowInsecure guard. Default off.
	AllowInsecureTLS bool
	// Splice carries the router-side options (API, metrics, probe interval,
	// the owner's sites). The zero value splices the inbound and the egress
	// mark only.
	Splice xray.SpliceOptions
}

// Digest returns the config digest of a provider document: sha256 of its bytes.
func Digest(providerRaw []byte) string {
	sum := sha256.Sum256(providerRaw)
	return hex.EncodeToString(sum[:])
}

// Apply installs providerRaw. When its digest already matches currentDigest and
// the rendered file exists, it is a no-op. It does NOT restart Xray — the
// caller reloads the supervisor when result.Changed is true.
func (a *Applier) Apply(ctx context.Context, providerRaw []byte, currentDigest string, renderExists bool) (ApplyResult, error) {
	res := ApplyResult{}
	if len(providerRaw) == 0 {
		return res, fmt.Errorf("apply: empty provider config")
	}
	if a.Tproxy == nil {
		return res, fmt.Errorf("apply: no tproxy inbound configured (nothing to splice)")
	}
	if a.WriteXray == nil {
		return res, fmt.Errorf("apply: no xray writer configured")
	}
	if a.Validate == nil {
		return res, fmt.Errorf("apply: no xray validator configured (refusing to install an unchecked config)")
	}

	res.DesiredDigest = Digest(providerRaw)
	if res.DesiredDigest == currentDigest && renderExists {
		res.Noop = true
		res.AppliedDigest = currentDigest
		return res, nil
	}

	// Read-only security gate. The old allowInsecure strip lived in the
	// subscription URI adapter, which the JSON path never touches.
	if !a.AllowInsecureTLS {
		if err := xray.ScanAllowInsecure(providerRaw); err != nil {
			return res, fmt.Errorf("apply: %w", err)
		}
		res.Operations = append(res.Operations, Operation{
			Kind: "scan_allow_insecure", Description: "provider config does not disable TLS verification",
		})
	}

	spliced, spliceRes, err := xray.Splice(providerRaw, a.Tproxy, a.Splice)
	if err != nil {
		return res, fmt.Errorf("apply: splice inbounds: %w", err)
	}
	res.ProbeInterval = spliceRes.ProbeInterval
	res.ProviderProbeInterval = spliceRes.ProviderProbeInterval
	res.DroppedInbounds = spliceRes.DroppedInbounds
	res.DroppedKeys, res.DroppedHosts = spliceRes.DroppedKeys, spliceRes.DNS.DroppedHosts
	if len(res.DroppedKeys) > 0 || len(res.DroppedHosts) > 0 {
		res.Operations = append(res.Operations, Operation{
			Kind: "drop_provider_parts",
			Description: fmt.Sprintf("left out what the router does not take: top-level keys %v, dns.hosts entries %v",
				res.DroppedKeys, res.DroppedHosts),
		})
	}
	res.Operations = append(res.Operations, Operation{
		Kind:        "splice_inbounds",
		Description: fmt.Sprintf("replaced %d provider inbound(s) with the tproxy inbound; %d other top-level keys kept verbatim", len(spliceRes.DroppedInbounds), len(spliceRes.TopLevelKeys)-1),
	})
	if spliceRes.APIListen != "" || spliceRes.MetricsListen != "" || spliceRes.ProbeInterval > 0 {
		res.Operations = append(res.Operations, Operation{
			Kind: "splice_router_runtime",
			Description: fmt.Sprintf("xray api on %s, metrics on %s, observatory interval %s (provider asked for %s)",
				orNone(spliceRes.APIListen), orNone(spliceRes.MetricsListen), orProvider(spliceRes.ProbeInterval), spliceRes.ProviderProbeInterval),
		})
	}
	if ur := spliceRes.UserRules; ur.Rules > 0 || ur.Dropped > 0 || len(ur.Skipped) > 0 {
		res.Operations = append(res.Operations, Operation{Kind: "splice_user_rules", Description: ur.Describe()})
	}

	// Write gate: never install what Xray will not start.
	if err := a.Validate.Test(ctx, spliced); err != nil {
		return res, fmt.Errorf("apply: refusing to install (previous config left in place): %w: %w", ErrRefused, err)
	}
	res.Operations = append(res.Operations, Operation{Kind: "xray_test", Description: "xray accepted the spliced config"})

	if a.ProviderPath != "" {
		save := config.SaveRaw
		if a.SecretStorage {
			save = config.SaveSecretRaw
		}
		if err := save(a.ProviderPath, providerRaw); err != nil {
			return res, fmt.Errorf("apply: persist provider config: %w", err)
		}
		res.Operations = append(res.Operations, Operation{Kind: "persist_provider", Description: "persisted provider document verbatim"})
	}

	if err := a.WriteXray(spliced); err != nil {
		return res, fmt.Errorf("apply: write xray.json: %w", err)
	}
	res.Operations = append(res.Operations, Operation{Kind: "write_xray", Description: "wrote spliced xray.json"})

	res.Changed = true
	res.AppliedDigest = res.DesiredDigest
	res.XrayBytes = len(spliced)
	return res, nil
}

// FileExists is a small helper for the daemon to decide renderExists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orProvider(d time.Duration) string {
	if d == 0 {
		return "unchanged"
	}
	return d.String()
}
