import { eventLog, routerCredentialBindings } from "@vectra/db";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as dbModule from "~/server/db";

import {
  bindCredentialAtRegistration,
  CONCURRENT_SESSION_WINDOW_MS,
  evaluateCheckInBinding,
  MAX_KNOWN_SOURCE_CLUSTERS,
  observeCheckInCredentialBinding,
  resetCredentialBindingStateForTest,
} from "./router-credential-binding";
import type { FakeDb } from "./testing/fake-db";

vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("./testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});
const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";
const CREDENTIAL_ID = "c0ffee00-0000-4000-8000-000000000001";
const B1 = `v1:${"1".repeat(64)}`;
const B2 = `v1:${"2".repeat(64)}`;
const NOW = new Date("2026-10-08T12:00:00.000Z");
const ago = (ms: number) => new Date(NOW.getTime() - ms);

function bindingRow(overrides: Partial<typeof routerCredentialBindings.$inferSelect> = {}) {
  return {
    routerId: ROUTER_ID,
    credentialId: CREDENTIAL_ID,
    boundDeviceBinding: B1,
    boundAt: new Date("2026-10-01T00:00:00.000Z"),
    lastDeviceBinding: B1,
    lastSourceCluster: "203.0.113.0/24",
    lastSeenAt: ago(45_000),
    knownSourceClusters: ["203.0.113.0/24"],
    updatedAt: ago(45_000),
    ...overrides,
  };
}

function evaluate(
  row: ReturnType<typeof bindingRow> | null,
  reportedBinding: string | null,
  sourceCluster = "203.0.113.0/24",
  credentialId = CREDENTIAL_ID,
) {
  return evaluateCheckInBinding({ row, routerId: ROUTER_ID, credentialId, reportedBinding, sourceCluster, now: NOW });
}

const kinds = (evaluation: ReturnType<typeof evaluate>) => evaluation.findings.map((finding) => finding.kind);

beforeEach(() => {
  fake.reset();
  resetCredentialBindingStateForTest();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("evaluateCheckInBinding", () => {
  it("binds a credential on first sight without reporting anything", () => {
    const evaluation = evaluate(null, B1);
    expect(evaluation.deviceMismatch).toBe(false);
    expect(evaluation.findings).toEqual([]);
    expect(evaluation.next).toEqual({
      routerId: ROUTER_ID,
      credentialId: CREDENTIAL_ID,
      boundDeviceBinding: B1,
      boundAt: NOW,
      lastDeviceBinding: B1,
      lastSourceCluster: "203.0.113.0/24",
      lastSeenAt: NOW,
      knownSourceClusters: ["203.0.113.0/24"],
    });
  });

  it("rebinds a rotated credential trust-on-first-use, keeping the router's networks", () => {
    const evaluation = evaluate(bindingRow({ credentialId: "older-credential" }), B2);
    expect(evaluation.deviceMismatch).toBe(false);
    expect(evaluation.next).toMatchObject({ credentialId: CREDENTIAL_ID, boundDeviceBinding: B2, boundAt: NOW });
  });

  it("binds an unbound credential the first time it reports a binding", () => {
    const row = bindingRow({ boundDeviceBinding: null, lastDeviceBinding: null });
    expect(evaluate(row, null).next).toMatchObject({ boundDeviceBinding: null, boundAt: row.boundAt });
    expect(evaluate(row, B1).next).toMatchObject({ boundDeviceBinding: B1, boundAt: NOW });
  });

  it("is a device mismatch only for a reported binding that differs", () => {
    expect(evaluate(bindingRow(), B1).deviceMismatch).toBe(false);
    // An omitted binding (an older agent after a rollback) is never one.
    expect(evaluate(bindingRow(), null).deviceMismatch).toBe(false);
    const mismatch = evaluate(bindingRow(), B2);
    expect(mismatch.deviceMismatch).toBe(true);
    expect(kinds(mismatch)).toEqual(["device_mismatch"]);
    expect(mismatch.findings[0]).toMatchObject({
      severity: "warning",
      metadata: { credentialId: CREDENTIAL_ID, boundDeviceBinding: B1, reportedDeviceBinding: B2 },
    });
    // The binding stays; what was seen last is recorded.
    expect(mismatch.next).toMatchObject({ boundDeviceBinding: B1, lastDeviceBinding: B2 });
  });

  it("flags two check-ins under 20 s apart from other networks or devices", () => {
    const recent = bindingRow({ lastSeenAt: ago(CONCURRENT_SESSION_WINDOW_MS - 1) });
    expect(kinds(evaluate(recent, B1, "198.51.100.0/24"))).toEqual(["concurrent_sessions", "new_source_cluster"]);
    expect(kinds(evaluate(recent, B2))).toEqual(["device_mismatch", "concurrent_sessions"]);
    expect(evaluate(recent, B1, "198.51.100.0/24").findings[0]).toMatchObject({
      severity: "warning",
      metadata: {
        previousSourceCluster: "203.0.113.0/24",
        sourceCluster: "198.51.100.0/24",
        secondsSincePrevious: 20,
      },
    });
    // Same network and device: one agent checking in twice quickly.
    expect(kinds(evaluate(recent, B1))).toEqual([]);
    // Absence of evidence is not a second device.
    expect(kinds(evaluate(recent, null))).toEqual([]);
    expect(kinds(evaluate(recent, B1, "unknown"))).toEqual([]);
    // 20 s or more apart is ordinary movement.
    const older = bindingRow({ lastSeenAt: ago(CONCURRENT_SESSION_WINDOW_MS) });
    expect(kinds(evaluate(older, B1, "198.51.100.0/24"))).toEqual(["new_source_cluster"]);
  });

  it("reports a network not seen before, but not the first one", () => {
    expect(kinds(evaluate(bindingRow({ knownSourceClusters: [] }), B1, "198.51.100.0/24"))).toEqual([]);
    const news = evaluate(bindingRow(), B1, "198.51.100.0/24");
    expect(news.findings).toEqual([
      expect.objectContaining({
        kind: "new_source_cluster",
        severity: "info",
        metadata: { sourceCluster: "198.51.100.0/24", knownSourceClusters: ["203.0.113.0/24"] },
      }),
    ]);
    expect(news.next.knownSourceClusters).toEqual(["203.0.113.0/24", "198.51.100.0/24"]);
    expect(kinds(evaluate(bindingRow(), B1, "unknown"))).toEqual([]);
  });

  it("keeps at most eight networks, most recently seen last", () => {
    const known = Array.from({ length: MAX_KNOWN_SOURCE_CLUSTERS }, (_, i) => `10.0.${i}.0/24`);
    const row = bindingRow({ knownSourceClusters: known });
    expect(evaluate(row, B1, "10.0.0.0/24").next.knownSourceClusters).toEqual([...known.slice(1), "10.0.0.0/24"]);
    expect(evaluate(row, B1, "10.9.9.0/24").next.knownSourceClusters).toEqual([...known.slice(1), "10.9.9.0/24"]);
    expect(evaluate(row, B1, "unknown").next.knownSourceClusters).toEqual(known);
  });
});

describe("observeCheckInCredentialBinding", () => {
  const observe = (overrides: Partial<Parameters<typeof observeCheckInCredentialBinding>[0]> = {}) =>
    observeCheckInCredentialBinding({
      routerId: ROUTER_ID,
      credentialId: CREDENTIAL_ID,
      deviceBinding: B1,
      clientIp: "203.0.113.77",
      now: NOW,
      ...overrides,
    });

  it("creates the row once a first-sight check-in is accepted, and writes no event", async () => {
    const observation = await observe();
    expect(observation.deviceMismatch).toBe(false);
    expect(fake.inserts(routerCredentialBindings)).toEqual([]);

    await observation.recordAccepted();

    expect(fake.inserts(routerCredentialBindings)).toEqual([
      expect.objectContaining({ routerId: ROUTER_ID, boundDeviceBinding: B1, lastSourceCluster: "203.0.113.0/24" }),
    ]);
    expect(fake.inserts(eventLog)).toEqual([]);
  });

  it("records a mismatch at once, and the check-in only if it is accepted", async () => {
    fake.reset({ selects: [[routerCredentialBindings, [[bindingRow()]]]] });

    const observation = await observe({ deviceBinding: B2 });

    expect(observation.deviceMismatch).toBe(true);
    expect(fake.inserts(eventLog)).toEqual([
      expect.objectContaining({ routerId: ROUTER_ID, type: "router.credential.device_mismatch", severity: "warning" }),
    ]);
    // A refused check-in never calls recordAccepted: nothing is remembered.
    expect(fake.updates(routerCredentialBindings)).toEqual([]);

    await observation.recordAccepted();
    expect(fake.updates(routerCredentialBindings)).toEqual([
      expect.objectContaining({ boundDeviceBinding: B1, lastDeviceBinding: B2, lastSeenAt: NOW }),
    ]);
  });

  it("reads the reported binding defensively", async () => {
    fake.reset({ selects: [[routerCredentialBindings, [[bindingRow()]]]] });
    const observation = await observe({ deviceBinding: { not: "a binding" } });
    expect(observation.deviceMismatch).toBe(false);
    await observation.recordAccepted();
    expect(fake.updates(routerCredentialBindings)[0]).toMatchObject({ lastDeviceBinding: null });
  });

  it("writes each kind of event at most once an hour per router", async () => {
    const row = bindingRow();
    fake.reset({ selects: [[routerCredentialBindings, [[row], [row], [row]]]] });

    await observe({ deviceBinding: B2 });
    await observe({ deviceBinding: B2 });
    await observe({ deviceBinding: B2, routerId: "7a1d3e9f-5c2b-4d4f-8e68-3b9c2d1f0a42" });

    expect(fake.inserts(eventLog).map((event) => [event.routerId, event.type])).toEqual([
      [ROUTER_ID, "router.credential.device_mismatch"],
      ["7a1d3e9f-5c2b-4d4f-8e68-3b9c2d1f0a42", "router.credential.device_mismatch"],
    ]);
  });

  it("fails open only when the row cannot be read (table not migrated), with a throttled warning", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    vi.spyOn(fake.db, "select").mockImplementation(() => {
      throw Object.assign(new Error('Failed query: select ...\nparams: s3cret'), {
        cause: new Error('relation "vectra_router_credential_binding" does not exist'),
      });
    });

    const first = await observe({ deviceBinding: B2 });
    expect(first.deviceMismatch).toBe(false);
    await expect(first.recordAccepted()).resolves.toBeUndefined();
    expect((await observe()).deviceMismatch).toBe(false);

    expect(warn).toHaveBeenCalledTimes(1);
    const logged = JSON.stringify(warn.mock.calls);
    expect(logged).toContain("does not exist");
    expect(logged).not.toContain("s3cret");
  });

  it("still reports a known mismatch when its event cannot be written", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    fake.reset({ selects: [[routerCredentialBindings, [[bindingRow()]]]] });
    vi.spyOn(fake.db, "insert").mockImplementation(() => {
      throw new Error("event log unavailable");
    });

    expect((await observe({ deviceBinding: B2 })).deviceMismatch).toBe(true);
  });

  it("never throws when recording the accepted check-in fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    fake.reset({ selects: [[routerCredentialBindings, [[bindingRow()]]]] });
    const observation = await observe();
    vi.spyOn(fake.db, "update").mockImplementation(() => {
      throw new Error("connection reset");
    });

    await expect(observation.recordAccepted()).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalledTimes(1);
  });
});

describe("bindCredentialAtRegistration", () => {
  const bind = (deviceBinding: unknown, credentialId = "c0ffee00-0000-4000-8000-000000000002") =>
    bindCredentialAtRegistration({ routerId: ROUTER_ID, credentialId, deviceBinding, now: NOW });

  it("binds a new router's credential to the device that registered it", async () => {
    await bind(B1);
    expect(fake.inserts(routerCredentialBindings)).toEqual([
      { routerId: ROUTER_ID, credentialId: "c0ffee00-0000-4000-8000-000000000002", boundDeviceBinding: B1, boundAt: NOW },
    ]);
    expect(fake.updates(routerCredentialBindings)).toEqual([]);
    expect(fake.inserts(eventLog)).toEqual([]);
  });

  it("falls back to updating when a check-in created the row meanwhile", async () => {
    fake.reset({ insertConflicts: [routerCredentialBindings] });
    await bind(B1);
    expect(fake.updates(routerCredentialBindings)).toEqual([
      { credentialId: "c0ffee00-0000-4000-8000-000000000002", boundDeviceBinding: B1, boundAt: NOW },
    ]);
  });

  it("records a rebind to a different device", async () => {
    fake.reset({ selects: [[routerCredentialBindings, [[bindingRow()]]]] });
    await bind(B2);
    expect(fake.updates(routerCredentialBindings)).toEqual([
      expect.objectContaining({ boundDeviceBinding: B2 }),
    ]);
    expect(fake.inserts(eventLog)).toEqual([
      expect.objectContaining({
        routerId: ROUTER_ID,
        type: "router.credential.rebound",
        severity: "info",
        metadata: {
          credentialId: "c0ffee00-0000-4000-8000-000000000002",
          previousCredentialId: CREDENTIAL_ID,
          previousDeviceBinding: B1,
          deviceBinding: B2,
        },
      }),
    ]);
  });

  it("keeps the router's binding when the registering agent reports none", async () => {
    fake.reset({ selects: [[routerCredentialBindings, [[bindingRow()]]]] });
    await bind(undefined);
    expect(fake.updates(routerCredentialBindings)).toEqual([
      expect.objectContaining({ boundDeviceBinding: B1 }),
    ]);
    expect(fake.inserts(eventLog)).toEqual([]);
  });

  it("never fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    vi.spyOn(fake.db, "select").mockImplementation(() => {
      throw new Error("relation does not exist");
    });
    await expect(bind(B1)).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalledTimes(1);
  });
});
