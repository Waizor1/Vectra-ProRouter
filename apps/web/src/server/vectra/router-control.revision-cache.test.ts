import {
  desiredRevisionSummarySchema,
  passwallDesiredConfigSchema,
  routerCheckInResponseSchema,
  summarizePasswallRevisionDiff,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import { passwallDesiredRevisions, passwallSecretBlobs } from "@vectra/db";
import { beforeEach, describe, expect, it } from "vitest";

import {
  resetRevisionSummaryCacheForTest,
  resolveDesiredRevisionWithDb,
} from "./router-control";
import { createSecretPayload, sanitizePasswallConfig } from "./secrets";

const ROUTER_ID = "5c2b7e10-9d4a-4f83-a1b6-0e73c5d8f924";
const REV_40 = "1a2b3c4d-0000-4000-8000-000000000040";
const REV_41 = "1a2b3c4d-0000-4000-8000-000000000041";
const REV_42 = "1a2b3c4d-0000-4000-8000-000000000042";

function config(selected: string, password: string): PasswallDesiredConfig {
  return passwallDesiredConfigSchema.parse({
    basicSettings: {
      main: { mainSwitch: true, selectedNodeId: selected },
      dns: {},
      log: {},
      maintenance: {},
    },
    nodes: [
      { id: "node-a", label: "A", protocol: "vless", address: "a.example", port: 443, password },
      { id: "node-b", label: "B", protocol: "vless", address: "b.example", port: 443, password },
    ],
    subscriptions: { items: [] },
    appUpdate: {},
    ruleManage: {
      geoipUrl: "https://example.test/geoip.dat",
      geositeUrl: "https://example.test/geosite.dat",
    },
  });
}

type Revision = {
  id: string;
  revisionNumber: number;
  status: string;
  config: PasswallDesiredConfig;
  secret: { id: string; ciphertext: string } | null;
};

/**
 * Answers selects from in-memory rows, choosing rows by the ids in the WHERE
 * clause (drizzle Params), and records which queries read the jsonb config —
 * the expensive part the cache exists to skip.
 */
function createDb(revisions: Revision[]) {
  const stats = { configReads: 0, secretReads: 0 };
  function paramValues(node: unknown, out: Set<unknown>, seen: WeakSet<object>) {
    if (!node || typeof node !== "object" || seen.has(node)) return out;
    seen.add(node);
    const record = node as Record<string, unknown>;
    if (record.constructor?.name === "Param") out.add(record.value);
    if (Array.isArray(record.queryChunks)) {
      for (const chunk of record.queryChunks) paramValues(chunk, out, seen);
    }
    return out;
  }
  const rowOf = (revision: Revision) => ({
    id: revision.id,
    routerId: ROUTER_ID,
    revisionNumber: revision.revisionNumber,
    status: revision.status,
    origin: "operator_draft",
    engineMode: "passwall",
    configDigest: `digest-${revision.revisionNumber}`,
    config: sanitizePasswallConfig(revision.config),
    secretBlobId: revision.secret?.id ?? null,
  });
  const db = {
    select(fields?: Record<string, unknown>) {
      return {
        from(table: unknown) {
          let params = new Set<unknown>();
          let ordered = false;
          const rows = () => {
            if (table === passwallDesiredRevisions) {
              if (fields && "config" in fields) stats.configReads += 1;
              const byId = revisions.filter((revision) => params.has(revision.id));
              if (byId.length > 0) return byId.map(rowOf);
              if (ordered) {
                const below = [...params].find((value) => typeof value === "number") as number;
                const previous = revisions
                  .filter((revision) => revision.revisionNumber < below)
                  .sort((a, b) => b.revisionNumber - a.revisionNumber)[0];
                return previous ? [rowOf(previous)] : [];
              }
              return [];
            }
            if (table === passwallSecretBlobs) {
              stats.secretReads += 1;
              return revisions.flatMap((revision) =>
                revision.secret && params.has(revision.secret.id)
                  ? [{ ciphertext: revision.secret.ciphertext }]
                  : [],
              );
            }
            throw new Error("unexpected table");
          };
          const chain = {
            where(cond: unknown) {
              params = paramValues(cond, new Set(), new WeakSet());
              return chain;
            },
            orderBy() {
              ordered = true;
              return chain;
            },
            limit: () => Promise.resolve(rows()),
          };
          return chain;
        },
      };
    },
  };
  return { db: db as never, stats };
}

function revision(id: string, revisionNumber: number, cfg: PasswallDesiredConfig, secretId: string | null): Revision {
  return {
    id,
    revisionNumber,
    status: "approved",
    config: cfg,
    secret: secretId ? { id: secretId, ciphertext: createSecretPayload(cfg) } : null,
  };
}

const routerOn = (activeRevisionId: string) =>
  ({
    id: ROUTER_ID,
    importState: "approved",
    engineMode: "passwall",
    activeRevisionId,
    lastAppliedRevisionId: null,
  }) as never;

/** The summary as it was computed before the cache: hydrate, diff, parse. */
function uncachedSummary(current: Revision, previous: Revision | null) {
  return desiredRevisionSummarySchema.parse({
    id: current.id,
    revisionNumber: current.revisionNumber,
    status: current.status,
    origin: "operator_draft",
    engineMode: "passwall",
    configDigest: `digest-${current.revisionNumber}`,
    config: current.config,
    impact: summarizePasswallRevisionDiff(previous?.config ?? null, current.config),
  });
}

describe("desired revision summary cache", () => {
  beforeEach(() => resetRevisionSummaryCacheForTest());

  it("answers a repeated check-in without reading or decrypting the revisions again", async () => {
    const rev40 = revision(REV_40, 40, config("node-a", "p1"), "s40");
    const rev41 = revision(REV_41, 41, config("node-b", "p1"), "s41");
    const { db, stats } = createDb([rev40, rev41]);

    const first = await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []);
    expect(stats.configReads).toBe(2);
    expect(stats.secretReads).toBe(2);

    const second = await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []);
    expect(stats.configReads).toBe(2);
    expect(stats.secretReads).toBe(2);

    // Same effective answer as the uncached computation: decrypted secrets
    // restored, impact diffed against revision 40.
    expect(first).toEqual(uncachedSummary(rev41, rev40));
    expect(second).toEqual(first);
    expect((second?.config as PasswallDesiredConfig).nodes[0]?.password).toBe("p1");
  });

  it("serves a newly published revision at once", async () => {
    const rev40 = revision(REV_40, 40, config("node-a", "p1"), "s40");
    const rev41 = revision(REV_41, 41, config("node-b", "p1"), "s41");
    const revisions = [rev40, rev41];
    const { db } = createDb(revisions);
    expect((await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []))?.id).toBe(REV_41);

    const rev42 = revision(REV_42, 42, config("node-a", "p2"), "s42");
    revisions.push(rev42);
    const next = await resolveDesiredRevisionWithDb(db, routerOn(REV_42), []);
    expect(next).toEqual(uncachedSummary(rev42, rev41));
  });

  it("re-hydrates when the revision's secret blob is replaced", async () => {
    const rev40 = revision(REV_40, 40, config("node-a", "p1"), "s40");
    const rev41 = revision(REV_41, 41, config("node-b", "old-secret"), "s41");
    const { db } = createDb([rev40, rev41]);
    await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []);

    // upsertRevisionSecretBlob is delete + insert: a new row id.
    rev41.config = config("node-b", "new-secret");
    rev41.secret = { id: "s41-reimported", ciphertext: createSecretPayload(rev41.config) };
    const next = await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []);
    expect((next?.config as PasswallDesiredConfig).nodes[0]?.password).toBe("new-secret");
  });

  it("reflects a status change, and a vanished diff base", async () => {
    const rev40 = revision(REV_40, 40, config("node-a", "p1"), "s40");
    const rev41 = revision(REV_41, 41, config("node-b", "p1"), "s41");
    const revisions = [rev40, rev41];
    const { db } = createDb(revisions);
    await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []);

    rev41.status = "queued";
    expect((await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []))?.status).toBe("queued");

    revisions.splice(0, 1);
    expect(await resolveDesiredRevisionWithDb(db, routerOn(REV_41), [])).toEqual(
      uncachedSummary(rev41, null),
    );
  });

  it("hands out a frozen summary that a full response parse reproduces exactly", async () => {
    const rev41 = revision(REV_41, 41, config("node-b", "p1"), "s41");
    const { db } = createDb([rev41]);
    const summary = await resolveDesiredRevisionWithDb(db, routerOn(REV_41), []);
    expect(Object.isFrozen(summary)).toBe(true);
    expect(Object.isFrozen((summary?.config as PasswallDesiredConfig).nodes[0])).toBe(true);

    // checkInRouter validates the answer with desiredRevision null and then
    // attaches the cached (already parsed) summary: equal to parsing it all.
    const base = {
      protocolVersion: "2026-04-v1",
      routerId: ROUTER_ID,
      status: "active",
      pollingIntervalSeconds: 45,
      configSyncState: { importState: "approved" },
      rescuePolicy: {},
      updatePolicy: {},
      jobs: [],
      operatorMessage: null,
    };
    const attached = { ...routerCheckInResponseSchema.parse({ ...base, desiredRevision: null }), desiredRevision: summary };
    expect(attached).toEqual(routerCheckInResponseSchema.parse({ ...base, desiredRevision: summary }));
  });
});
