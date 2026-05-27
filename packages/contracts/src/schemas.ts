import { z } from "zod";

export const VECTRA_PROTOCOL_VERSION = "2026-04-v1" as const;
export const SYNTHETIC_DEGRADED_MESSAGE =
  "Subscription expired or upstream proxy unavailable";
export const MASKED_SECRET_PLACEHOLDER = "<stored-secret>" as const;

const passthroughPrimitiveSchema = z.union([
  z.string(),
  z.number(),
  z.boolean(),
  z.array(z.string()),
  z.null(),
]);

export const passwallExtrasSchema = z.record(
  z.string(),
  passthroughPrimitiveSchema,
);

export const routerStatusSchema = z.enum([
  "pending",
  "active",
  "offline",
  "direct",
  "rescue",
  "disabled",
]);
export const supportStateSchema = z.enum(["certified", "pilot", "blocked"]);

export const credentialTypeSchema = z.enum(["bootstrap", "agent_token"]);
export const controllerChannelSchema = z.enum(["stable", "beta"]);
export const rescueModeSchema = z.enum(["proxy", "direct"]);
export const routerLogSourceSchema = z.enum([
  "all",
  "controller",
  "passwall",
  "dnsmasq",
  "system",
]);
export const recoveryPhaseSchema = z.enum([
  "idle",
  "monitoring",
  "controller_restart_wait",
  "direct_settle",
  "reboot_wait",
  "post_reboot_check",
  "passwall_retry_wait",
  "operator_attention",
]);
export const routerImportStateSchema = z.enum([
  "awaiting_import",
  "import_review",
  "approved",
  "out_of_sync",
]);
export const incidentStateSchema = z.enum(["open", "resolved"]);
export const incidentTypeSchema = z.enum([
  "proxy_outage",
  "server_unreachable",
  "subscription_degraded",
  "entered_direct_mode",
  "recovered",
]);

export const jobTypeSchema = z.enum([
  "apply_passwall_config",
  "refresh_subscriptions",
  "ensure_passwall_runtime",
  "verify_passwall_routes",
  "inspect_subscriptions",
  "refresh_rules",
  "collect_router_logs",
  "collect_optimization_baseline",
  "run_terminal_command",
  "run_rescue_repair",
  "update_controller",
  "update_passwall_packages",
  "validate_firmware",
  "enter_direct_mode",
  "reconnect",
]);

export const jobStateSchema = z.enum([
  "queued",
  "delivered",
  "running",
  "succeeded",
  "failed",
  "cancelled",
]);

export const jobResultStatusSchema = z.enum(["accepted", "success", "failure"]);
export const severitySchema = z.enum(["info", "warning", "critical"]);
export const artifactTypeSchema = z.enum([
  "controller",
  "passwall_package",
  "passwall_bundle",
  "firmware",
]);
export const secretBlobScopeSchema = z.enum([
  "router_import",
  "desired_revision",
]);

export const passwallNodeProtocolSchema = z.enum([
  "xray",
  "sing-box",
  "shadowsocks-libev",
  "shadowsocks-rust",
  "hysteria2",
  "trojan",
  "vmess",
  "vless",
  "socks",
  "balancing",
  "urltest",
  "shunt",
  "iface",
  "custom",
]);

export const passwallTransportSchema = z.enum([
  "tcp",
  "udp",
  "grpc",
  "ws",
  "quic",
  "xhttp",
  "httpupgrade",
  "custom",
]);

export const dnsStrategySchema = z.enum(["UseIP", "UseIPv4", "UseIPv6"]);
export const remoteDnsProtocolSchema = z.enum([
  "tcp",
  "udp",
  "doh",
  "tls",
  "quic",
  "http3",
]);
export const logLevelSchema = z.enum(["debug", "info", "warning", "error"]);
export const subscriptionFilterModeSchema = z.enum(["0", "1", "2", "3", "4"]);
export const passwallUpdateStrategySchema = z.enum([
  "package-only",
  "package-preferred",
  "expert-fallback",
]);
export const passwallArtifactOriginSchema = z.enum(["vectra", "upstream"]);
export const passwallFallbackPolicySchema = z.enum([
  "package-only",
  "adaptive-component-fallback",
]);
export const passwallJobStrategySchema = z.enum([
  "managed-stack-package-first",
  "xray-built-in-first",
]);
export const passwallUpdateScopeSchema = z.enum([
  "managed-stack",
  "scoped-package",
]);
export const passwallPackageUpdateStatusSchema = z.enum([
  "updated",
  "package-updated",
  "already-current",
  "runtime-updated",
  "runtime-only-converged",
  "storage-blocked",
  "delivery-blocked",
  "failed",
]);
export const passwallPackagePathUsedSchema = z.enum([
  "package",
  "built-in-updater",
  "xray-binary-payload",
  "not-needed",
]);
export const subscriptionPreviewFetchStateSchema = z.enum([
  "ok",
  "disabled",
  "http_error",
  "network_error",
  "parse_error",
]);
export const subscriptionPreviewPayloadModeSchema = z.enum([
  "plain-lines",
  "base64-lines",
  "ssd-json",
  "single-link",
  "unknown",
]);
export const subscriptionPreviewStateSchema = z.enum([
  "fresh",
  "pending",
  "stale",
  "failed",
  "missing",
  "disabled",
]);
export const ruleScheduleModeSchema = z.enum(["daily", "weekly", "interval"]);

export const passwallSocksConfigSchema = z.object({
  id: z.string().min(1),
  enabled: z.boolean().default(true),
  nodeId: z.string().min(1),
  port: z.number().int().min(1).max(65535),
  httpPort: z.number().int().min(0).max(65535).optional(),
  bindLocal: z.boolean().default(true),
  autoswitchBackupNodeIds: z.array(z.string()).default([]),
  extras: passwallExtrasSchema.default({}),
});

export const passwallShuntRuleSchema = z.object({
  id: z.string().min(1),
  label: z.string().min(1),
  outboundNodeId: z.string().optional(),
  domainRules: z.array(z.string()).default([]),
  ipRules: z.array(z.string()).default([]),
  extras: passwallExtrasSchema.default({}),
});

export const passwallNodeSchema = z.object({
  id: z.string().min(1),
  label: z.string().min(1),
  protocol: passwallNodeProtocolSchema,
  enabled: z.boolean().default(true),
  group: z.string().default("default"),
  address: z.string().optional(),
  port: z.number().int().min(0).max(65535).optional(),
  username: z.string().optional(),
  password: z.string().optional(),
  transport: passwallTransportSchema.optional(),
  tls: z.boolean().optional(),
  tags: z.array(z.string()).default([]),
  extras: passwallExtrasSchema.default({}),
});

export const passwallSubscriptionSchema = z.object({
  id: z.string().min(1),
  remark: z.string().min(1),
  url: z.string().min(1),
  enabled: z.boolean().default(true),
  addMode: z.enum(["1", "2"]).default("2"),
  metadata: z
    .object({
      remainingTraffic: z.string().optional(),
      expiresAt: z.string().optional(),
    })
    .default({}),
  extras: passwallExtrasSchema.default({}),
});

export const passwallBasicSettingsSchema = z.object({
  main: z.object({
    mainSwitch: z.boolean().default(true),
    selectedNodeId: z.string().optional(),
    localhostProxy: z.boolean().default(true),
    clientProxy: z.boolean().default(true),
    nodeSocksPort: z.number().int().min(1).max(65535).default(1070),
    nodeSocksBindLocal: z.boolean().default(true),
    socksMainSwitch: z.boolean().default(false),
    extras: passwallExtrasSchema.default({}),
  }),
  dns: z.object({
    directQueryStrategy: dnsStrategySchema.default("UseIP"),
    remoteDnsProtocol: remoteDnsProtocolSchema.default("tcp"),
    remoteDns: z.string().default("1.1.1.1"),
    remoteDnsDoh: z.string().default("https://1.1.1.1/dns-query"),
    remoteDnsClientIp: z.string().optional(),
    remoteDnsDetour: z.enum(["remote", "direct"]).default("remote"),
    remoteFakeDns: z.boolean().default(false),
    remoteDnsQueryStrategy: dnsStrategySchema.default("UseIPv4"),
    dnsHosts: z.array(z.string()).default([]),
    dnsRedirect: z.boolean().default(true),
    extras: passwallExtrasSchema.default({}),
  }),
  log: z.object({
    enableNodeLog: z.boolean().default(true),
    level: logLevelSchema.default("warning"),
    extras: passwallExtrasSchema.default({}),
  }),
  maintenance: z.object({
    backupPaths: z
      .array(z.string())
      .default([
        "/etc/config/passwall2",
        "/etc/config/passwall2_server",
        "/usr/share/passwall2/domains_excluded",
      ]),
    extras: passwallExtrasSchema.default({}),
  }),
  socks: z.array(passwallSocksConfigSchema).default([]),
  shuntRules: z.array(passwallShuntRuleSchema).default([]),
});

export const passwallSubscriptionSettingsSchema = z.object({
  filterKeywordMode: subscriptionFilterModeSchema.default("0"),
  discardList: z.array(z.string()).default([]),
  keepList: z.array(z.string()).default([]),
  typePreferences: z
    .object({
      shadowsocks: z.string().optional(),
      trojan: z.string().optional(),
      vmess: z.string().optional(),
      vless: z.string().optional(),
      hysteria2: z.string().optional(),
    })
    .default({}),
  domainStrategy: z
    .enum(["auto", "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"])
    .default("auto"),
  items: z.array(passwallSubscriptionSchema).default([]),
});

export const passwallAppUpdateSchema = z.object({
  binaryPaths: z
    .object({
      xray: z.string().default("/usr/bin/xray"),
      singBox: z.string().default("/usr/bin/sing-box"),
      hysteria: z.string().default("/usr/bin/hysteria"),
      geoview: z.string().default("/usr/bin/geoview"),
    })
    .default({
      xray: "/usr/bin/xray",
      singBox: "/usr/bin/sing-box",
      hysteria: "/usr/bin/hysteria",
      geoview: "/usr/bin/geoview",
    }),
  updateStrategy: passwallUpdateStrategySchema.default("package-preferred"),
  targetVersions: z
    .object({
      appVersion: z.string().optional(),
      xray: z.string().optional(),
      singBox: z.string().optional(),
      hysteria: z.string().optional(),
      geoview: z.string().optional(),
    })
    .default({}),
  extras: passwallExtrasSchema.default({}),
});

export const passwallRuleManageSchema = z.object({
  geoipUrl: z.string().url(),
  geositeUrl: z.string().url(),
  assetDirectory: z.string().default("/usr/share/v2ray/"),
  autoUpdate: z.boolean().default(false),
  scheduleMode: ruleScheduleModeSchema.default("daily"),
  scheduleDay: z.number().int().min(0).max(7).optional(),
  scheduleHour: z.number().int().min(0).max(23).optional(),
  intervalHours: z.number().int().min(1).max(24).optional(),
  enabledAssets: z
    .array(z.enum(["geoip", "geosite"]))
    .default(["geoip", "geosite"]),
  shuntRules: z.array(passwallShuntRuleSchema).default([]),
  extras: passwallExtrasSchema.default({}),
});

export const passwallDesiredConfigSchema = z.object({
  schemaVersion: z.literal(1).default(1),
  basicSettings: passwallBasicSettingsSchema,
  nodes: z.array(passwallNodeSchema).default([]),
  subscriptions: passwallSubscriptionSettingsSchema,
  appUpdate: passwallAppUpdateSchema,
  ruleManage: passwallRuleManageSchema,
});

export const passwallImportedStateSchema = z.object({
  config: passwallDesiredConfigSchema,
  rawSnapshot: z.record(z.string(), z.unknown()).default({}),
  configDigest: z.string().min(1),
  importedAt: z.string().datetime().optional(),
  source: z
    .enum(["register", "check_in", "operator_reimport"])
    .default("check_in"),
});

export const serviceRuntimeStateSchema = z.enum([
  "running",
  "stopped",
  "degraded",
  "unknown",
]);

export const routerResourcesSchema = z.object({
  memoryTotalMb: z.number().nonnegative().default(0),
  memoryAvailableMb: z.number().nonnegative().default(0),
  swapTotalMb: z.number().nonnegative().default(0),
  swapFreeMb: z.number().nonnegative().default(0),
  overlayFreeMb: z.number().nonnegative().default(0),
  tmpFreeMb: z.number().nonnegative().default(0),
});

export const routerServiceHealthSchema = z.object({
  controller: serviceRuntimeStateSchema.default("unknown"),
  passwall: serviceRuntimeStateSchema.default("unknown"),
  passwallServer: serviceRuntimeStateSchema.default("unknown"),
  dnsmasq: serviceRuntimeStateSchema.default("unknown"),
});

export const routerLastRescueSchema = z.object({
  mode: rescueModeSchema,
  reason: z.string().min(1),
  happenedAt: z.string().datetime(),
});

export const routerReachabilityProbeSchema = z.object({
  id: z.string().min(1).optional(),
  label: z.string().min(1).optional(),
  reachable: z.boolean(),
  checkedAt: z.string().datetime(),
  targetUrl: z.string().url(),
  statusCode: z.number().int().nonnegative().optional(),
  error: z.string().min(1).optional(),
});

export const routerTelegramReachabilitySchema = z.object({
  reachable: z.boolean().optional(),
  checkedAt: z.string().datetime(),
  status: z.enum(["reachable", "partial", "blocked"]).optional(),
  reachableCount: z.number().int().nonnegative().optional(),
  totalCount: z.number().int().nonnegative().optional(),
  targetUrl: z.string().url().optional(),
  statusCode: z.number().int().nonnegative().optional(),
  error: z.string().min(1).optional(),
  checks: z.array(routerReachabilityProbeSchema).default([]),
});

export const routerYoutubeReachabilitySchema = routerTelegramReachabilitySchema;
export const routerInstagramReachabilitySchema =
  routerTelegramReachabilitySchema;

export const routerGroupedReachabilitySchema = z.object({
  reachable: z.boolean().optional(),
  checkedAt: z.string().datetime(),
  status: z.enum(["reachable", "healthy", "partial", "blocked"]).optional(),
  reachableCount: z.number().int().nonnegative().optional(),
  totalCount: z.number().int().nonnegative().optional(),
  targetUrl: z.string().url().optional(),
  statusCode: z.number().int().nonnegative().optional(),
  error: z.string().min(1).optional(),
  checks: z.array(routerReachabilityProbeSchema).default([]),
});

export const routerSafetyEventSchema = z
  .object({
    type: z.string().min(1),
    severity: z.enum(["info", "warning", "critical"]),
    component: z.string().min(1).optional(),
    source: z.string().min(1).optional(),
    message: z.string().min(1),
    observedAt: z.string().datetime(),
    evidence: z.string().min(1).optional(),
  })
  .passthrough();

export const routerInventorySchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  deviceIdentifier: z.string().min(1),
  devicePublicKey: z.string().min(1),
  controllerVersion: z.string().min(1),
  controllerRuntimeVersion: z.string().min(1).optional(),
  hostname: z.string().optional(),
  panelDomain: z.string().optional(),
  model: z.string().min(1),
  boardName: z.string().min(1),
  layoutFamily: z.string().optional(),
  target: z.string().min(1),
  architecture: z.string().min(1),
  openwrtRelease: z.string().min(1),
  openwrtDescription: z.string().optional(),
  passwallEnabled: z.boolean(),
  selectedNodeId: z.string().nullable().optional(),
  selectedNodeLabel: z.string().nullable().optional(),
  nodeCount: z.number().int().nonnegative(),
  subscriptionCount: z.number().int().nonnegative(),
  configDigest: z.string().min(1).nullable().optional(),
  appliedRevisionId: z.string().uuid().nullable().optional(),
  packageVersions: z.record(z.string(), z.string().nullable()).default({}),
  binaryVersions: z.record(z.string(), z.string().nullable()).default({}),
  rulesAssets: z
    .object({
      assetDirectory: z.string().optional(),
      geoipVersion: z.string().nullable().optional(),
      geositeVersion: z.string().nullable().optional(),
      geoipUpdatedAt: z.string().datetime().nullable().optional(),
      geositeUpdatedAt: z.string().datetime().nullable().optional(),
    })
    .default({}),
  resources: routerResourcesSchema,
  serviceHealth: routerServiceHealthSchema,
  lastRescue: routerLastRescueSchema.nullable().optional(),
  panelReachability: routerGroupedReachabilitySchema.optional(),
  ruReachability: routerGroupedReachabilitySchema.optional(),
  foreignReachability: routerGroupedReachabilitySchema.optional(),
  telegramReachability: routerTelegramReachabilitySchema.optional(),
  youtubeReachability: routerYoutubeReachabilitySchema.optional(),
  instagramReachability: routerInstagramReachabilitySchema.optional(),
  safetyEvents: z.array(routerSafetyEventSchema).optional(),
  rawSnapshot: z.record(z.string(), z.unknown()).optional(),
});

export const rescuePolicySchema = z.object({
  healthUrls: z
    .array(z.string().url())
    .min(1)
    .default([
      "https://www.gstatic.com/generate_204",
      "https://cp.cloudflare.com/",
    ]),
  triggerFailureCount: z.number().int().min(1).default(3),
  recoverySuccessCount: z.number().int().min(1).default(2),
  cooldownSeconds: z.number().int().min(30).default(300),
  requireDirectPathSuccess: z.boolean().default(true),
  directModeReason: z.string().min(1).default(SYNTHETIC_DEGRADED_MESSAGE),
  panelOutageThresholdSeconds: z.number().int().min(300).default(3600),
  probeCacheTtlSeconds: z.number().int().min(30).default(300),
  controllerRestartSettleSeconds: z.number().int().min(30).default(90),
  directSettleSeconds: z.number().int().min(15).default(45),
  postRebootSettleSeconds: z.number().int().min(60).default(240),
  passwallWarmupSeconds: z.number().int().min(30).default(75),
  rebootCooldownSeconds: z.number().int().min(300).default(43200),
});

export const updatePolicySchema = z.object({
  controllerChannel: controllerChannelSchema.default("stable"),
  passwallPackageStrategy:
    passwallUpdateStrategySchema.default("package-preferred"),
  allowFirmware: z.boolean().default(false),
  guardedFirmware: z.boolean().default(true),
});

export const routerRegisterRequestSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  inventory: routerInventorySchema,
  passwallImport: passwallImportedStateSchema.optional(),
});

export const routerJobSchema = z.object({
  id: z.string().uuid(),
  type: jobTypeSchema,
  state: jobStateSchema,
  createdAt: z.string().datetime(),
  desiredRevisionId: z.string().uuid().nullable().optional(),
  payload: z.record(z.string(), z.unknown()).default({}),
});

export const packageArtifactPayloadSchema = z.object({
  name: z.string().min(1),
  artifactUrl: z.string().url(),
  sha256: z.string().min(1),
  signatureUrl: z.string().url().nullable().optional(),
  artifactVersion: z.string().min(1),
  source: passwallArtifactOriginSchema.default("vectra"),
  required: z.boolean().default(true),
  downloadSizeBytes: z.number().int().nonnegative().nullable().optional(),
  installedSizeBytes: z.number().int().nonnegative().nullable().optional(),
});

export const updateControllerJobPayloadSchema = z.object({
  channel: controllerChannelSchema.default("stable"),
  packageList: z
    .array(z.string().min(1))
    .min(1)
    .default(["vectra-controller-agent", "luci-app-vectra-controller"]),
  packageArtifacts: z.array(packageArtifactPayloadSchema).default([]),
  artifactUrl: z.string().url().nullable().optional(),
  sha256: z.string().min(1).nullable().optional(),
  signatureUrl: z.string().url().nullable().optional(),
  artifactVersion: z.string().nullable().optional(),
});

export const collectRouterLogsJobPayloadSchema = z.object({
  source: routerLogSourceSchema.default("all"),
  lines: z.number().int().min(50).max(400).default(200),
});

export const collectOptimizationBaselineJobPayloadSchema = z
  .object({
    logSource: routerLogSourceSchema.default("all"),
    logLines: z.number().int().min(50).max(400).default(160),
    includeLogs: z.boolean().default(true),
    includeRoutes: z.boolean().default(true),
  })
  .passthrough();

export const inspectSubscriptionsJobPayloadSchema = z.object({}).passthrough();

export const ensurePasswallRuntimeActionSchema = z.enum([
  "compact_geodata",
  "dnsmasq_full",
]);

export const ensurePasswallRuntimeJobPayloadSchema = z
  .object({
    actions: z
      .array(ensurePasswallRuntimeActionSchema)
      .min(1)
      .max(4)
      .default(["compact_geodata", "dnsmasq_full"]),
    onboardingRunId: z.string().uuid().nullable().optional(),
    onboardingAttempt: z.number().int().nonnegative().nullable().optional(),
    assetDirectory: z.string().trim().min(1).default("/usr/share/v2ray/"),
    geoipUrl: z
      .string()
      .url()
      .default(
        "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat",
      ),
    geositeUrl: z
      .string()
      .url()
      .default(
        "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
      ),
    resourceFloors: z
      .object({
        memoryAvailableMb: z.number().int().nonnegative().default(64),
        overlayFreeMb: z.number().int().nonnegative().default(16),
        tmpFreeMb: z.number().int().nonnegative().default(32),
      })
      .default({
        memoryAvailableMb: 64,
        overlayFreeMb: 16,
        tmpFreeMb: 32,
      }),
  })
  .passthrough();

export const verifyPasswallRoutesJobPayloadSchema = z
  .object({
    expectedPolicy: z
      .enum(["standard-non-hh", "hh-exempt", "subscription-only"])
      .default("standard-non-hh"),
    onboardingRunId: z.string().uuid().nullable().optional(),
    onboardingAttempt: z.number().int().nonnegative().nullable().optional(),
  })
  .passthrough();

export const rescueRepairActionSchema = z.enum([
  "restart_controller",
  "restart_passwall",
  "restart_dnsmasq",
  "refresh_rules",
  "refresh_subscriptions",
  "reconnect_proxy",
]);

export const rescueRepairRequestedBySchema = z.enum([
  "auto_rescue",
  "operator",
  "telegram",
]);

export const runRescueRepairJobPayloadSchema = z
  .object({
    actions: z.array(rescueRepairActionSchema).min(1).max(8),
    timeoutSeconds: z.number().int().min(10).max(180).default(90),
    caseId: z.string().uuid().nullable().optional(),
    reason: z.string().trim().max(500).nullable().optional(),
    requestedBy: rescueRepairRequestedBySchema.default("auto_rescue"),
  })
  .strict();

export const runTerminalCommandJobPayloadSchema = z.object({
  command: z.string().trim().min(1).max(8000),
  timeoutSeconds: z.number().int().min(5).max(120).default(30),
  purpose: z
    .enum([
      "controller-self-update",
      "controller-self-update-compat",
      "passwall-clear-ipsets",
      "router-reboot",
      "router-hostname-update",
    ])
    .optional(),
  artifactVersion: z.string().nullable().optional(),
  hostname: z.string().min(1).max(63).nullable().optional(),
  onboardingRunId: z.string().uuid().nullable().optional(),
  onboardingAttempt: z.number().int().nonnegative().nullable().optional(),
});

export const updatePasswallPackagesJobPayloadSchema = z.object({
  channel: controllerChannelSchema.default("stable"),
  packageList: z.array(z.string().min(1)).min(1),
  packageArtifacts: z.array(packageArtifactPayloadSchema).min(1),
  targetVersion: z.string().min(1),
  strategy: passwallJobStrategySchema.default("managed-stack-package-first"),
  packageTargetVersion: z.string().min(1).nullable().optional(),
  runtimeTargetVersion: z.string().min(1).nullable().optional(),
  targetReleaseTag: z.string().min(1).nullable().optional(),
  originSource: passwallArtifactOriginSchema.default("vectra"),
  fallbackPolicy: passwallFallbackPolicySchema.default(
    "adaptive-component-fallback",
  ),
  updateScope: passwallUpdateScopeSchema.default("managed-stack"),
  artifactUrl: z.string().url().nullable().optional(),
  sha256: z.string().min(1).nullable().optional(),
  signatureUrl: z.string().url().nullable().optional(),
  artifactVersion: z.string().nullable().optional(),
});

export const passwallPackageUpdateResultEntrySchema = z.object({
  package: z.string().min(1),
  targetVersion: z.string().min(1),
  packageTargetVersion: z.string().min(1).nullable().optional(),
  runtimeTargetVersion: z.string().min(1).nullable().optional(),
  status: passwallPackageUpdateStatusSchema,
  pathUsed: passwallPackagePathUsedSchema,
  packageVersionBefore: z.string().nullable().optional(),
  packageVersionAfter: z.string().nullable().optional(),
  runtimeVersionBefore: z.string().nullable().optional(),
  runtimeVersionAfter: z.string().nullable().optional(),
  driftDetected: z.boolean().default(false),
  error: z.string().nullable().optional(),
});

export const passwallPackageUpdateResultPayloadSchema = z
  .object({
    packageList: z.array(z.string().min(1)).min(1),
    targetVersion: z.string().min(1),
    strategy: passwallJobStrategySchema.default("managed-stack-package-first"),
    packageTargetVersion: z.string().min(1).nullable().optional(),
    runtimeTargetVersion: z.string().min(1).nullable().optional(),
    targetReleaseTag: z.string().nullable().optional(),
    originSource: passwallArtifactOriginSchema,
    fallbackPolicy: passwallFallbackPolicySchema,
    updateScope: passwallUpdateScopeSchema,
    packageResults: z.array(passwallPackageUpdateResultEntrySchema).default([]),
    driftDetected: z.boolean().default(false),
    deliveryBlocked: z.boolean().default(false),
    deliveryBlockedReason: z.string().nullable().optional(),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const validateFirmwareJobPayloadSchema = z.object({
  manifestId: z.string().uuid().nullable().optional(),
  channel: controllerChannelSchema.default("stable"),
  boardName: z.string().min(1).nullable().optional(),
  target: z.string().min(1).nullable().optional(),
  architecture: z.string().min(1).nullable().optional(),
  layoutFamily: z.string().min(1).nullable().optional(),
  artifactUrl: z.string().url(),
  sha256: z.string().min(1),
  signatureUrl: z.string().url().nullable().optional(),
  artifactVersion: z.string().min(1).nullable().optional(),
  validationCommand: z
    .string()
    .min(1)
    .default("sysupgrade -T /tmp/firmware.bin"),
});

export const routerLogSnapshotSchema = z.object({
  id: routerLogSourceSchema,
  label: z.string().min(1),
  command: z.string().min(1),
  content: z.string(),
  truncated: z.boolean().default(false),
});

export const routerLogResultPayloadSchema = z
  .object({
    source: routerLogSourceSchema,
    requestedLines: z.number().int().min(50).max(400),
    collectedAt: z.string().datetime(),
    snapshots: z.array(routerLogSnapshotSchema).default([]),
    stdout: z.string().nullable().optional(),
    stderr: z.string().nullable().optional(),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const routerTerminalResultPayloadSchema = z
  .object({
    command: z.string().min(1),
    timeoutSeconds: z.number().int().min(5).max(120),
    startedAt: z.string().datetime(),
    completedAt: z.string().datetime(),
    durationMs: z.number().int().min(0).nullable().optional(),
    exitCode: z.number().int().nullable().optional(),
    timedOut: z.boolean().default(false),
    stdout: z.string().nullable().optional(),
    stderr: z.string().nullable().optional(),
    stdoutTruncated: z.boolean().default(false),
    stderrTruncated: z.boolean().default(false),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const verifyPasswallRouteSlotResultSchema = z
  .object({
    slotId: z.string().min(1),
    expected: z.string().nullable().optional(),
    ruleId: z.string().nullable().optional(),
    ruleLabel: z.string().nullable().optional(),
    boundNodeId: z.string().nullable().optional(),
    boundNodeLabel: z.string().nullable().optional(),
    bindingOk: z.boolean().default(false),
    ruleExtrasOk: z.boolean().default(false),
    nodeExtrasOk: z.boolean().default(false),
    smokeOk: z.boolean().default(false),
    statusCode: z.number().int().nullable().optional(),
    command: z.string().nullable().optional(),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const verifyPasswallRoutesResultPayloadSchema = z
  .object({
    verifierVersion: z.string().min(1),
    verifiedAt: z.string().datetime(),
    ok: z.boolean(),
    exempt: z.boolean().default(false),
    selectedNodeId: z.string().nullable().optional(),
    selectedNodeLabel: z.string().nullable().optional(),
    slots: z.array(verifyPasswallRouteSlotResultSchema).default([]),
    services: z
      .object({
        controller: z.string().nullable().optional(),
        passwall: z.string().nullable().optional(),
        passwallServer: z.string().nullable().optional(),
        dnsmasq: z.string().nullable().optional(),
      })
      .default({}),
    resources: z.record(z.string(), z.unknown()).default({}),
    packageVersions: z.record(z.string(), z.string()).default({}),
    binaryVersions: z.record(z.string(), z.string()).default({}),
    errors: z.array(z.string()).default([]),
  })
  .passthrough();

export const optimizationBaselineProcessSchema = z.object({
  pid: z.number().int().positive(),
  role: z.string().min(1),
  command: z.string().min(1),
  vmSizeKb: z.number().int().nonnegative().nullable().optional(),
  vmRssKb: z.number().int().nonnegative().nullable().optional(),
  threads: z.number().int().nonnegative().nullable().optional(),
});

export const optimizationBaselineConntrackSchema = z.object({
  count: z.number().int().nonnegative().nullable().optional(),
  max: z.number().int().nonnegative().nullable().optional(),
});

export const optimizationBaselineResultPayloadSchema = z
  .object({
    baselineVersion: z.string().min(1),
    collectedAt: z.string().datetime(),
    ok: z.boolean(),
    selectedNodeId: z.string().nullable().optional(),
    selectedNodeLabel: z.string().nullable().optional(),
    passwallEnabled: z.boolean().default(false),
    resources: z.record(z.string(), z.unknown()).default({}),
    serviceHealth: z
      .object({
        controller: z.string().nullable().optional(),
        passwall: z.string().nullable().optional(),
        passwallServer: z.string().nullable().optional(),
        dnsmasq: z.string().nullable().optional(),
      })
      .default({}),
    safetyEvents: z.array(z.record(z.string(), z.unknown())).default([]),
    packageVersions: z.record(z.string(), z.string()).default({}),
    binaryVersions: z.record(z.string(), z.string()).default({}),
    processes: z.array(optimizationBaselineProcessSchema).default([]),
    conntrack: optimizationBaselineConntrackSchema.default({}),
    logs: routerLogResultPayloadSchema.nullable().optional(),
    routeVerification: verifyPasswallRoutesResultPayloadSchema
      .nullable()
      .optional(),
    warnings: z.array(z.string()).default([]),
    errors: z.array(z.string()).default([]),
  })
  .passthrough();

export const ensurePasswallRuntimeActionResultSchema = z
  .object({
    action: ensurePasswallRuntimeActionSchema,
    status: z.enum(["success", "skipped", "failure"]),
    command: z.string().nullable().optional(),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const ensurePasswallRuntimeResultPayloadSchema = z
  .object({
    ok: z.boolean(),
    repaired: z.boolean().default(false),
    checkedAt: z.string().datetime(),
    actions: z.array(ensurePasswallRuntimeActionResultSchema).default([]),
    commands: z.array(z.string()).default([]),
    services: z
      .object({
        controller: z.string().nullable().optional(),
        passwall: z.string().nullable().optional(),
        passwallServer: z.string().nullable().optional(),
        dnsmasq: z.string().nullable().optional(),
      })
      .default({}),
    resources: z.record(z.string(), z.unknown()).default({}),
    rulesAssets: z.record(z.string(), z.unknown()).default({}),
    packageVersions: z.record(z.string(), z.string()).default({}),
    binaryVersions: z.record(z.string(), z.string()).default({}),
    errors: z.array(z.string()).default([]),
  })
  .passthrough();

export const rescueRepairServiceHealthSchema = z.object({
  controller: z.string().nullable().optional(),
  passwall: z.string().nullable().optional(),
  passwallServer: z.string().nullable().optional(),
  dnsmasq: z.string().nullable().optional(),
});

export const rescueRepairHealthSnapshotSchema = z
  .object({
    capturedAt: z.string().datetime(),
    passwallEnabled: z.boolean().nullable().optional(),
    rescueMode: rescueModeSchema.nullable().optional(),
    selectedNodeId: z.string().nullable().optional(),
    selectedNodeLabel: z.string().nullable().optional(),
    serviceHealth: rescueRepairServiceHealthSchema.default({}),
    serverReachable: z.boolean().nullable().optional(),
    publicReachable: z.boolean().nullable().optional(),
  })
  .passthrough();

export const rescueRepairActionResultStatusSchema = z.enum([
  "success",
  "failure",
  "scheduled",
  "skipped",
  "unsupported",
]);

export const rescueRepairActionResultSchema = z
  .object({
    action: rescueRepairActionSchema,
    status: rescueRepairActionResultStatusSchema,
    command: z.string().min(1).nullable().optional(),
    startedAt: z.string().datetime(),
    completedAt: z.string().datetime(),
    durationMs: z.number().int().min(0),
    stdout: z.string().max(4000).nullable().optional(),
    stderr: z.string().max(4000).nullable().optional(),
    error: z.string().max(1000).nullable().optional(),
  })
  .passthrough();

export const rescueRepairResultPayloadSchema = z
  .object({
    caseId: z.string().uuid().nullable().optional(),
    requestedBy: rescueRepairRequestedBySchema,
    reason: z.string().nullable().optional(),
    actions: z.array(rescueRepairActionSchema).min(1),
    timeoutSeconds: z.number().int().min(10).max(180),
    startedAt: z.string().datetime(),
    completedAt: z.string().datetime(),
    before: rescueRepairHealthSnapshotSchema,
    after: rescueRepairHealthSnapshotSchema,
    results: z.array(rescueRepairActionResultSchema),
    recoveredProxy: z.boolean().default(false),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const subscriptionPreviewFingerprintSchema = z.object({
  fingerprint: z.string().min(1),
});

export const subscriptionPreviewEntrySchema = z.object({
  subscriptionId: z.string().min(1),
  subscriptionKey: z.string().min(1),
  remark: z.string().min(1),
  urlHash: z.string().min(1),
  enabled: z.boolean().default(true),
  accessMode: z.enum(["auto", "direct", "proxy"]).default("auto"),
  userAgent: z.string().min(1).nullable().optional(),
  fetchState: subscriptionPreviewFetchStateSchema,
  httpStatus: z.number().int().nullable().optional(),
  payloadMode: subscriptionPreviewPayloadModeSchema.default("unknown"),
  payloadNodeCount: z.number().int().nonnegative().nullable().optional(),
  resolvedPayloadNodeCount: z
    .number()
    .int()
    .nonnegative()
    .nullable()
    .optional(),
  payloadFingerprints: z
    .array(subscriptionPreviewFingerprintSchema)
    .default([]),
  checkedAt: z.string().datetime(),
});

export const subscriptionInspectResultPayloadSchema = z
  .object({
    checkedAt: z.string().datetime(),
    subscriptionDigest: z.string().min(1),
    entries: z.array(subscriptionPreviewEntrySchema).default([]),
    error: z.string().nullable().optional(),
  })
  .passthrough();

export const desiredRevisionSummarySchema = z.object({
  id: z.string().uuid(),
  revisionNumber: z.number().int().nonnegative(),
  status: z.string().min(1),
  origin: z.string().min(1).default("operator_draft"),
  configDigest: z.string().nullable().optional(),
  config: passwallDesiredConfigSchema,
  impact: z.object({
    changedSections: z.array(z.string()),
    requiresRestart: z.boolean(),
    refreshSubscriptions: z.boolean(),
    refreshRules: z.boolean(),
    packageInstall: z.boolean(),
    firmwareValidation: z.boolean(),
  }),
});

export const routerConfigSyncStateSchema = z.object({
  importState: routerImportStateSchema,
  pendingImportRevisionId: z.string().uuid().nullable().optional(),
  activeRevisionId: z.string().uuid().nullable().optional(),
  lastAppliedRevisionId: z.string().uuid().nullable().optional(),
  lastConfigDigest: z.string().nullable().optional(),
  requestImport: z.boolean().default(false),
});

export const routerRegisterResponseSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  routerId: z.string().uuid(),
  status: routerStatusSchema,
  issuedToken: z.string().min(1),
  pollingIntervalSeconds: z.number().int().min(15),
  pendingApproval: z.boolean(),
  configSyncState: routerConfigSyncStateSchema,
  rescuePolicy: rescuePolicySchema,
  updatePolicy: updatePolicySchema,
  operatorMessage: z.string().nullable(),
});

export const routerCheckInRequestSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  routerId: z.string().uuid(),
  inventory: routerInventorySchema,
  passwallImport: passwallImportedStateSchema.optional(),
  health: z.object({
    currentMode: rescueModeSchema.default("proxy"),
    publicConnectivityFailures: z.number().int().min(0).default(0),
    directConnectivitySuccesses: z.number().int().min(0).default(0),
    proxyConnectivitySuccesses: z.number().int().min(0).default(0),
    serverReachable: z.boolean().default(true),
    recoveryPhase: recoveryPhaseSchema.default("idle"),
    lastRecoveryAction: z.string().nullable().optional(),
    awaitingOperator: z.boolean().default(false),
  }),
});

export const routerCheckInResponseSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  routerId: z.string().uuid(),
  status: routerStatusSchema,
  pollingIntervalSeconds: z.number().int().min(15),
  configSyncState: routerConfigSyncStateSchema,
  rescuePolicy: rescuePolicySchema,
  updatePolicy: updatePolicySchema,
  desiredRevision: desiredRevisionSummarySchema.nullable(),
  jobs: z.array(routerJobSchema),
  operatorMessage: z.string().nullable(),
});

export const incidentTransitionSchema = z.object({
  type: incidentTypeSchema,
  state: incidentStateSchema,
  reason: z.string().min(1),
  happenedAt: z.string().datetime().optional(),
  metadata: z.record(z.string(), z.unknown()).default({}),
});

export const jobResultRequestSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  routerId: z.string().uuid(),
  jobId: z.string().uuid(),
  status: jobResultStatusSchema,
  appliedRevisionId: z.string().uuid().nullable().optional(),
  configDigest: z.string().min(1).nullable().optional(),
  stdout: z.string().max(16000).optional(),
  stderr: z.string().max(16000).optional(),
  incidentTransitions: z.array(incidentTransitionSchema).default([]),
  result: z.record(z.string(), z.unknown()).default({}),
});

export const jobResultResponseSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  acknowledged: z.boolean(),
});

export const artifactMetadataSchema = z.object({
  id: z.string().uuid(),
  type: artifactTypeSchema,
  channel: controllerChannelSchema,
  name: z.string().min(1),
  version: z.string().min(1),
  architecture: z.string().nullable(),
  boardName: z.string().nullable(),
  layoutFamily: z.string().nullable(),
  downloadUrl: z.string().url(),
  checksumSha256: z.string().min(1),
  signatureUrl: z.string().url().nullable(),
  metadata: z.record(z.string(), z.unknown()).default({}),
});

export const firmwareManifestSchema = z.object({
  id: z.string().uuid(),
  boardName: z.string().min(1),
  target: z.string().min(1),
  architecture: z.string().min(1),
  layoutFamily: z.string().min(1),
  channel: controllerChannelSchema,
  version: z.string().min(1),
  validationCommand: z.string().min(1),
  artifact: artifactMetadataSchema,
  rolloutPolicy: z.record(z.string(), z.unknown()).default({}),
});

export const rescueEvaluationInputSchema = z.object({
  policy: rescuePolicySchema,
  currentMode: rescueModeSchema,
  failedProxyChecks: z.number().int().min(0),
  successfulDirectChecks: z.number().int().min(0),
  successfulProxyChecks: z.number().int().min(0),
  lastTransitionAt: z.date().nullable().optional(),
  now: z.date().default(() => new Date()),
});

export const rescueEvaluationResultSchema = z.object({
  nextMode: rescueModeSchema,
  shouldTransition: z.boolean(),
  reason: z.string().nullable(),
});

export type PasswallDesiredConfig = z.infer<typeof passwallDesiredConfigSchema>;
export type PasswallImportedState = z.infer<typeof passwallImportedStateSchema>;
export type PasswallNode = z.infer<typeof passwallNodeSchema>;
export type PasswallSubscription = z.infer<typeof passwallSubscriptionSchema>;
export type RouterReachabilityProbe = z.infer<
  typeof routerReachabilityProbeSchema
>;
export type RouterTelegramReachability = z.infer<
  typeof routerTelegramReachabilitySchema
>;
export type RouterYoutubeReachability = z.infer<
  typeof routerYoutubeReachabilitySchema
>;
export type RouterInstagramReachability = z.infer<
  typeof routerInstagramReachabilitySchema
>;
export type RouterSafetyEvent = z.infer<typeof routerSafetyEventSchema>;
export type RouterInventory = z.infer<typeof routerInventorySchema>;
export type RescuePolicy = z.infer<typeof rescuePolicySchema>;
export type UpdatePolicy = z.infer<typeof updatePolicySchema>;
export type RouterJob = z.infer<typeof routerJobSchema>;
export type RouterLogSource = z.infer<typeof routerLogSourceSchema>;
export type PackageArtifactPayload = z.infer<
  typeof packageArtifactPayloadSchema
>;
export type UpdateControllerJobPayload = z.infer<
  typeof updateControllerJobPayloadSchema
>;
export type CollectRouterLogsJobPayload = z.infer<
  typeof collectRouterLogsJobPayloadSchema
>;
export type CollectOptimizationBaselineJobPayload = z.infer<
  typeof collectOptimizationBaselineJobPayloadSchema
>;
export type InspectSubscriptionsJobPayload = z.infer<
  typeof inspectSubscriptionsJobPayloadSchema
>;
export type RunTerminalCommandJobPayload = z.infer<
  typeof runTerminalCommandJobPayloadSchema
>;
export type RescueRepairAction = z.infer<typeof rescueRepairActionSchema>;
export type RunRescueRepairJobPayload = z.infer<
  typeof runRescueRepairJobPayloadSchema
>;
export type UpdatePasswallPackagesJobPayload = z.infer<
  typeof updatePasswallPackagesJobPayloadSchema
>;
export type ValidateFirmwareJobPayload = z.infer<
  typeof validateFirmwareJobPayloadSchema
>;
export type RouterLogSnapshot = z.infer<typeof routerLogSnapshotSchema>;
export type RouterLogResultPayload = z.infer<
  typeof routerLogResultPayloadSchema
>;
export type OptimizationBaselineResultPayload = z.infer<
  typeof optimizationBaselineResultPayloadSchema
>;
export type RouterTerminalResultPayload = z.infer<
  typeof routerTerminalResultPayloadSchema
>;
export type RescueRepairResultPayload = z.infer<
  typeof rescueRepairResultPayloadSchema
>;
export type SubscriptionPreviewEntry = z.infer<
  typeof subscriptionPreviewEntrySchema
>;
export type SubscriptionInspectResultPayload = z.infer<
  typeof subscriptionInspectResultPayloadSchema
>;
export type FirmwareManifest = z.infer<typeof firmwareManifestSchema>;
export type RouterConfigSyncState = z.infer<typeof routerConfigSyncStateSchema>;
export type SupportState = z.infer<typeof supportStateSchema>;
