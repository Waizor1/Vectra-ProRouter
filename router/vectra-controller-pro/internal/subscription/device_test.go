package subscription

import (
	"os"
	"strings"
	"testing"
)

// Production vector measured on the live AX3000T. Both inputs are TRIMMED
// before hashing; /tmp/sysinfo/model is 25 bytes on disk and 24 after trim.
const (
	prodMAC   = "cc:d8:43:b1:bd:0c"
	prodModel = "Xiaomi Mi Router AX3000T"
	prodHWID  = "760386f8b139baf471f566a22efed5a4cd24a2241636d84524bd30bc28c08b4a"
)

// TestHWIDMatchesPassWall pins the exact device identity the provider keys on.
// If this changes, every router in the fleet is refused.
func TestHWIDMatchesPassWall(t *testing.T) {
	cases := []struct {
		name  string
		mac   string
		model string
	}{
		{"exact production vector", prodMAC, prodModel},
		// /tmp/sysinfo/model HAS a trailing newline on disk. The trimmed form
		// is what reproduces the production HWID — hashing the raw bytes
		// yields a different identity and the provider rejects the device.
		{"model with trailing newline", prodMAC, prodModel + "\n"},
		{"mac with trailing newline", prodMAC + "\n", prodModel},
		{"both untrimmed", prodMAC + "\n", prodModel + "\n"},
		{"model with trailing CRLF", prodMAC, prodModel + "\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeHWID(tc.mac, tc.model); got != prodHWID {
				t.Fatalf("HWID mismatch:\n got  %s\n want %s", got, prodHWID)
			}
		})
	}
}

// The on-disk model really is 25 bytes / 24 trimmed — assert the arithmetic the
// production vector depends on so a future "helpful" untrimmed read is caught.
func TestModelTrimLengths(t *testing.T) {
	onDisk := prodModel + "\n"
	if len(onDisk) != 25 {
		t.Fatalf("on-disk model length = %d, want 25", len(onDisk))
	}
	if len(prodModel) != 24 {
		t.Fatalf("trimmed model length = %d, want 24", len(prodModel))
	}
}

func TestHWIDChangesWhenIdentityChanges(t *testing.T) {
	if ComputeHWID(prodMAC, "Xiaomi Mi Router AX3000") == prodHWID {
		t.Fatal("a different model must produce a different HWID")
	}
	if ComputeHWID("cc:d8:43:b1:bd:0d", prodModel) == prodHWID {
		t.Fatal("a different MAC must produce a different HWID")
	}
}

// ReadDeviceFacts must trim, lowercase the MAC, and reproduce the prod HWID
// from files shaped exactly like the router's.
func TestReadDeviceFactsTrimsAndComputesHWID(t *testing.T) {
	files := map[string]string{
		MACPath:   prodMAC + "\n",
		ModelPath: prodModel + "\n", // 25 bytes, as measured
		ReleaseP: "DISTRIB_ID='OpenWrt'\nDISTRIB_RELEASE='24.10.6'\n" +
			"DISTRIB_ARCH='aarch64_cortex-a53'\n",
	}
	facts := readDeviceFactsFrom(func(p string) ([]byte, error) {
		v, ok := files[p]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(v), nil
	})
	if facts.MAC != prodMAC {
		t.Errorf("MAC = %q, want %q", facts.MAC, prodMAC)
	}
	if facts.Model != prodModel {
		t.Errorf("Model = %q (len %d), want %q (len %d)", facts.Model, len(facts.Model), prodModel, len(prodModel))
	}
	if facts.OSRelease != "24.10.6" {
		t.Errorf("OSRelease = %q, want 24.10.6", facts.OSRelease)
	}
	if facts.HWID != prodHWID {
		t.Errorf("HWID = %s, want %s", facts.HWID, prodHWID)
	}
	if err := facts.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestReadDeviceFactsUppercaseMACIsNormalized(t *testing.T) {
	facts := readDeviceFactsFrom(func(p string) ([]byte, error) {
		switch p {
		case MACPath:
			return []byte("CC:D8:43:B1:BD:0C\n"), nil
		case ModelPath:
			return []byte(prodModel + "\n"), nil
		}
		return nil, os.ErrNotExist
	})
	if facts.HWID != prodHWID {
		t.Fatalf("uppercase MAC must normalize to the production HWID; got %s", facts.HWID)
	}
}

func TestDeviceFactsValidateReportsMissingSources(t *testing.T) {
	facts := readDeviceFactsFrom(func(string) ([]byte, error) { return nil, os.ErrNotExist })
	err := facts.Validate()
	if err == nil {
		t.Fatal("expected an error when the identity files are unreadable")
	}
	for _, want := range []string{MACPath, ModelPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s: %v", want, err)
		}
	}
	if facts.HWID != "" {
		t.Error("HWID must stay empty when MAC/model are unknown")
	}
}
