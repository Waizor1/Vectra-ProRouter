import { describe, expect, it, vi } from "vitest";
import { z } from "zod";

import {
  parseJsonBody,
  redactQueryParams,
  stackFrames,
  toRouteErrorResponse,
} from "./_lib";

describe("toRouteErrorResponse", () => {
  it("preserves explicit route HTTP status codes", async () => {
    const response = toRouteErrorResponse(
      Object.assign(new Error("Forbidden router action"), { status: 403 }),
    );

    await expect(response.json()).resolves.toEqual({
      error: "Forbidden router action",
    });
    expect(response.status).toBe(403);
  });

  it("answers a payload that fails validation with 400", () => {
    const result = z.object({ a: z.string() }).safeParse({});
    const response = toRouteErrorResponse(result.error);

    expect(response.status).toBe(400);
  });

  // A Drizzle error's message quotes its SQL and bound parameters; register
  // answers anonymous callers, so nothing of it may reach the response. The
  // server log keeps the message and stack, minus the parameters.
  it("never echoes an error that carries no public status", async () => {
    const consoleError = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const response = toRouteErrorResponse(
      new Error(
        'Failed query: select * from "vectra_router_credential" where "token_hash" = $1\nparams: s3cret-token-hash',
      ),
    );

    expect(response.status).toBe(500);
    await expect(response.json()).resolves.toEqual({
      error: "Internal server error",
    });
    const logged = JSON.stringify(consoleError.mock.calls);
    expect(logged).toContain("Failed query");
    expect(logged).toContain("at ");
    expect(logged).not.toContain("s3cret-token-hash");
    consoleError.mockRestore();
  });
});

describe("redactQueryParams / stackFrames", () => {
  it("cuts everything from the bound parameters on", () => {
    expect(
      redactQueryParams("Failed query: select 1\nparams: a,b\n    at x"),
    ).toBe("Failed query: select 1\nparams: [redacted]");
  });

  // A bound value that looks like a stack frame must not end up logged.
  it("logs only the real frames, never a frame-like parameter", () => {
    const error = new Error(
      "Failed query: insert into x values ($1)\nparams: \n    at s3cret-token (fake.ts:1:1)",
    );
    const frames = stackFrames(error);

    expect(frames).not.toContain("s3cret-token");
    expect(frames.split("\n").every((line) => /^\s+at /.test(line))).toBe(true);
    expect(frames.length).toBeGreaterThan(0);
  });
});

describe("parseJsonBody", () => {
  it("parses a JSON body within the cap", async () => {
    const request = new Request("https://example.test/api/router/check-in", {
      method: "POST",
      body: JSON.stringify({ ok: true }),
    });

    await expect(parseJsonBody(request)).resolves.toEqual({ ok: true });
  });

  it("refuses a declared length over the cap without reading it", async () => {
    const request = new Request("https://example.test/api/router/register", {
      method: "POST",
      headers: { "content-length": String(64) },
      body: "x".repeat(64),
    });

    await expect(parseJsonBody(request, 32)).rejects.toMatchObject({
      status: 413,
    });
  });

  it("stops reading a streamed body once it passes the cap", async () => {
    let pulls = 0;
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        pulls += 1;
        controller.enqueue(new Uint8Array(16).fill(0x20));
        if (pulls > 1000) {
          controller.close();
        }
      },
    });
    const request = new Request("https://example.test/api/router/register", {
      method: "POST",
      body,
      // @ts-expect-error -- undici needs this for a streamed request body
      duplex: "half",
    });

    await expect(parseJsonBody(request, 64)).rejects.toMatchObject({
      status: 413,
    });
    expect(pulls).toBeLessThan(10);
  });

  it("answers malformed JSON with 400", async () => {
    const request = new Request("https://example.test/api/router/check-in", {
      method: "POST",
      body: "{not json",
    });

    await expect(parseJsonBody(request)).rejects.toMatchObject({
      status: 400,
      message: "Request body must be valid JSON",
    });
  });
});
