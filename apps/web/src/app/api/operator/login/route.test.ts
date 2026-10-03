import { beforeEach, describe, expect, it, vi } from "vitest";

const setCookie = vi.fn();

vi.mock("~/env", () => ({
  env: {
    NODE_ENV: "test",
    VECTRA_OPERATOR_USER: "operator",
    VECTRA_OPERATOR_PASSWORD: "correct horse battery staple",
  },
}));
vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({ set: setCookie })),
}));
vi.mock("~/server/operator-session", () => ({
  createOperatorSession: vi.fn(async () => "session"),
  getOperatorCookieName: () => "vectra_operator",
  getOperatorSessionMaxAgeSeconds: () => 3600,
}));

async function loadRoute() {
  vi.resetModules();
  return import("./route");
}

function login(
  POST: (request: Request) => Promise<Response>,
  password: string,
  clientIp = "198.51.100.10",
) {
  const body = new URLSearchParams({ username: "operator", password });
  return POST(
    new Request("https://example.test/api/operator/login", {
      method: "POST",
      headers: {
        "content-type": "application/x-www-form-urlencoded",
        "x-vectra-client-ip": clientIp,
      },
      body,
    }),
  );
}

describe("POST /api/operator/login", () => {
  beforeEach(() => {
    setCookie.mockClear();
  });

  it("signs in with the right password and refuses the wrong one", async () => {
    const { POST } = await loadRoute();

    const wrong = await login(POST, "correct horse battery");
    expect(wrong.headers.get("location")).toBe("/login?error=1");

    const right = await login(POST, "correct horse battery staple");
    expect(right.headers.get("location")).toBe("/fleet");
    expect(setCookie).toHaveBeenCalledTimes(1);
  });

  it("stops an address after 5 attempts a minute, even with the right password", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 5; i += 1) {
      expect((await login(POST, `guess-${i}`)).headers.get("location")).toBe(
        "/login?error=1",
      );
    }
    const blocked = await login(POST, "correct horse battery staple");

    expect(blocked.headers.get("location")).toBe("/login?error=rate");
    expect(setCookie).not.toHaveBeenCalled();
    // Another address is unaffected.
    expect(
      (
        await login(POST, "correct horse battery staple", "203.0.113.7")
      ).headers.get("location"),
    ).toBe("/fleet");
  });

  it("caps attempts across all addresses at 30 a minute", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 30; i += 1) {
      await login(POST, "wrong", `198.51.100.${i}`);
    }
    const blocked = await login(
      POST,
      "correct horse battery staple",
      "203.0.113.99",
    );

    expect(blocked.headers.get("location")).toBe("/login?error=rate");
  });
});
