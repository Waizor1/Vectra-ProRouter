import { describe, expect, it } from "vitest";

import {
  MemoryWindowRateLimiter,
  readRequestIp,
} from "~/server/vectra/public-install-rate-limit";

describe("public install register rate limiter", () => {
  it("blocks traffic after the configured request budget", () => {
    const limiter = new MemoryWindowRateLimiter(2, 1000);

    expect(limiter.consume("1.2.3.4", 0)).toMatchObject({ allowed: true });
    expect(limiter.consume("1.2.3.4", 1)).toMatchObject({ allowed: true });
    expect(limiter.consume("1.2.3.4", 2)).toMatchObject({ allowed: false });
    expect(limiter.consume("1.2.3.4", 1200)).toMatchObject({ allowed: true });
  });

  it("prefers the proxy-controlled Vectra client-ip header", () => {
    const request = new Request("https://example.test/api/router/register", {
      headers: {
        "x-vectra-client-ip": "198.51.100.10",
        "x-real-ip": "203.0.113.20",
        "x-forwarded-for": "203.0.113.200, 203.0.113.1",
      },
    });

    expect(readRequestIp(request)).toBe("198.51.100.10");
  });

  it("does not trust arbitrary x-forwarded-for directly", () => {
    const request = new Request("https://example.test/api/router/register", {
      headers: {
        "x-forwarded-for": "198.51.100.10, 203.0.113.1",
      },
    });

    expect(readRequestIp(request)).toBe("unknown");
  });

  it("does not trust arbitrary x-real-ip directly", () => {
    const request = new Request("https://example.test/api/router/register", {
      headers: {
        "x-real-ip": "198.51.100.10",
      },
    });

    expect(readRequestIp(request)).toBe("unknown");
  });
});
