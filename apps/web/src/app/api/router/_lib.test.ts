import { describe, expect, it } from "vitest";

import { toRouteErrorResponse } from "./_lib";

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
});
