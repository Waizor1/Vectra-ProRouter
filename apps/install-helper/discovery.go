package main

import (
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/jackpal/gateway"
	"golang.org/x/crypto/ssh"
)

type systemGatewayResolver struct{}

func (systemGatewayResolver) DefaultGatewayIP() (string, error) {
	ip, err := gateway.DiscoverGateway()
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

type sshHostFingerprinter struct{}

func (sshHostFingerprinter) ProbeFingerprint(targetIP string) (string, bool, error) {
	var observedFingerprint string
	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.Password("invalid-password")},
		HostKeyCallback: captureFingerprintCallback(&observedFingerprint),
		Timeout:         3 * time.Second,
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(targetIP, "22"), config)
	if client != nil {
		_ = client.Close()
	}

	if observedFingerprint != "" {
		return observedFingerprint, true, nil
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return "", false, nil
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "connection refused") {
		return "", false, nil
	}
	return "", false, err
}

func candidateIPs(resolver gatewayResolver) []scanCandidate {
	candidates := map[string]scanCandidate{
		"192.168.99.1": {IP: "192.168.99.1", Source: "known_ip"},
		"192.168.98.1": {IP: "192.168.98.1", Source: "known_ip"},
		"192.168.1.1":  {IP: "192.168.1.1", Source: "known_ip"},
	}

	if resolver != nil {
		if gatewayIP, err := resolver.DefaultGatewayIP(); err == nil && gatewayIP != "" {
			candidate := scanCandidate{
				IP:          gatewayIP,
				Source:      "default_gateway",
				Recommended: true,
			}
			if _, ok := candidates[gatewayIP]; ok {
				candidate.Recommended = true
			}
			candidates[gatewayIP] = candidate
		}
	}

	list := make([]scanCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		list = append(list, candidate)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Recommended != list[j].Recommended {
			return list[i].Recommended
		}
		return list[i].IP < list[j].IP
	})
	return list
}

func discoverCandidates(
	resolver gatewayResolver,
	fingerprinter hostFingerprinter,
	state *helperStateStore,
) scanResponse {
	candidates := candidateIPs(resolver)
	response := scanResponse{
		Candidates: make([]scanCandidate, 0, len(candidates)),
	}

	for _, candidate := range candidates {
		next := candidate
		fingerprint, reachable, err := fingerprinter.ProbeFingerprint(candidate.IP)
		if err == nil {
			next.SSHReachable = reachable
		}
		if fingerprint != "" {
			next.HostKeyFingerprint = fingerprint
			trusted := state.trustedFingerprint(candidate.IP)
			switch {
			case trusted == "":
				next.FingerprintState = "new"
			case trusted == fingerprint:
				next.FingerprintState = "trusted"
			default:
				next.FingerprintState = "mismatch"
				next.Recommended = false
			}
		} else {
			next.FingerprintState = "unknown"
		}
		response.Candidates = append(response.Candidates, next)
	}

	for _, candidate := range response.Candidates {
		if candidate.Recommended && candidate.SSHReachable && candidate.FingerprintState != "mismatch" {
			response.RecommendedTargetIP = candidate.IP
			return response
		}
	}
	for _, candidate := range response.Candidates {
		if candidate.SSHReachable && candidate.FingerprintState != "mismatch" {
			response.RecommendedTargetIP = candidate.IP
			return response
		}
	}
	return response
}

func captureFingerprintCallback(out *string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if out != nil {
			*out = ssh.FingerprintSHA256(key)
		}
		return nil
	}
}
