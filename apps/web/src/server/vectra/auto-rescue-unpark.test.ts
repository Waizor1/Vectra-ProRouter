import { PgDialect } from "drizzle-orm/pg-core";
import { describe, expect, it } from "vitest";

import { clearStaleControlPlaneRecoveryParks } from "./auto-rescue";

const NOW = new Date("2026-08-22T16:00:00.000Z");
const ROUTER_ID = "99c6599d-03f5-483f-aadc-a48776a4531a";
const INCIDENT_ID = "938b7b49-ae25-48db-a5ff-56ea41ca7663";

// Every query in clearStaleControlPlaneRecoveryParks ends in a single await, so
// one canned result is consumed per query, in call order:
//   open incidents -> router -> support snapshot -> latest snapshot -> attempts
function createMockDb(results: unknown[][]) {
  let index = 0;
  const inserted: Record<string, unknown>[] = [];

  // Rendering each where clause to SQL is what gives these tests teeth. A
  // mock that swallows the predicate cannot tell `gte` from `lte`, nor notice
  // a dropped routerId scope -- both were verified to slip through silently
  // before this was added.
  const dialect = new PgDialect();
  const wheres: { sql: string; params: unknown[] }[] = [];

  const chain: Record<string, unknown> = {
    from: () => chain,
    where: (condition: unknown) => {
      if (condition) {
        const query = dialect.sqlToQuery(condition as never);
        wheres.push({ sql: query.sql, params: query.params });
      }
      return chain;
    },
    orderBy: () => chain,
    limit: () => chain,
    then: (resolve: (value: unknown) => void) => resolve(results[index++] ?? []),
  };

  const db = {
    select: () => chain,
    insert: () => ({
      values: (values: Record<string, unknown>) => {
        inserted.push(values);
        return Promise.resolve([]);
      },
    }),
  };

  // The mock is positional: it hands back canned rows in call order and never
  // inspects the where/orderBy predicates. That is enough to exercise the
  // guards, but it means an extra or reordered query in the production code
  // would silently shift every later result and could leave these tests green
  // for the wrong reason. consumed() lets a test pin the query count.
  return { db: db as never, inserted, consumed: () => index, wheres };
}

// Shape mirrors a real prod incident: metadata frozen at park time, still
// claiming the foreign path is blocked long after the router recovered.
function parkedIncident(
  metadataOverrides: Record<string, unknown> = {},
  overrides: Record<string, unknown> = {},
) {
  return {
    id: INCIDENT_ID,
    routerId: ROUTER_ID,
    type: "proxy_outage",
    state: "open",
    reason:
      "After auto-reboot and PassWall retry, foreign resources are still unavailable; router left in direct mode.",
    metadata: {
      origin: "control-plane-recovery",
      ruStatus: "reachable",
      panelStatus: "reachable",
      foreignStatus: "blocked",
      recoveryPhase: "operator_attention",
      awaitingOperator: true,
      ...metadataOverrides,
    },
    openedAt: new Date("2026-08-11T08:09:16.685Z"),
    resolvedAt: null,
    ...overrides,
  };
}

function certifiedRouter(overrides: Record<string, unknown> = {}) {
  return {
    id: ROUTER_ID,
    displayName: "yarik",
    hostname: "yarik",
    deviceIdentifier: "device-yarik",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    routePolicyExempt: null,
    routePolicyExemptReason: null,
    lastSeenAt: NOW,
    ...overrides,
  };
}

function snapshot(
  options: {
    foreignStatus?: string;
    passwallEnabled?: boolean;
    passwallService?: string;
  } = {},
) {
  const passwallEnabled = options.passwallEnabled ?? true;
  return {
    routerId: ROUTER_ID,
    passwallEnabled,
    createdAt: NOW,
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      passwallEnabled,
      foreignReachability: { status: options.foreignStatus ?? "healthy" },
      serviceHealth: { passwall: options.passwallService ?? "running" },
    },
  };
}

// The happy path: one park, one healthy router, no prior attempts.
function healthyPark(overrides: {
  incidents?: unknown[];
  router?: Record<string, unknown>;
  snap?: ReturnType<typeof snapshot>;
  attempts?: unknown[];
} = {}) {
  const snap = overrides.snap ?? snapshot();
  return [
    overrides.incidents ?? [parkedIncident()],
    [certifiedRouter(overrides.router)],
    [snap],
    [snap],
    overrides.attempts ?? [],
  ];
}

describe("clearStaleControlPlaneRecoveryParks", () => {
  it("queues the operator reconnect job and records why", async () => {
    const { db, inserted, consumed } = createMockDb(healthyPark());

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(1);

    // Pins the query sequence the positional mock depends on:
    // open incidents -> router -> support snapshot -> latest snapshot ->
    // prior attempts. If production grows a query, this fails instead of
    // quietly feeding the wrong row to the wrong check.
    expect(consumed()).toBe(5);

    // resumeProxy + clearRescue is what makes the agent reset the recovery
    // phase and emit the resolved transition. The monitor's own
    // reconnect_proxy repair action resumes the proxy but leaves the phase
    // parked, so it can never close the incident.
    expect(inserted[0]).toEqual({
      routerId: ROUTER_ID,
      type: "reconnect",
      state: "queued",
      payload: { resumeProxy: true, clearRescue: true },
    });

    // No dedupe key: the column is globally unique and gets nulled on job
    // completion, so any generated key risks colliding with a surviving row.
    expect(inserted[0]).not.toHaveProperty("dedupeKey");

    // The unpark restarts the proxy on a router the operator sees as healthy,
    // so it has to leave a trail to correlate the blip against.
    expect(inserted[1]?.type).toBe("fleet.control_plane_park_cleared");
    expect(inserted[1]?.routerId).toBe(ROUTER_ID);
  });

  it("asks the database only for open incidents", async () => {
    const { db, wheres } = createMockDb(healthyPark());
    await clearStaleControlPlaneRecoveryParks(db, NOW);

    expect(wheres[0]!.sql).toContain('"state" =');
    expect(wheres[0]!.params).toContain("open");
  });

  it("scopes the attempt budget to this router and to this incident", async () => {
    const { db, wheres } = createMockDb(healthyPark());
    await clearStaleControlPlaneRecoveryParks(db, NOW);

    // Last query of the sweep is the attempt count.
    const attempts = wheres[wheres.length - 1]!;

    // Without the routerId scope one busy router would starve the whole fleet.
    expect(attempts.sql).toContain('"router_id" =');
    expect(attempts.params).toContain(ROUTER_ID);
    expect(attempts.params).toContain("reconnect");

    // `>=` against the incident's openedAt, not `<=` and not a rolling window:
    // a rolling window is not a cap at all, it just slows the loop down.
    expect(attempts.sql).toContain('"created_at" >=');
    // The dialect serialises the bound Date to ISO before it reaches Postgres.
    expect(attempts.params).toContain("2026-08-11T08:09:16.685Z");
  });

  it("leaves a genuine outage to the operator", async () => {
    const { db, inserted } = createMockDb(
      healthyPark({
        snap: snapshot({ foreignStatus: "blocked", passwallEnabled: false }),
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("does not act on a router whose proxy switch is on but stack is dead", async () => {
    const { db, inserted } = createMockDb(
      healthyPark({ snap: snapshot({ passwallService: "stopped" }) }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("does not queue work for an offline router", async () => {
    const { db, inserted } = createMockDb(
      healthyPark({
        router: { lastSeenAt: new Date("2026-08-06T08:53:36.519Z") },
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("never touches the seed-list no-touch router", async () => {
    // hh flaps into direct by design and must stay out of every unattended
    // path.
    const { db, inserted } = createMockDb(
      healthyPark({ router: { hostname: "hh", displayName: "hh" } }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("honours an exemption set only through the per-router database flag", async () => {
    // The seed list is just the fallback; an operator exempting a router by
    // flag must be respected by unattended paths too.
    const { db, inserted } = createMockDb(
      healthyPark({
        router: {
          routePolicyExempt: true,
          routePolicyExemptReason: "operator hold",
        },
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("gives up after the attempt budget instead of restarting xray on a loop", async () => {
    const { db, inserted } = createMockDb(
      healthyPark({
        attempts: [
          {
            id: "job-2",
            state: "succeeded",
            createdAt: new Date("2026-08-22T15:00:00.000Z"),
          },
          {
            id: "job-1",
            state: "succeeded",
            createdAt: new Date("2026-08-22T14:00:00.000Z"),
          },
        ],
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("holds off while a recent attempt is still settling", async () => {
    const { db, inserted } = createMockDb(
      healthyPark({
        attempts: [
          {
            id: "job-1",
            state: "succeeded",
            createdAt: new Date("2026-08-22T15:55:00.000Z"),
          },
        ],
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("does not stack a second attempt on top of one still in flight", async () => {
    // Delivery is capped per check-in and exclusive jobs starve the queue, so
    // an attempt can sit queued past the cooldown. Two would reach the router
    // together and restart PassWall twice back to back.
    const { db, inserted } = createMockDb(
      healthyPark({
        attempts: [
          {
            id: "job-1",
            state: "queued",
            createdAt: new Date("2026-08-22T14:00:00.000Z"),
          },
        ],
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });

  it("stands back when the router has other open incidents", async () => {
    // The agent's resolve transition carries no metadata, which is why it
    // closes the park at all -- but that same branch resolves EVERY open
    // incident on the router. Unattended, that would erase signals this path
    // never looked at.
    const { db, inserted } = createMockDb(
      healthyPark({
        incidents: [
          parkedIncident(),
          {
            id: "0f2b0d1c-0000-4000-8000-000000000001",
            routerId: ROUTER_ID,
            type: "subscription_degraded",
            state: "open",
            reason: "Subscription returned a placeholder node set.",
            metadata: {},
            openedAt: new Date("2026-08-20T00:00:00.000Z"),
            resolvedAt: null,
          },
        ],
      }),
    );

    await expect(clearStaleControlPlaneRecoveryParks(db, NOW)).resolves.toBe(0);
    expect(inserted).toEqual([]);
  });
});
