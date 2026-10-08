import { partnerWebhookPayloadSchema } from "@vectra/contracts";
import { partnerWebhooks } from "@vectra/db";
import { describe, expect, it, vi } from "vitest";

// The seam between the notice and the outbox: nothing here is mocked except
// the environment, so a payload the contract would refuse fails this test and
// not the partner's backend in production.
const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "test",
    VECTRA_CONNECT_WEBHOOK_URL: "https://backend.example.test/hooks/prorouter",
    VECTRA_CONNECT_WEBHOOK_SECRET: "vendor-access-seam-vectra-secret-0123456789",
    VECTRA_PARTNERS: JSON.stringify([
      {
        id: "bloopcat",
        brand: "bloopcat",
        label: "BloopCat",
        secrets: ["bloopcat-partner-secret-0123456789abcdef"],
        webhookUrl: "https://bloopcat.example/partner/prorouter/webhook",
        webhookSecret: "bloopcat-webhook-secret-0123456789abcdef",
      },
    ]),
  } as Record<string, unknown>,
}));
vi.mock("~/env", () => envMock);
vi.mock("~/server/db", () => ({ db: {} }));

const { createFakeDb } = await import("./testing/fake-db");
const { notifyVendorAccessWithDb } = await import("./vendor-access");

const ROUTER_ID = "6f0c2d8e-4b1a-4c3e-9d57-2a8b1c0e9f31";

describe("vendor access reaches the partner's outbox", () => {
  it("queues a payload the webhook contract accepts, for the router's own partner", async () => {
    const fake = createFakeDb();

    const id = await notifyVendorAccessWithDb(
      fake.db as never,
      { id: ROUTER_ID, ownerRef: "bc_1", partnerId: "bloopcat" },
      { kind: "config_apply", by: "operator" },
    );

    expect(id).toEqual(expect.any(String));
    const [row] = fake.inserts(partnerWebhooks);
    expect(row).toMatchObject({
      event: "router.vendor_access",
      routerId: ROUTER_ID,
      partnerId: "bloopcat",
    });
    const payload = partnerWebhookPayloadSchema.parse(row!.payload);
    expect(payload).toMatchObject({
      event: "router.vendor_access",
      routerId: ROUTER_ID,
      ownerRef: "bc_1",
      detail: { kind: "config_apply", by: "operator" },
    });
  });

  it("queues nothing for Vectra's own router even with webhooks configured", async () => {
    const fake = createFakeDb();

    expect(
      await notifyVendorAccessWithDb(
        fake.db as never,
        { id: ROUTER_ID, ownerRef: "vc_1", partnerId: null },
        { kind: "reboot", by: "operator" },
      ),
    ).toBeNull();
    expect(fake.inserts(partnerWebhooks)).toEqual([]);
  });
});
