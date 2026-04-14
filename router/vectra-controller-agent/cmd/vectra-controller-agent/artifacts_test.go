package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseArtifactJobPrefersPackageList(t *testing.T) {
	job := parseArtifactJob(map[string]interface{}{
		"artifactUrl":       "https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/vectra-controller-agent_0.1.6-r1_aarch64_cortex-a53.ipk",
		"sha256":            "abc123",
		"signatureUrl":      "https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/Packages.sig",
		"artifactVersion":   "0.1.6-r1",
		"validationCommand": "sysupgrade -T /tmp/firmware.bin",
		"packageList": []interface{}{
			"vectra-controller-agent",
			"luci-app-vectra-controller",
		},
		"packages": []interface{}{
			"legacy-package",
		},
	}, []string{"fallback"})

	if got, want := job.ArtifactURL, "https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/vectra-controller-agent_0.1.6-r1_aarch64_cortex-a53.ipk"; got != want {
		t.Fatalf("artifact url = %q, want %q", got, want)
	}
	if got, want := job.ArtifactVersion, "0.1.6-r1"; got != want {
		t.Fatalf("artifact version = %q, want %q", got, want)
	}
	if got, want := job.ValidationCommand, "sysupgrade -T /tmp/firmware.bin"; got != want {
		t.Fatalf("validation command = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(job.PackageList, []string{
		"vectra-controller-agent",
		"luci-app-vectra-controller",
	}) {
		t.Fatalf("unexpected package list: %#v", job.PackageList)
	}
}

func TestParseArtifactJobReadsExplicitPackageArtifacts(t *testing.T) {
	job := parseArtifactJob(map[string]interface{}{
		"packageList": []interface{}{
			"vectra-controller-agent",
			"luci-app-vectra-controller",
		},
		"packageArtifacts": []interface{}{
			map[string]interface{}{
				"name":            "vectra-controller-agent",
				"artifactUrl":     "https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/vectra-controller-agent_0.1.3-r1_aarch64_cortex-a53.ipk",
				"sha256":          "deadbeef",
				"artifactVersion": "0.1.3-r1",
			},
			map[string]interface{}{
				"name":            "luci-app-vectra-controller",
				"artifactUrl":     "https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/luci-app-vectra-controller_0.1.3-r1_all.ipk",
				"sha256":          "feedface",
				"artifactVersion": "0.1.3-r1",
			},
		},
	}, nil)

	if len(job.PackageArtifacts) != 2 {
		t.Fatalf("expected 2 explicit package artifacts, got %d", len(job.PackageArtifacts))
	}
	if got, want := job.PackageArtifacts[0].Name, "vectra-controller-agent"; got != want {
		t.Fatalf("first package artifact name = %q, want %q", got, want)
	}
	if got, want := job.PackageArtifacts[1].SHA256, "feedface"; got != want {
		t.Fatalf("second package artifact sha256 = %q, want %q", got, want)
	}
}

func TestParsePackagesIndex(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "Packages")
	body := `Package: vectra-controller-agent
Version: 0.1.6-r1
Filename: vectra-controller-agent_0.1.6-r1_aarch64_cortex-a53.ipk
SHA256sum: aaaabbbb

Package: luci-app-vectra-controller
Version: 0.1.6-r1
Filename: luci-app-vectra-controller_0.1.6-r1_all.ipk
SHA256sum: ccccdddd
`

	if err := os.WriteFile(indexPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write packages index: %v", err)
	}

	entries, err := parsePackagesIndex(indexPath)
	if err != nil {
		t.Fatalf("parse packages index: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if got, want := entries[0].Package, "vectra-controller-agent"; got != want {
		t.Fatalf("entry[0].Package = %q, want %q", got, want)
	}
	if got, want := entries[1].Filename, "luci-app-vectra-controller_0.1.6-r1_all.ipk"; got != want {
		t.Fatalf("entry[1].Filename = %q, want %q", got, want)
	}
}
