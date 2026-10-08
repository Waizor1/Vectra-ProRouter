import { beforeEach, describe, expect, it, vi } from "vitest";

const { enqueue, schedule } = vi.hoisted(() => ({
  enqueue: vi.fn(async (..._args: unknown[]): Promise<string | null> => "w1"),
  schedule: vi.fn(),
}));
vi.mock("./partner-webhooks", () => ({
  enqueuePartnerWebhookWithDb: enqueue,
  schedulePartnerWebhookDelivery: schedule,
}));
vi.mock("~/server/db", () => ({ db: {} }));

const { notifyVendorAccessWithDb } = await import("./vendor-access");

beforeEach(() => {
  enqueue.mockReset();
  enqueue.mockResolvedValue("w1");
  schedule.mockReset();
});

describe("vendor access", () => {
  it("tells a partner when Vectra's engineer reaches its router", async () => {
    const id = await notifyVendorAccessWithDb(
      {} as never,
      { id: "r1", ownerRef: "bc_1", partnerId: "bloopcat" },
      { kind: "terminal", by: "operator" },
    );

    expect(id).toBe("w1");
    expect(enqueue).toHaveBeenCalledWith(expect.anything(), {
      event: "router.vendor_access",
      routerId: "r1",
      ownerRef: "bc_1",
      partnerId: "bloopcat",
      detail: { kind: "terminal", by: "operator" },
    });
    expect(schedule).toHaveBeenCalledTimes(1);
  });

  it("says who acted: a Telegram button is not an operator", async () => {
    await notifyVendorAccessWithDb(
      {} as never,
      { id: "r1", ownerRef: "bc_1", partnerId: "bloopcat" },
      { kind: "safe_repair", by: "telegram" },
    );

    expect(enqueue).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({
        detail: { kind: "safe_repair", by: "telegram" },
      }),
    );
  });

  it.each([
    ["Vectra's own router", { id: "r2", ownerRef: "vc_1", partnerId: null }],
    [
      "a router explicitly on the default partner",
      { id: "r2", ownerRef: "vc_1", partnerId: "vectra" },
    ],
    ["an unclaimed router", { id: "r3", ownerRef: null, partnerId: "bloopcat" }],
  ])("says nothing for %s", async (_name, router) => {
    expect(
      await notifyVendorAccessWithDb({} as never, router, {
        kind: "reboot",
        by: "operator",
      }),
    ).toBeNull();
    expect(enqueue).not.toHaveBeenCalled();
    expect(schedule).not.toHaveBeenCalled();
  });

  it("does not start a delivery when the partner has no webhook target", async () => {
    enqueue.mockResolvedValue(null);

    expect(
      await notifyVendorAccessWithDb(
        {} as never,
        { id: "r1", ownerRef: "bc_1", partnerId: "bloopcat" },
        { kind: "rename", by: "operator" },
      ),
    ).toBeNull();
    expect(schedule).not.toHaveBeenCalled();
  });

  it("never fails the operator's action when the notice cannot be queued", async () => {
    enqueue.mockRejectedValue(new Error("database is down"));
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);

    await expect(
      notifyVendorAccessWithDb(
        {} as never,
        { id: "r1", ownerRef: "bc_1", partnerId: "bloopcat" },
        { kind: "direct_mode", by: "operator" },
      ),
    ).resolves.toBeNull();
    expect(log).toHaveBeenCalled();
    log.mockRestore();
  });
});
