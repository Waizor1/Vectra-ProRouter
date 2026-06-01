import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { happCryptRouter } from "~/server/api/routers/happ-crypt";
import {
  __clearHappCryptCacheForTests,
  HAPP_CRYPT5_PREFIX,
  mintHappCrypt5Link,
} from "./happ-crypt";

const SUB_URL = "https://sub.example.invalid/api/sub/secret-token";
const CRYPT5_LINK = `${HAPP_CRYPT5_PREFIX}AbCdEf123`;
const ENDPOINT = "https://crypto.example.invalid/api-v2.php";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("mintHappCrypt5Link", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    __clearHappCryptCacheForTests();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns the happ://crypt5/ link on success", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ encrypted_link: CRYPT5_LINK }),
    );

    const link = await mintHappCrypt5Link(SUB_URL, { endpoint: ENDPOINT });

    expect(link).toBe(CRYPT5_LINK);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [calledUrl, init] = fetchMock.mock.calls[0] as [
      string | URL,
      RequestInit,
    ];
    expect(String(calledUrl)).toBe(ENDPOINT);
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ url: SUB_URL });
  });

  it("throws when the API returns a non-crypt5 link", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ encrypted_link: "happ://crypt4/legacy" }),
    );

    await expect(
      mintHappCrypt5Link(SUB_URL, { endpoint: ENDPOINT }),
    ).rejects.toThrow(/non-crypt5/);
    // Wrong-shaped responses are non-transient: no retry storm.
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("throws when the API never returns a 2xx", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "boom" }, 502));

    await expect(
      mintHappCrypt5Link(SUB_URL, { endpoint: ENDPOINT, timeoutMs: 50 }),
    ).rejects.toThrow(/crypt5 API failed/);
    // Initial attempt + 2 retries.
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("caches the link to avoid a second network call for the same URL", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ encrypted_link: CRYPT5_LINK }),
    );

    const first = await mintHappCrypt5Link(SUB_URL, { endpoint: ENDPOINT });
    const second = await mintHappCrypt5Link(SUB_URL, { endpoint: ENDPOINT });

    expect(first).toBe(CRYPT5_LINK);
    expect(second).toBe(CRYPT5_LINK);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("rejects an empty url without any network call", async () => {
    await expect(
      mintHappCrypt5Link("   ", { endpoint: ENDPOINT }),
    ).rejects.toThrow(/empty subscription URL/);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects a non-https endpoint without any network call", async () => {
    await expect(
      mintHappCrypt5Link(SUB_URL, {
        endpoint: "http://crypto.example.invalid/api-v2.php",
      }),
    ).rejects.toThrow(/must be https/);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("happCrypt.encrypt tRPC procedure", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    __clearHappCryptCacheForTests();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function createCaller() {
    return createCallerFactory(happCryptRouter as never)({
      db: {} as never,
      operatorSession: { subject: "operator" } as never,
      headers: new Headers(),
    }) as {
      encrypt: (input: { url: string }) => Promise<{ link: string }>;
    };
  }

  it("returns { link } for a valid url", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ encrypted_link: CRYPT5_LINK }),
    );
    const caller = createCaller();

    const result = await caller.encrypt({ url: SUB_URL });

    expect(result).toEqual({ link: CRYPT5_LINK });
  });

  it("rejects an invalid url at the zod boundary before any network call", async () => {
    const caller = createCaller();

    await expect(caller.encrypt({ url: "not-a-url" })).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
