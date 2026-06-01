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
  process: { xrayBinary: "/usr/bin/xray", workDir: "./work" },
  inbounds: {
    tproxy: { listenIP: "0.0.0.0", port: 12345 },
    socks: {
      listenIP: "127.0.0.1",
      port: 1080,
      username: "socks-user",
      password: "socks-real-pass",
    },
  },
  nodes: [
    {
      id: "world-de",
      remark: "WorldProxy DE",
      enabled: true,
      outbound: {
        protocol: "vless",
        server: "de1.example.online",
        port: 443,
        settings: {
          vless: {
            uuid: "11111111-2222-3333-4444-555555555555",
            flow: "xtls-rprx-vision",
            encryption: "none",
          },
        },
        stream: {
          transport: "tcp",
          security: "reality",
          reality: {
            serverName: "www.cloudflare.com",
            publicKey: "real-reality-public-key",
            shortId: "deadbeef",
            fingerprint: "firefox",
          },
        },
      },
    },
  ],
  routing: {
    domainStrategy: "IPIfNonMatch",
    rules: [{ tag: "world-proxy", outboundTag: "node-world-de" }],
  },
  subscriptions: [
    {
      id: "primary",
      remark: "BloopCat",
      url: "https://sub.example.com/api/sub/REAL_TOKEN",
      enabled: true,
      headers: { Authorization: "Bearer real-bearer-token" },
    },
  ],
  geo: {
    assetDir: "/usr/share/xray",
    geoipUrl:
      "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
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
    const masked = sanitizeXrayConfig(priorRealConfig) as Record<
      string,
      unknown
    >;
    const maskedNodes = masked.nodes as Array<Record<string, unknown>>;
    const resubmitted = xrayDesiredConfigSchema.parse({
      ...masked,
      nodes: [{ ...maskedNodes[0]!, remark: "WorldProxy DE (renamed)" }],
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
    expect(persistedJson).not.toContain("11111111-2222-3333-4444-555555555555");
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

    const hydratedNodes = (hydrated as Record<string, unknown>).nodes as Array<
      Record<string, unknown>
    >;
    const vless = (
      (hydratedNodes[0]!.outbound as Record<string, unknown>)
        .settings as Record<string, unknown>
    ).vless as Record<string, unknown>;
    expect(vless.uuid).toBe("11111111-2222-3333-4444-555555555555");
    const reality = (
      (hydratedNodes[0]!.outbound as Record<string, unknown>)
        .stream as Record<string, unknown>
    ).reality as Record<string, unknown>;
    expect(reality.publicKey).toBe("real-reality-public-key");
    expect(reality.shortId).toBe("deadbeef");

    const hydratedSub = (
      (hydrated as Record<string, unknown>).subscriptions as Array<
        Record<string, unknown>
      >
    )[0]!;
    expect(hydratedSub.url).toBe("https://sub.example.com/api/sub/REAL_TOKEN");
    expect((hydratedSub.headers as Record<string, unknown>).Authorization).toBe(
      "Bearer real-bearer-token",
    );

    const hydratedSocks = (
      (hydrated as Record<string, unknown>).inbounds as Record<string, unknown>
    ).socks as Record<string, unknown>;
    expect(hydratedSocks.password).toBe("socks-real-pass");

    // The cosmetic edit is preserved through restore + hydrate.
    expect(hydratedNodes[0]!.remark).toBe("WorldProxy DE (renamed)");
  });
});
