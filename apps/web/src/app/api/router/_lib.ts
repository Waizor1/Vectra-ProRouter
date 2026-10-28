import { createHash } from "node:crypto";

import { zodV3Error } from "@vectra/contracts";
import { ZodError } from "zod";

// The largest body a live router has sent is ~620 KB (a register carrying its
// whole PassWall import, 2026-10-03). Reading stops at this cap instead of
// buffering whatever an anonymous caller streams at register.
export const ROUTER_REQUEST_BODY_MAX_BYTES = 2 * 1024 * 1024;
// Check-in and job-result come from an authenticated router, which retries a
// refused request forever: their cap is far looser, and an oversized field
// inside is cut down rather than refused (router-payload-bounds).
export const AUTHENTICATED_ROUTER_BODY_MAX_BYTES = 8 * 1024 * 1024;

function readErrorStatus(error: Error) {
  const value =
    (error as { status?: unknown; statusCode?: unknown }).status ??
    (error as { statusCode?: unknown }).statusCode;
  return typeof value === "number" && value >= 400 && value < 600
    ? value
    : null;
}

function routeError(status: number, message: string) {
  return Object.assign(new Error(message), { status });
}

// A Drizzle query error ends its message with "params: <bound values>" —
// router tokens, keys and configs. Everything from there on is cut before the
// message is logged.
export function redactQueryParams(text: string) {
  return text.replace(/params:[\s\S]*$/, "params: [redacted]");
}

// Only the frames of the stack: its head repeats the message, and a bound
// value could itself contain lines that look like frames, so everything up to
// the end of the message is skipped before frames are picked out.
export function stackFrames(error: Error) {
  const stack = error.stack ?? "";
  const messageAt = error.message ? stack.indexOf(error.message) : -1;
  const trace =
    messageAt >= 0 ? stack.slice(messageAt + error.message.length) : stack;
  return trace
    .split("\n")
    .filter((line) => /^\s+at /.test(line))
    .join("\n");
}

async function readBodyBytes(request: Request, maxBytes: number) {
  const declaredLength = Number(request.headers.get("content-length"));
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    throw routeError(413, "Request body is too large");
  }

  // Counted while streaming: content-length is optional (chunked) and a
  // client may lie about it.
  const chunks: Uint8Array[] = [];
  let received = 0;
  if (request.body) {
    const reader = request.body.getReader();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      received += value.byteLength;
      if (received > maxBytes) {
        await reader.cancel().catch(() => undefined);
        throw routeError(413, "Request body is too large");
      }
      chunks.push(value);
    }
  }

  return Buffer.concat(chunks);
}

function parseJsonBytes(bytes: Buffer): unknown {
  try {
    return JSON.parse(bytes.toString("utf8")) as unknown;
  } catch {
    throw routeError(400, "Request body must be valid JSON");
  }
}

export async function parseJsonBody(
  request: Request,
  maxBytes = ROUTER_REQUEST_BODY_MAX_BYTES,
): Promise<unknown> {
  return parseJsonBytes(await readBodyBytes(request, maxBytes));
}

/**
 * parseJsonBody plus the sha256 (lower-case hex) of the exact bytes received,
 * which is what a router's check-in signature covers.
 */
export async function parseJsonBodyWithDigest(
  request: Request,
  maxBytes = ROUTER_REQUEST_BODY_MAX_BYTES,
): Promise<{ payload: unknown; bodySha256Hex: string }> {
  const bytes = await readBodyBytes(request, maxBytes);
  return {
    payload: parseJsonBytes(bytes),
    bodySha256Hex: createHash("sha256").update(bytes).digest("hex"),
  };
}

// A status error may carry the exact JSON answer it was written for (a
// machine-readable `code` next to the message, see routerErrorWithBody in
// router-auth-policy.ts); every other status error answers `{ error }`.
function readResponseBody(error: Error) {
  const body = (error as { responseBody?: unknown }).responseBody;
  return body !== null && typeof body === "object" && !Array.isArray(body)
    ? (body as Record<string, unknown>)
    : null;
}

export function toRouteErrorResponse(error: unknown) {
  if (error instanceof ZodError) {
    return Response.json(
      {
        error: "Invalid request payload",
        issues: zodV3Error(error).flatten(),
      },
      { status: 400 },
    );
  }

  // Only an error that carries its own HTTP status was written for the
  // caller. Anything else is answered generically: register is reachable
  // anonymously, and a Drizzle error quotes its SQL and parameters.
  const status = error instanceof Error ? readErrorStatus(error) : null;
  if (error instanceof Error && status !== null) {
    return Response.json(readResponseBody(error) ?? { error: error.message }, {
      status,
    });
  }

  if (error instanceof Error) {
    console.error(
      "[router-api] unhandled error:",
      error.constructor.name,
      redactQueryParams(error.message),
      stackFrames(error),
    );
  } else {
    console.error("[router-api] unhandled non-error throw:", typeof error);
  }
  return Response.json({ error: "Internal server error" }, { status: 500 });
}
