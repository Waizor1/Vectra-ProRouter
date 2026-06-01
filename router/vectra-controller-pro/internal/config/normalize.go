package config

import "fmt"

// ApplyNormalization applies the operator-explicit Normalization toggles to c
// IN PLACE and returns a human-readable list of every value it changed.
//
// Per the schema contract ("no silent normalization, but loggable"), every
// toggle defaults to OFF: with a zero-valued Normalization block this function
// changes nothing and returns nil. When a toggle is on, the caller is expected
// to log the returned change list (see xray.Engine.Render), so the
// transformation is auditable rather than silent.
//
// Currently implemented: ForceFingerprint. When enabled, every node's TLS and
// REALITY stream Fingerprint is overridden with FingerprintValue. A node whose
// fingerprint already equals FingerprintValue is left untouched and not logged.
func ApplyNormalization(c *Config) []string {
	if c == nil {
		return nil
	}
	var changes []string

	if c.Normalization.ForceFingerprint {
		fp := c.Normalization.FingerprintValue
		for i := range c.Nodes {
			s := c.Nodes[i].Outbound.Stream
			if s == nil {
				continue
			}
			id := c.Nodes[i].ID
			if s.TLS != nil && s.TLS.Fingerprint != fp {
				changes = append(changes, fmt.Sprintf(
					"normalization.forceFingerprint: nodes[%s].stream.tls.fingerprint: %q -> %q",
					id, s.TLS.Fingerprint, fp))
				s.TLS.Fingerprint = fp
			}
			if s.REALITY != nil && s.REALITY.Fingerprint != fp {
				changes = append(changes, fmt.Sprintf(
					"normalization.forceFingerprint: nodes[%s].stream.reality.fingerprint: %q -> %q",
					id, s.REALITY.Fingerprint, fp))
				s.REALITY.Fingerprint = fp
			}
		}
	}

	return changes
}
