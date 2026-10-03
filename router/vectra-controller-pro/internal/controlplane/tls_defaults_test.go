package controlplane

import (
	"runtime/debug"
	"strings"
	"testing"
)

// The router's TLS — to the panel, to the provider, to the feed — runs with
// the toolchain's own defaults, not those of the Go version go.mod once named:
// an old `go` line keeps 3DES, SHA-1 signatures, RSA keys under 1024 bits and
// no post-quantum key exchange switched on through GODEBUG, in every binary
// the module builds.
func TestTheModuleRunsWithCurrentTLSDefaults(t *testing.T) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	godebug := ""
	for _, s := range bi.Settings {
		if s.Key == "DefaultGODEBUG" {
			godebug = s.Value
		}
	}
	for _, old := range []string{"tls3des=1", "tlssha1=1", "tlsmlkem=0", "rsa1024min=0", "x509negativeserial=1", "tlsrsakex=1"} {
		if strings.Contains(","+godebug+",", ","+old+",") {
			t.Errorf("built with %s: go.mod's go line predates the toolchain (%s); DefaultGODEBUG=%s", old, bi.GoVersion, godebug)
		}
	}
}
