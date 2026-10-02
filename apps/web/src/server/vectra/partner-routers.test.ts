import { describe, expect, it, vi } from "vitest";
vi.mock("~/env", () => ({
  env: {
    NODE_ENV: "test",
    VECTRA_SECRETS_KEY: "fake-local-test-secrets-key-only-123456",
    VECTRA_CONNECT_WEBHOOK_URL: "https://fake.example/hooks",
    VECTRA_CONNECT_WEBHOOK_SECRET: "fake-webhook-secret",
  },
}));
vi.mock("~/server/db", () => ({ db: {} }));
import {
  jobs,
  routers,
  routerInventorySnapshots,
  partnerWebhooks,
} from "@vectra/db";
import { routerConnectTelemetrySchema } from "@vectra/contracts";
import { createFakeDb } from "./testing/fake-db";
import { buildPartnerRequestHeaders } from "./partner-request-signature";
import { type PartnerApiDeps, type IdempotencyRecord } from "./partner-api";
import {
  handlePartnerRouterAction,
  handlePartnerRoutersRead,
  projectPartnerRouter,
  queuePartnerActionWithDb,
  readPartnerRoutersWithDb,
  type PartnerRoutersDeps,
} from "./partner-routers";
import {
  notifyPartnerActionResultWithDb,
  reportedPartnerTransitions,
  sweepPartnerOfflineWithDb,
} from "./partner-router-events";
const ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const OTHER = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f32";
const NOW = new Date("2026-10-01T15:00:00Z");
const SECRET = "fake-partner-secret";
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
function deps(): PartnerRoutersDeps {
  const records = new Map<string, IdempotencyRecord>();
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
    findIdempotent: async (key) => records.get(key) ?? null,
    storeIdempotent: async (key, record) => {
      records.set(key, record);
      return record;
    },
  };
  return {
    api,
    read: vi.fn(async () => []),
    action: vi.fn(async () => ({
      ok: true,
      status: 202,
      body: { actionId: ID, state: "queued" },
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
function queueDb(row = router()) {
  return createFakeDb({
    selects: [[routerInventorySnapshots, [[inventory()]]]],
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
    expect(snapshot.capabilities).toEqual([
      "restart_vpn",
      "refresh_subscription",
    ]);
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
    const { verifyPartnerSignature } = await import("./partner-signature");
    const fetchImpl = vi.fn(async (_url: string | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      if (typeof init?.body !== "string")
        throw new Error("Expected JSON string body");
      expect(
        verifyPartnerSignature({
          secret: "fake-webhook-secret",
          timestamp: headers.get("X-Vectra-Partner-Timestamp"),
          signature: headers.get("X-Vectra-Partner-Signature"),
          rawBody: init.body,
          nowMs: NOW.getTime(),
        }),
      ).toEqual({ ok: true });
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
    ).toEqual(["restart_vpn", "refresh_subscription"]);
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
});
