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

const { notifyVendorAccessForRoutersWithDb, notifyVendorAccessWithDb } =
  await import("./vendor-access");

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

  it("logs which router and which kind were lost, never the owner or the query", async () => {
    // A driver error carries the failed statement WITH its parameters, and the
    // webhook payload (which holds the partner's ownerRef) is one of them.
    enqueue.mockRejectedValue(
      Object.assign(
        new Error(
          'Failed query: insert into "partner_webhooks" ... params: {"ownerRef":"bc_1"}',
        ),
        { cause: new Error("connection terminated") },
      ),
    );
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);

    await notifyVendorAccessWithDb(
      {} as never,
      { id: "r1", ownerRef: "bc_1", partnerId: "bloopcat" },
      { kind: "maintenance", by: "operator" },
    );

    const logged = JSON.stringify(log.mock.calls);
    expect(logged).toContain("r1");
    expect(logged).toContain("maintenance");
    expect(logged).toContain("connection terminated");
    expect(logged).not.toContain("bc_1");
    expect(logged).not.toContain("partner_webhooks");
    log.mockRestore();
  });
});

describe("vendor access for a batch of routers", () => {
  const rows = [
    { id: "r-vectra", ownerRef: "vc_1", partnerId: null },
    { id: "r-bloop", ownerRef: "bc_1", partnerId: "bloopcat" },
    { id: "r-free", ownerRef: null, partnerId: "bloopcat" },
  ];
  const clientReturning = (found: unknown[]) => {
    const chain = { from: () => chain, where: () => Promise.resolve(found) };
    return { select: () => chain } as never;
  };

  it("announces each partner router once and nobody else", async () => {
    const announced = await notifyVendorAccessForRoutersWithDb(
      clientReturning(rows),
      ["r-vectra", "r-bloop", "r-free"],
      { kind: "config_apply", by: "operator" },
    );

    expect(announced).toBe(1);
    expect(enqueue).toHaveBeenCalledTimes(1);
    expect(enqueue).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({
        routerId: "r-bloop",
        partnerId: "bloopcat",
        detail: { kind: "config_apply", by: "operator" },
      }),
    );
  });

  it("reads nothing for an empty batch", async () => {
    const client = { select: vi.fn() } as never;

    expect(
      await notifyVendorAccessForRoutersWithDb(client, [], {
        kind: "config_apply",
        by: "operator",
      }),
    ).toBe(0);
    expect((client as { select: unknown }).select).not.toHaveBeenCalled();
  });

  it("never fails the operator's action when the routers cannot be read", async () => {
    const chain = { from: () => chain, where: () => Promise.reject(new Error("db down")) };
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);

    await expect(
      notifyVendorAccessForRoutersWithDb({ select: () => chain } as never, ["r-bloop"], {
        kind: "config_apply",
        by: "operator",
      }),
    ).resolves.toBe(0);
    expect(log).toHaveBeenCalled();
    log.mockRestore();
  });
});
