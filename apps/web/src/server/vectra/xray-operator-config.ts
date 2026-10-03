import {
  MASKED_SECRET_PLACEHOLDER,
  type XrayDesiredConfig,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";

/**
 * Fleet defaults for the xray-direct operator config.
 *
 * This module is the ONLY place that authors an operator config for Vectra
 * Controller Pro. Everything it emits must survive
 * `internal/config/config.go`'s `DisallowUnknownFields()` decode — the zod
 * schema is `.strict()` for exactly that reason, so a typo here fails the
 * panel test suite rather than the router.
 *
 * What is NOT here, deliberately: outbounds, dns, routing, policy, stats.
 * Since the "consume provider JSON" pivot the router fetches the provider's
 * complete Xray document from the subscription URL and adopts it verbatim.
 * The panel only ships the four blocks the controller itself needs.
 */

/** Where the fleet actually keeps geoip.dat/geosite.dat (uci v2ray_location_asset). */
export const XRAY_GEO_ASSET_DIR = "/usr/share/v2ray";

export const XRAY_GEOIP_URL =
  "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat";

export const XRAY_GEOSITE_URL =
  "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat";

/**
 * The fleet is 234 MiB Xiaomi AX3000T hardware. -500 makes xray meaningfully
 * less attractive to the OOM killer than userspace default (0) without making
 * it unkillable, and an 80 MiB soft cap keeps a runaway config from taking the
 * router down with it.
 */
export const XRAY_OOM_SCORE_ADJ = -500;
export const XRAY_MEMORY_SOFT_MIB = 80;

/**
 * Selects WHICH payload the provider returns. "v2rayNG/1.9.5" yields the
 * complete JSON config array; a "passwall2/*" or "Xray/*" UA yields the
 * degraded base64 vless:// link list, which is NOT what the json parse path
 * wants. See `Subscription.UserAgent` in internal/config/types.go.
 *
 * NEVER a "Happ…" agent that is not the real client's
 * "Happ/<version>/<OS>/<build>": the provider's anti-fraud sweep deletes such a
 * device on the first request and disables the customer's account. It used to
 * be "Happ/1.0". The router refuses to send one (internal/uaguard) and refuses a
 * config that carries one, so this default would now fail every apply.
 *
 * Still a stand-in: a router is not a v2rayNG client either. The durable answer
 * is a router User-Agent the subscription backend recognises and answers with
 * the JSON variant — an owner decision, not something to guess here.
 */
export const XRAY_SUBSCRIPTION_USER_AGENT = "v2rayNG/1.9.5";

/** Stable id for the single primary subscription the panel manages. */
export const XRAY_PRIMARY_SUBSCRIPTION_ID = "primary";

export type BuildXrayOperatorConfigInput = {
  /** Router hostname / display name; surfaces in the controller's own logs. */
  instanceName: string;
  /** Provider subscription URL. Secret — masked at rest, encrypted in the blob. */
  subscriptionUrl: string;
  /**
   * Which provider profile to run, matched EXACTLY against the entry's
   * "remarks" string (e.g. "⚡Extreme Польша 🇵🇱"). Omit for the first profile:
   * `SelectEntry` falls back to entryIndex, which is 0.
   */
  entryRemark?: string | null;
  /**
   * User-Agent the router fetches the subscription with. Omit for the fleet
   * default (XRAY_SUBSCRIPTION_USER_AGENT) — the OPERATOR's path only. A
   * customer's subscription (partner claim) never gets this default: it goes
   * through withFreshPrimarySubscription instead, whose agent is either the
   * backend's stated literal or null for the router's own signed one.
   */
  userAgent?: string | null;
};

// Mirror of router/vectra-controller-pro/internal/uaguard: the real Happ
// client's shape. Anything else that claims to be Happ gets the customer's
// device deleted by the provider's anti-fraud sweep on the first request.
const WELL_FORMED_HAPP_USER_AGENT =
  /^Happ\/\d+(\.\d+){1,3}\/[A-Za-z][A-Za-z0-9]*\/\d{6,}$/;

// The router's own signed agent (cmd/vctl/signed_ua.go): made fresh per
// request and bound to its device key. Nobody else can make one, so a
// literal claiming this shape is a forgery, not a legitimate stated agent.
const ROUTER_OWN_USER_AGENT_PREFIX = /^vectrarouter\//i;

/**
 * The router refuses (internal/uaguard) a config whose User-Agent claims to be
 * Happ without being the real client's "Happ/<version>/<OS>/<build>", so such a
 * value would fail every apply. Refuse it where it is authored instead.
 *
 * Also refuses a literal claiming to be the router's own signed agent
 * (`VectraRouter/…`): only the router itself can make that agent, so an
 * operator- or backend-stated literal claiming to be it is a forgery — the
 * field should be left out instead, so the router signs its own.
 */
export function assertUsableSubscriptionUserAgent(userAgent: string) {
  const trimmed = userAgent.trim();
  if (trimmed.length === 0) {
    throw new Error("Subscription User-Agent must not be empty.");
  }
  if (ROUTER_OWN_USER_AGENT_PREFIX.test(trimmed)) {
    throw new Error(
      `Subscription User-Agent "${trimmed}" claims to be the router's own signed agent, which only the router itself can make; omit userAgent instead so the router signs one.`,
    );
  }
  if (
    trimmed.toLowerCase().startsWith("happ") &&
    !WELL_FORMED_HAPP_USER_AGENT.test(trimmed)
  ) {
    throw new Error(
      `Subscription User-Agent "${trimmed}" claims to be Happ but is not the real client's "Happ/<version>/<OS>/<build>"; the router refuses it.`,
    );
  }
  return trimmed;
}

/**
 * The fleet's primary subscription entry, built from scratch.
 *
 * `userAgent: null` means the router's own signed agent: the key is omitted
 * from the result entirely (never emitted empty), which is how vctl is told
 * to sign one itself. The only default lives in buildXrayOperatorConfig.
 */
function buildPrimarySubscription(input: {
  subscriptionUrl: string;
  userAgent: string | null;
  entryRemark?: string | null;
}) {
  const entryRemark = input.entryRemark?.trim() ?? "";
  const url = assertUsableSubscriptionUrl(input.subscriptionUrl);
  const userAgent =
    input.userAgent === null
      ? null
      : assertUsableSubscriptionUserAgent(input.userAgent);
  return {
    id: XRAY_PRIMARY_SUBSCRIPTION_ID,
    url,
    enabled: true,
    ...(userAgent !== null ? { userAgent } : {}),
    mode: "json" as const,
    // entryRemark wins when set; otherwise entryIndex 0 = first profile.
    ...(entryRemark.length > 0 ? { entryRemark } : { entryIndex: 0 }),
  };
}

/**
 * `internal/config/validate.go` refuses a non-https subscription URL —
 * cleartext lets an on-path attacker reshape the whole proxy config. Enforced
 * here, at the authoring boundary, rather than in the zod schema: at rest the
 * URL is replaced by MASKED_SECRET_PLACEHOLDER and the masked row still has to
 * re-parse through the schema on read.
 */
export function assertUsableSubscriptionUrl(url: string) {
  const trimmed = url.trim();
  if (trimmed.length === 0) {
    throw new Error("Subscription URL is required.");
  }
  if (trimmed === MASKED_SECRET_PLACEHOLDER) {
    throw new Error(
      "Subscription URL is masked. Submit the real URL, or leave it unset to keep the stored one.",
    );
  }
  if (!trimmed.startsWith("https://")) {
    throw new Error(
      "Subscription URL must be https:// — the router refuses cleartext subscription fetches.",
    );
  }
  return trimmed;
}

/**
 * Build a complete operator config from fleet defaults.
 *
 * Every default is pinned explicitly rather than left for the controller's own
 * ApplyDefaults, so the delivered document says exactly what the router will
 * run ("no silent normalization" — internal/config/types.go).
 */
export function buildXrayOperatorConfig(
  input: BuildXrayOperatorConfigInput,
): XrayDesiredConfig {
  return xrayDesiredConfigSchema.parse({
    schema: 1,
    instance: {
      name: input.instanceName,
      logLevel: "warning",
    },
    process: {
      xrayBinary: "/usr/bin/xray",
      workDir: "/var/run/vectra-controller-pro",
      memorySoftMiB: XRAY_MEMORY_SOFT_MIB,
      oomScoreAdj: XRAY_OOM_SCORE_ADJ,
      restartBackoff: {
        initialMs: 500,
        factor: 2,
        maxMs: 60_000,
        reset: "60s",
      },
      reloadGrace: "5s",
      startTimeout: "15s",
    },
    inbounds: {
      tproxy: {
        listenIP: "0.0.0.0",
        port: 12345,
        fwmark: 1,
        udpEnabled: true,
        tag: "tproxy-in",
        sniffing: {
          enabled: true,
          // Never "fakedns": the provider document carries no fakedns block and
          // validate.go rejects it outright.
          destOverride: ["http", "tls", "quic"],
          // Route on the sniffed domain but still dial the original
          // destination — the provider's routing rules are domain-based.
          routeOnly: true,
        },
      },
    },
    geo: {
      assetDir: XRAY_GEO_ASSET_DIR,
      geoipUrl: XRAY_GEOIP_URL,
      geositeUrl: XRAY_GEOSITE_URL,
      // Off by default: 234 MiB routers should not pull two .dat files on every
      // supervised restart. Refresh is a separate scheduled job.
      updateOnStart: false,
    },
    subscriptions: [
      buildPrimarySubscription({
        subscriptionUrl: input.subscriptionUrl,
        userAgent: input.userAgent ?? XRAY_SUBSCRIPTION_USER_AGENT,
        entryRemark: input.entryRemark,
      }),
    ],
  });
}

/**
 * Return a copy of `config` whose subscriptions are exactly ONE freshly built
 * primary entry: the given URL and User-Agent, the first provider profile.
 *
 * This is how a partner claim (ADR-0006) hands a router to a new owner. The
 * subscription is the owner's; nothing of a previous one may survive into it —
 * not its headers, not its pinned profile (a remark from another feed would
 * fail entry selection on the router), not a second entry. Everything outside
 * `subscriptions` (process, inbounds, geo, ui) is the operator's and is kept.
 *
 * `userAgent` is never invented by the panel: it is either the literal the
 * Vectra backend states for the customer's subscription, or `null` — the
 * router's own signed agent, made fresh per request and bound to its device
 * key. A literal claiming to be that agent (`VectraRouter/…`) is refused; see
 * assertUsableSubscriptionUserAgent.
 */
export function withFreshPrimarySubscription(
  config: XrayDesiredConfig,
  input: { subscriptionUrl: string; userAgent: string | null },
): XrayDesiredConfig {
  return xrayDesiredConfigSchema.parse({
    ...config,
    subscriptions: [
      buildPrimarySubscription({
        subscriptionUrl: input.subscriptionUrl,
        userAgent: input.userAgent,
      }),
    ],
  });
}

/**
 * Return a copy of `config` with the operator's UI lock on or off.
 *
 * "Off" removes the `ui` block instead of emitting `{ lock: false }`: a vctl
 * that predates the block decodes with DisallowUnknownFields and would refuse
 * the whole config, so an unlocked router must not see the key at all.
 */
export function withUiLock(
  config: XrayDesiredConfig,
  lock: boolean,
): XrayDesiredConfig {
  const rest: Record<string, unknown> = { ...config };
  delete rest.ui;
  return xrayDesiredConfigSchema.parse(
    lock ? { ...rest, ui: { lock: true } } : rest,
  );
}

/** Whether the operator config locks the router's UI to the simple view. */
export function isUiLocked(config: XrayDesiredConfig | null | undefined) {
  return config?.ui?.lock === true;
}

/**
 * Return a copy of `config` fetching the primary subscription with a
 * different User-Agent. Only the primary (first) subscription is panel-managed.
 */
export function withSubscriptionUserAgent(
  config: XrayDesiredConfig,
  userAgent: string,
): XrayDesiredConfig {
  const value = assertUsableSubscriptionUserAgent(userAgent);
  const subscriptions = config.subscriptions ?? [];

  if (subscriptions.length === 0) {
    throw new Error(
      "Config has no subscription entry to update. Rebuild it from fleet defaults.",
    );
  }

  return xrayDesiredConfigSchema.parse({
    ...config,
    subscriptions: subscriptions.map((subscription, index) =>
      index === 0 ? { ...subscription, userAgent: value } : subscription,
    ),
  });
}

/**
 * Return a copy of `config` running a different provider profile.
 *
 * Pass null/"" to fall back to the first profile (entryIndex 0). The two
 * selectors are mutually exclusive on the router — `SelectEntry` short-circuits
 * on a non-empty remark — so only one is ever emitted.
 */
export function withSelectedProfile(
  config: XrayDesiredConfig,
  entryRemark: string | null | undefined,
): XrayDesiredConfig {
  const remark = entryRemark?.trim() ?? "";
  const subscriptions = config.subscriptions ?? [];

  if (subscriptions.length === 0) {
    throw new Error(
      "Config has no subscription to select a profile on. Set the subscription URL first.",
    );
  }

  return xrayDesiredConfigSchema.parse({
    ...config,
    subscriptions: subscriptions.map((subscription, index) => {
      // Only the primary subscription drives profile selection.
      if (index !== 0) {
        return subscription;
      }
      // Drop BOTH selectors first, then set exactly one: leaving a stale
      // entryIndex alongside a remark would be ambiguous to read back, and
      // leaving a stale remark would silently win over the index on the router.
      const rest: Record<string, unknown> = { ...subscription };
      delete rest.entryRemark;
      delete rest.entryIndex;
      return remark.length > 0
        ? { ...rest, entryRemark: remark }
        : { ...rest, entryIndex: 0 };
    }),
  });
}

/**
 * Return a copy of `config` pointing at a different subscription URL.
 * Only the primary (first) subscription is panel-managed.
 */
export function withSubscriptionUrl(
  config: XrayDesiredConfig,
  subscriptionUrl: string,
): XrayDesiredConfig {
  const url = assertUsableSubscriptionUrl(subscriptionUrl);
  const subscriptions = config.subscriptions ?? [];

  if (subscriptions.length === 0) {
    throw new Error(
      "Config has no subscription entry to update. Rebuild it from fleet defaults.",
    );
  }

  return xrayDesiredConfigSchema.parse({
    ...config,
    subscriptions: subscriptions.map((subscription, index) =>
      index === 0 ? { ...subscription, url } : subscription,
    ),
  });
}

/**
 * Read back which profile a config currently selects. `null` means "the first
 * profile" (no remark pinned).
 */
export function selectedProfileRemark(
  config: XrayDesiredConfig,
): string | null {
  const remark = config.subscriptions?.[0]?.entryRemark?.trim() ?? "";
  return remark.length > 0 ? remark : null;
}
