// Synthetic developer-only fixture. Not shipped in the IPK.
package main
import "vectra-controller-pro/internal/state"
func main() {
 // Nil argument panics before any file read; no router identity or key involved.
 _, _ = state.ImportLegacyIdentity(nil, "synthetic-unused-state.json")
}
