import { describe, expect, it } from "vitest";

import {
  MASKED_SECRET_PLACEHOLDER,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";

import {
  assertUsableSubscriptionUserAgent,
  buildXrayOperatorConfig,
  isUiLocked,
  selectedProfileRemark,
  withFreshPrimarySubscription,
  withSelectedProfile,
  withSubscriptionUrl,
  withSubscriptionUserAgent,
  withUiLock,
  XRAY_GEO_ASSET_DIR,
  XRAY_GEOIP_URL,
  XRAY_GEOSITE_URL,
  XRAY_MEMORY_SOFT_MIB,
  XRAY_OOM_SCORE_ADJ,
} from "./xray-operator-config";

const SUBSCRIPTION_URL = "https://sub.example.com/api/sub/REAL_TOKEN";

function build(entryRemark?: string | null) {
  return buildXrayOperatorConfig({
    instanceName: "kirill-msk",
    subscriptionUrl: SUBSCRIPTION_URL,
    entryRemark,
  });
}

// ---------------------------------------------------------------------------
// Lock-step with the Go contract at
// router/vectra-controller-pro/internal/config/types.go.
//
// `internal/config/config.go` decodes with DisallowUnknownFields(), so ANY key
// the Go struct does not declare is a fatal decode error on the router. These
// key sets are transcribed from the Go json tags; if the Go struct changes,
// these lists must change with it.
// ---------------------------------------------------------------------------
const GO_ROOT_KEYS = [
  "schema",
  "instance",
  "process",
  "inbounds",
  "geo",
  "subscriptions",
];
const GO_PROCESS_KEYS = [
  "xrayBinary",
  "workDir",
  "configFile",
  "logDir",
  "memorySoftMiB",
  "memoryHardMiB",
  "oomScoreAdj",
  "niceLevel",
  "gomaxprocs",
  "restartBackoff",
  "reloadGrace",
  "startTimeout",
];
const GO_TPROXY_KEYS = [
  "listenIP",
  "port",
  "fwmark",
  "udpEnabled",
  "sniffing",
  "tag",
  "killSwitch",
];
const GO_GEO_KEYS = [
  "assetDir",
  "geoipUrl",
  "geositeUrl",
  "updateSchedule",
  "updateOnStart",
  "extraAssets",
];
const GO_SUBSCRIPTION_KEYS = [
  "id",
  "remark",
  "url",
  "enabled",
  "userAgent",
  "group",
  "headers",
  "mode",
  "entryIndex",
  "entryRemark",
  "maxBytes",
  "allowInsecureTls",
];

describe("buildXrayOperatorConfig — Go DisallowUnknownFields contract", () => {
  it("emits only keys the Go config.Config struct declares", () => {
    const config = build() as unknown as Record<string, unknown>;

    expect(Object.keys(config).every((k) => GO_ROOT_KEYS.includes(k))).toBe(
      true,
    );

    const process = config.process as Record<string, unknown>;
    expect(
      Object.keys(process).filter((k) => !GO_PROCESS_KEYS.includes(k)),
    ).toEqual([]);

    const tproxy = (config.inbounds as Record<string, unknown>)
      .tproxy as Record<string, unknown>;
    expect(
      Object.keys(tproxy).filter((k) => !GO_TPROXY_KEYS.includes(k)),
    ).toEqual([]);

    const geo = config.geo as Record<string, unknown>;
    expect(Object.keys(geo).filter((k) => !GO_GEO_KEYS.includes(k))).toEqual([]);

    const subscription = (config.subscriptions as Array<Record<string, unknown>>)[0]!;
    expect(
      Object.keys(subscription).filter(
        (k) => !GO_SUBSCRIPTION_KEYS.includes(k),
      ),
    ).toEqual([]);
  });

  it("carries none of the pre-pivot blocks the panel used to author", () => {
    // The regression this whole change exists to prevent: the old schema always
    // emitted dns/nodes/routing/normalization (they had zod .default()), each of
    // which is an unknown field to the Go decoder and would abort the apply job
    // with `json: unknown field "dns"`.
    const config = build() as unknown as Record<string, unknown>;
    for (const removed of [
      "dns",
      "nodes",
      "routing",
      "normalization",
      "policy",
      "stats",
      "api",
      "reverse",
      "metrics",
      "fakedns",
    ]) {
      expect(config).not.toHaveProperty(removed);
    }
  });

  it("rejects any unknown key at the schema boundary rather than on the router", () => {
    expect(
      xrayDesiredConfigSchema.safeParse({
        ...build(),
        routing: { domainStrategy: "IPIfNonMatch" },
      }).success,
    ).toBe(false);
  });

  it("satisfies the required-field checks in internal/config/validate.go", () => {
    const config = build();
    expect(config.schema).toBe(1);
    expect(config.process.xrayBinary.length).toBeGreaterThan(0);
    expect(config.process.workDir.length).toBeGreaterThan(0);
    expect(config.process.oomScoreAdj).toBeGreaterThanOrEqual(-1000);
    expect(config.process.oomScoreAdj).toBeLessThanOrEqual(1000);
    expect(config.process.restartBackoff.initialMs).toBeGreaterThan(0);
    expect(config.process.restartBackoff.factor).toBeGreaterThanOrEqual(1);
    expect(config.process.restartBackoff.maxMs).toBeGreaterThanOrEqual(
      config.process.restartBackoff.initialMs,
    );
    // tproxy is required — it is the only inbound the controller splices in.
    expect(config.inbounds.tproxy.port).toBeGreaterThan(0);
    expect(config.inbounds.tproxy.port).toBeLessThanOrEqual(65535);
    expect(config.subscriptions![0]!.id.length).toBeGreaterThan(0);
    expect(config.subscriptions![0]!.url.startsWith("https://")).toBe(true);
  });

  it("never asks Xray to sniff into fakedns, which validate.go refuses", () => {
    // The provider document carries no fakedns block, so destOverride:fakedns
    // is a hard Xray start failure.
    expect(build().inbounds.tproxy.sniffing.destOverride).not.toContain(
      "fakedns",
    );
    expect(
      xrayDesiredConfigSchema.safeParse({
        ...build(),
        inbounds: {
          tproxy: {
            ...build().inbounds.tproxy,
            sniffing: { enabled: true, destOverride: ["fakedns"] },
          },
        },
      }).success,
    ).toBe(false);
  });
});

describe("buildXrayOperatorConfig — fleet defaults", () => {
  it("pins the fleet geo sources and 234 MiB-hardware process limits", () => {
    const config = build();
    expect(config.geo.assetDir).toBe(XRAY_GEO_ASSET_DIR);
    expect(config.geo.assetDir).toBe("/usr/share/v2ray");
    expect(config.geo.geoipUrl).toBe(XRAY_GEOIP_URL);
    expect(config.geo.geositeUrl).toBe(XRAY_GEOSITE_URL);
    expect(config.process.oomScoreAdj).toBe(XRAY_OOM_SCORE_ADJ);
    expect(config.process.oomScoreAdj).toBe(-500);
    expect(config.process.memorySoftMiB).toBe(XRAY_MEMORY_SOFT_MIB);
    expect(config.process.memorySoftMiB).toBe(80);
  });

  it("requests the JSON payload variant from the provider", () => {
    // A "passwall2/*" or "Xray/*" UA yields the degraded base64 link list, not
    // the complete Xray document the json parse path needs.
    const subscription = build().subscriptions![0]!;
    expect(subscription.mode).toBe("json");
    expect(subscription.userAgent).toBe("v2rayNG/1.9.5");
    // The router refuses a malformed Happ agent outright (internal/uaguard):
    // the provider's sweep deletes the device that sends one.
    expect(subscription.userAgent).not.toMatch(/^happ/i);
    expect(subscription.enabled).toBe(true);
  });

  it("names the instance after the router", () => {
    expect(build().instance.name).toBe("kirill-msk");
  });
});

describe("provider profile selection", () => {
  it("defaults to the first profile when no remark is pinned", () => {
    const config = build();
    expect(config.subscriptions![0]!.entryIndex).toBe(0);
    expect(config.subscriptions![0]!.entryRemark).toBeUndefined();
    expect(selectedProfileRemark(config)).toBeNull();
  });

  it("pins a named profile by its exact remarks string", () => {
    const config = build("⚡Extreme Польша 🇵🇱");
    expect(config.subscriptions![0]!.entryRemark).toBe("⚡Extreme Польша 🇵🇱");
    // SelectEntry short-circuits on a non-empty remark, so entryIndex must not
    // also be emitted — one selector at a time.
    expect(config.subscriptions![0]!.entryIndex).toBeUndefined();
    expect(selectedProfileRemark(config)).toBe("⚡Extreme Польша 🇵🇱");
  });

  it("switches profiles without disturbing anything else in the config", () => {
    const before = build("⚡Extreme Польша 🇵🇱");
    const after = withSelectedProfile(before, "🇷🇺🇪🇺 Авто Самый стабильный");

    expect(after.subscriptions![0]!.entryRemark).toBe(
      "🇷🇺🇪🇺 Авто Самый стабильный",
    );
    // The credential and every other block are carried over untouched.
    expect(after.subscriptions![0]!.url).toBe(SUBSCRIPTION_URL);
    expect(after.process).toEqual(before.process);
    expect(after.inbounds).toEqual(before.inbounds);
    expect(after.geo).toEqual(before.geo);
  });

  it("falls back to the first profile when the remark is cleared", () => {
    const after = withSelectedProfile(build("⚡Extreme Польша 🇵🇱"), null);
    expect(after.subscriptions![0]!.entryRemark).toBeUndefined();
    expect(after.subscriptions![0]!.entryIndex).toBe(0);
    expect(selectedProfileRemark(after)).toBeNull();
  });
});

describe("subscription URL handling", () => {
  it("refuses a cleartext subscription URL, as validate.go does", () => {
    expect(() =>
      buildXrayOperatorConfig({
        instanceName: "r",
        subscriptionUrl: "http://sub.example.com/api/sub/TOKEN",
      }),
    ).toThrow(/https/);
  });

  it("refuses an empty URL", () => {
    expect(() =>
      buildXrayOperatorConfig({ instanceName: "r", subscriptionUrl: "  " }),
    ).toThrow(/required/i);
  });

  it("refuses to persist the masked placeholder as a real URL", () => {
    // Guards the trap where an edit surface round-trips a masked config and the
    // literal placeholder would become the fetch URL.
    expect(() =>
      buildXrayOperatorConfig({
        instanceName: "r",
        subscriptionUrl: MASKED_SECRET_PLACEHOLDER,
      }),
    ).toThrow(/masked/i);
  });

  it("replaces the URL while keeping the selected profile", () => {
    const before = build("⚡Extreme Польша 🇵🇱");
    const after = withSubscriptionUrl(before, "https://sub.example.com/new");
    expect(after.subscriptions![0]!.url).toBe("https://sub.example.com/new");
    expect(after.subscriptions![0]!.entryRemark).toBe("⚡Extreme Польша 🇵🇱");
  });
});

// ---------------------------------------------------------------------------
// Cross-language contract lock.
//
// The panel and the Go decoder are two halves of one contract written in two
// languages, and nothing used to check them against each other. That gap
// shipped a real bug: the pre-pivot schema emitted dns/nodes/routing
// unconditionally via zod .default(), while Go's config.Load runs with
// DisallowUnknownFields, so every xray apply job would have died at decode.
// Neither suite caught it — this one only checked the panel's own zod schema,
// and the Go e2e test feeds a Go-authored config.
//
// These goldens live in the Go tree and are decoded there by
// internal/config/panel_contract_test.go. Here we prove the builder still
// produces exactly those bytes. Drift on either side now fails a test instead
// of a router.
//
// Regenerating: see router/vectra-controller-pro/internal/config/testdata/panel/README.md
// ---------------------------------------------------------------------------
describe("cross-language contract with the Go decoder", () => {
  const GOLDEN_DIR =
    "../../router/vectra-controller-pro/internal/config/testdata/panel";
  const GOLDEN_INSTANCE = "golden-router";
  const GOLDEN_URL =
    "https://subscription.example.test/api/sub/GOLDEN_TOKEN";
  const GOLDEN_REMARK = "⚡Extreme Польша 🇵🇱";

  const readGolden = async (name: string) => {
    const { readFile } = await import("node:fs/promises");
    return readFile(`${GOLDEN_DIR}/${name}`, "utf8");
  };

  const base = () =>
    buildXrayOperatorConfig({
      instanceName: GOLDEN_INSTANCE,
      subscriptionUrl: GOLDEN_URL,
    });

  it("still emits the golden the Go decoder is tested against", async () => {
    expect(JSON.stringify(base(), null, 2) + "\n").toBe(
      await readGolden("operator-config.json"),
    );
  });

  it("still emits the golden profile variant", async () => {
    expect(
      JSON.stringify(withSelectedProfile(base(), GOLDEN_REMARK), null, 2) + "\n",
    ).toBe(await readGolden("operator-config-profile.json"));
  });
});

describe("subscription User-Agent", () => {
  it("defaults to the fleet agent and takes the one it is given", () => {
    expect(build().subscriptions?.[0]?.userAgent).toBe("v2rayNG/1.9.5");
    expect(
      buildXrayOperatorConfig({
        instanceName: "kirill-msk",
        subscriptionUrl: SUBSCRIPTION_URL,
        userAgent: "v2rayNG/1.9.6",
      }).subscriptions?.[0]?.userAgent,
    ).toBe("v2rayNG/1.9.6");
    expect(
      withSubscriptionUserAgent(build(), "v2rayNG/1.9.6").subscriptions?.[0]
        ?.userAgent,
    ).toBe("v2rayNG/1.9.6");
  });

  it("refuses a Happ agent the provider's sweep would punish, as vctl does", () => {
    expect(() => assertUsableSubscriptionUserAgent("Happ/1.0")).toThrow(/Happ/);
    expect(() => assertUsableSubscriptionUserAgent("happ")).toThrow();
    expect(() =>
      assertUsableSubscriptionUserAgent("Happ/1.0/OpenWrt/1"),
    ).toThrow();
    expect(
      assertUsableSubscriptionUserAgent("Happ/4.2.1/Windows/2609041405606"),
    ).toBe("Happ/4.2.1/Windows/2609041405606");
    expect(assertUsableSubscriptionUserAgent(" v2rayNG/1.9.5 ")).toBe(
      "v2rayNG/1.9.5",
    );
  });

  it("refuses a literal claiming to be the router's own signed agent", () => {
    // Only the router itself can make a VectraRouter/… agent (vctl
    // signed_ua.go). An operator or backend stating one as a literal would be
    // a forgery, so it is refused the same way a fake Happ agent is.
    expect(() =>
      assertUsableSubscriptionUserAgent("VectraRouter/0.4.0 (AX3000T)"),
    ).toThrow(/router's own signed agent/);
    // Case-insensitively.
    expect(() =>
      assertUsableSubscriptionUserAgent("vectrarouter/0.4.0"),
    ).toThrow(/router's own signed agent/);
  });
});

describe("withFreshPrimarySubscription (a partner claim)", () => {
  it("replaces the whole subscription and keeps the operator's blocks", () => {
    const previousOwner = xrayDesiredConfigSchema.parse({
      ...withUiLock(build("⚡Extreme Польша 🇵🇱"), true),
      inbounds: {
        tproxy: { ...build().inbounds.tproxy, killSwitch: true },
      },
      subscriptions: [
        {
          ...build("⚡Extreme Польша 🇵🇱").subscriptions![0]!,
          headers: { Authorization: "Bearer previous-owner" },
          remark: "previous",
        },
        {
          id: "secondary",
          url: "https://other.example.test/sub",
          enabled: true,
        },
      ],
    });

    const next = withFreshPrimarySubscription(previousOwner, {
      subscriptionUrl: "https://sub.example.test/api/sub/NEW_OWNER",
      userAgent: "v2rayNG/1.9.6",
    });

    expect(next.subscriptions).toEqual([
      {
        id: "primary",
        url: "https://sub.example.test/api/sub/NEW_OWNER",
        enabled: true,
        userAgent: "v2rayNG/1.9.6",
        mode: "json",
        entryIndex: 0,
      },
    ]);
    expect(next.ui).toEqual({ lock: true });
    expect(next.inbounds.tproxy.killSwitch).toBe(true);
    expect(next.process).toEqual(previousOwner.process);
  });

  it("omits userAgent entirely for the router's own signed agent (null)", () => {
    // The backend states no literal: the router fetches with its own agent,
    // and the panel must not invent one in its place — the key is simply
    // absent, never emitted empty or defaulted to the fleet agent.
    const previousOwner = withFreshPrimarySubscription(build(), {
      subscriptionUrl: "https://sub.example.test/api/sub/OLD_OWNER",
      userAgent: "v2rayNG/1.9.6",
    });

    const next = withFreshPrimarySubscription(previousOwner, {
      subscriptionUrl: "https://sub.example.test/api/sub/NEW_OWNER",
      userAgent: null,
    });

    expect(next.subscriptions).toEqual([
      {
        id: "primary",
        url: "https://sub.example.test/api/sub/NEW_OWNER",
        enabled: true,
        mode: "json",
        entryIndex: 0,
      },
    ]);
    expect(next.subscriptions![0]).not.toHaveProperty("userAgent");
  });

  it("still refuses a cleartext URL", () => {
    expect(() =>
      withFreshPrimarySubscription(build(), {
        subscriptionUrl: "http://sub.example.test/x",
        userAgent: null,
      }),
    ).toThrow(/https/);
  });

  it("still refuses a literal claiming to be the router's own agent", () => {
    expect(() =>
      withFreshPrimarySubscription(build(), {
        subscriptionUrl: "https://sub.example.test/x",
        userAgent: "VectraRouter/0.4.0 (AX3000T)",
      }),
    ).toThrow(/router's own signed agent/);
  });
});

describe("operator UI lock", () => {
  it("emits ui.lock only while locked", () => {
    const locked = withUiLock(build(), true);
    expect(locked.ui).toEqual({ lock: true });
    expect(isUiLocked(locked)).toBe(true);

    // Unlocked = no `ui` key at all: a vctl without the block decodes with
    // DisallowUnknownFields and would refuse even { lock: false }.
    const unlocked = withUiLock(locked, false);
    expect(unlocked).not.toHaveProperty("ui");
    expect(isUiLocked(unlocked)).toBe(false);
    expect(build()).not.toHaveProperty("ui");
  });

  it("stays inside the keys the Go config declares", () => {
    const locked = withUiLock(build(), true) as unknown as Record<string, unknown>;
    expect(
      Object.keys(locked).filter((k) => ![...GO_ROOT_KEYS, "ui"].includes(k)),
    ).toEqual([]);
    expect(Object.keys(locked.ui as object)).toEqual(["lock"]);
  });
});
