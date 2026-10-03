import { ZodError } from "zod";

// The largest body a live router has sent is ~620 KB (a register carrying its
// whole PassWall import, 2026-10-03). Reading stops at this cap instead of
// buffering whatever an anonymous caller streams at register.
export const ROUTER_REQUEST_BODY_MAX_BYTES = 2 * 1024 * 1024;

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

export async function parseJsonBody(
  request: Request,
  maxBytes = ROUTER_REQUEST_BODY_MAX_BYTES,
): Promise<unknown> {
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

  try {
    return JSON.parse(Buffer.concat(chunks).toString("utf8")) as unknown;
  } catch {
    throw routeError(400, "Request body must be valid JSON");
  }
}

export function toRouteErrorResponse(error: unknown) {
  if (error instanceof ZodError) {
    return Response.json(
      {
        error: "Invalid request payload",
        issues: error.flatten(),
      },
      { status: 400 },
    );
  }

  // Only an error that carries its own HTTP status was written for the
  // caller. Anything else is answered generically: register is reachable
  // anonymously, and a Drizzle error quotes its SQL and parameters — which is
  // why only the error's class is logged too.
  const status = error instanceof Error ? readErrorStatus(error) : null;
  if (error instanceof Error && status !== null) {
    return Response.json({ error: error.message }, { status });
  }

  console.error(
    "[router-api] unhandled error:",
    error instanceof Error ? error.constructor.name : typeof error,
  );
  return Response.json({ error: "Internal server error" }, { status: 500 });
}
