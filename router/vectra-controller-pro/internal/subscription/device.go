package subscription

import (
	"fmt"
	"os"
	"strings"
)

// Canonical on-router source paths for the device identity the provider keys on.
const (
	MACPath   = "/sys/class/net/eth0/address"
	ModelPath = "/tmp/sysinfo/model"
	ReleaseP  = "/etc/openwrt_release"
)

// DeviceFacts is the identity the provider requires on every subscription
// request. If x-hwid changes, the provider refuses the device outright, which
// is why these are read from the exact files PassWall2 read — NOT from
// `ubus call system board`, whose `model` is a DIFFERENT string.
type DeviceFacts struct {
	MAC       string // /sys/class/net/eth0/address, trimmed
	Model     string // /tmp/sysinfo/model, trimmed (the file has a trailing newline)
	OSRelease string // DISTRIB_RELEASE from /etc/openwrt_release
	HWID      string // sha256(MAC + "-" + Model), lowercase hex
}

// ReadDeviceFacts collects the identity from the live filesystem.
// Missing files are not fatal: the caller decides. Fields that could not be
// read stay empty and HWID is only computed when both MAC and Model are known.
func ReadDeviceFacts() DeviceFacts {
	return readDeviceFactsFrom(os.ReadFile)
}

func readDeviceFactsFrom(readFile func(string) ([]byte, error)) DeviceFacts {
	var f DeviceFacts
	if b, err := readFile(MACPath); err == nil {
		f.MAC = strings.ToLower(strings.TrimSpace(string(b)))
	}
	if b, err := readFile(ModelPath); err == nil {
		// TRIM: the on-disk file is 25 bytes / 24 after trim on the AX3000T and
		// only the trimmed form reproduces the production HWID.
		f.Model = strings.TrimSpace(string(b))
	}
	if b, err := readFile(ReleaseP); err == nil {
		f.OSRelease = parseDistribRelease(string(b))
	}
	if f.MAC != "" && f.Model != "" {
		f.HWID = ComputeHWID(f.MAC, f.Model)
	}
	return f
}

// Validate reports why the identity is unusable, or nil when it is complete.
func (f DeviceFacts) Validate() error {
	var missing []string
	if f.MAC == "" {
		missing = append(missing, MACPath)
	}
	if f.Model == "" {
		missing = append(missing, ModelPath)
	}
	if len(missing) > 0 {
		return fmt.Errorf("device identity incomplete; unreadable: %s", strings.Join(missing, ", "))
	}
	return nil
}

func parseDistribRelease(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "DISTRIB_RELEASE" {
			continue
		}
		return strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return ""
}
