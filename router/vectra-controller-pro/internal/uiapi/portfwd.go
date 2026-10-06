package uiapi

import "vectra-controller-pro/internal/portfwd"

// BuildPortForwards answers `port_forwards` from what the router has,
// whether its WAN is behind CGNAT and whether «past the VPN» is in effect
// (portfwd.DirectActive). Every list is [] rather than null when empty.
func BuildPortForwards(st portfwd.State, cgnat bool, directActive *bool) PortForwards {
	out := PortForwards{Rules: []PortForward{}, Devices: []PortForwardDevice{}, CGNAT: cgnat, DirectActive: directActive, Max: portfwd.MaxRules}
	for _, r := range st.Rules {
		out.Rules = append(out.Rules, PortForward{ID: r.ID, Preset: r.Preset, DestIP: r.DestIP, DeviceName: st.DeviceName(r.DestIP),
			Port: r.Port, Proto: r.Proto, Direct: r.Direct, Enabled: r.Enabled})
	}
	for _, d := range st.Devices {
		out.Devices = append(out.Devices, PortForwardDevice{Name: d.Name, IP: d.IP})
	}
	return out
}
