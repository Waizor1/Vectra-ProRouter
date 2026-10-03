import { isIP } from "node:net";

type Bucket = {
  windowStartedAt: number;
  hits: number;
};

// Enough distinct keys for any real burst; past it the oldest windows go
// first, so a flood of fresh addresses cannot grow the map without bound.
const DEFAULT_MAX_KEYS = 10_000;

export class MemoryWindowRateLimiter {
  private readonly buckets = new Map<string, Bucket>();

  constructor(
    private readonly limit: number,
    private readonly windowMs: number,
    private readonly maxKeys = DEFAULT_MAX_KEYS,
  ) {}

  consume(key: string, now = Date.now()) {
    const bucket = this.buckets.get(key);

    if (!bucket || now - bucket.windowStartedAt >= this.windowMs) {
      // Re-inserted, not updated: the map stays ordered by window start, so
      // eviction only ever has to look at its head.
      this.buckets.delete(key);
      this.evict(now);
      this.buckets.set(key, { windowStartedAt: now, hits: 1 });
      return {
        allowed: true,
        remaining: this.limit - 1,
        resetAt: now + this.windowMs,
      };
    }

    bucket.hits += 1;

    return {
      allowed: bucket.hits <= this.limit,
      remaining: Math.max(0, this.limit - bucket.hits),
      resetAt: bucket.windowStartedAt + this.windowMs,
    };
  }

  /** Hits in the key's current window, without counting one. */
  peek(key: string, now = Date.now()) {
    const bucket = this.buckets.get(key);
    return bucket && now - bucket.windowStartedAt < this.windowMs
      ? bucket.hits
      : 0;
  }

  get size() {
    return this.buckets.size;
  }

  private evict(now: number) {
    for (const [key, bucket] of this.buckets) {
      if (
        now - bucket.windowStartedAt < this.windowMs &&
        this.buckets.size < this.maxKeys
      ) {
        return;
      }
      this.buckets.delete(key);
    }
  }
}

export function readRequestIp(request: Request) {
  const trustedClientIp = request.headers.get("x-vectra-client-ip")?.trim();
  if (trustedClientIp) {
    return trustedClientIp;
  }

  // Deliberately ignore client-controlled X-Forwarded-For / X-Real-IP here.
  // Production Caddy injects x-vectra-client-ip from {remote_host}; direct app
  // traffic without that trusted proxy marker shares a single conservative bucket.
  return "unknown";
}

/**
 * The bucket key for a client address. An IPv6 client usually holds a whole
 * /64 and can rotate through it freely, so it is one bucket per /64; an
 * IPv4-mapped address is its IPv4 address.
 */
export function rateLimitKeyForIp(ip: string) {
  const address = ip.replace(/%.*$/, "");
  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(address);
  if (mapped?.[1]) {
    return mapped[1];
  }
  if (isIP(address) !== 6) {
    return ip;
  }

  const [head = "", tail] = address.split("::");
  const hextets = (part: string | undefined) =>
    part
      ? part
          .split(":")
          // An embedded IPv4 tail fills two hextets.
          .flatMap((group) => (group.includes(".") ? ["0", "0"] : [group]))
      : [];
  const leading = hextets(head);
  const trailing = hextets(tail);
  const groups =
    tail === undefined
      ? leading
      : [
          ...leading,
          ...Array<string>(8 - leading.length - trailing.length).fill("0"),
          ...trailing,
        ];

  return `${groups
    .slice(0, 4)
    .map((group) => group.toLowerCase().replace(/^0+(?=.)/, ""))
    .join(":")}::/64`;
}

export const publicInstallRegisterRateLimiter = new MemoryWindowRateLimiter(
  24,
  10 * 60 * 1000,
);

// Per authenticated router. A router checks in every ~45-60 s and reports two
// results per job (accepted + final), so these only bite a token that is being
// abused, never a router at work.
export const routerCheckInRateLimiter = new MemoryWindowRateLimiter(
  30,
  60 * 1000,
);
export const routerJobResultRateLimiter = new MemoryWindowRateLimiter(
  60,
  60 * 1000,
);

export function routerRateLimitedResponse(resetAt: number) {
  return Response.json(
    { error: "Too many router requests. Retry later." },
    {
      status: 429,
      headers: {
        "retry-after": String(
          Math.max(1, Math.ceil((resetAt - Date.now()) / 1000)),
        ),
      },
    },
  );
}
