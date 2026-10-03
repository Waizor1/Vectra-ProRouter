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

const RIGHT = "correct horse battery staple";

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

    const right = await login(POST, RIGHT);
    expect(right.headers.get("location")).toBe("/fleet");
    expect(setCookie).toHaveBeenCalledTimes(1);
  });

  it("locks an address out after 5 failures, even with the right password", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 5; i += 1) {
      expect((await login(POST, `guess-${i}`)).headers.get("location")).toBe(
        "/login?error=1",
      );
    }
    const blocked = await login(POST, RIGHT);

    expect(blocked.headers.get("location")).toBe("/login?error=rate");
    expect(setCookie).not.toHaveBeenCalled();
    // Another address is unaffected.
    expect(
      (await login(POST, RIGHT, "203.0.113.7")).headers.get("location"),
    ).toBe("/fleet");
  });

  it("never counts a successful login", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 10; i += 1) {
      expect((await login(POST, RIGHT)).headers.get("location")).toBe("/fleet");
    }
  });

  // Outside flooding exhausts the overall failure budget; an operator on a
  // clean address must still get in.
  it("lets a clean address sign in while the overall failure budget is spent", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 30; i += 1) {
      await login(POST, "wrong", `198.51.100.${i}`);
    }

    expect(
      (await login(POST, RIGHT, "203.0.113.99")).headers.get("location"),
    ).toBe("/fleet");
  });

  it("refuses an address that is part of the flood, even with the right password", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 30; i += 1) {
      await login(POST, "wrong", `198.51.100.${i}`);
    }

    expect(
      (await login(POST, RIGHT, "198.51.100.3")).headers.get("location"),
    ).toBe("/login?error=rate");
    expect(setCookie).not.toHaveBeenCalled();
  });

  // The decision is made after the body is read, with no await before the
  // failure is counted: a burst from one address gets at most 5 compares.
  it("gives a burst of concurrent attempts from one address at most 5 compares", async () => {
    const { POST } = await loadRoute();

    const responses = await Promise.all(
      Array.from({ length: 20 }, (_, i) => login(POST, `guess-${i}`)),
    );
    const compared = responses.filter(
      (response) => response.headers.get("location") === "/login?error=1",
    );

    expect(compared).toHaveLength(5);
    expect(
      responses.filter(
        (response) => response.headers.get("location") === "/login?error=rate",
      ),
    ).toHaveLength(15);
  });

  it("refuses even a clean address once 300 failures a minute are reached", async () => {
    const { POST } = await loadRoute();

    for (let i = 0; i < 299; i += 1) {
      await login(POST, "wrong", `10.0.${Math.floor(i / 250)}.${i % 250}`);
    }
    expect(
      (await login(POST, RIGHT, "203.0.113.50")).headers.get("location"),
    ).toBe("/fleet");

    await login(POST, "wrong", "10.9.9.9");
    expect(
      (await login(POST, RIGHT, "203.0.113.51")).headers.get("location"),
    ).toBe("/login?error=rate");
  });
});
