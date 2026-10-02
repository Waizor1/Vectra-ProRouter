import { describe, expect, it, vi } from "vitest";

import {
  handleRouterClaimRequest,
  handleRouterUnbindRequest,
  IDEMPOTENT_REPLAY_HEADER,
  type IdempotencyRecord,
  MAX_BODY_BYTES,
  type PartnerApiDeps,
  readRawBody,
} from "./partner-api";

import { buildPartnerRequestHeaders } from "./partner-request-signature";
// Every dependency is injected; the real database is never reached.
vi.mock("~/server/db", () => ({ db: {} }));

const SECRET = "partner-api-test-secret-0123456789abcdef";
const NOW = new Date("2026-09-28T10:00:00.000Z");
const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";

const CLAIM_BODY = {
  code: "7KQ4M9XD",
  device: null,
  owner: { ref: "acct-42", label: "iv***" },
  subscription: {
    url: "https://sub.example.test/api/sub/TOKEN",
    userAgent: "v2rayNG/1.9.6",
  },
};

function signedRequest(
  method: "POST" | "DELETE",
  path: string,
  body: unknown,
  options: {
    key?: string;
    secret?: string;
    timestamp?: string;
    raw?: string;
  } = {},
) {
  const raw = options.raw ?? JSON.stringify(body);
  const timestamp = options.timestamp ?? String(NOW.getTime() / 1000);
  const headers: Record<string, string> = {
    "content-type": "application/json",
    ...buildPartnerRequestHeaders(
      options.secret ?? SECRET,
      method,
      `https://router.vectra-pro.net${path}`,
      raw,
      options.key ?? "default-test-key",
      Number(timestamp) * 1000,
    ),
  };
  return new Request(`https://router.vectra-pro.net${path}`, {
    method,
    headers,
    body: raw,
  });
}

function createDeps(overrides: Partial<PartnerApiDeps> = {}) {
  const store = new Map<string, IdempotencyRecord>();
  const nonces = new Set<string>();
  const deps: PartnerApiDeps = {
    secret: SECRET,
    reserveNonce: async (id) => {
      if (nonces.has(id)) return false;
      nonces.add(id);
      return true;
    },
    now: () => NOW,
    claim: vi.fn(async () => ({
      ok: true as const,
      status: 200 as const,
      body: {
        routerId: ROUTER_ID,
        state: "claimed" as const,
        alreadyClaimed: false,
        deviceIdentifier: "vectra-test",
        devicePublicKey: null,
        model: null,
      },
    })),
    unbind: vi.fn(async () => ({
      ok: true as const,
      status: 200 as const,
      body: { routerId: ROUTER_ID, state: "unclaimed" as const },
    })),
    findIdempotent: vi.fn(async (key: string) => store.get(key) ?? null),
    storeIdempotent: vi.fn(async (key: string, record: IdempotencyRecord) => {
      const existing = store.get(key);
      if (existing) {
        return existing;
      }
      store.set(key, record);
      return record;
    }),
    ...overrides,
  };
  return { deps, store };
}

const claim = (request: Request, deps: PartnerApiDeps) =>
  handleRouterClaimRequest(request, deps);

describe("POST /api/partner/router-claims — authentication", () => {
  it("answers the claim when the signature is good", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY),
      deps,
    );

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({
      routerId: ROUTER_ID,
      state: "claimed",
      alreadyClaimed: false,
      deviceIdentifier: "vectra-test",
      devicePublicKey: null,
      model: null,
    });
    expect(deps.claim).toHaveBeenCalledWith({
      code: "7KQ4M9XD",
      device: null,
      owner: { ref: "acct-42", label: "iv***" },
      subscription: CLAIM_BODY.subscription,
    });
    expect(response.headers.get("cache-control")).toBe("no-store");
  });

  it("answers 401 to a bad signature and never runs the claim", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        secret: "someone-elses-secret-0123456789abcdef",
      }),
      deps,
    );

    expect(response.status).toBe(401);
    expect(await response.json()).toEqual({ error: "bad_signature" });
    expect(deps.claim).not.toHaveBeenCalled();
  });

  it("answers 401 to a stale timestamp", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        timestamp: String(NOW.getTime() / 1000 - 301),
      }),
      deps,
    );

    expect(response.status).toBe(401);
    expect(await response.json()).toEqual({ error: "stale_timestamp" });
    expect(deps.claim).not.toHaveBeenCalled();
  });

  it("is off (503) while no partner secret is configured", async () => {
    const { deps } = createDeps({ secret: undefined });

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY),
      deps,
    );

    expect(response.status).toBe(503);
    expect(deps.claim).not.toHaveBeenCalled();
  });
});

describe("POST /api/partner/router-claims — body size", () => {
  // A chunked body: no Content-Length, 1 KiB at a time, 1 MiB in all.
  function chunkedRequest() {
    let pulled = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        pulled += 1;
        if (pulled > 1024) {
          controller.close();
          return;
        }
        controller.enqueue(new Uint8Array(1024).fill(0x61));
      },
    });
    const request = new Request(
      "https://router.vectra-pro.net/api/partner/router-claims",
      {
        method: "POST",
        body: stream,
        // Node's fetch Request needs this for a streamed body.
        duplex: "half",
      } as RequestInit & { duplex: "half" },
    );
    return { request, pulled: () => pulled };
  }

  it("stops reading a chunked body past the cap, before any signature check", async () => {
    const { deps } = createDeps();
    const { request, pulled } = chunkedRequest();
    expect(request.headers.get("content-length")).toBeNull();

    const response = await claim(request, deps);

    expect(response.status).toBe(400);
    expect(await response.json()).toEqual({
      error: "invalid",
      detail: `body must be at most ${MAX_BODY_BYTES} bytes`,
    });
    // It read just past 16 KiB, not the whole megabyte.
    expect(pulled()).toBeLessThan(40);
    expect(deps.claim).not.toHaveBeenCalled();
  });

  it("reads a body up to the cap whole", async () => {
    const body = new Uint8Array(MAX_BODY_BYTES).fill(0x62);
    const read = await readRawBody(
      new Request("https://router.vectra-pro.net/x", { method: "POST", body }),
    );
    expect(read?.byteLength).toBe(MAX_BODY_BYTES);
    expect(
      await readRawBody(
        new Request("https://router.vectra-pro.net/x", {
          method: "POST",
          body: new Uint8Array(MAX_BODY_BYTES + 1),
        }),
      ),
    ).toBeNull();
  });
});

describe("POST /api/partner/router-claims — body", () => {
  it.each([
    [
      "both code and device",
      {
        ...CLAIM_BODY,
        device: { deviceIdentifier: "d", devicePublicKey: "k" },
      },
    ],
    ["neither code nor device", { ...CLAIM_BODY, code: null }],
    ["an unknown key", { ...CLAIM_BODY, plan: "router" }],
    [
      "an owner ref that looks like an email",
      { ...CLAIM_BODY, owner: { ref: "a@b.c", label: "x" } },
    ],
    ["no subscription", { ...CLAIM_BODY, subscription: undefined }],
  ])("answers 400 invalid to %s", async (_name, body) => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", body),
      deps,
    );

    expect(response.status).toBe(400);
    expect(await response.json()).toMatchObject({ error: "invalid" });
    expect(deps.claim).not.toHaveBeenCalled();
  });

  it("claims with the router's own signed agent when subscription.userAgent is omitted", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", {
        ...CLAIM_BODY,
        subscription: { url: CLAIM_BODY.subscription.url },
      }),
      deps,
    );

    expect(response.status).toBe(200);
    expect(deps.claim).toHaveBeenCalledWith({
      code: "7KQ4M9XD",
      device: null,
      owner: { ref: "acct-42", label: "iv***" },
      subscription: { url: CLAIM_BODY.subscription.url },
    });
  });

  it("claims with the router's own signed agent when subscription.userAgent is null", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", {
        ...CLAIM_BODY,
        subscription: { url: CLAIM_BODY.subscription.url, userAgent: null },
      }),
      deps,
    );

    expect(response.status).toBe(200);
    expect(deps.claim).toHaveBeenCalledWith({
      code: "7KQ4M9XD",
      device: null,
      owner: { ref: "acct-42", label: "iv***" },
      subscription: { url: CLAIM_BODY.subscription.url, userAgent: null },
    });
  });

  it.each([
    ["blank", { url: CLAIM_BODY.subscription.url, userAgent: "   " }],
    [
      "a literal claiming to be the router's own agent",
      {
        url: CLAIM_BODY.subscription.url,
        userAgent: "VectraRouter/0.4.0 (AX3000T)",
      },
    ],
    [
      "that literal in any case",
      { url: CLAIM_BODY.subscription.url, userAgent: "vectrarouter/0.4.0" },
    ],
  ])(
    "answers 400 invalid when subscription.userAgent is %s — the panel never invents one",
    async (_name, subscription) => {
      const { deps } = createDeps();

      const response = await claim(
        signedRequest("POST", "/api/partner/router-claims", {
          ...CLAIM_BODY,
          subscription,
        }),
        deps,
      );

      expect(response.status).toBe(400);
      const body = (await response.json()) as { error: string; detail: string };
      expect(body.error).toBe("invalid");
      expect(body.detail).toMatch(/^subscription\.userAgent: /);
      expect(deps.claim).not.toHaveBeenCalled();
    },
  );

  it("answers 400 to a body that is not JSON", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", null, {
        raw: "{nope",
      }),
      deps,
    );

    expect(response.status).toBe(400);
    expect(await response.json()).toEqual({
      error: "invalid",
      detail: "body must be JSON",
    });
  });

  it.each([
    [404, "unknown_code"],
    [410, "expired"],
    [409, "claimed_by_other"],
  ] as const)("passes a %i %s outcome through", async (status, error) => {
    const { deps } = createDeps({
      claim: vi.fn(async () => ({
        ok: false as const,
        status,
        body: { error },
      })),
    });

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY),
      deps,
    );

    expect(response.status).toBe(status);
    expect(await response.json()).toEqual({ error });
  });
});

describe("POST /api/partner/router-claims — Idempotency-Key", () => {
  it("replays the stored answer for the same key and body", async () => {
    const { deps } = createDeps();
    const first = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-1",
      }),
      deps,
    );

    // The claim would now say "already claimed"; the key must not let it.
    deps.claim = vi.fn(async () => ({
      ok: true as const,
      status: 200 as const,
      body: {
        routerId: ROUTER_ID,
        state: "claimed" as const,
        alreadyClaimed: true,
        deviceIdentifier: "vectra-test",
        devicePublicKey: null,
        model: null,
      },
    }));
    const second = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-1",
      }),
      deps,
    );

    expect(second.status).toBe(200);
    expect(await second.json()).toEqual(await first.json());
    expect(second.headers.get(IDEMPOTENT_REPLAY_HEADER)).toBe("true");
    expect(deps.claim).not.toHaveBeenCalled();
  });

  it("refuses the same key with a different body", async () => {
    const { deps } = createDeps();
    await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-2",
      }),
      deps,
    );

    const response = await claim(
      signedRequest(
        "POST",
        "/api/partner/router-claims",
        { ...CLAIM_BODY, owner: { ref: "acct-43", label: "y" } },
        { key: "k-2" },
      ),
      deps,
    );

    expect(response.status).toBe(422);
    expect(await response.json()).toEqual({
      error: "idempotency_key_mismatch",
    });
  });

  it("does not store a failure, so a retry with the key can still succeed", async () => {
    const { deps, store } = createDeps();
    deps.claim = vi.fn(async () => ({
      ok: false as const,
      status: 404 as const,
      body: { error: "unknown_code" as const },
    }));
    await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-3",
      }),
      deps,
    );
    expect(store.has("k-3")).toBe(false);

    deps.claim = createDeps().deps.claim;
    const retry = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-3",
      }),
      deps,
    );
    expect(retry.status).toBe(200);
    expect(store.get("k-3")).toMatchObject({ statusCode: 200 });
  });

  it("answers with what a concurrent twin stored first", async () => {
    const { deps, store } = createDeps();
    // A twin with the same key and body finished between our lookup and store.
    deps.findIdempotent = vi.fn(async () => null);
    const twinAnswer = {
      routerId: ROUTER_ID,
      state: "claimed",
      alreadyClaimed: false,
      deviceIdentifier: "vectra-test",
      devicePublicKey: null,
      model: null,
    };
    const first = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-4",
      }),
      deps,
    );
    expect(await first.json()).toEqual(twinAnswer);
    deps.claim = vi.fn(async () => ({
      ok: true as const,
      status: 200 as const,
      body: {
        routerId: ROUTER_ID,
        state: "claimed" as const,
        alreadyClaimed: true,
        deviceIdentifier: "vectra-test",
        devicePublicKey: null,
        model: null,
      },
    }));

    const second = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "k-4",
      }),
      deps,
    );

    expect(await second.json()).toEqual(twinAnswer);
    expect(store.size).toBe(1);
  });

  it("refuses a malformed signed key before executing", async () => {
    const { deps } = createDeps();

    const response = await claim(
      signedRequest("POST", "/api/partner/router-claims", CLAIM_BODY, {
        key: "has space",
      }),
      deps,
    );

    expect(response.status).toBe(401);
    expect(deps.claim).not.toHaveBeenCalled();
  });
});

describe("DELETE /api/partner/router-claims/:routerId", () => {
  const path = `/api/partner/router-claims/${ROUTER_ID}`;

  it("unbinds when the signed body names the same router", async () => {
    const { deps } = createDeps();

    const response = await handleRouterUnbindRequest(
      signedRequest("DELETE", path, {
        routerId: ROUTER_ID,
        ownerRef: "acct-42",
      }),
      ROUTER_ID,
      deps,
    );

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({
      routerId: ROUTER_ID,
      state: "unclaimed",
    });
    expect(deps.unbind).toHaveBeenCalledWith({
      routerId: ROUTER_ID,
      ownerRef: "acct-42",
    });
  });

  it("refuses a signed body that names another router (replay onto another path)", async () => {
    const { deps } = createDeps();

    const response = await handleRouterUnbindRequest(
      signedRequest("DELETE", path, {
        routerId: "11111111-1111-4111-8111-111111111111",
      }),
      ROUTER_ID,
      deps,
    );

    expect(response.status).toBe(400);
    expect(deps.unbind).not.toHaveBeenCalled();
  });

  it("requires the body", async () => {
    const { deps } = createDeps();

    const response = await handleRouterUnbindRequest(
      signedRequest("DELETE", path, null, { raw: "" }),
      ROUTER_ID,
      deps,
    );

    expect(response.status).toBe(400);
    expect(deps.unbind).not.toHaveBeenCalled();
  });

  it("passes 404 not_claimed through", async () => {
    const { deps } = createDeps({
      unbind: vi.fn(async () => ({
        ok: false as const,
        status: 404 as const,
        body: { error: "not_claimed" as const },
      })),
    });

    const response = await handleRouterUnbindRequest(
      signedRequest("DELETE", path, { routerId: ROUTER_ID }),
      ROUTER_ID,
      deps,
    );

    expect(response.status).toBe(404);
    expect(await response.json()).toEqual({ error: "not_claimed" });
  });

  it("answers 401 without a valid signature", async () => {
    const { deps } = createDeps();

    const response = await handleRouterUnbindRequest(
      signedRequest(
        "DELETE",
        path,
        { routerId: ROUTER_ID },
        { secret: "x".repeat(40) },
      ),
      ROUTER_ID,
      deps,
    );

    expect(response.status).toBe(401);
    expect(deps.unbind).not.toHaveBeenCalled();
  });
});
