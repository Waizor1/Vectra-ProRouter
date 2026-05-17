package main

import (
	"strings"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/inventory"
)

var controllerAgentRuntimeVersion = ""

func collectInventoryWithRuntimeVersion(base controlplane.RouterInventory) controlplane.RouterInventory {
	collected := inventory.NewCollector().Collect(base)
	applyControllerRuntimeVersion(&collected)
	return collected
}

func applyControllerRuntimeVersion(inventory *controlplane.RouterInventory) {
	version := strings.TrimSpace(controllerAgentRuntimeVersion)
	if version == "" || inventory == nil {
		return
	}

	inventory.ControllerRuntimeVersion = version
}
