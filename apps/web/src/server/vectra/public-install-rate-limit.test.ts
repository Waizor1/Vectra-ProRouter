import { describe, expect, it } from "vitest";

import {
  MemoryWindowRateLimiter,
  rateLimitKeyForIp,
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

  it("stays bounded under a flood of distinct keys", () => {
    const limiter = new MemoryWindowRateLimiter(2, 1000, 100);

    for (let i = 0; i < 1000; i += 1) {
      limiter.consume(`10.0.${Math.floor(i / 256)}.${i % 256}`, i);
    }

    expect(limiter.size).toBeLessThanOrEqual(100);
  });

  it("drops expired windows before live ones", () => {
    const limiter = new MemoryWindowRateLimiter(1, 1000, 2);

    limiter.consume("old", 0);
    limiter.consume("live", 900);
    // "old" has expired by now; "live" is still blocked within its window.
    limiter.consume("new", 1100);

    expect(limiter.size).toBe(2);
    expect(limiter.consume("live", 1200)).toMatchObject({ allowed: false });
  });

  it("keys IPv6 clients by their /64", () => {
    expect(rateLimitKeyForIp("2001:db8:1:2:aaaa::1")).toBe("2001:db8:1:2::/64");
    expect(rateLimitKeyForIp("2001:0db8:0001:0002:ffff:1:2:3")).toBe(
      "2001:db8:1:2::/64",
    );
    expect(rateLimitKeyForIp("2001:db8::1")).toBe("2001:db8:0:0::/64");
    expect(rateLimitKeyForIp("::1")).toBe("0:0:0:0::/64");
    expect(rateLimitKeyForIp("fe80::1%eth0")).toBe("fe80:0:0:0::/64");
    expect(rateLimitKeyForIp("::ffff:198.51.100.7")).toBe("198.51.100.7");
    expect(rateLimitKeyForIp("198.51.100.7")).toBe("198.51.100.7");
    expect(rateLimitKeyForIp("unknown")).toBe("unknown");
  });

  it("lets a whole /64 share one enrollment budget", () => {
    const limiter = new MemoryWindowRateLimiter(2, 1000);
    const hits = ["2001:db8:1:2::a", "2001:db8:1:2::b", "2001:db8:1:2::c"].map(
      (ip) => limiter.consume(rateLimitKeyForIp(ip), 0).allowed,
    );

    expect(hits).toEqual([true, true, false]);
    expect(
      limiter.consume(rateLimitKeyForIp("2001:db8:1:3::a"), 0).allowed,
    ).toBe(true);
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
