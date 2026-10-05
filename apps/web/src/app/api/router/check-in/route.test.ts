import { beforeEach, describe, expect, it, vi } from "vitest";

const checkInRouter = vi.fn(async () => ({ ok: true }));

vi.mock("~/server/vectra/router-control", () => ({ checkInRouter }));
vi.mock("~/server/vectra/router-auto-onboarding", () => ({
  safelyMaybeAdvanceRouterOnboarding: vi.fn(async () => undefined),
}));
vi.mock("~/server/vectra/auth", () => ({
  authenticateRouter: vi.fn(async (headers: Headers) => {
    const routerId = headers.get("x-vectra-router-id");
    return routerId
      ? { router: { id: routerId }, credential: { devicePublicKey: "key" } }
      : null;
  }),
}));

const { POST } = await import("./route");

function checkIn(routerId: string, body = "{}") {
  return POST(
    new Request("https://example.test/api/router/check-in", {
      method: "POST",
      headers: { "x-vectra-router-id": routerId },
      body,
    }),
  );
}

describe("POST /api/router/check-in", () => {
  beforeEach(() => {
    checkInRouter.mockClear();
  });

  // A router checks in every ~45-60 s; a token hammering the endpoint is
  // turned away before its body is read or the database is touched.
  it("rate-limits one router token without affecting another", async () => {
    const statuses: number[] = [];
    for (let i = 0; i < 31; i += 1) {
      statuses.push((await checkIn("router-a")).status);
    }

    expect(statuses.slice(0, 30).every((status) => status === 200)).toBe(true);
    expect(statuses[30]).toBe(429);
    expect(checkInRouter).toHaveBeenCalledTimes(30);
    expect((await checkIn("router-b")).status).toBe(200);
  });

  // Register keeps a 2 MB cap; an authenticated router's check-in is let
  // through up to 8 MB, since the agent retries a refused one forever.
  it("reads a check-in body well past the register cap", async () => {
    const body = JSON.stringify({ pad: "x".repeat(3 * 1024 * 1024) });

    expect((await checkIn("router-big", body)).status).toBe(200);
    expect(checkInRouter).toHaveBeenCalledTimes(1);
  });
});

describe("POST /api/router/check-in observability and reads", () => {
  it("hands the authenticated router row to the check-in and reports Server-Timing", async () => {
    checkInRouter.mockClear();
    const response = await checkIn("router-timing");
    expect(response.status).toBe(200);
    expect(response.headers.get("server-timing")).toMatch(/^app;dur=\d+\.\d$/);
    expect(checkInRouter).toHaveBeenCalledWith("router-timing", {}, {
      devicePublicKey: "key",
      router: { id: "router-timing" },
    });

    const unauthorized = await POST(
      new Request("https://example.test/api/router/check-in", { method: "POST", body: "{}" }),
    );
    expect(unauthorized.status).toBe(401);
    expect(unauthorized.headers.get("server-timing")).toMatch(/^app;dur=/);
  });
});
