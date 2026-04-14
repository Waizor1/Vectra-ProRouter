package passwall

import "strings"

func BuildApplyPlan(config DesiredConfig, options ApplyOptions) ApplyPlan {
	plan := ApplyPlan{
		Operations:           []Operation{},
		RequiresRestart:      false,
		RefreshSubscriptions: options.RefreshSubscriptions,
		RefreshRules:         options.RefreshRules,
		PackageInstall:       hasPackageWorkflow(config.AppUpdate),
	}

	globalCommands := renderGlobalCommands(config)
	if len(globalCommands) > 0 {
		plan.Operations = append(plan.Operations, Operation{
			Kind:            "uci_apply",
			Section:         "basicSettings",
			Description:     "Apply global, DNS, logging, app path, and rule settings via UCI.",
			RestartRequired: options.RestartService,
			UCICommands:     globalCommands,
		})
		plan.RequiresRestart = options.RestartService
	}

	nodeCommands := renderNodeCommands(config)
	if len(nodeCommands) > 0 {
		plan.Operations = append(plan.Operations, Operation{
			Kind:            "node_sync",
			Section:         "nodes",
			Description:     "Replace managed node, socks, and shunt sections to match desired configuration.",
			RestartRequired: options.RestartService,
			UCICommands:     nodeCommands,
		})
		plan.RequiresRestart = options.RestartService
	}

	subscriptionCommands := renderSubscriptionCommands(config)
	if len(subscriptionCommands) > 0 {
		operation := Operation{
			Kind:            "subscription_sync",
			Section:         "subscriptions",
			Description:     "Write subscription settings and imported subscription entries to UCI.",
			RestartRequired: options.RestartService,
			UCICommands:     subscriptionCommands,
		}
		if plan.RefreshSubscriptions {
			operation.Commands = []CommandSpec{{
				Name: "lua",
				Args: []string{"/usr/share/passwall2/subscribe.lua", "start", "all"},
			}}
		}
		plan.Operations = append(plan.Operations, operation)
		plan.RequiresRestart = options.RestartService
	}

	if plan.RefreshRules {
		assets := strings.Join(config.RuleManage.EnabledAssets, ",")
		if assets == "" {
			assets = "geoip,geosite"
		}
		plan.Operations = append(plan.Operations, Operation{
			Kind:        "rule_refresh",
			Section:     "ruleManage",
			Description: "Refresh geo assets through the canonical PassWall2 updater.",
			Commands: []CommandSpec{{
				Name: "lua",
				Args: []string{"/usr/share/passwall2/rule_update.lua", "log", assets},
			}},
		})
	}

	if plan.PackageInstall {
		plan.Operations = append(plan.Operations, Operation{
			Kind:        "package_update",
			Section:     "appUpdate",
			Description: "Package lane is requested. Router-side executor only applies binary paths and leaves install orchestration to a higher layer.",
		})
	}

	if plan.RequiresRestart {
		plan.Operations = append(plan.Operations, Operation{
			Kind:        "service_restart",
			Section:     "runtime",
			Description: "Restart PassWall2 to apply committed UCI changes.",
			Commands: []CommandSpec{{
				Name: "/etc/init.d/passwall2",
				Args: []string{"restart"},
			}},
		})
	}

	return plan
}

func hasPackageWorkflow(config AppUpdateConfig) bool {
	return config.TargetVersions.AppVersion != "" ||
		config.TargetVersions.Xray != "" ||
		config.TargetVersions.SingBox != "" ||
		config.TargetVersions.Hysteria != "" ||
		config.TargetVersions.Geoview != ""
}
