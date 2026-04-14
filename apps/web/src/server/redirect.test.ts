import { describe, expect, it } from "vitest";

import { relativeRedirect } from "~/server/redirect";

describe("relativeRedirect", () => {
  it("returns a relative Location header for reverse-proxy-safe redirects", () => {
    const response = relativeRedirect("/fleet");

    expect(response.status).toBe(303);
    expect(response.headers.get("location")).toBe("/fleet");
  });

  it("preserves query strings and custom statuses", () => {
    const response = relativeRedirect("/login?error=1", 307);

    expect(response.status).toBe(307);
    expect(response.headers.get("location")).toBe("/login?error=1");
  });
});
