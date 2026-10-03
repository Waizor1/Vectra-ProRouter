import { describe, it, expect, vi } from "vitest";
vi.mock("~/server/db", () => ({ db: {} }));
import vectors from "./testing/partner-v2-golden-vectors.json";
import {
  canonicalPartnerMessage,
  canonicalPartnerQuery,
  signPartnerRequest,
  buildPartnerRequestHeaders,
} from "./partner-request-signature";
import { authenticatePartnerRequest, type PartnerApiDeps } from "./partner-api";
const NOW = 1790856000000,
  SECRET = "synthetic-partner-v2-test-key";
function deps() {
  const ids = new Set<string>();
  return {
    secret: SECRET,
    now: () => new Date(NOW),
    reserveNonce: vi.fn(async (id: string) => {
      if (ids.has(id)) return false;
      ids.add(id);
      return true;
    }),
  } as unknown as PartnerApiDeps;
}
const target = "https://fake.example/api/partner/routers?ownerRef=acct-a";
function req(method = "GET", url = target, body = "", key = "") {
  return new Request(url, {
    method,
    headers: buildPartnerRequestHeaders(SECRET, method, url, body, key, NOW),
    ...(method === "GET" ? {} : { body }),
  });
}
const check = (
  r: Request,
  d = deps(),
  body = "",
  method = r.method,
  path = new URL(r.url).pathname,
) =>
  authenticatePartnerRequest(
    r,
    new TextEncoder().encode(body),
    d,
    method,
    path,
  );
describe("mandatory partner request v2", () => {
  it("matches all agreed Python golden vectors including integer timestamp and UTF-8 body", () => {
    for (const vector of vectors) {
      expect(canonicalPartnerMessage(vector)).toBe(vector.canonicalMessage);
      expect(signPartnerRequest(vector.secret, vector)).toBe(vector.signature);
    }
  });
  it("canonicalizes query via RFC3986 and rejects duplicate keys", () => {
    expect(
      canonicalPartnerQuery(new URL("https://fake/?ownerRef=acct%3Aa+%2B%2F~")),
    ).toBe("ownerRef=acct%3Aa%20%2B%2F~");
    expect(() =>
      canonicalPartnerQuery(new URL("https://fake/?ownerRef=a&ownerRef=b")),
    ).toThrow();
  });
  it("refuses captured GET headers across owner, target, method and query before private reads", async () => {
    const original = req();
    for (const url of [
      target.replace("acct-a", "acct-b"),
      target.replace("routers?", "routers/router-b?"),
      target + "&extra=x",
      target + "&ownerRef=acct-a",
    ]) {
      const d = deps();
      expect(
        (await check(new Request(url, { headers: original.headers }), d))
          ?.status,
      ).toBe(401);
      expect(d.reserveNonce).not.toHaveBeenCalled();
    }
    expect(
      (
        await check(
          new Request(target, {
            method: "POST",
            headers: original.headers,
            body: "",
          }),
        )
      )?.status,
    ).toBe(401);
  });
  it("binds idempotency key, nonce, version and exact bytes without v1 fallback", async () => {
    const url = "https://fake.example/api/partner/routers/router-a/actions",
      body = '{"action":"reboot"}';
    const original = req("POST", url, body, "job-a");
    for (const [name, value] of [
      ["Idempotency-Key", "job-b"],
      ["X-Vectra-Partner-Request-Id", "22222222-2222-4222-8222-222222222222"],
      ["X-Vectra-Partner-Version", "1"],
    ]) {
      const headers = new Headers(original.headers);
      headers.set(name!, value!);
      expect(
        (
          await check(
            new Request(url, { method: "POST", body, headers }),
            deps(),
            body,
          )
        )?.status,
      ).toBe(401);
    }
    expect((await check(original, deps(), body + " "))?.status).toBe(401);
  });
  it("atomically consumes exact nonce even concurrently and fails closed on outage", async () => {
    const d = deps(),
      r = req();
    const replies = await Promise.all([
      check(r.clone(), d),
      check(r.clone(), d),
    ]);
    expect(replies.filter((x) => x === null)).toHaveLength(1);
    expect(replies.find((x) => x)?.status).toBe(409);
    const unavailable = deps();
    unavailable.reserveNonce = async () => {
      throw new Error("fake database unavailable");
    };
    expect((await check(req(), unavailable))?.status).toBe(503);
  });
  it("retains future timestamps through their signed window plus margin", async () => {
    const d = deps();
    const headers = buildPartnerRequestHeaders(
      SECRET,
      "GET",
      target,
      "",
      "",
      NOW + 300000,
    );
    expect(await check(new Request(target, { headers }), d)).toBeNull();
    expect(d.reserveNonce).toHaveBeenCalledWith(
      expect.any(String),
      expect.any(String),
      new Date(NOW + 900000),
    );
  });
});
