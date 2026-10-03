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

function checkIn(routerId: string) {
  return POST(
    new Request("https://example.test/api/router/check-in", {
      method: "POST",
      headers: { "x-vectra-router-id": routerId },
      body: "{}",
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
});
