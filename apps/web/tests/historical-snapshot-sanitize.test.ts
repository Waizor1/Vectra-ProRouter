import { describe, expect, it } from "vitest";

import {
  sanitizePasswallRawSnapshot,
  sanitizeUCIAssignment,
} from "../scripts/sanitize-historical-passwall-snapshots.mjs";

describe("historical PassWall2 snapshot sanitization", () => {
  it("masks sensitive keys without dropping non-secret operational fields", () => {
    const sanitized = sanitizePasswallRawSnapshot({
      node: {
        remarks: "Primary node",
        address: "example.com",
        port: 443,
        username: "operator",
        password: "raw-secret",
        uuid: "11111111-1111-4111-8111-111111111111",
      },
      subscription: {
        url: "https://example.com/subscription-token",
        addMode: "2",
      },
    });

    expect(sanitized).toEqual({
      node: {
        remarks: "Primary node",
        address: "example.com",
        port: 443,
        username: "<stored-secret>",
        password: "<stored-secret>",
        uuid: "<stored-secret>",
      },
      subscription: {
        url: "<stored-secret>",
        addMode: "2",
      },
    });
  });

  it("masks sensitive UCI assignment lines while preserving harmless lines", () => {
    expect(
      sanitizeUCIAssignment("passwall2.node_1.password='raw-secret'")
    ).toBe("passwall2.node_1.password='<stored-secret>'");
    expect(sanitizeUCIAssignment("passwall2.node_1.remarks='Node 1'")).toBeNull();
  });
});
