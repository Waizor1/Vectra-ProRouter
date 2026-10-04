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
  // xray-direct engine (Vectra Controller Pro) job types. Additive; only
  // delivered to routers whose engineMode is "xray-direct".
  "apply_xray_config",
  "reload_xray_outbound",
  "refresh_xray_subscriptions",
  "update_xray_assets",
  "connect_router_action",
]);

// engineMode discriminates which router controller owns the device. The 18
// live routers run the legacy PassWall2 path ("passwall", the default); the
// standalone Vectra Controller Pro reports "xray-direct".
export const engineModeSchema = z.enum(["passwall", "xray-direct"]);

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
  // Standalone Xray binary shipped alongside the Vectra Controller Pro feed.
  "xray_binary",
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
  // Every sibling section carries its unmodelled UCI options through here;
  // this one did not, so zod stripped them out of the imported config and the
  // agent's apply — which deletes and rewrites the section — had nothing to
  // restore them from. Whatever PassWall keeps in global_subscribe beyond the
  // fields above was therefore discarded on every apply.
  extras: passwallExtrasSchema.default({}),
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

// ---------------------------------------------------------------------------
// xray-direct operator config (Vectra Controller Pro engine).
//
// This mirrors the canonical Go struct at
// router/vectra-controller-pro/internal/config/types.go (the `Config` struct,
// schema version 1) FIELD FOR FIELD. Keep the two in lock-step.
//
// Two hard constraints drive the shape below:
//
//  1. `internal/config/config.go` decodes with `DisallowUnknownFields()`. Any
//     key the Go struct does not declare is a FATAL decode error on the router,
//     not a warning. Every object here is therefore `.strict()` rather than
//     `.passthrough()`: an unknown key is rejected at the panel boundary, where
//     an operator sees a validation error, instead of on the router, where it
//     would fail the apply job.
//
//  2. This is the OPERATOR config only. Since the "consume provider JSON"
//     pivot the panel no longer authors the proxy config at all — no
//     outbounds/nodes, no dns, no routing, no policy/stats/api. The router
//     fetches the provider's Xray document itself from the subscription URL and
//     adopts it byte-for-byte. Routing a provider document through this panel
//     would corrupt it (stableStringify key sorting, jsonb key reordering,
//     value masking, zod re-serialization), so the ONLY thing that travels is
//     the four load-bearing blocks the controller itself needs.
//
// Go `omitempty` optionals become `.optional()`. Range checks mirror
// `internal/config/validate.go` so the panel cannot persist a config the
// router would refuse.
// ---------------------------------------------------------------------------

// destOverride values the router accepts. `internal/config/validate.go`
// explicitly REJECTS "fakedns": the provider document carries no fakedns block,
// so sniffing into a pool that does not exist is a hard Xray start failure.
export const XRAY_DEST_OVERRIDE_VALUES = ["http", "tls", "quic"] as const;

const xraySniffingSchema = z
  .object({
    enabled: z.boolean(),
    destOverride: z.array(z.enum(XRAY_DEST_OVERRIDE_VALUES)).optional(),
    domainsExcluded: z.array(z.string()).optional(),
    metadataOnly: z.boolean().optional(),
    routeOnly: z.boolean().optional(),
  })
  .strict();

const xrayInstanceSchema = z
  .object({
    name: z.string().optional(),
    logLevel: z.enum(["debug", "info", "warning", "error", "none"]).optional(),
  })
  .strict();

// Mirrors validate.go: initialMs > 0, factor >= 1, maxMs >= initialMs.
const xrayBackoffSchema = z
  .object({
    initialMs: z.number().int().positive(),
    factor: z.number().min(1),
    maxMs: z.number().int().positive(),
    reset: z.string().optional(),
  })
  .strict()
  .refine((backoff) => backoff.maxMs >= backoff.initialMs, {
    message: "restartBackoff.maxMs must be >= restartBackoff.initialMs",
    path: ["maxMs"],
  });

const xrayProcessSchema = z
  .object({
    // validate.go: required, non-empty.
    xrayBinary: z.string().min(1),
    workDir: z.string().min(1),
    configFile: z.string().optional(),
    logDir: z.string().optional(),
    memorySoftMiB: z.number().int().nonnegative().optional(),
    memoryHardMiB: z.number().int().nonnegative().optional(),
    // validate.go: -1000..1000. No omitempty on the Go side, so always emitted.
    oomScoreAdj: z.number().int().min(-1000).max(1000),
    niceLevel: z.number().int().min(-20).max(19).optional(),
    gomaxprocs: z.number().int().positive().optional(),
    restartBackoff: xrayBackoffSchema,
    reloadGrace: z.string().optional(),
    startTimeout: z.string().optional(),
  })
  .strict();

// The ONE inbound the controller splices into the provider document. The
// provider's own socks/http inbounds are dropped by the splice (they carry no
// "listen" key and would bind 0.0.0.0, i.e. an open proxy on the LAN).
const xrayTproxyInboundSchema = z
  .object({
    listenIP: z.string(),
    port: z.number().int().min(1).max(65535),
    fwmark: z.number().int().optional(),
    udpEnabled: z.boolean(),
    sniffing: xraySniffingSchema,
    tag: z.string().optional(),
    // Fail LAN-client traffic CLOSED at the firewall when the proxy is down.
    // Off by default; the router's own control-plane/DNS traffic is unaffected.
    killSwitch: z.boolean().optional(),
  })
  .strict();

// validate.go treats a missing tproxy inbound as an error ("it is the only
// inbound the controller splices into the provider config"), so it is required
// here rather than optional.
const xrayInboundsSchema = z
  .object({
    tproxy: xrayTproxyInboundSchema,
  })
  .strict();

const xrayGeoFileSchema = z
  .object({
    filename: z.string().min(1),
    url: z.string(),
    sha256: z.string().optional(),
  })
  .strict();

const xrayGeoSchema = z
  .object({
    assetDir: z.string().min(1),
    geoipUrl: z.string(),
    geositeUrl: z.string(),
    updateSchedule: z.string().optional(),
    updateOnStart: z.boolean(),
    extraAssets: z.array(xrayGeoFileSchema).optional(),
  })
  .strict();

export const XRAY_SUBSCRIPTION_MODES = ["json", "link-list"] as const;

// The upstream provider feed. Fetching it is the ROUTER's job; only the
// coordinates travel through the panel.
//
// `url` is deliberately a bare string, NOT a `.url()` or `https://` check: at
// rest the panel stores this config with the URL replaced by
// MASKED_SECRET_PLACEHOLDER (cleartext lives only in the encrypted secret
// blob), and the masked row still has to re-parse through this schema on read.
// The https:// requirement from validate.go is enforced at the authoring
// boundary instead, where cleartext is available.
const xraySubscriptionSchema = z
  .object({
    // validate.go: required, non-empty.
    id: z.string().min(1),
    remark: z.string().optional(),
    url: z.string(),
    enabled: z.boolean(),
    // Selects WHICH payload the provider returns. "v2rayNG/1.9.5" yields the
    // complete JSON config array; "passwall2/*", "Xray/*" and "sing-box/*"
    // yield the degraded base64 vless:// link list. A "Happ…" agent that is not
    // the real client's "Happ/<version>/<OS>/<build>" gets the customer's
    // device deleted by the provider's sweep — the router refuses to send one.
    userAgent: z.string().optional(),
    group: z.string().optional(),
    headers: z.record(z.string(), z.string()).optional(),
    mode: z.enum(XRAY_SUBSCRIPTION_MODES).optional(),
    // Which document of the provider array to adopt. entryRemark wins when set
    // (matched exactly against the entry's "remarks"); otherwise entryIndex,
    // which defaults to 0 — the first profile.
    entryIndex: z.number().int().nonnegative().optional(),
    entryRemark: z.string().optional(),
    maxBytes: z.number().int().nonnegative().optional(),
    // Disables the refuse-on-`"allowInsecure":true` guard. Default false: a
    // provider document that disables TLS verification is refused outright.
    allowInsecureTls: z.boolean().optional(),
  })
  .strict();

// The operator's per-router UI policy. `lock` pins the router's own web UI to
// the owner's simple view (the router enforces it; ADR-0006 customer routers).
//
// Emitted ONLY while the lock is on: a vctl that predates this block decodes
// with DisallowUnknownFields and would refuse `ui` outright, so "unlocked" is
// represented by the block's absence, never by `{ lock: false }`.
const xrayUiSchema = z
  .object({
    lock: z.boolean(),
  })
  .strict();

export const xrayDesiredConfigSchema = z
  .object({
    schema: z.literal(1),
    instance: xrayInstanceSchema,
    process: xrayProcessSchema,
    inbounds: xrayInboundsSchema,
    geo: xrayGeoSchema,
    subscriptions: z.array(xraySubscriptionSchema).optional(),
    ui: xrayUiSchema.optional(),
  })
  .strict();

// Size caps on what a router sends. Register answers anonymous callers, so no
// free-form field a router reports may be unbounded. Each cap is measured on
// the serialized JSON and sits well above the largest the live fleet has sent
// (2026-10-03: an imported PassWall config 280 KB and its raw UCI snapshot
// 318 KB; a job result 336 KB; version maps under 1 KB; the inventory
// rawSnapshot and more than one incident transition never seen at all).
export const ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS = 1024 * 1024;
export const ROUTER_RAW_SNAPSHOT_MAX_CHARS = 64 * 1024;
export const ROUTER_VERSION_MAP_MAX_CHARS = 16 * 1024;
export const ROUTER_JOB_RESULT_MAX_CHARS = 1024 * 1024;
export const ROUTER_INCIDENT_TRANSITIONS_MAX = 20;

// Measured on the RAW input, before the inner schema applies its defaults:
// the panel's own bounding of an authenticated router's payload measures the
// raw JSON too, and the two must agree on what is over the cap.
function serializedWithin<T extends z.ZodTypeAny>(maxChars: number, schema: T) {
  return z
    .unknown()
    .refine(
      (value) => {
        try {
          return (
            ((JSON.stringify(value) as string | undefined)?.length ?? 0) <=
            maxChars
          );
        } catch {
          return false;
        }
      },
      { message: `must serialize to at most ${maxChars} characters` },
    )
    .pipe(schema);
}

export const passwallImportedStateSchema = z.object({
  config: serializedWithin(
    ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS,
    passwallDesiredConfigSchema,
  ),
  rawSnapshot: serializedWithin(
    ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS,
    z.record(z.string(), z.unknown()),
  ).default({}),
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
  // xray is the proxy process an xray-direct controller owns directly. Optional
  // so passwall routers (which never report it) are unaffected; an xray-direct
  // controller always sends it.
  xray: serviceRuntimeStateSchema.optional(),
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

// Optional measurements from a router check-in. Absence remains unknown;
// heartbeat or a successful configuration apply is never a VPN verdict.
export const connectRouterActionNameSchema = z.enum([
  "select_entry",
  "set_rules",
  "set_service",
  "set_wifi",
  "restart_vpn",
  "reboot",
  "update_now",
  "set_auto_update",
  "refresh_subscription",
]);

// What a router may advertise for its owner: the actions it executes, plus
// flags that refine one ("set_service_auto": set_service takes the entryId
// ":auto", back to the service's default). An unknown name would fail the
// whole check-in, so a new flag is added here before routers send it.
export const connectRouterCapabilitySchema = z.enum([
  ...connectRouterActionNameSchema.options,
  "set_service_auto",
]);

const connectEntryRefSchema = z.string().regex(/^[A-Za-z0-9._:-]{1,64}$/);

export const routerConnectTelemetrySchema = z.object({
  ownerRef: z
    .string()
    .regex(/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/)
    .nullable()
    .optional(),
  capabilities: z.array(connectRouterCapabilitySchema).max(32).optional(),
  availableVersion: z.string().min(1).max(128).nullable().optional(),
  uptimeSec: z.number().int().nonnegative().optional(),
  verdict: z
    .enum(["ok", "reserve", "down", "direct", "leak", "stopped"])
    .optional(),
  exitCountry: z
    .string()
    .regex(/^[A-Z]{2}$/)
    .nullable()
    .optional(),
  lanClients: z.number().int().nonnegative().optional(),
  location: z
    .object({ mode: z.enum(["auto", "entry"]), entryId: z.string().nullable() })
    .optional(),
  entries: z
    .array(
      z.object({
        id: z.string().min(1),
        name: z.string(),
        country: z
          .string()
          .regex(/^[A-Z]{2}$/)
          .nullable(),
      }),
    )
    .max(300)
    .optional(),
  sites: z
    .object({
      direct: z.array(z.string()).max(300),
      vpn: z.array(z.string()).max(300),
    })
    .optional(),
  // entryId: where the service runs now (null: the main VPN). auto: the
  // owner made no choice, the service is on its default. entries: the cached
  // locations that carry it (absent: not known). stale: the owner's choice
  // does not run now, the service is on its default meanwhile.
  services: z
    .array(
      z.object({
        id: z.string(),
        entryId: z.string().nullable(),
        auto: z.boolean().optional(),
        entries: z.array(connectEntryRefSchema).max(200).optional(),
        stale: z.boolean().optional(),
      }),
    )
    .max(100)
    .optional(),
  // Confidential wire field: the panel strips and encrypts password before
  // inventory persistence. Only the current owner can hydrate the snapshot.
  wifi: z
    .array(
      z.object({
        band: z.string(),
        ssid: z.string(),
        password: z
          .string()
          .regex(/^[\x20-\x7e]{8,63}$/)
          .optional(),
      }),
    )
    .max(8)
    .optional(),
  routerPasswordSet: z.boolean().optional(),
  supportAccess: z.boolean().optional(),
  autoUpdate: z.boolean().optional(),
});

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
  // engineMode + xray fields are reported by an xray-direct controller so the
  // panel can confirm which engine owns the device and see xray health. All
  // optional/additive — a passwall router omits them and is unaffected.
  engineMode: engineModeSchema.optional(),
  xrayEnabled: z.boolean().optional(),
  xrayVersion: z.string().optional(),
  selectedNodeId: z.string().nullable().optional(),
  selectedNodeLabel: z.string().nullable().optional(),
  // vctl: whether the router's owner allows the panel's support shell.
  remoteShell: z.boolean().optional(),
  nodeCount: z.number().int().nonnegative(),
  subscriptionCount: z.number().int().nonnegative(),
  configDigest: z.string().min(1).nullable().optional(),
  appliedRevisionId: z.string().uuid().nullable().optional(),
  packageVersions: serializedWithin(
    ROUTER_VERSION_MAP_MAX_CHARS,
    z.record(z.string(), z.string().nullable()),
  ).default({}),
  binaryVersions: serializedWithin(
    ROUTER_VERSION_MAP_MAX_CHARS,
    z.record(z.string(), z.string().nullable()),
  ).default({}),
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
  rawSnapshot: serializedWithin(
    ROUTER_RAW_SNAPSHOT_MAX_CHARS,
    z.record(z.string(), z.unknown()),
  ).optional(),
  connect: routerConnectTelemetrySchema.optional(),
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

export const routerReauthProofSchema = z.object({
  signedAt: z.string().datetime({ offset: true }),
  signature: z.string().min(1),
});

// Proof that a registering router holds its device key (ADR-0006): an
// ed25519 signature by the key behind inventory.devicePublicKey, standard
// base64, over exactly the UTF-8 bytes
//   "vectra-register/v1\n<deviceIdentifier>\n<timestamp>"
// with the timestamp in unix seconds, base 10. Required only to adopt a record
// the panel created ahead of the router's first contact (a pre-claimed router).
export const routerRegisterProofSchema = z.object({
  timestamp: z.number().int().nonnegative(),
  signature: z.string().min(1).max(256),
});

export const routerRegisterRequestSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  inventory: routerInventorySchema,
  passwallImport: passwallImportedStateSchema.optional(),
  recoveryProof: routerReauthProofSchema.optional(),
  // Optional, and a malformed one counts as none: an ordinary registration
  // (older vctl, the PassWall agent) must never fail over it.
  proof: routerRegisterProofSchema.nullable().optional().catch(null),
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
  // Reinstalls the fleet's pinned official xray build. Opt-in only: never part
  // of the default action set. Controllers older than 0.1.13-r41 reject it.
  "xray_binary",
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
    // Optional pin for "xray_binary"; the controller falls back to its
    // compiled-in fleet build and refuses a version without its checksum.
    xrayVersion: z
      .string()
      .regex(/^\d+\.\d+\.\d+$/)
      .optional(),
    xraySha256: z
      .string()
      .regex(/^[0-9a-f]{64}$/)
      .optional(),
    // When the overlay cannot hold old and new side by side, the controller
    // only removes an xray that still runs if this is set.
    xrayReplaceInPlace: z.boolean().optional(),
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

// The desired-config carrier accepts either the passwall config (default,
// unchanged for the 18 live routers) or an xray-direct config. engineMode is
// the discriminator and defaults to "passwall" so existing payloads parse
// exactly as before. passwall is listed first in the union so passwall configs
// continue to parse through the passwall schema unchanged.
export const desiredRevisionSummarySchema = z.object({
  id: z.string().uuid(),
  revisionNumber: z.number().int().nonnegative(),
  status: z.string().min(1),
  origin: z.string().min(1).default("operator_draft"),
  engineMode: engineModeSchema.default("passwall"),
  configDigest: z.string().nullable().optional(),
  config: z.union([passwallDesiredConfigSchema, xrayDesiredConfigSchema]),
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

// ---------------------------------------------------------------------------
// ADR-0006 — a customer links a router to their Vectra account by QR.
//
// A router that is not linked yet reports, on every check-in, the hash of the
// one-time code it currently shows (never the code itself):
//   codeHash = hex(sha256("vectra-claim-codehash/v1:" + CODE_UPPERCASE))
// and when that code stops being valid. `claim` is null/absent once the router
// is configured. The panel answers (register AND check-in) with the key the
// router seals its QR to, the Telegram bot for the "open in Telegram" deep
// link, and — once claimed — a masked label of the owning account.
// ---------------------------------------------------------------------------
export const routerClaimSchema = z.object({
  codeHash: z
    .string()
    .regex(/^[0-9a-fA-F]{64}$/, "codeHash must be a hex sha256 digest")
    .transform((value) => value.toLowerCase()),
  expiresAt: z.string().datetime({ offset: true }),
});

export const routerClaimKeySchema = z.object({
  kid: z.number().int().nonnegative(),
  // Raw 32-byte X25519 public key, base64.
  publicKey: z.string().min(1),
});

export const routerOwnerSchema = z.object({
  ownerRef: z
    .string()
    .regex(/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/)
    .optional(),
  label: z.string().min(1),
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
  claimKey: routerClaimKeySchema.optional(),
  botUsername: z.string().min(1).optional(),
  // null = not linked (vctl reads an absent owner as "no news").
  owner: routerOwnerSchema.nullable().optional(),
  // Sent only while the Vectra account has unlinked the router and nobody has
  // claimed it since: the explicit "drop the previous owner's config" signal.
  // Absent otherwise — `owner: null` alone is what every fleet router gets.
  released: z.literal(true).optional(),
});

export const routerCheckInRequestSchema = z.object({
  protocolVersion: z.literal(VECTRA_PROTOCOL_VERSION),
  routerId: z.string().uuid(),
  inventory: routerInventorySchema,
  passwallImport: passwallImportedStateSchema.optional(),
  claim: routerClaimSchema.nullable().optional(),
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

// Panel-authored route policy. The controller binds exactly what this names
// instead of running its own semantic scorer, so changing a canonical exit or a
// per-router exemption is a panel deploy rather than a controller rebuild plus
// a fleet-wide controller rollout.
//
// Optional on purpose: a controller older than this field ignores it, and a
// panel that cannot resolve targets for a router omits it. Both cases fall back
// to the controller's built-in scorer, which stays as the offline safety net.
export const fleetRoutePolicyDirectiveSlotSchema = z.object({
  id: z.string().min(1),
  nodeId: z.string().min(1),
  // Advisory, operator-facing: "label | host:port | transport | protocol".
  fingerprint: z.string().optional(),
  ruleExtras: z.record(z.string(), z.string()).optional(),
  nodeExtras: z.record(z.string(), z.string()).optional(),
});

export const fleetRoutePolicyDirectiveSchema = z.object({
  version: z.string().min(1),
  exempt: z.boolean(),
  reason: z.string().optional(),
  slots: z.array(fleetRoutePolicyDirectiveSlotSchema),
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
  routePolicy: fleetRoutePolicyDirectiveSchema.nullish(),
  claimKey: routerClaimKeySchema.optional(),
  botUsername: z.string().min(1).optional(),
  // null = not linked (vctl reads an absent owner as "no news").
  owner: routerOwnerSchema.nullable().optional(),
  // Sent only while the Vectra account has unlinked the router and nobody has
  // claimed it since: the explicit "drop the previous owner's config" signal.
  // Absent otherwise — `owner: null` alone is what every fleet router gets.
  released: z.literal(true).optional(),
});

// ---------------------------------------------------------------------------
// Partner API (Vectra backend -> panel), server to server only.
//
//   POST   /api/partner/router-claims            partnerRouterClaimRequestSchema
//   DELETE /api/partner/router-claims/:routerId  partnerRouterUnbindRequestSchema
//
// All inbound partner requests require canonical method/path/query/body-digest/
// idempotency-key/timestamp/request-id signature v2 plus durable replay protection.
// See ai_docs/develop/features/connect-partner-api.json. Outbound webhooks retain
// their separate timestamp + newline + rawBody body-signature protocol.
//
// Objects are .strict(): this is a new contract between two systems, and a
// misspelt key should fail the integration loudly instead of being dropped.
// ---------------------------------------------------------------------------
export const partnerOwnerRefSchema = z
  .string()
  .trim()
  // An opaque account id. No '@' or spaces: the panel stores no personal data.
  .regex(
    /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/,
    "owner.ref must be an opaque account id (letters, digits, . _ : -)",
  );

const PARTNER_USER_AGENT_BLANK =
  "userAgent must not be blank — omit it for the router's own signed agent, or state the literal User-Agent to fetch with.";

const PARTNER_USER_AGENT_IS_ROUTER_OWN =
  'userAgent must not start with "VectraRouter/" — only the router can make that agent; omit the field so the router signs its own.';

function claimsToBeRouterOwnUserAgent(value: string) {
  return /^vectrarouter\//i.test(value);
}

function hasNoControlCharacters(value: string) {
  for (const char of value) {
    const code = char.charCodeAt(0);
    if (code < 0x20 || code === 0x7f) {
      return false;
    }
  }
  return true;
}

export const partnerRouterClaimRequestSchema = z
  .object({
    code: z.string().trim().min(1).max(32).nullable().optional(),
    device: z
      .object({
        deviceIdentifier: z.string().trim().min(1).max(128),
        // The router's ed25519 key (the QR's pk): base64 of 32 bytes. A record
        // made ahead of first contact is handed only to a router that proves
        // it holds this key, so it has to be a key.
        devicePublicKey: z
          .string()
          .trim()
          .regex(
            /^[A-Za-z0-9+/_-]{43}=?$/,
            "device.devicePublicKey must be the base64 of a 32-byte ed25519 key",
          ),
        // The QR's one-time nonce `n`, standard base64 of its 16 bytes. The
        // code derived from it must be one the router is showing now.
        nonce: z
          .string()
          .trim()
          .regex(
            /^[A-Za-z0-9+/]{22}==$/,
            "device.nonce must be the standard base64 of the QR's 16-byte nonce",
          ),
      })
      .strict()
      .nullable()
      .optional(),
    owner: z
      .object({
        ref: partnerOwnerRefSchema,
        // Shown on the router's own page, which keeps at most 64 characters.
        label: z
          .string()
          .trim()
          .min(1)
          .max(64)
          .refine(hasNoControlCharacters, "owner.label must be one line"),
      })
      .strict(),
    subscription: z
      .object({
        url: z.string().trim().min(1).max(2048),
        // Absent or null: the router fetches with its OWN signed agent —
        // `VectraRouter/<version> vr1.<token>`, made fresh per request and
        // bound to the router's device key (see
        // router/vectra-controller-pro/cmd/vctl/signed_ua.go) — never
        // invented by the panel. A present string is the literal the backend
        // states for a customer's own subscription (e.g. a real JSON
        // client's agent, for a provider that only serves JSON to one of
        // those); omitting the field is not inventing one. Blank is refused
        // as ambiguous, and a value claiming to be the router's own agent
        // (starting with "VectraRouter/") is refused too: only the router
        // can make that agent, so the field should be left out instead.
        userAgent: z
          .string()
          .trim()
          .min(1, PARTNER_USER_AGENT_BLANK)
          .max(256)
          .refine(hasNoControlCharacters, "userAgent must be one line")
          .refine(
            (value) => !claimsToBeRouterOwnUserAgent(value),
            PARTNER_USER_AGENT_IS_ROUTER_OWN,
          )
          .nullable()
          .optional(),
      })
      .strict(),
  })
  .strict()
  .superRefine((value, ctx) => {
    const hasCode = value.code !== null && value.code !== undefined;
    const hasDevice = value.device !== null && value.device !== undefined;
    if (hasCode === hasDevice) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Exactly one of code or device is required.",
        path: hasCode ? ["device"] : ["code"],
      });
    }
  });

export const partnerRouterClaimResponseSchema = z.object({
  routerId: z.string().uuid(),
  state: z.literal("claimed"),
  alreadyClaimed: z.boolean(),
  deviceIdentifier: z.string().min(1).max(128),
  devicePublicKey: z
    .string()
    .regex(/^[A-Za-z0-9+/]{43}=$/)
    .nullable(),
  model: z.string().nullable(),
});

// The body a DELETE must carry. The signature covers the body only, so the
// router id is repeated here to bind the signature to the router it unbinds —
// otherwise a signed DELETE could be replayed against any other router path
// inside the timestamp window. `ownerRef` is required and must still own the
// router: a late retry from an old owner's flow cannot unbind the next owner,
// and no unbind goes through without naming whose router it is.
export const partnerRouterUnbindRequestSchema = z
  .object({
    routerId: z.string().uuid(),
    ownerRef: partnerOwnerRefSchema,
  })
  .strict();

export const partnerWebhookEventSchema = z.enum([
  "router.claimed",
  "router.ready",
  "router.failed",
  "router.online",
  "router.offline",
  "router.vpn_down",
  "router.vpn_up",
  "router.updated",
  "router.action",
]);

export const partnerWebhookPayloadSchema = z.object({
  event: partnerWebhookEventSchema,
  routerId: z.string().uuid(),
  ownerRef: z.string().min(1),
  at: z.string().datetime(),
  detail: z.union([z.string(), z.record(z.string(), z.unknown())]).optional(),
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
  incidentTransitions: z
    .array(incidentTransitionSchema)
    .max(ROUTER_INCIDENT_TRANSITIONS_MAX)
    .default([]),
  result: serializedWithin(
    ROUTER_JOB_RESULT_MAX_CHARS,
    z.record(z.string(), z.unknown()),
  ).default({}),
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
export type XrayDesiredConfig = z.infer<typeof xrayDesiredConfigSchema>;
export type RouterClaim = z.infer<typeof routerClaimSchema>;
export type RouterClaimKey = z.infer<typeof routerClaimKeySchema>;
export type RouterRegisterProof = z.infer<typeof routerRegisterProofSchema>;
export type PartnerRouterClaimRequest = z.infer<
  typeof partnerRouterClaimRequestSchema
>;
export type PartnerRouterClaimResponse = z.infer<
  typeof partnerRouterClaimResponseSchema
>;
export type PartnerRouterUnbindRequest = z.infer<
  typeof partnerRouterUnbindRequestSchema
>;
export type PartnerWebhookEvent = z.infer<typeof partnerWebhookEventSchema>;
export type PartnerWebhookPayload = z.infer<typeof partnerWebhookPayloadSchema>;
export type EngineMode = z.infer<typeof engineModeSchema>;
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
export type RouterReauthProof = z.infer<typeof routerReauthProofSchema>;
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
