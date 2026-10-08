import { describe, expect, it } from "vitest";
import { partnerWebhooks, routers } from "@vectra/db";
import {
  partnerWebhookEventSchema,
  routerCheckInResponseSchema,
  routerRegisterResponseSchema,
} from "@vectra/contracts";

describe("partner scoping schema", () => {
  it("stores the partner of a router and of a webhook", () => {
    expect(routers.partnerId.name).toBe("partner_id");
    expect(partnerWebhooks.partnerId.name).toBe("partner_id");
  });

  it("knows the vendor-access event", () => {
    expect(partnerWebhookEventSchema.parse("router.vendor_access")).toBe(
      "router.vendor_access",
    );
  });

  it("lets a check-in answer name the owner's brand", () => {
    const shape = routerCheckInResponseSchema.shape as Record<string, unknown>;
    expect(shape.brand).toBeDefined();
  });

  it("lets a register answer name the owner's brand", () => {
    const shape = routerRegisterResponseSchema.shape as Record<string, unknown>;
    expect(shape.brand).toBeDefined();
  });
});
