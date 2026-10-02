import { describe, expect, it, vi } from "vitest";

// createOperatorDraftRevisionWithDb digests + encrypts through the secret
// helpers, which depend on VECTRA_SECRETS_KEY. That key is intentionally unset
// in the shared test env (one of the known env-gated failures), so mock ~/env
// here to prove the real save-path wiring without perturbing the shared key.
vi.mock("~/env", () => ({
  env: {
    VECTRA_SECRETS_KEY: "test-xray-save-restore-key-0123456789",
  },
}));

const { MASKED_SECRET_PLACEHOLDER, xrayDesiredConfigSchema } = await import(
  "@vectra/contracts",
);
const { passwallDesiredRevisions, passwallSecretBlobs, routers } = await import(
  "@vectra/db",
);
const { createOperatorDraftRevisionWithDb } = await import("./router-control");
const { createXraySecretPayload, hydrateXrayConfig, sanitizeXrayConfig } =
  await import("./secrets");

// The real cleartext xray config the operator originally saved. Its secrets
// live only inside the encrypted blob of the prior revision.
const priorRealConfig = xrayDesiredConfigSchema.parse({
  schema: 1,
  instance: { name: "canary-router", logLevel: "info" },
  process: {
    xrayBinary: "/usr/bin/xray",
    workDir: "/var/run/vectra-controller-pro",
    oomScoreAdj: -500,
    memorySoftMiB: 80,
    restartBackoff: { initialMs: 500, factor: 2, maxMs: 60_000, reset: "60s" },
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
        destOverride: ["http", "tls", "quic"],
        routeOnly: true,
      },
    },
  },
  subscriptions: [
    {
      id: "primary",
      remark: "BloopCat",
      url: "https://sub.example.com/api/sub/REAL_TOKEN",
      enabled: true,
      userAgent: "Happ/1.0",
      mode: "json",
      entryRemark: "⚡Extreme Польша 🇵🇱",
      headers: { Authorization: "Bearer real-bearer-token" },
    },
  ],
  geo: {
    assetDir: "/usr/share/v2ray",
    geoipUrl:
      "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
    geositeUrl:
      "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
    updateOnStart: false,
  },
});

// A minimal, call-order-faithful in-memory Drizzle stand-in for the
// xray-direct branch of createOperatorDraftRevisionWithDb. Rows are stored per
// table by reference identity; selects return rows newest-first so a `.limit(1)`
// matches the `orderBy(... desc)` the code uses (latest revision / latest blob).
// Predicates are no-ops: in this scenario only the source revision and its blob
// exist before the insert, so every pre-insert lookup unambiguously resolves to
// the source — exactly what the restore path must read.
function createFakeDb(seed: {
  rows: Map<unknown, Array<Record<string, unknown>>>;
}) {
  let idCounter = 0;

  const buildSelect = (table: unknown) => {
    const chain = {
      from: () => chain,
      where: () => chain,
      orderBy: () => chain,
      limit: (n: number) => {
        const rows = [...(seed.rows.get(table) ?? [])].reverse();
        return Promise.resolve(rows.slice(0, n));
      },
    };
    return chain;
  };

  return {
    select: () => ({
      from: (table: unknown) => buildSelect(table),
    }),
    insert: (table: unknown) => ({
      // Push on `values()` so inserts without a trailing `.returning()` (e.g.
      // the secret-blob upsert) still persist; `.returning()` reads it back.
      values: (value: Record<string, unknown>) => {
        const row = {
          id: (value.id as string) ?? `generated-${++idCounter}`,
          createdAt: new Date(Date.now() + idCounter),
          ...value,
        };
        const list = seed.rows.get(table) ?? [];
        list.push(row);
        seed.rows.set(table, list);
        const result: Promise<Array<Record<string, unknown>>> & {
          returning: () => Promise<Array<Record<string, unknown>>>;
        } = Object.assign(Promise.resolve([row]), {
          returning: () => Promise.resolve([row]),
        });
        return result;
      },
    }),
    delete: (_table: unknown) => ({
      where: () => Promise.resolve(undefined),
    }),
  } as never;
}

describe("createOperatorDraftRevisionWithDb (xray restore wiring)", () => {
  it("restores real secrets from the prior revision when a masked config is re-submitted", async () => {
    // The prior xray-direct revision: masked jsonb config at rest, real secrets
    // only inside the encrypted blob.
    const priorRevisionId = "prior-xray-revision";
    const rows = new Map<unknown, Array<Record<string, unknown>>>();
    rows.set(routers, [
      {
        id: "router-1",
        engineMode: "xray-direct",
        pendingImportRevisionId: null,
        activeRevisionId: priorRevisionId,
      },
    ]);
    rows.set(passwallDesiredRevisions, [
      {
        id: priorRevisionId,
        routerId: "router-1",
        revisionNumber: 4,
        engineMode: "xray-direct",
        origin: "operator_draft",
        status: "draft",
        config: sanitizeXrayConfig(priorRealConfig),
      },
    ]);
    rows.set(passwallSecretBlobs, [
      {
        id: "prior-blob",
        routerId: "router-1",
        desiredRevisionId: priorRevisionId,
        scope: "desired_revision",
        ciphertext: createXraySecretPayload(priorRealConfig),
        createdAt: new Date(),
      },
    ]);

    const db = createFakeDb({ rows });

    // The edit UI loads the MASKED prior config and re-submits it (placeholders
    // in place of every secret) with one cosmetic change.
    const masked = sanitizeXrayConfig(priorRealConfig);
    const maskedSubscriptions = masked.subscriptions!;
    const resubmitted = xrayDesiredConfigSchema.parse({
      ...masked,
      // Cosmetic change: switch which provider profile the router runs.
      subscriptions: [
        {
          ...maskedSubscriptions[0]!,
          entryRemark: "🇷🇺🇪🇺 Авто Самый стабильный",
        },
      ],
    });

    const revision = await createOperatorDraftRevisionWithDb(db, {
      routerId: "router-1",
      engineMode: "xray-direct",
      xrayConfig: resubmitted,
      config: undefined as never,
    });

    // 1) The persisted jsonb `config` is masked at rest (no real secrets).
    const persistedJson = JSON.stringify(revision.config);
    expect(persistedJson).toContain(MASKED_SECRET_PLACEHOLDER);
    expect(persistedJson).not.toContain("REAL_TOKEN");
    expect(persistedJson).not.toContain("real-bearer-token");

    // 2) The newly written secret blob hydrates back to the REAL prior secrets,
    //    NOT the literal placeholders. This is the latent save-path trap closed:
    //    re-submitting a masked config must not encrypt placeholders as "real".
    const newBlob = (rows.get(passwallSecretBlobs) ?? []).at(-1);
    expect(newBlob?.desiredRevisionId).toBe(revision.id);
    const hydrated = hydrateXrayConfig(
      revision.config as never,
      newBlob?.ciphertext as string,
    );
    const hydratedJson = JSON.stringify(hydrated);
    expect(hydratedJson).not.toContain(MASKED_SECRET_PLACEHOLDER);

    const hydratedSub = hydrated.subscriptions![0]!;
    expect(hydratedSub.url).toBe("https://sub.example.com/api/sub/REAL_TOKEN");
    expect(hydratedSub.headers?.Authorization).toBe("Bearer real-bearer-token");

    // The cosmetic edit is preserved through restore + hydrate.
    expect(hydratedSub.entryRemark).toBe("🇷🇺🇪🇺 Авто Самый стабильный");
  });
});
