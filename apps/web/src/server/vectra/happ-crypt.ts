/**
 * HAPP CRYPT v5 minting seam.
 *
 * Happ "crypto links" encrypt a plaintext subscription URL so that only the
 * Happ end-user app can decrypt it, hiding the underlying VLESS keys. crypt5
 * (the current standard) has a closed algorithm, so the only license-clean way
 * to mint a real `happ://crypt5/` link is Happ's official API:
 *
 *   POST https://crypto.happ.su/api-v2.php  {"url":"<plaintext sub URL>"}
 *     -> 200 {"encrypted_link":"happ://crypt5/<payload>"}
 *
 * This mirrors the Go implementation in
 * router/vectra-controller-pro/internal/happcrypt/api.go: HTTPS-pin the
 * endpoint, bound the request with a timeout, retry transient failures with
 * linear backoff, and validate that the returned link is a crypt5 link.
 */

/** Happ's official crypto-link minting service (Cloudflare-fronted, no auth). */
export const DEFAULT_HAPP_CRYPT_ENDPOINT = "https://crypto.happ.su/api-v2.php";

/** Required prefix of a minted crypt5 link (matches Go happcrypt.PrefixV5). */
export const HAPP_CRYPT5_PREFIX = "happ://crypt5/";

const DEFAULT_TIMEOUT_MS = 15_000;
const RETRIES = 2;
const BACKOFF_STEP_MS = 500;
const MAX_RESPONSE_BYTES = 1 << 20;

/**
 * Short-lived cache keyed by the source subscription URL. The underlying URL is
 * stable until rotation, so caching the minted link lets us avoid re-sending a
 * secret-bearing URL off-box on every operator request. The TTL is intentionally
 * short so a rotated URL re-mints promptly.
 */
const CACHE_TTL_MS = 10 * 60 * 1000;

type CacheEntry = { link: string; expiresAt: number };
const linkCache = new Map<string, CacheEntry>();

export type MintHappCrypt5Options = {
  endpoint?: string;
  timeoutMs?: number;
};

function requireHttpsEndpoint(raw: string): URL {
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    throw new Error("happcrypt: invalid endpoint URL");
  }
  // A downgraded endpoint must never carry a secret-bearing URL.
  if (parsed.protocol !== "https:") {
    throw new Error(
      `happcrypt: endpoint must be https (got "${parsed.protocol.replace(/:$/, "")}")`,
    );
  }
  return parsed;
}

async function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Mint a `happ://crypt5/` link for a plaintext subscription URL via Happ's
 * official API.
 *
 * Throws if the source URL is empty, the endpoint is not https, the API never
 * returns a 2xx response, or the response is not a crypt5 link. Successful
 * results are cached for a short TTL keyed by the source URL.
 *
 * The source URL and the minted link are secret-bearing; they are never logged.
 */
export async function mintHappCrypt5Link(
  url: string,
  opts: MintHappCrypt5Options = {},
): Promise<string> {
  const subUrl = url.trim();
  if (subUrl === "") {
    throw new Error("happcrypt: empty subscription URL");
  }

  const endpoint = requireHttpsEndpoint(
    opts.endpoint ?? DEFAULT_HAPP_CRYPT_ENDPOINT,
  );
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;

  const now = Date.now();
  const cached = linkCache.get(subUrl);
  if (cached && cached.expiresAt > now) {
    return cached.link;
  }
  if (cached) {
    linkCache.delete(subUrl);
  }

  const body = JSON.stringify({ url: subUrl });
  let lastError: unknown;

  for (let attempt = 0; attempt <= RETRIES; attempt++) {
    if (attempt > 0) {
      await delay(attempt * BACKOFF_STEP_MS);
    }

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    let response: Response;
    try {
      response = await fetch(endpoint, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          accept: "application/json",
        },
        body,
        signal: controller.signal,
      });
    } catch (error) {
      lastError = error;
      clearTimeout(timer);
      continue;
    }
    clearTimeout(timer);

    let raw: string;
    try {
      raw = await readBounded(response);
    } catch (error) {
      lastError = error;
      continue;
    }

    if (!response.ok) {
      lastError = new Error(
        `happcrypt: API status ${response.status}: ${raw.slice(0, 300)}`,
      );
      continue;
    }

    let parsed: { encrypted_link?: unknown };
    try {
      parsed = JSON.parse(raw) as { encrypted_link?: unknown };
    } catch (error) {
      lastError = error;
      continue;
    }

    const link = parsed.encrypted_link;
    if (typeof link !== "string" || !link.startsWith(HAPP_CRYPT5_PREFIX)) {
      // A wrong-shaped response is non-transient: fail fast rather than retry.
      throw new Error(
        `happcrypt: API returned a non-crypt5 link "${
          typeof link === "string" ? link : String(link)
        }"`,
      );
    }

    linkCache.set(subUrl, { link, expiresAt: Date.now() + CACHE_TTL_MS });
    return link;
  }

  throw new Error(
    `happcrypt: crypt5 API failed after ${RETRIES + 1} attempt(s)${
      lastError instanceof Error ? `: ${lastError.message}` : ""
    }`,
  );
}

async function readBounded(response: Response): Promise<string> {
  const text = await response.text();
  return text.length > MAX_RESPONSE_BYTES
    ? text.slice(0, MAX_RESPONSE_BYTES)
    : text;
}

/** Test-only helper to reset the module-level cache between cases. */
export function __clearHappCryptCacheForTests(): void {
  linkCache.clear();
}
