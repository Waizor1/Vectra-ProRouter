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

  it("reads the client ip from forwarding headers", () => {
    const request = new Request("https://example.test/api/router/register", {
      headers: {
        "x-forwarded-for": "198.51.100.10, 203.0.113.1",
      },
    });

    expect(readRequestIp(request)).toBe("198.51.100.10");
  });
});
