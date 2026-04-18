type Bucket = {
  windowStartedAt: number;
  hits: number;
};

export class MemoryWindowRateLimiter {
  private readonly buckets = new Map<string, Bucket>();

  constructor(
    private readonly limit: number,
    private readonly windowMs: number,
  ) {}

  consume(key: string, now = Date.now()) {
    const bucket = this.buckets.get(key);

    if (!bucket || now - bucket.windowStartedAt >= this.windowMs) {
      this.buckets.set(key, { windowStartedAt: now, hits: 1 });
      return {
        allowed: true,
        remaining: this.limit - 1,
        resetAt: now + this.windowMs,
      };
    }

    bucket.hits += 1;
    this.buckets.set(key, bucket);

    return {
      allowed: bucket.hits <= this.limit,
      remaining: Math.max(0, this.limit - bucket.hits),
      resetAt: bucket.windowStartedAt + this.windowMs,
    };
  }
}

export function readRequestIp(request: Request) {
  const forwarded = request.headers.get("x-forwarded-for");
  if (forwarded) {
    return forwarded.split(",")[0]?.trim() ?? "unknown";
  }

  return request.headers.get("x-real-ip") ?? "unknown";
}

export const publicInstallRegisterRateLimiter = new MemoryWindowRateLimiter(
  24,
  10 * 60 * 1000,
);
