import { ZodError } from "zod";

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
      { status: 400 }
    );
  }

  if (error instanceof Error) {
    return Response.json({ error: error.message }, { status: 400 });
  }

  console.error("[router-api]", error);
  return Response.json({ error: "Internal server error" }, { status: 500 });
}
