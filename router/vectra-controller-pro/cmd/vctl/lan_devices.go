package main

import (
	"encoding/json"
	"os/exec"
	"sort"

	"vectra-controller-pro/internal/firewall"
)

// withLANDevices gives a ruleset the LAN's devices: the IPv6 refusal leaves
// them out, and xray may not dial into them (firewall.CounterLANDial).
// Asked of netifd when a ruleset is loaded, not every loop.
func withLANDevices(spec firewall.Spec) firewall.Spec {
	spec.LANDevices = lanDevices()
	return spec
}

// lanDevices is the router's LAN-side devices, for the IPv6 refusal's
// exemption and the LAN egress guard (firewall.Spec.LANDevices): what netifd set up statically — the
// LAN, a guest network — that is up and carries no default route, and no
// device a default route leaves by. nil on any doubt; the refusal then stays
// whole.
var lanDevices = func() []string {
	out, err := exec.Command("ubus", "-t", "5", "call", "network.interface", "dump").Output()
	if err != nil {
		return nil
	}
	return lanDevicesFrom(out)
}

// lanDevicesFrom reads `ubus call network.interface dump`.
func lanDevicesFrom(dump []byte) []string {
	var d struct {
		Interface []struct {
			Up       bool   `json:"up"`
			Proto    string `json:"proto"`
			L3Device string `json:"l3_device"`
			Route    []struct {
				Mask int `json:"mask"`
			} `json:"route"`
		} `json:"interface"`
	}
	if json.Unmarshal(dump, &d) != nil {
		return nil
	}
	outward := map[string]bool{}
	for _, i := range d.Interface {
		for _, r := range i.Route {
			if r.Mask == 0 {
				outward[i.L3Device] = true
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, i := range d.Interface {
		dev := i.L3Device
		if !i.Up || i.Proto != "static" || dev == "" || dev == "lo" || outward[dev] || seen[dev] {
			continue
		}
		seen[dev] = true
		out = append(out, dev)
	}
	sort.Strings(out)
	return out
}
