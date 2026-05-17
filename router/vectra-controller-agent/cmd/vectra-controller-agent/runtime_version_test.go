package main

import (
	"os"
	"strings"
	"testing"

	"vectra-controller-agent/internal/controlplane"
)

func TestApplyControllerRuntimeVersionUsesLdflagValue(t *testing.T) {
	previousVersion := controllerAgentRuntimeVersion
	t.Cleanup(func() {
		controllerAgentRuntimeVersion = previousVersion
	})

	controllerAgentRuntimeVersion = " 0.1.13-r23 "
	inventory := controlplane.RouterInventory{ControllerVersion: "0.1.13-r22"}

	applyControllerRuntimeVersion(&inventory)

	if inventory.ControllerRuntimeVersion != "0.1.13-r23" {
		t.Fatalf("ControllerRuntimeVersion = %q, want %q", inventory.ControllerRuntimeVersion, "0.1.13-r23")
	}
	if inventory.ControllerVersion != "0.1.13-r22" {
		t.Fatalf("ControllerVersion = %q, want installed package metadata to stay unchanged", inventory.ControllerVersion)
	}
}

func TestApplyControllerRuntimeVersionSkipsEmptyLdflagValue(t *testing.T) {
	previousVersion := controllerAgentRuntimeVersion
	t.Cleanup(func() {
		controllerAgentRuntimeVersion = previousVersion
	})

	controllerAgentRuntimeVersion = " "
	inventory := controlplane.RouterInventory{ControllerVersion: "0.1.13-r23"}

	applyControllerRuntimeVersion(&inventory)

	if inventory.ControllerRuntimeVersion != "" {
		t.Fatalf("ControllerRuntimeVersion = %q, want empty when ldflag is absent", inventory.ControllerRuntimeVersion)
	}
}

func TestOpenWrtMakefileInjectsRuntimeVersionLdflag(t *testing.T) {
	makefile, err := os.ReadFile("../../openwrt/Makefile")
	if err != nil {
		t.Fatalf("read OpenWrt package Makefile: %v", err)
	}

	content := string(makefile)
	for _, want := range []string{
		"GO_PKG_LDFLAGS_X:=",
		"main.controllerAgentRuntimeVersion=$(PKG_VERSION)-r$(PKG_RELEASE)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("OpenWrt package Makefile does not contain %q", want)
		}
	}
}
