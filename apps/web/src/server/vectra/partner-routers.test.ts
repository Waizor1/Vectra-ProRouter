import { createHash } from "node:crypto";
import { type SQL } from "drizzle-orm";
import { PgDialect } from "drizzle-orm/pg-core";
import { describe, expect, it, vi } from "vitest";
vi.mock("~/env", () => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "fake-local-test-secrets-key-only-123456",
    VECTRA_CONNECT_WEBHOOK_URL: "https://fake.example/hooks",
    VECTRA_CONNECT_WEBHOOK_SECRET: "fake-webhook-secret",
    // A second partner with its own webhook: its routers' events are queued.
    VECTRA_PARTNERS: JSON.stringify([
      {
        id: "bloopcat",
        brand: "bloopcat",
        label: "BloopCat",
        secrets: ["fake-bloopcat-partner-secret-0123456789ab"],
        webhookUrl: "https://bloopcat.example/hooks",
        webhookSecret: "fake-bloopcat-webhook-secret-0123456789",
      },
    ]),
  },
}));
vi.mock("~/server/db", () => ({ db: {} }));
import {
  jobs,
  routers,
  routerInventorySnapshots,
  partnerWebhooks,
} from "@vectra/db";
import { readFileSync } from "node:fs";
import {
  routerCheckInRequestSchema,
  routerConnectTelemetrySchema,
} from "@vectra/contracts";
import { createFakeDb } from "./testing/fake-db";
import { keyedDigest } from "./secrets";
import { buildPartnerRequestHeaders } from "./partner-request-signature";
import { type PartnerApiDeps } from "./partner-api";
import { createMemoryIdempotency } from "./testing/memory-idempotency";
import {
  cancelPartnerActionWithDb,
  handlePartnerRouterAction,
  handlePartnerRouterActionCancel,
  handlePartnerRoutersRead,
  heldVerdictWithDb,
  projectPartnerRouter,
  queuePartnerActionWithDb,
  readPartnerRoutersWithDb,
  type PartnerRoutersDeps,
} from "./partner-routers";
import {
  notifyPartnerActionResultWithDb,
  notifyPartnerCheckInWithDb,
  reportedPartnerTransitions,
  sweepPartnerOfflineWithDb,
} from "./partner-router-events";
const dialect = new PgDialect();
const ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const OTHER = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f32";
const NOW = new Date("2026-10-01T15:00:00Z");
const SECRET = "fake-partner-secret";
const JOB = "1d2c3b4a-5f6e-4d7c-9b8a-0f1e2d3c4b5a";
function router(overrides = {}) {
  return {
    id: ID,
    ownerRef: "acct-42",
    releasedAt: null,
    claimedAt: new Date(NOW.getTime() - 1000),
    engineMode: "xray-direct",
    approvedAt: NOW,
    lastAppliedRevisionId: "revision-1",
    lastSeenAt: NOW,
    deviceIdentifier: "vectra-test",
    model: "Xiaomi AX3000T",
    status: "active",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    ...overrides,
  } as unknown as typeof routers.$inferSelect;
}
function inventory(overrides = {}) {
  return {
    routerId: ID,
    createdAt: NOW,
    payload: {
      engineMode: "xray-direct",
      controllerVersion: "0.5.0",
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      resources: { memoryAvailableMb: 70, memoryTotalMb: 256 },
      ...overrides,
    },
  } as unknown as typeof routerInventorySnapshots.$inferSelect;
}
function action(overrides = {}) {
  return {
    routerId: ID,
    ownerRef: "acct-42",
    action: "restart_vpn" as const,
    params: {},
    ...overrides,
  };
}
function keyedDigestOf(input: unknown) {
  return keyedDigest("partner-action-v1", JSON.stringify(input));
}
function deps(): PartnerRoutersDeps {
  const memory = createMemoryIdempotency();
  const nonces = new Set<string>();
  const api: PartnerApiDeps = {
    secret: SECRET,
    reserveNonce: async (id) => {
      if (nonces.has(id)) return false;
      nonces.add(id);
      return true;
    },
    now: () => NOW,
    claim: vi.fn(),
    unbind: vi.fn(),
    reserveIdempotent: memory.reserveIdempotent,
    finishIdempotent: memory.finishIdempotent,
  };
  return {
    api,
    read: vi.fn(async () => []),
    action: vi.fn(async () => ({
      ok: true,
      status: 202,
      body: { actionId: ID, state: "queued" },
    })),
    cancel: vi.fn(async () => ({
      ok: true,
      status: 200,
      body: { actionId: JOB, state: "cancelled" },
    })),
  };
}
function request(body: unknown = action(), id = ID, key = "test-key") {
  const raw = JSON.stringify(body);
  return new Request(`https://fake.example/api/partner/routers/${id}/actions`, {
    method: "POST",
    body: raw,
    headers: {
      ...buildPartnerRequestHeaders(
        SECRET,
        "POST",
        `https://fake.example/api/partner/routers/${id}/actions`,
        raw,
        key,
        NOW.getTime(),
      ),
      "Idempotency-Key": key,
    },
  });
}
// The router reports what it supports for its current owner; the panel never
// assumes a capability (see the "nothing reported" snapshot test).
function reportingInventory() {
  return inventory({
    connect: {
      ownerRef: "acct-42",
      capabilities: ["restart_vpn", "refresh_subscription"],
    },
  });
}
function queueDb(row = router()) {
  return createFakeDb({
    selects: [[routerInventorySnapshots, [[reportingInventory()]]]],
    updateReturns: [[routers, [[row]]]],
  });
}

describe("partner snapshot ownership and measured truth", () => {
  it("keeps absent measurements unknown even when heartbeat is online", () => {
    const snapshot = projectPartnerRouter(router(), inventory(), NOW);
    expect(snapshot.online).toBe(true);
    expect(snapshot.verdict).toBeNull();
    expect(snapshot.exitCountry).toBeNull();
    expect(snapshot.lanClients).toBeNull();
    // Nothing reported = nothing supported: the panel never invents actions.
    expect(snapshot.capabilities).toEqual([]);
  });
  it("only projects validated measurements and never raw credentials", () => {
    const connect = routerConnectTelemetrySchema.parse({
      verdict: "reserve",
      exitCountry: "DE",
      lanClients: 3,
      uptimeSec: 50,
      wifi: [{ band: "5G", ssid: "Guest", password: "should-be-stripped" }],
    });
    const snapshot = projectPartnerRouter(
      router(),
      inventory({
        connect,
        rawSnapshot: { password: "do-not-expose" },
        devicePublicKey: "not-public-here",
      }),
      NOW,
    );
    expect(snapshot).toMatchObject({
      verdict: "reserve",
      exitCountry: "DE",
      lanClients: 3,
      uptimeSec: 50,
      wifi: [{ band: "5G", ssid: "Guest" }],
    });
    expect(JSON.stringify(snapshot)).not.toMatch(
      /should-be-stripped|do-not-expose|not-public-here/,
    );
  });
  // The row is rewritten only on a material change or the heartbeat: the
  // card's uptime runs on from the row's time instead of standing still.
  it("advances uptime from the time of the stored row", () => {
    const row = {
      ...inventory({ connect: routerConnectTelemetrySchema.parse({ uptimeSec: 50 }) }),
      createdAt: new Date(NOW.getTime() - 120_000),
    };
    const snapshot = projectPartnerRouter(
      router({ claimedAt: new Date(NOW.getTime() - 3_600_000) }),
      row,
      NOW,
    );
    expect(snapshot.uptimeSec).toBe(170);
  });
  // An offline router's uptime stops at its last check-in: aging it to now
  // showed days of uptime for a router switched off (review, 2026-10-02).
  it("stops the uptime at the router's last check-in", () => {
    const row = {
      ...inventory({ connect: routerConnectTelemetrySchema.parse({ uptimeSec: 50 }) }),
      createdAt: new Date(NOW.getTime() - 3_600_000),
    };
    const snapshot = projectPartnerRouter(
      router({
        claimedAt: new Date(NOW.getTime() - 7_200_000),
        lastSeenAt: new Date(NOW.getTime() - 3_000_000),
      }),
      row,
      NOW,
    );
    expect(snapshot.uptimeSec).toBe(650);
  });
  it("does not expose a previous owner's telemetry", () => {
    expect(
      projectPartnerRouter(
        router({ claimedAt: new Date(NOW.getTime() + 1000) }),
        inventory({
          connect: { wifi: [{ band: "5G", ssid: "old-owner" }], verdict: "ok" },
        }),
        NOW,
      ).wifi,
    ).toBeNull();
  });
  it("returns no capabilities for unsupported hardware or passwall", () => {
    expect(
      projectPartnerRouter(router({ engineMode: "passwall" }), inventory(), NOW)
        .capabilities,
    ).toEqual([]);
    expect(
      projectPartnerRouter(
        router({ boardName: "unknown", target: "unknown" }),
        inventory({ boardName: "unknown", target: "unknown" }),
        NOW,
      ).capabilities,
    ).toEqual([]);
  });
  // vctl r15–r18 left the verdict out of every other check-in after a
  // reboot (1111, 2026-10-05): a short gap keeps the last measured verdict
  // and its country; a long one, or one from before the claim, is unknown.
  it("keeps the last measured verdict over a short gap without one", () => {
    const owned = router({ claimedAt: new Date(NOW.getTime() - 3_600_000) });
    const gapless = inventory({ connect: { ownerRef: "acct-42", uptimeSec: 74 } });
    const measuredRow = {
      ...inventory({
        connect: { ownerRef: "acct-42", verdict: "ok", exitCountry: "PL" },
      }),
      createdAt: new Date(NOW.getTime() - 45_000),
    };
    const held = { inventory: measuredRow, unknownSince: new Date(NOW.getTime() - 30_000) };
    expect(projectPartnerRouter(owned, gapless, NOW, held)).toMatchObject({
      verdict: "ok",
      exitCountry: "PL",
      uptimeSec: 74,
    });
    const long = { ...held, unknownSince: new Date(NOW.getTime() - 181_000) };
    expect(projectPartnerRouter(owned, gapless, NOW, long)).toMatchObject({
      verdict: null,
      exitCountry: null,
    });
    const beforeClaim = router({ claimedAt: new Date(NOW.getTime() - 10_000) });
    expect(projectPartnerRouter(beforeClaim, gapless, NOW, held).verdict).toBeNull();
    // After a location switch the held country may be the old route's.
    const moved = inventory({
      connect: {
        ownerRef: "acct-42",
        location: { mode: "entry", entryId: "e2" },
      },
    });
    expect(projectPartnerRouter(owned, moved, NOW, held)).toMatchObject({
      verdict: "ok",
      exitCountry: null,
    });
    // A row the router reported for another owner is never held.
    const foreign = {
      ...held,
      inventory: {
        ...measuredRow,
        payload: {
          ...measuredRow.payload,
          connect: { ownerRef: "acct-7", verdict: "ok", exitCountry: "PL" },
        },
      } as typeof measuredRow,
    };
    expect(projectPartnerRouter(owned, gapless, NOW, foreign).verdict).toBeNull();
    const down = inventory({ connect: { ownerRef: "acct-42", verdict: "down" } });
    expect(projectPartnerRouter(owned, down, NOW, held)).toMatchObject({
      verdict: "down",
      exitCountry: null,
    });
  });
  it("reads the held verdict from the snapshot before the gap", async () => {
    const owned = router({ claimedAt: new Date(NOW.getTime() - 3_600_000) });
    const latest = inventory({ connect: { ownerRef: "acct-42" } });
    const measuredRow = {
      ...inventory({ connect: { ownerRef: "acct-42", verdict: "ok" } }),
      createdAt: new Date(NOW.getTime() - 90_000),
    };
    const fake = createFakeDb({
      selects: [
        [routers, [[owned], [owned]]],
        [
          routerInventorySnapshots,
          [[latest], [measuredRow], [{ createdAt: new Date(NOW.getTime() - 45_000) }]],
        ],
      ],
    });
    const [snapshot] = await readPartnerRoutersWithDb(fake.db as never, "acct-42", ID, NOW);
    expect(snapshot?.verdict).toBe("ok");
    // A snapshot with a verdict needs no second look.
    const plain = createFakeDb({
      selects: [
        [routers, [[owned], [owned]]],
        [
          routerInventorySnapshots,
          [[inventory({ connect: { ownerRef: "acct-42", verdict: "down" } })], [measuredRow]],
        ],
      ],
    });
    const [own] = await readPartnerRoutersWithDb(plain.db as never, "acct-42", ID, NOW);
    expect(own?.verdict).toBe("down");
    expect(plain.calls.filter((c) => c.table === routerInventorySnapshots)).toHaveLength(1);
  });
  it("looks up a held verdict only for routers older than r19, in a bounded window", async () => {
    const queries: Array<{ sql: string; params: unknown[] }> = [];
    const rows: unknown[][] = [
      [
        {
          ...inventory({ connect: { ownerRef: "acct-42", verdict: "ok" } }),
          createdAt: new Date(NOW.getTime() - 60_000),
        },
      ],
      [{ createdAt: new Date(NOW.getTime() - 30_000) }],
    ];
    const chain = {
      where(condition: SQL) {
        queries.push(dialect.sqlToQuery(condition));
        return chain;
      },
      orderBy: () => chain,
      limit: () => Promise.resolve(rows.shift() ?? []),
    };
    const client = { select: () => ({ from: () => chain }) };
    const gap = inventory({ connect: { ownerRef: "acct-42" } });
    const held = await heldVerdictWithDb(client as never, ID, gap, NOW);
    expect(held?.unknownSince).toEqual(new Date(NOW.getTime() - 30_000));
    expect(queries).toHaveLength(2);
    // Never further back than the hold plus one heartbeat (15 min) and a minute.
    const since = new Date(NOW.getTime() - 180_000 - 15 * 60_000 - 60_000);
    expect(queries[0]!.sql).toMatch(/"created_at" > \$\d+/);
    expect(queries[0]!.params).toContainEqual(since.toISOString());
    expect(queries[1]!.sql).toMatch(/"created_at" <= \$\d+/);
    // r19 holds its own verdict; no Connect telemetry or a verdict present:
    // nothing to look up.
    queries.length = 0;
    for (const latest of [
      inventory({
        controllerRuntimeVersion: "0.7.0-r19",
        connect: { ownerRef: "acct-42" },
      }),
      inventory(),
      inventory({ connect: { ownerRef: "acct-42", verdict: "down" } }),
    ])
      expect(await heldVerdictWithDb(client as never, ID, latest, NOW)).toBeNull();
    expect(queries).toHaveLength(0);
  });
  it("filters foreign, released, and wrong-target rows even with an injected provider", async () => {
    const fake = createFakeDb({
      selects: [
        [
          routers,
          [
            [
              router({ ownerRef: "foreign" }),
              router({ releasedAt: NOW }),
              router({ id: OTHER }),
            ],
          ],
        ],
      ],
    });
    expect(
      await readPartnerRoutersWithDb(fake.db as never, "acct-42", ID, NOW),
    ).toEqual([]);
  });
  it("requires HMAC for GET and gives foreign owner a 404", async () => {
    const d = deps();
    expect(
      (
        await handlePartnerRoutersRead(
          new Request(
            "https://fake.example/api/partner/routers/6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31?ownerRef=foreign",
          ),
          ID,
          d,
        )
      ).status,
    ).toBe(401);
    expect(d.read).not.toHaveBeenCalled();
    const signed = new Request(
      "https://fake.example/api/partner/routers/6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31?ownerRef=foreign",
      {
        headers: buildPartnerRequestHeaders(
          SECRET,
          "GET",
          "https://fake.example/api/partner/routers/6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31?ownerRef=foreign",
          "",
          "",
          NOW.getTime(),
        ),
      },
    );
    expect((await handlePartnerRoutersRead(signed, ID, d)).status).toBe(404);
  });
});

describe("signed actions", () => {
  it("binds URL to the signed routerId", async () => {
    const d = deps();
    expect(
      (await handlePartnerRouterAction(request(action(), OTHER), OTHER, d))
        .status,
    ).toBe(400);
    expect(d.action).not.toHaveBeenCalled();
  });
  it("rejects body tampering, missing id and missing idempotency key", async () => {
    const d = deps();
    const r = request();
    const tampered = new Request(r.url, {
      method: "POST",
      headers: r.headers,
      body: JSON.stringify(action({ ownerRef: "foreign" })),
    });
    expect((await handlePartnerRouterAction(tampered, ID, d)).status).toBe(401);
    expect(
      (
        await handlePartnerRouterAction(
          request({ ownerRef: "acct-42", action: "restart_vpn", params: {} }),
          ID,
          d,
        )
      ).status,
    ).toBe(400);
    expect(
      (await handlePartnerRouterAction(request(action(), ID, ""), ID, d))
        .status,
    ).toBe(400);
    expect(d.action).not.toHaveBeenCalled();
  });
  it("replays byte-identical retries and rejects cross-owner and cross-router key reuse", async () => {
    const d = deps();
    expect((await handlePartnerRouterAction(request(), ID, d)).status).toBe(
      202,
    );
    expect(
      (await handlePartnerRouterAction(request(), ID, d)).headers.get(
        "Idempotent-Replayed",
      ),
    ).toBe("true");
    expect(
      (
        await handlePartnerRouterAction(
          request(action({ ownerRef: "other" })),
          ID,
          d,
        )
      ).status,
    ).toBe(422);
    expect(
      (
        await handlePartnerRouterAction(
          request(action({ routerId: OTHER }), OTHER),
          OTHER,
          d,
        )
      ).status,
    ).toBe(422);
    expect(d.action).toHaveBeenCalledTimes(1);
  });
  it("queues only typed native jobs with bound owner and stable actionId", async () => {
    const fake = queueDb();
    const result = await queuePartnerActionWithDb(
      fake.db as never,
      action(),
      "key",
      NOW,
    );
    expect(result.status).toBe(202);
    expect(fake.inserts(jobs)[0]).toMatchObject({
      type: "reload_xray_outbound",
      payload: {
        ownerRef: "acct-42",
        origin: "partner_action",
        action: "restart_vpn",
        actionId: result.body.actionId,
      },
    });
    expect(fake.inserts(jobs)[0]).not.toHaveProperty("command");
  });
  it("refreshes the signed subscription through the existing native job", async () => {
    const fake = queueDb();
    expect(
      (
        await queuePartnerActionWithDb(
          fake.db as never,
          action({ action: "refresh_subscription" }),
          "key",
          NOW,
        )
      ).status,
    ).toBe(202);
    expect(fake.inserts(jobs)[0]?.type).toBe("refresh_xray_subscriptions");
  });
  it("foreign owner, released router and unready claim cannot queue jobs", async () => {
    for (const [row, status] of [
      [router({ ownerRef: "foreign" }), 404],
      [router({ releasedAt: NOW }), 404],
      [router({ lastAppliedRevisionId: null }), 409],
    ] as const) {
      const fake = queueDb(row);
      expect(
        (await queuePartnerActionWithDb(fake.db as never, action(), "key", NOW))
          .status,
      ).toBe(status);
      expect(fake.inserts(jobs)).toEqual([]);
    }
  });
  it("returns not_supported for every unimplemented action", async () => {
    for (const name of [
      "select_entry",
      "set_rules",
      "set_service",
      "set_wifi",
      "reboot",
      "update_now",
      "set_auto_update",
      "set_port_forwards",
    ]) {
      const fake = queueDb();
      expect(
        await queuePartnerActionWithDb(
          fake.db as never,
          action({ action: name }),
          "key",
          NOW,
        ),
      ).toMatchObject({ status: 409, body: { error: "not_supported" } });
      expect(fake.inserts(jobs)).toEqual([]);
    }
  });
  it("rejects extra parameters on native operations", async () => {
    const fake = queueDb();
    expect(
      (
        await queuePartnerActionWithDb(
          fake.db as never,
          action({ params: { command: "bad" } }),
          "key",
          NOW,
        )
      ).status,
    ).toBe(400);
    expect(fake.inserts(jobs)).toEqual([]);
  });
});

// Services telemetry v2: the per-service server picker. The router says
// where each service runs, whether the owner chose it, which cached
// locations carry it, and whether the owner's choice no longer runs.
// The check-in vctl puts on the wire (see tests/contract/vctl-check-in).
const checkInFixture: unknown = JSON.parse(
  readFileSync(
    new URL(
      "../../../../../router/vectra-controller-pro/testdata/contract/check-in-request.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
describe("services telemetry v2", () => {
  const A = "a".repeat(64);
  const B = "b".repeat(64);
  const services = [
    { id: "ai", entryId: B, auto: true, entries: [A, B] },
    { id: "telegram", entryId: null, auto: false, entries: [A, B] },
    { id: "tiktok", entryId: null, auto: false, entries: [B], stale: true },
    { id: "youtube", entryId: A, auto: false, entries: [] },
  ];
  function servicesInventory(capabilities: string[]) {
    return inventory({
      connect: routerConnectTelemetrySchema.parse({
        ownerRef: "acct-42",
        capabilities,
        entries: [
          { id: A, name: "🇩🇪 Германия", country: null },
          { id: B, name: "🇷🇺🇰🇿 Казахстан", country: null },
        ],
        services,
      }),
    });
  }
  function servicesDb(capabilities: string[]) {
    return createFakeDb({
      selects: [
        [routerInventorySnapshots, [[servicesInventory(capabilities)]]],
      ],
      updateReturns: [[routers, [[router()]]]],
    });
  }
  it("keeps auto, entries and stale through the contract and the snapshot", () => {
    const snapshot = projectPartnerRouter(
      router(),
      servicesInventory(["set_service", "set_service_auto"]),
      NOW,
    );
    expect(snapshot.services).toEqual(services);
    expect(snapshot.capabilities).toEqual(["set_service", "set_service_auto"]);
  });
  it("still accepts a router that reports only id and entryId", () => {
    const parsed = routerConnectTelemetrySchema.parse({
      services: [{ id: "youtube", entryId: null }],
    });
    expect(parsed.services).toEqual([{ id: "youtube", entryId: null }]);
  });
  it("bounds the carriers and their ids", () => {
    for (const entries of [
      Array.from({ length: 201 }, (_, i) => `e${i}`),
      ["../bad"],
      ["a".repeat(65)],
      [""],
    ])
      expect(
        routerConnectTelemetrySchema.safeParse({
          services: [{ id: "youtube", entryId: null, entries }],
        }).success,
      ).toBe(false);
    expect(
      routerConnectTelemetrySchema.safeParse({
        services: [
          {
            id: "youtube",
            entryId: null,
            entries: Array.from({ length: 200 }, (_, i) => `e${i}`),
          },
        ],
      }).success,
    ).toBe(true);
  });
  it("drops a capability the panel does not know instead of the check-in", () => {
    expect(
      routerConnectTelemetrySchema.parse({
        capabilities: ["set_service", "some_future_flag", "set_service_auto"],
      }).capabilities,
    ).toEqual(["set_service", "set_service_auto"]);
    const checkIn = structuredClone(checkInFixture) as {
      inventory: { connect: Record<string, unknown> };
    };
    checkIn.inventory.connect.capabilities = ["set_rules", "some_future_flag"];
    const parsed = routerCheckInRequestSchema.safeParse(checkIn);
    expect(parsed.success && parsed.data.inventory.connect?.capabilities).toEqual(
      ["set_rules"],
    );
    // Still bounded: a flood is refused.
    expect(
      routerConnectTelemetrySchema.safeParse({
        capabilities: Array.from({ length: 65 }, (_, i) => `f${i}`),
      }).success,
    ).toBe(false);
  });
  it("queues set_service :auto for a router that understands it", async () => {
    const fake = servicesDb(["set_service", "set_service_auto"]);
    const result = await queuePartnerActionWithDb(
      fake.db as never,
      action({
        action: "set_service",
        params: { service: "ai", entryId: ":auto" },
      }),
      "key",
      NOW,
    );
    expect(result.status).toBe(202);
    expect(fake.inserts(jobs)[0]).toMatchObject({
      type: "connect_router_action",
      payload: {
        action: "set_service",
        params: { service: "ai", entryId: ":auto" },
      },
    });
  });
  it("refuses :auto to a router that does not advertise set_service_auto", async () => {
    const fake = servicesDb(["set_service"]);
    expect(
      await queuePartnerActionWithDb(
        fake.db as never,
        action({
          action: "set_service",
          params: { service: "ai", entryId: ":auto" },
        }),
        "key",
        NOW,
      ),
    ).toMatchObject({ status: 409, body: { error: "not_supported" } });
    expect(fake.inserts(jobs)).toEqual([]);
  });
  it("queues a one-band set_wifi for a router that advertises set_wifi_band", async () => {
    const fake = servicesDb(["set_wifi", "set_wifi_band"]);
    const result = await queuePartnerActionWithDb(
      fake.db as never,
      action({
        action: "set_wifi",
        params: { ssid: "Fake 5G", password: "fake-guest-pass-123", band: "5g" },
      }),
      "key",
      NOW,
    );
    expect(result.status).toBe(202);
    // The band travels in the sealed parameters, with the password.
    const { hydratePartnerJobPayload } =
      await import("./partner-router-secrets");
    const job = fake.inserts(jobs)[0] as unknown as typeof jobs.$inferSelect;
    expect(hydratePartnerJobPayload(job, "acct-42")).toMatchObject({
      action: "set_wifi",
      params: { ssid: "Fake 5G", band: "5g" },
    });
    expect(
      projectPartnerRouter(
        router(),
        servicesInventory(["set_wifi", "set_wifi_band"]),
        NOW,
      ).capabilities,
    ).toEqual(["set_wifi", "set_wifi_band"]);
  });
  it("refuses a band to a router without set_wifi_band, still takes all bands", async () => {
    const band = servicesDb(["set_wifi"]);
    expect(
      await queuePartnerActionWithDb(
        band.db as never,
        action({
          action: "set_wifi",
          params: { ssid: "Fake 2G", password: "fake-guest-pass-123", band: "2g" },
        }),
        "key",
        NOW,
      ),
    ).toMatchObject({ status: 409, body: { error: "not_supported" } });
    expect(band.inserts(jobs)).toEqual([]);
    const all = servicesDb(["set_wifi"]);
    expect(
      (
        await queuePartnerActionWithDb(
          all.db as never,
          action({
            action: "set_wifi",
            params: { ssid: "Fake", password: "fake-guest-pass-123" },
          }),
          "key",
          NOW,
        )
      ).status,
    ).toBe(202);
  });
  it("never takes :auto for a location", async () => {
    const fake = servicesDb(["select_entry", "set_service_auto"]);
    expect(
      (
        await queuePartnerActionWithDb(
          fake.db as never,
          action({ action: "select_entry", params: { entryId: ":auto" } }),
          "key",
          NOW,
        )
      ).status,
    ).toBe(400);
    expect(fake.inserts(jobs)).toEqual([]);
  });
});

describe("reported events", () => {
  it("never derives VPN status from heartbeat, job success, or missing measurements", () => {
    expect(reportedPartnerTransitions(null, inventory().payload)).toEqual([]);
    expect(
      reportedPartnerTransitions(
        inventory({ connect: { verdict: "ok" } }).payload,
        inventory().payload,
      ),
    ).toEqual([]);
  });
  it("compares with the last measured verdict across check-ins without one", () => {
    // down → (no verdict: vctl's measurement was stale) → ok is a recovery.
    expect(
      reportedPartnerTransitions(
        inventory().payload,
        inventory({ connect: { verdict: "ok" } }).payload,
        "direct",
      ),
    ).toEqual([{ event: "router.vpn_up", detail: { verdict: "ok" } }]);
    expect(
      reportedPartnerTransitions(
        inventory().payload,
        inventory({ connect: { verdict: "ok" } }).payload,
        "ok",
      ),
    ).toEqual([]);
  });
  it("reports measured VPN transitions and observed runtime updates", () => {
    expect(
      reportedPartnerTransitions(
        inventory({ connect: { verdict: "ok" } }).payload,
        inventory({ controllerVersion: "0.6.0", connect: { verdict: "leak" } })
          .payload,
      ),
    ).toEqual([
      { event: "router.vpn_down", detail: { verdict: "leak" } },
      { event: "router.updated", detail: { from: "0.5.0", to: "0.6.0" } },
    ]);
  });
  it("queues terminal action results only for the owner that queued them", async () => {
    const fake = createFakeDb({ updateReturns: [[routers, [[router()]]]] });
    const job = {
      routerId: ID,
      payload: { origin: "partner_action", ownerRef: "acct-42", actionId: ID },
    } as never;
    await notifyPartnerActionResultWithDb(fake.db as never, {
      job,
      ownerRef: "foreign",
      status: "success",
    });
    await notifyPartnerActionResultWithDb(fake.db as never, {
      job,
      ownerRef: "acct-42",
      status: "accepted",
    });
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
    await notifyPartnerActionResultWithDb(fake.db as never, {
      job,
      ownerRef: "acct-42",
      status: "success",
    });
    expect(fake.inserts(partnerWebhooks)[0]).toMatchObject({
      event: "router.action",
      payload: { detail: { actionId: ID, state: "applied" } },
    });
  });
  it("offline CAS losing to a fresh heartbeat emits no event", async () => {
    const stale = router({ lastSeenAt: new Date(NOW.getTime() - 181000) });
    const fake = createFakeDb({
      selects: [[routers, [[stale]]]],
      updateReturns: [[routers, [[]]]],
    });
    await sweepPartnerOfflineWithDb(fake.db as never, NOW);
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
  });
});

describe("partner action failure reason", () => {
  // The router says why an action failed (service_path_unavailable, …); the
  // owner's journal got only "failed" (1111, 2026-10-02). Only a plain code
  // passes: nothing the router echoes, never a credential.
  it("carries a plain failure code into the webhook detail, nothing else", async () => {
    const job = {
      id: "00000000-0000-4000-8000-000000000099",
      routerId: ID,
      payload: { origin: "partner_action", ownerRef: "acct-42", actionId: "00000000-0000-4000-8000-000000000099" },
    } as unknown as typeof jobs.$inferSelect;
    for (const [code, want] of [
      ["service_path_unavailable", "service_path_unavailable"],
      ["dns_v6_unavailable", "dns_v6_unavailable"],
      ["wifi password hunter2", undefined],
      [undefined, undefined],
    ] as const) {
      const outbox = createFakeDb({ updateReturns: [[routers, [[router()]]]] });
      await notifyPartnerActionResultWithDb(outbox.db as never, {
        job,
        ownerRef: "acct-42",
        status: "failure",
        code,
      });
      const row = outbox.inserts(partnerWebhooks)[0] as
        | { payload?: { detail?: Record<string, unknown> } }
        | undefined;
      const detail = row?.payload?.detail;
      expect(detail?.state).toBe("failed");
      expect(detail?.detail).toBe(want);
    }
  });
});

// The router.action result follows the partner that queued the action: a job
// without a partner is Vectra Connect's, and a router that changed hands since
// is not told about the other partner's action.
describe("a partner action's result stays with its partner", () => {
  const ACTION = "00000000-0000-4000-8000-000000000098";
  function jobOf(partnerId?: string) {
    return {
      id: ACTION,
      routerId: ID,
      payload: {
        origin: "partner_action",
        ownerRef: "acct-42",
        actionId: ACTION,
        ...(partnerId ? { partnerId } : {}),
      },
    } as unknown as typeof jobs.$inferSelect;
  }
  async function notify(job: typeof jobs.$inferSelect, row: ReturnType<typeof router>) {
    const outbox = createFakeDb({ updateReturns: [[routers, [[row]]]] });
    await notifyPartnerActionResultWithDb(outbox.db as never, {
      job,
      ownerRef: "acct-42",
      status: "success",
    });
    return outbox.inserts(partnerWebhooks);
  }

  it("is told to the partner that owns both the job and the router", async () => {
    expect(await notify(jobOf("bloopcat"), router({ partnerId: "bloopcat" }))).toHaveLength(1);
    expect(await notify(jobOf("vectra"), router({ partnerId: "vectra" }))).toHaveLength(1);
    // before there were partners: no partner on either side is Vectra Connect's.
    expect(await notify(jobOf(), router({ partnerId: null }))).toHaveLength(1);
  });

  it("is not told when the router now belongs to another partner", async () => {
    expect(await notify(jobOf("bloopcat"), router({ partnerId: "vectra" }))).toEqual([]);
    expect(await notify(jobOf("bloopcat"), router({ partnerId: null }))).toEqual([]);
    expect(await notify(jobOf(), router({ partnerId: "bloopcat" }))).toEqual([]);
  });

  it("locks the router of the job's partner only", async () => {
    for (const [job, params, isNull] of [
      [jobOf("bloopcat"), ["bloopcat"], false],
      [jobOf(), ["vectra"], true],
    ] as const) {
      const base = createFakeDb({ updateReturns: [[routers, [[router()]]]] });
      const queries: Array<{ sql: string; params: unknown[] }> = [];
      const db = {
        ...base.db,
        update: (table: unknown) => ({
          set: (set: Record<string, unknown>) => {
            const inner = base.db.update(table).set(set);
            return {
              where: (condition: SQL) => {
                queries.push(dialect.sqlToQuery(condition));
                return inner.where();
              },
            };
          },
        }),
        transaction: async <T>(run: (tx: unknown) => Promise<T>) => run(db),
      };
      await notifyPartnerActionResultWithDb(db as never, {
        job,
        ownerRef: "acct-42",
        status: "success",
      });
      expect(queries).toHaveLength(1);
      expect(queries[0]!.sql).toMatch(/"partner_id" = \$\d+/);
      expect(queries[0]!.params).toEqual(expect.arrayContaining([...params]));
      if (isNull) expect(queries[0]!.sql).toMatch(/"partner_id" is null/);
      else expect(queries[0]!.sql).not.toMatch(/"partner_id" is null/);
    }
  });
});

// Each event is queued for the partner that owns the router, so the dispatcher
// can send it to that partner's address.
describe("a router's webhooks carry its partner", () => {
  const LONG_AGO = new Date(NOW.getTime() - 600_000);

  it.each([
    ["bloopcat", "bloopcat"],
    [null, "vectra"],
  ])("a check-in event of a router owned by %s goes to %s", async (owner, want) => {
    const previous = router({ partnerId: owner, lastSeenAt: LONG_AGO });
    const fake = createFakeDb({ updateReturns: [[routers, [[previous]]]] });
    await notifyPartnerCheckInWithDb(fake.db as never, previous, {} as never, NOW);
    expect(fake.inserts(partnerWebhooks)).toEqual([
      expect.objectContaining({ event: "router.online", partnerId: want }),
    ]);
  });

  it.each([
    ["bloopcat", "bloopcat"],
    [null, "vectra"],
  ])("the offline sweep of a router owned by %s tells %s", async (owner, want) => {
    const stale = router({ partnerId: owner, lastSeenAt: LONG_AGO });
    const fake = createFakeDb({
      selects: [[routers, [[stale]]]],
      updateReturns: [[routers, [[stale]]]],
    });
    await sweepPartnerOfflineWithDb(fake.db as never, NOW);
    expect(fake.inserts(partnerWebhooks)).toEqual([
      expect.objectContaining({ event: "router.offline", partnerId: want }),
    ]);
  });

  it("an action result goes to the partner that queued the action", async () => {
    const job = {
      id: JOB,
      routerId: ID,
      payload: {
        origin: "partner_action",
        ownerRef: "acct-42",
        actionId: JOB,
        partnerId: "bloopcat",
      },
    } as unknown as typeof jobs.$inferSelect;
    const fake = createFakeDb({
      updateReturns: [[routers, [[router({ partnerId: "bloopcat" })]]]],
    });
    await notifyPartnerActionResultWithDb(fake.db as never, {
      job,
      ownerRef: "acct-42",
      status: "success",
    });
    expect(fake.inserts(partnerWebhooks)).toEqual([
      expect.objectContaining({ event: "router.action", partnerId: "bloopcat" }),
    ]);
    // The partner is passed on: nothing is read to find it again.
    expect(fake.calls.filter((call) => call.kind === "select")).toEqual([]);
  });
});

describe("HTTP to native job to signed fake-provider webhook", () => {
  it("preserves the action and ownership across the complete local boundary", async () => {
    const fake = queueDb();
    const d = deps();
    d.action = (input, key) =>
      queuePartnerActionWithDb(fake.db as never, input, key, NOW);
    const accepted = await handlePartnerRouterAction(request(), ID, d);
    expect(accepted.status).toBe(202);
    const queued = fake.inserts(jobs)[0] as unknown as typeof jobs.$inferSelect;
    const ack = (await accepted.json()) as { actionId: string };
    expect(queued.id).toBe(ack.actionId);
    const outbox = createFakeDb({ updateReturns: [[routers, [[router()]]]] });
    await notifyPartnerActionResultWithDb(outbox.db as never, {
      job: queued,
      ownerRef: "acct-42",
      status: "success",
    });
    const row = {
      id: ID,
      ...outbox.inserts(partnerWebhooks)[0],
    } as unknown as typeof partnerWebhooks.$inferSelect;
    const { deliverPartnerWebhook } = await import("./partner-webhooks");
    const { verifyPartnerRequest } = await import(
      "./partner-request-signature"
    );
    const fetchImpl = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      if (typeof init?.body !== "string")
        throw new Error("Expected JSON string body");
      expect(
        verifyPartnerRequest({
          request: new Request(url, { method: "POST", headers, body: init.body }),
          secret: "fake-webhook-secret",
          rawBody: new TextEncoder().encode(init.body),
          nowMs: NOW.getTime(),
          method: "POST",
          path: "/hooks",
        }),
      ).toMatchObject({ ok: true, requestId: row.id });
      expect(JSON.parse(init.body)).toMatchObject({
        event: "router.action",
        ownerRef: "acct-42",
        routerId: ID,
        detail: { actionId: ack.actionId, state: "applied" },
      });
      expect(headers.get("X-Vectra-Webhook-Id")).toBe(row.id);
      return new Response(null, { status: 200 });
    });
    expect(
      await deliverPartnerWebhook(
        row,
        { url: "https://fake.example/hooks", secret: "fake-webhook-secret" },
        { fetchImpl, nowMs: NOW.getTime() },
      ),
    ).toEqual({ ok: true, status: 200 });
    expect(fetchImpl).toHaveBeenCalledOnce();
  });
});

describe("negotiated full typed management", () => {
  const entryId = "a".repeat(64);
  const cases = [
    ["select_entry", { entryId }],
    ["set_rules", { direct: ["*.Example.com"], vpn: ["vpn.test"] }],
    ["set_service", { service: "youtube", entryId }],
    ["set_wifi", { ssid: "Fake guest", password: "fake-guest-pass-123" }],
    ["reboot", {}],
    ["update_now", {}],
    ["set_auto_update", { enabled: true }],
  ] as const;
  function negotiated() {
    return inventory({
      connect: {
        ownerRef: "acct-42",
        capabilities: cases.map(([name]) => name),
        availableVersion: "0.6.0",
        entries: [{ id: entryId, name: "Observed entry", country: null }],
        services: [{ id: "youtube", entryId: null }],
      },
    });
  }
  it("queues all seven advertised operations with the exact typed envelope", async () => {
    const { hydratePartnerJobPayload } =
      await import("./partner-router-secrets");
    for (const [name, params] of cases) {
      const fake = createFakeDb({
        selects: [[routerInventorySnapshots, [[negotiated()]]]],
        updateReturns: [[routers, [[router()]]]],
      });
      const result = await queuePartnerActionWithDb(
        fake.db as never,
        action({ action: name, params }),
        `test-${name}`,
        NOW,
      );
      expect(result.status).toBe(202);
      const job = fake.inserts(jobs)[0] as unknown as typeof jobs.$inferSelect;
      expect(job.type).toBe("connect_router_action");
      expect(hydratePartnerJobPayload(job, "acct-42")).toMatchObject({
        origin: "partner_action",
        actionId: job.id,
        ownerRef: "acct-42",
        action: name,
      });
      if (name === "set_wifi")
        expect(
          JSON.stringify(fake.calls.map(({ table: _table, ...call }) => call)),
        ).not.toContain("fake-guest-pass-123");
    }
  });
  it("accepts the first service choice from the agreed supported-service catalogue with default bindings", async () => {
    // r37 emits its authoritative ServiceByID catalogue, including defaults;
    // entryId:null means the default path, not an invented explicit selection.
    const services = ["youtube", "tiktok", "telegram"].map((id) => ({
      id,
      entryId: null,
    }));
    const reported = inventory({
      connect: {
        ownerRef: "acct-42",
        capabilities: ["set_service"],
        services,
        entries: [{ id: entryId, name: "Observed entry", country: null }],
      },
    });
    expect(projectPartnerRouter(router(), reported, NOW).services).toEqual(
      services,
    );
    for (const service of services) {
      const fake = createFakeDb({
        selects: [[routerInventorySnapshots, [[reported]]]],
        updateReturns: [[routers, [[router()]]]],
      });
      const result = await queuePartnerActionWithDb(
        fake.db as never,
        action({
          action: "set_service",
          params: { service: service.id, entryId },
        }),
        `first-${service.id}`,
        NOW,
      );
      expect(result.status).toBe(202);
      expect(fake.inserts(jobs)[0]).toMatchObject({
        type: "connect_router_action",
        payload: {
          action: "set_service",
          params: { service: service.id, entryId },
        },
      });
    }
    const fake = createFakeDb({
      selects: [[routerInventorySnapshots, [[reported]]]],
      updateReturns: [[routers, [[router()]]]],
    });
    const unknown = await queuePartnerActionWithDb(
      fake.db as never,
      action({
        action: "set_service",
        params: { service: "unknown-service", entryId },
      }),
      "unknown-service",
      NOW,
    );
    expect(unknown.status).toBe(400);
    expect(fake.inserts(jobs)).toEqual([]);
  });
  it("keeps old firmware truthful and rejects forged owner/unknown selected identities", async () => {
    expect(
      projectPartnerRouter(router(), inventory(), NOW).capabilities,
    ).toEqual([]);
    // A capability list reported for a former owner is not the current one's.
    expect(
      projectPartnerRouter(
        router(),
        inventory({
          connect: { ownerRef: "foreign", capabilities: ["restart_vpn"] },
        }),
        NOW,
      ).capabilities,
    ).toEqual([]);
    expect(
      projectPartnerRouter(
        router(),
        inventory({
          connect: { ownerRef: "foreign", capabilities: ["reboot"] },
        }),
        NOW,
      ).capabilities,
    ).not.toContain("reboot");
    for (const params of [
      { entryId: "missing" },
      { entryId, command: "bad" },
    ]) {
      const fake = createFakeDb({
        selects: [[routerInventorySnapshots, [[negotiated()]]]],
        updateReturns: [[routers, [[router()]]]],
      });
      expect(
        (
          await queuePartnerActionWithDb(
            fake.db as never,
            action({ action: "select_entry", params }),
            "key",
            NOW,
          )
        ).status,
      ).toBe(400);
      expect(fake.inserts(jobs)).toEqual([]);
    }
  });
  it("rejects duplicate action identity with different owner or params at the database seam", async () => {
    const fake = createFakeDb({
      selects: [
        [routerInventorySnapshots, [[negotiated()]]],
        [jobs, [[{ id: ID, payload: { requestHash: "other" } }]]],
      ],
      updateReturns: [[routers, [[router()]]]],
    });
    expect(
      (
        await queuePartnerActionWithDb(
          fake.db as never,
          action({ action: "reboot" }),
          "key",
          NOW,
        )
      ).status,
    ).toBe(422);
    expect(fake.inserts(jobs)).toEqual([]);
  });
  it("stores a keyed hash of a set_wifi request, never the bare sha256 of the password-bearing input", async () => {
    const fake = createFakeDb({
      selects: [[routerInventorySnapshots, [[negotiated()]]]],
      updateReturns: [[routers, [[router()]]]],
    });
    const input = action({
      action: "set_wifi",
      params: { ssid: "Fake guest", password: "fake-guest-pass-123" },
    });
    expect(
      (await queuePartnerActionWithDb(fake.db as never, input, "wifi-key", NOW))
        .status,
    ).toBe(202);
    const stored = (fake.inserts(jobs)[0] as { payload: { requestHash: string } })
      .payload.requestHash;
    expect(stored).toMatch(/^hmac1:[0-9a-f]{64}$/);
    const bare = createHash("sha256").update(JSON.stringify(input)).digest("hex");
    expect(JSON.stringify(fake.calls.map(({ table: _t, ...call }) => call))).not.toContain(bare);
  });
  it("still recognises a retry of a job queued before the keyed hash (legacy sha256)", async () => {
    const input = action({ action: "reboot" });
    const legacy = createHash("sha256").update(JSON.stringify(input)).digest("hex");
    const fake = createFakeDb({
      selects: [
        [routerInventorySnapshots, [[negotiated()]]],
        [jobs, [[{ id: OTHER, state: "queued", payload: { requestHash: legacy } }]]],
      ],
      updateReturns: [[routers, [[router()]]]],
    });
    const result = await queuePartnerActionWithDb(fake.db as never, input, "key", NOW);
    expect(result).toEqual({ ok: true, status: 202, body: { actionId: OTHER, state: "queued" } });
    expect(fake.inserts(jobs)).toEqual([]);
  });
});

// Partners share the jobs table: the dedupe key carries the partner's scope,
// the payload keeps the key as the partner sent it (the router.action webhook
// echoes it back).
describe("an action's idempotency key per partner", () => {
  it.each([
    ["bloopcat", "K", "partner-action:bloopcat:K"],
    ["vectra", "K", "partner-action:K"],
    ["vectra", "act:7", "partner-action:vectra:act:7"],
  ])("partner %s, key %s: dedupe key %s, payload keeps the raw key", async (partnerId, key, dedupeKey) => {
    const fake = queueDb(router({ partnerId }));
    const result = await queuePartnerActionWithDb(
      fake.db as never,
      action(),
      key,
      NOW,
      partnerId,
    );
    expect(result.status).toBe(202);
    const [job] = fake.inserts(jobs);
    expect(job).toMatchObject({ dedupeKey, payload: { idempotencyKey: key } });
  });
});

// Partners share the routers table: a router is visible and actionable only to
// the partner that claimed it. fake-db does not evaluate predicates, so the
// explicit JS check is pinned by scripting the other partner's row into the
// answer and the SQL predicate by reading it off the query.
describe("partners do not see each other's routers", () => {
  type Where = { table: unknown; sql: string; params: unknown[] };
  function recording(base: ReturnType<typeof createFakeDb>) {
    const wheres: Where[] = [];
    const db = {
      ...base.db,
      select: () => ({
        from: (table: unknown) => {
          const inner = base.db.select().from(table);
          const chain = {
            ...inner,
            where: (condition: SQL) => {
              wheres.push({ table, ...dialect.sqlToQuery(condition) });
              return chain;
            },
          };
          return chain;
        },
      }),
      update: (table: unknown) => ({
        set: (set: Record<string, unknown>) => {
          const inner = base.db.update(table).set(set);
          return {
            where: (condition: SQL) => {
              wheres.push({ table, ...dialect.sqlToQuery(condition) });
              return inner.where();
            },
          };
        },
      }),
      transaction: async <T>(run: (tx: unknown) => Promise<T>) => run(db),
    };
    return { db, wheres: (table: unknown) => wheres.filter((w) => w.table === table) };
  }
  // The ownership predicate of a partner, as it shows in the SQL of a query.
  const IS_NULL = /"partner_id" is null/;
  const EQUALS = /"partner_id" = \$\d+/;

  it("hides another partner's router with the same ownerRef", async () => {
    const fake = createFakeDb({
      selects: [[routers, [[router({ partnerId: "bloopcat" })]]]],
    });
    expect(
      await readPartnerRoutersWithDb(fake.db as never, "acct-42", undefined, NOW, "vectra"),
    ).toEqual([]);
    const reverse = createFakeDb({
      selects: [[routers, [[router({ partnerId: null })]]]],
    });
    expect(
      await readPartnerRoutersWithDb(reverse.db as never, "acct-42", undefined, NOW, "bloopcat"),
    ).toEqual([]);
  });

  it("hides a router that changed hands between the list and its share-locked re-read", async () => {
    const fake = createFakeDb({
      selects: [
        [routers, [[router()], [router({ partnerId: "bloopcat" })]]],
        [routerInventorySnapshots, [[reportingInventory()]]],
      ],
    });
    expect(
      await readPartnerRoutersWithDb(fake.db as never, "acct-42", ID, NOW, "vectra"),
    ).toEqual([]);
  });

  it.each([
    ["vectra", null],
    ["vectra", "vectra"],
    ["bloopcat", "bloopcat"],
  ])("shows partner %s its own router (stored as %s)", async (partnerId, stored) => {
    const own = router({ partnerId: stored });
    const fake = createFakeDb({
      selects: [
        [routers, [[own], [own]]],
        [routerInventorySnapshots, [[reportingInventory()]]],
      ],
    });
    const snapshots = await readPartnerRoutersWithDb(
      fake.db as never,
      "acct-42",
      ID,
      NOW,
      partnerId,
    );
    expect(snapshots.map((s) => s.routerId)).toEqual([ID]);
  });

  it("asks the database for the caller's routers only, in the list and in the locked re-read", async () => {
    for (const [partnerId, params] of [
      ["vectra", ["vectra"]],
      ["bloopcat", ["bloopcat"]],
    ] as const) {
      const own = router({ partnerId: partnerId === "vectra" ? null : partnerId });
      const spy = recording(
        createFakeDb({
          selects: [
            [routers, [[own], [own]]],
            [routerInventorySnapshots, [[reportingInventory()]]],
          ],
        }),
      );
      await readPartnerRoutersWithDb(spy.db as never, "acct-42", ID, NOW, partnerId);
      const asked = spy.wheres(routers);
      expect(asked).toHaveLength(2);
      for (const query of asked) {
        expect(query.sql).toMatch(EQUALS);
        expect(query.params).toEqual(expect.arrayContaining([...params]));
        if (partnerId === "vectra") expect(query.sql).toMatch(IS_NULL);
        else expect(query.sql).not.toMatch(IS_NULL);
      }
    }
  });

  it("refuses an action on another partner's router", async () => {
    const fake = queueDb(router({ partnerId: "bloopcat" }));
    expect(
      (await queuePartnerActionWithDb(fake.db as never, action(), "key", NOW, "vectra"))
        .status,
    ).toBe(404);
    expect(fake.inserts(jobs)).toEqual([]);
    // and the other way round: a router from before there were partners is Vectra Connect's.
    const legacy = queueDb(router({ partnerId: null }));
    expect(
      (await queuePartnerActionWithDb(legacy.db as never, action(), "key", NOW, "bloopcat"))
        .status,
    ).toBe(404);
    expect(legacy.inserts(jobs)).toEqual([]);
  });

  it("stores the partner on the queued job", async () => {
    const fake = queueDb(router({ partnerId: "bloopcat" }));
    const result = await queuePartnerActionWithDb(
      fake.db as never,
      action(),
      "key",
      NOW,
      "bloopcat",
    );
    expect(result.status).toBe(202);
    expect(fake.inserts(jobs)[0]).toMatchObject({
      payload: { partnerId: "bloopcat", ownerRef: "acct-42", idempotencyKey: "key" },
    });
    const own = queueDb();
    await queuePartnerActionWithDb(own.db as never, action(), "key", NOW);
    expect(own.inserts(jobs)[0]).toMatchObject({ payload: { partnerId: "vectra" } });
  });

  it("locks and rechecks only the caller's router when it queues an action", async () => {
    for (const partnerId of ["vectra", "bloopcat"]) {
      const spy = recording(queueDb(router({ partnerId })));
      await queuePartnerActionWithDb(spy.db as never, action(), "key", NOW, partnerId);
      const [lock] = spy.wheres(routers);
      expect(lock!.sql).toMatch(EQUALS);
      expect(lock!.params).toContain(partnerId);
      if (partnerId === "vectra") expect(lock!.sql).toMatch(IS_NULL);
      else expect(lock!.sql).not.toMatch(IS_NULL);
    }
  });
});

// Review 2026-10-02: a command the backend gave up on was never cancelled, so
// "try again" could run it twice. The partner cancels it by its own key — but
// only while the router has never been handed it. A check-in leaves the job
// `queued` (a lost answer must redeliver it) and stamps deliveredAt instead.
describe("cancelling an owner's action by the partner's key", () => {
  const KEY = "rb7-key";
  function ownJob(overrides: Record<string, unknown> = {}) {
    return {
      id: JOB,
      routerId: ID,
      type: "connect_router_action",
      state: "queued",
      deliveredAt: null,
      dedupeKey: `partner-action:${KEY}`,
      payload: {
        origin: "partner_action",
        ownerRef: "acct-42",
        actionId: JOB,
        action: "reboot",
        idempotencyKey: KEY,
      },
      ...overrides,
    };
  }
  function cancelInput(overrides = {}) {
    return { routerId: ID, ownerRef: "acct-42", idempotencyKey: KEY, ...overrides };
  }
  function cancelRequest(body: unknown = cancelInput(), id = ID, key = "cancel-key") {
    const raw = JSON.stringify(body);
    const url = `https://fake.example/api/partner/routers/${id}/actions/cancel`;
    return new Request(url, {
      method: "POST",
      body: raw,
      headers: {
        ...buildPartnerRequestHeaders(SECRET, "POST", url, raw, key, NOW.getTime()),
        "Idempotency-Key": key,
      },
    });
  }
  type Row = Record<string, unknown>;
  function cancelDb(job: Row | undefined, extra: { row?: Row } = {}) {
    return createFakeDb({
      selects: [[jobs, [job ? [job] : []]]],
      updateReturns: [
        [routers, [[extra.row ?? (router() as unknown as Row)]]],
        [jobs, [[{ ...job, state: "cancelled" }]]],
      ],
    });
  }

  it("cancels a job the router was never handed, and tags the cancel as the partner's", async () => {
    const fake = cancelDb(ownJob());
    const result = await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW);
    expect(result).toEqual({ ok: true, status: 200, body: { actionId: JOB, state: "cancelled" } });
    expect(fake.updates(jobs)).toEqual([
      expect.objectContaining({
        state: "cancelled",
        completedAt: NOW,
        payload: expect.objectContaining({ cancelledBy: "partner", actionId: JOB }),
      }),
    ]);
  });

  it("answers not_found (never queued) when no action of this owner on this router has the key", async () => {
    for (const job of [
      undefined,
      ownJob({ routerId: OTHER }),
      ownJob({ payload: { origin: "operator", ownerRef: "acct-42" } }),
      ownJob({ payload: { origin: "partner_action", ownerRef: "foreign", actionId: JOB } }),
    ]) {
      const fake = cancelDb(job);
      expect(await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW)).toEqual({
        ok: true,
        status: 200,
        body: { state: "not_found" },
      });
      expect(fake.updates(jobs)).toEqual([]);
    }
  });

  it("does not cancel a queued job the router was already handed: 409 delivered", async () => {
    const fake = cancelDb(ownJob({ deliveredAt: new Date(NOW.getTime() - 5_000) }));
    expect(await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW)).toEqual({
      ok: false,
      status: 409,
      body: { error: "not_cancellable", actionId: JOB, state: "delivered" },
    });
    expect(fake.updates(jobs)).toEqual([]);
  });

  it("does not cancel a running or finished job and says what it is", async () => {
    for (const state of ["running", "succeeded", "failed"]) {
      const fake = cancelDb(ownJob({ state, deliveredAt: NOW }));
      expect(await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW)).toMatchObject({
        status: 409,
        body: { error: "not_cancellable", state },
      });
      expect(fake.updates(jobs)).toEqual([]);
    }
  });

  it("reports delivered when a check-in stamped the job between the read and the cancel", async () => {
    const fake = createFakeDb({
      selects: [[jobs, [[ownJob()], [ownJob({ deliveredAt: NOW })]]]],
      updateReturns: [
        [routers, [[router()]]],
        [jobs, [[]]],
      ],
    });
    expect(await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW)).toMatchObject({
      status: 409,
      body: { state: "delivered" },
    });
  });

  it("repeats 200 only for the partner's own cancel; a job cancelled by anyone else is 409", async () => {
    const own = cancelDb(ownJob({ state: "cancelled", payload: { ...ownJob().payload, cancelledBy: "partner" } }));
    expect(await cancelPartnerActionWithDb(own.db as never, cancelInput(), NOW)).toMatchObject({
      status: 200,
      body: { state: "cancelled" },
    });
    // The stuck-job janitor or an operator cancelled it — maybe after it ran.
    const other = cancelDb(ownJob({ state: "cancelled", deliveredAt: NOW }));
    expect(await cancelPartnerActionWithDb(other.db as never, cancelInput(), NOW)).toEqual({
      ok: false,
      status: 409,
      body: { error: "not_cancellable", actionId: JOB, state: "cancelled", by: "other" },
    });
    expect(own.updates(jobs)).toEqual([]);
    expect(other.updates(jobs)).toEqual([]);
  });

  it("never touches a foreign owner's or a released router", async () => {
    for (const row of [router({ ownerRef: "foreign" }), router({ releasedAt: NOW })]) {
      const fake = cancelDb(ownJob(), { row });
      expect((await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW)).status).toBe(404);
      expect(fake.updates(jobs)).toEqual([]);
    }
  });

  it("never touches another partner's router, and no job of its own", async () => {
    for (const [row, partnerId] of [
      [router({ partnerId: "bloopcat" }), "vectra"],
      [router({ partnerId: null }), "bloopcat"],
    ] as const) {
      const fake = cancelDb(ownJob(), { row });
      expect(
        (await cancelPartnerActionWithDb(fake.db as never, cancelInput(), NOW, partnerId)).status,
      ).toBe(404);
      expect(fake.updates(jobs)).toEqual([]);
    }
  });

  it("answers not_found for a job queued by another partner, a job without a partner being Vectra Connect's", async () => {
    const theirs = cancelDb(
      ownJob({ payload: { ...ownJob().payload, partnerId: "bloopcat" } }),
      { row: router({ partnerId: "vectra" }) },
    );
    expect(await cancelPartnerActionWithDb(theirs.db as never, cancelInput(), NOW)).toEqual({
      ok: true,
      status: 200,
      body: { state: "not_found" },
    });
    expect(theirs.updates(jobs)).toEqual([]);
    // queued before there were partners: Vectra Connect's, nobody else's.
    const legacy = cancelDb(ownJob(), { row: router({ partnerId: "bloopcat" }) });
    expect(
      await cancelPartnerActionWithDb(legacy.db as never, cancelInput(), NOW, "bloopcat"),
    ).toEqual({ ok: true, status: 200, body: { state: "not_found" } });
    expect(legacy.updates(jobs)).toEqual([]);
    // its own partner's job is cancelled.
    const own = cancelDb(
      ownJob({ payload: { ...ownJob().payload, partnerId: "bloopcat" } }),
      { row: router({ partnerId: "bloopcat" }) },
    );
    expect(
      await cancelPartnerActionWithDb(own.db as never, cancelInput(), NOW, "bloopcat"),
    ).toMatchObject({ status: 200, body: { state: "cancelled" } });
  });

  it("locks only the caller's router when it cancels", async () => {
    for (const partnerId of ["vectra", "bloopcat"]) {
      const base = cancelDb(ownJob({ payload: { ...ownJob().payload, partnerId } }), {
        row: router({ partnerId }),
      });
      const queries: Array<{ sql: string; params: unknown[] }> = [];
      const db = {
        ...base.db,
        update: (table: unknown) => ({
          set: (set: Record<string, unknown>) => {
            const inner = base.db.update(table).set(set);
            return {
              where: (condition: SQL) => {
                if (table === routers) queries.push(dialect.sqlToQuery(condition));
                return inner.where();
              },
            };
          },
        }),
        transaction: async <T>(run: (tx: unknown) => Promise<T>) => run(db),
      };
      await cancelPartnerActionWithDb(
        db as never,
        cancelInput({ idempotencyKey: KEY }),
        NOW,
        partnerId,
      );
      expect(queries).toHaveLength(1);
      expect(queries[0]!.sql).toMatch(/"partner_id" = \$\d+/);
      expect(queries[0]!.params).toContain(partnerId);
      if (partnerId === "vectra") expect(queries[0]!.sql).toMatch(/"partner_id" is null/);
      else expect(queries[0]!.sql).not.toMatch(/"partner_id" is null/);
    }
  });

  // fake-db does not evaluate predicates, so the lookup is read off the query.
  function lookedUpDedupeKeys(job: Row | undefined, row?: Row) {
    const base = cancelDb(job, { row });
    const asked: unknown[] = [];
    const db = {
      ...base.db,
      select: () => ({
        from: (table: unknown) => {
          const inner = base.db.select().from(table);
          return {
            where: (condition: SQL) => {
              asked.push(...dialect.sqlToQuery(condition).params);
              return inner.where();
            },
          };
        },
      }),
      transaction: async <T>(run: (tx: unknown) => Promise<T>) => run(db),
    };
    return { db, asked };
  }

  it.each([
    ["bloopcat", "K", "partner-action:bloopcat:K"],
    ["vectra", "K", "partner-action:K"],
    ["vectra", "act:7", "partner-action:vectra:act:7"],
  ])("partner %s cancelling key %s finds the job under %s", async (partnerId, key, dedupeKey) => {
    const job = ownJob({
      dedupeKey,
      payload: { ...ownJob().payload, idempotencyKey: key, partnerId },
    });
    const { db, asked } = lookedUpDedupeKeys(job, router({ partnerId }) as unknown as Row);
    const result = await cancelPartnerActionWithDb(
      db as never,
      cancelInput({ idempotencyKey: key }),
      NOW,
      partnerId,
    );
    expect(asked).toContain(dedupeKey);
    expect(result).toEqual({ ok: true, status: 200, body: { actionId: JOB, state: "cancelled" } });
  });

  it("is signed v2, needs an Idempotency-Key and binds the path router into the body", async () => {
    const d = deps();
    expect((await handlePartnerRouterActionCancel(cancelRequest(), ID, d)).status).toBe(200);
    expect(d.cancel).toHaveBeenCalledWith(cancelInput(), "vectra");
    const other = deps();
    expect((await handlePartnerRouterActionCancel(cancelRequest(cancelInput({ routerId: OTHER }), ID, "k-2"), ID, other)).status).toBe(400);
    expect((await handlePartnerRouterActionCancel(cancelRequest(cancelInput({ idempotencyKey: "bad key" }), ID, "k-3"), ID, other)).status).toBe(400);
    expect((await handlePartnerRouterActionCancel(cancelRequest(cancelInput(), ID, ""), ID, other)).status).toBe(400);
    const r = cancelRequest();
    const tampered = new Request(r.url, {
      method: "POST",
      headers: r.headers,
      body: JSON.stringify(cancelInput({ ownerRef: "foreign" })),
    });
    expect((await handlePartnerRouterActionCancel(tampered, ID, other)).status).toBe(401);
    expect(other.cancel).not.toHaveBeenCalled();
  });
});

// Review 2026-10-02: a same-key retry was validated again before the dedupe
// lookup, so an action queued while the router was ready turned into
// not_ready / not_supported / invalid_params on its retry.
describe("a same-key retry is answered from its job before any validation", () => {
  it("replays the queued action even when the router is no longer ready or capable", async () => {
    const queued = { id: JOB, state: "queued", payload: { requestHash: keyedDigestOf(action()) } };
    for (const row of [
      router({ lastAppliedRevisionId: null }),
      router({ engineMode: "passwall" }),
    ]) {
      const fake = createFakeDb({
        selects: [
          [jobs, [[queued]]],
          [routerInventorySnapshots, [[]]],
        ],
        updateReturns: [[routers, [[row]]]],
      });
      expect(await queuePartnerActionWithDb(fake.db as never, action(), "key", NOW)).toEqual({
        ok: true,
        status: 202,
        body: { actionId: JOB, state: "queued" },
      });
      expect(fake.inserts(jobs)).toEqual([]);
    }
  });

  // Review 2026-10-03: the reply was hard-coded `queued`.
  it("answers a same-key retry with the job's real state", async () => {
    for (const state of ["running", "succeeded", "failed", "cancelled"]) {
      const fake = createFakeDb({
        selects: [[jobs, [[{ id: JOB, state, payload: { requestHash: keyedDigestOf(action()) } }]]]],
        updateReturns: [[routers, [[router()]]]],
      });
      expect(await queuePartnerActionWithDb(fake.db as never, action(), "key", NOW)).toEqual({
        ok: true,
        status: 202,
        body: { actionId: JOB, state },
      });
      expect(fake.inserts(jobs)).toEqual([]);
    }
  });

  it("still refuses another body under the same key before validating it", async () => {
    const fake = createFakeDb({
      selects: [[jobs, [[{ id: JOB, payload: { requestHash: keyedDigestOf(action()) } }]]]],
      updateReturns: [[routers, [[router({ lastAppliedRevisionId: null })]]]],
    });
    expect(
      await queuePartnerActionWithDb(fake.db as never, action({ params: { bad: 1 } }), "key", NOW),
    ).toMatchObject({ status: 422, body: { error: "idempotency_key_mismatch" } });
  });
});

// Review 2026-10-02: when every answer to the queue call was lost, the backend
// never learnt the panel's actionId and the router's real result matched
// nothing. The result now carries the partner's own key for the action.
describe("router.action carries the partner's Idempotency-Key", () => {
  it("stores the key on the queued job", async () => {
    const fake = queueDb();
    await queuePartnerActionWithDb(fake.db as never, action(), "rb7-key", NOW);
    expect(fake.inserts(jobs)[0]?.payload).toMatchObject({ idempotencyKey: "rb7-key" });
  });

  it("puts the key into the result, from the payload or an older job's dedupe key", async () => {
    for (const job of [
      { id: JOB, routerId: ID, dedupeKey: "partner-action:rb7-key", payload: { origin: "partner_action", ownerRef: "acct-42", actionId: JOB, idempotencyKey: "rb7-key" } },
      { id: JOB, routerId: ID, dedupeKey: "partner-action:rb7-key", payload: { origin: "partner_action", ownerRef: "acct-42", actionId: JOB } },
    ]) {
      const fake = createFakeDb({ updateReturns: [[routers, [[router()]]]] });
      await notifyPartnerActionResultWithDb(fake.db as never, {
        job: job as never,
        ownerRef: "acct-42",
        status: "success",
      });
      expect(fake.inserts(partnerWebhooks)[0]?.payload).toMatchObject({
        detail: { actionId: JOB, idempotencyKey: "rb7-key", state: "applied" },
      });
    }
  });
});

// Port forwards (contract 2026-10-06, router/vectra-controller-pro/docs/
// superpowers/specs/2026-10-06-port-forwards-contract.md): the action is
// offered only to a router that advertises it, the telemetry reaches the
// partner field by field and nothing else does.
describe("port forwards", () => {
  const telemetry = {
    rules: [
      {
        id: "3fa1c09e",
        preset: "minecraft-java",
        destIp: "192.168.1.50",
        deviceName: "gaming-pc",
        port: "25565",
        proto: "tcp",
        direct: false,
        enabled: true,
      },
    ],
    devices: [
      { name: "gaming-pc", ip: "192.168.1.50" },
      { name: null, ip: "192.168.1.60" },
    ],
    cgnat: false,
    directActive: true,
  };
  const params = {
    rules: [
      {
        id: "3fa1c09e",
        preset: "minecraft-java",
        destIp: "192.168.1.50",
        port: "25565",
        proto: "tcp",
        direct: false,
        enabled: true,
      },
      {
        destIp: "192.168.1.60",
        port: "3478-3479",
        proto: "both",
        direct: true,
        enabled: true,
      },
    ],
  };
  function portForwardsInventory(connect: Record<string, unknown>) {
    return inventory({
      connect: routerConnectTelemetrySchema.parse({
        ownerRef: "acct-42",
        ...connect,
      }),
    });
  }
  function portForwardsDb(capabilities: string[]) {
    return createFakeDb({
      selects: [
        [
          routerInventorySnapshots,
          [[portForwardsInventory({ capabilities, portForwards: telemetry })]],
        ],
      ],
      updateReturns: [[routers, [[router()]]]],
    });
  }

  it("drops a malformed portForwards to null and keeps the rest of the report", () => {
    const parsed = routerConnectTelemetrySchema.parse({
      ownerRef: "acct-42",
      portForwards: {
        ...telemetry,
        rules: [{ ...telemetry.rules[0], id: "NOT-HEX" }],
      },
    });
    expect(parsed.portForwards).toBeNull();
    expect(parsed.ownerRef).toBe("acct-42");
    const tooMany = routerConnectTelemetrySchema.parse({
      portForwards: {
        ...telemetry,
        rules: Array.from({ length: 33 }, () => telemetry.rules[0]),
      },
    });
    expect(tooMany.portForwards).toBeNull();
  });

  it("accepts the contract's telemetry, null and absent", () => {
    expect(
      routerConnectTelemetrySchema.parse({ portForwards: telemetry })
        .portForwards,
    ).toEqual(telemetry);
    expect(
      routerConnectTelemetrySchema.parse({ portForwards: null }).portForwards,
    ).toBeNull();
    expect("portForwards" in routerConnectTelemetrySchema.parse({})).toBe(
      false,
    );
    // directActive null: no rule asks for «past the VPN».
    expect(
      routerConnectTelemetrySchema.safeParse({
        portForwards: { ...telemetry, directActive: null },
      }).success,
    ).toBe(true);
  });
  it("is kept by the real check-in schema next to vctl's fixture", () => {
    const checkIn = structuredClone(checkInFixture) as {
      inventory: { connect: Record<string, unknown> };
    };
    checkIn.inventory.connect.portForwards = telemetry;
    const parsed = routerCheckInRequestSchema.parse(checkIn);
    expect(parsed.inventory.connect?.portForwards).toEqual(telemetry);
  });
  it("bounds the telemetry: out-of-contract reports drop to null, never pass through", () => {
    const tooManyRules = {
      ...telemetry,
      rules: Array.from({ length: 33 }, (_, i) => ({
        ...telemetry.rules[0],
        id: i.toString(16).padStart(8, "0"),
      })),
    };
    const tooManyDevices = {
      ...telemetry,
      devices: Array.from({ length: 65 }, (_, i) => ({
        name: null,
        ip: `192.168.1.${i + 1}`,
      })),
    };
    for (const portForwards of [
      tooManyRules,
      tooManyDevices,
      { ...telemetry, cgnat: "false" },
      { ...telemetry, rules: [{ ...telemetry.rules[0], proto: "icmp" }] },
      { ...telemetry, rules: [{ ...telemetry.rules[0], id: "../bad" }] },
      { ...telemetry, rules: [{ ...telemetry.rules[0], port: "1".repeat(65) }] },
      {
        ...telemetry,
        devices: [{ name: "x".repeat(257), ip: "192.168.1.50" }],
      },
      { rules: [], devices: [], cgnat: false },
      [],
    ])
      expect(
        routerConnectTelemetrySchema.parse({ portForwards }).portForwards,
      ).toBeNull();
    expect(
      routerConnectTelemetrySchema.safeParse({
        portForwards: {
          ...telemetry,
          rules: tooManyRules.rules.slice(0, 32),
          devices: tooManyDevices.devices.slice(0, 64),
        },
      }).success,
    ).toBe(true);
  });
  it("projects exactly the contract's fields, never a MAC or anything else", () => {
    const injected = structuredClone(telemetry) as Record<string, unknown> & {
      rules: Record<string, unknown>[];
      devices: Record<string, unknown>[];
    };
    injected.secret = "fake-secret";
    injected.rules[0]!.mac = "aa:bb:cc:dd:ee:ff";
    injected.devices[0]!.mac = "aa:bb:cc:dd:ee:ff";
    injected.devices[1]!.hostname = "android-1234";
    // Straight from a stored payload, not through the check-in schema.
    const snapshot = projectPartnerRouter(
      router(),
      inventory({ connect: { ownerRef: "acct-42", portForwards: injected } }),
      NOW,
    );
    expect(snapshot.portForwards).toEqual(telemetry);
    expect(JSON.stringify(snapshot)).not.toContain("aa:bb:cc");
    expect(JSON.stringify(snapshot)).not.toContain("fake-secret");
    expect(JSON.stringify(snapshot)).not.toContain("android-1234");
    // And the check-in drops them before anything is stored.
    const checkIn = structuredClone(checkInFixture) as {
      inventory: { connect: Record<string, unknown> };
    };
    checkIn.inventory.connect.portForwards = injected;
    expect(
      routerCheckInRequestSchema.parse(checkIn).inventory.connect
        ?.portForwards,
    ).toEqual(telemetry);
  });
  it("projects null for an older router, a router without fw4, another owner", () => {
    expect(
      projectPartnerRouter(router(), reportingInventory(), NOW).portForwards,
    ).toBeNull();
    expect(
      projectPartnerRouter(
        router(),
        portForwardsInventory({ portForwards: null }),
        NOW,
      ).portForwards,
    ).toBeNull();
    expect(projectPartnerRouter(router(), null, NOW).portForwards).toBeNull();
    expect(
      projectPartnerRouter(
        router({ ownerRef: "acct-43" }),
        portForwardsInventory({ portForwards: telemetry }),
        NOW,
      ).portForwards,
    ).toBeNull();
  });
  it("advertises and queues set_port_forwards for a router that offers it", async () => {
    expect(
      projectPartnerRouter(
        router(),
        portForwardsInventory({
          capabilities: ["set_port_forwards"],
          portForwards: telemetry,
        }),
        NOW,
      ).capabilities,
    ).toEqual(["set_port_forwards"]);
    const fake = portForwardsDb(["set_port_forwards"]);
    expect(
      await queuePartnerActionWithDb(
        fake.db as never,
        action({ action: "set_port_forwards", params }),
        "key",
        NOW,
      ),
    ).toMatchObject({ status: 202, body: { state: "queued" } });
    expect(fake.inserts(jobs)[0]).toMatchObject({
      type: "connect_router_action",
      payload: { action: "set_port_forwards", params },
    });
    // The whole list may be emptied.
    const cleared = portForwardsDb(["set_port_forwards"]);
    expect(
      (
        await queuePartnerActionWithDb(
          cleared.db as never,
          action({ action: "set_port_forwards", params: { rules: [] } }),
          "key",
          NOW,
        )
      ).status,
    ).toBe(202);
  });
  it("refuses it to a router that does not advertise it, and bad params", async () => {
    const older = portForwardsDb(["set_rules"]);
    expect(
      await queuePartnerActionWithDb(
        older.db as never,
        action({ action: "set_port_forwards", params }),
        "key",
        NOW,
      ),
    ).toMatchObject({ status: 409, body: { error: "not_supported" } });
    expect(older.inserts(jobs)).toEqual([]);
    for (const bad of [
      { rules: [{ ...params.rules[1], deviceName: "gaming-pc" }] },
      { rules: [{ ...params.rules[1], port: "0" }] },
      { rules: params.rules[1] },
    ]) {
      const fake = portForwardsDb(["set_port_forwards"]);
      expect(
        await queuePartnerActionWithDb(
          fake.db as never,
          action({ action: "set_port_forwards", params: bad }),
          "key",
          NOW,
        ),
      ).toMatchObject({ status: 400, body: { error: "invalid_params" } });
      expect(fake.inserts(jobs)).toEqual([]);
    }
  });
});

describe("the calling partner reaches the data layer", () => {
  const BLOOP_SECRET = "bloopcat-partner-test-secret-0123456789ab";
  const bloopcat = {
    id: "bloopcat",
    brand: "bloopcat",
    label: "BloopCat",
    secrets: [BLOOP_SECRET],
    webhook: null,
    claimKey: null,
    botUsername: null,
  };
  const KEY = "k-bloop";
  const signed = (method: "GET" | "POST", url: string, raw: string, key: string) =>
    new Request(url, {
      method,
      headers: buildPartnerRequestHeaders(
        BLOOP_SECRET,
        method,
        url,
        raw,
        key,
        NOW.getTime(),
        undefined,
        "bloopcat",
      ),
      ...(method === "GET" ? {} : { body: raw }),
    });
  const asBloopcat = () => {
    const d = deps();
    d.api.partners = (id) => (id === "bloopcat" ? bloopcat : null);
    return d;
  };

  it("hands read, action and cancel the partner's id", async () => {
    const d = asBloopcat();
    const base = `https://fake.example/api/partner/routers/${ID}`;

    expect(
      (await handlePartnerRoutersRead(signed("GET", `${base}?ownerRef=acct-42`, "", ""), ID, d)).status,
    ).toBe(404);
    expect(d.read).toHaveBeenCalledWith("acct-42", ID, "bloopcat");

    const queued = JSON.stringify(action());
    expect(
      (await handlePartnerRouterAction(signed("POST", `${base}/actions`, queued, KEY), ID, d)).status,
    ).toBe(202);
    expect(d.action).toHaveBeenCalledWith(action(), KEY, "bloopcat");

    const cancel = JSON.stringify({ routerId: ID, ownerRef: "acct-42", idempotencyKey: KEY });
    expect(
      (await handlePartnerRouterActionCancel(signed("POST", `${base}/actions/cancel`, cancel, "c-bloop"), ID, d)).status,
    ).toBe(200);
    expect(d.cancel).toHaveBeenCalledWith(
      { routerId: ID, ownerRef: "acct-42", idempotencyKey: KEY },
      "bloopcat",
    );
  });

  it("hands the default partner's id when the request names none", async () => {
    const d = deps();
    expect((await handlePartnerRouterAction(request(), ID, d)).status).toBe(202);
    expect(d.action).toHaveBeenCalledWith(action(), "test-key", "vectra");
  });

  it("does not accept the default partner's secret under another partner's id", async () => {
    const d = asBloopcat();
    const url = `https://fake.example/api/partner/routers/${ID}/actions`;
    const raw = JSON.stringify(action());
    const borrowed = new Request(url, {
      method: "POST",
      body: raw,
      headers: buildPartnerRequestHeaders(SECRET, "POST", url, raw, KEY, NOW.getTime(), undefined, "bloopcat"),
    });
    expect((await handlePartnerRouterAction(borrowed, ID, d)).status).toBe(401);
    expect(d.action).not.toHaveBeenCalled();
  });
});
