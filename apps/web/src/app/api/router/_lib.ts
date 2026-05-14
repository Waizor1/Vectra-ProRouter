import { ZodError } from "zod";

function readErrorStatus(error: Error) {
  const value =
    (error as { status?: unknown; statusCode?: unknown }).status ??
    (error as { statusCode?: unknown }).statusCode;
  return typeof value === "number" && value >= 400 && value < 600
    ? value
    : null;
}

export async function parseJsonBody(request: Request): Promise<unknown> {
  try {
    return (await request.json()) as unknown;
  } catch {
    throw new Error("Request body must be valid JSON");
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

  if (error instanceof Error) {
    return Response.json(
      { error: error.message },
      { status: readErrorStatus(error) ?? 400 },
    );
  }

  console.error("[router-api]", error);
  return Response.json({ error: "Internal server error" }, { status: 500 });
}
