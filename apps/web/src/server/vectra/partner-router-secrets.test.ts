import { describe, expect, it, vi } from "vitest";
vi.mock("~/env", () => ({
  env: { VECTRA_SECRETS_KEY: "fake-local-test-secrets-key-only-123456" },
}));
import type { RouterInventory } from "@vectra/contracts";
import type { jobs, routerInventorySnapshots, routers } from "@vectra/db";
import {
  protectConnectInventory,
  hydrateConnectWifi,
  protectPartnerParams,
  hydratePartnerJobPayload,
} from "./partner-router-secrets";
const PASSWORD = "fake-guest-pass-123";
const ID = "router-test";
const NOW = new Date("2026-10-01T17:00:00Z");
const router = {
  id: ID,
  ownerRef: "acct-42",
  releasedAt: null,
  claimedAt: NOW,
} as typeof routers.$inferSelect;
function wire(ownerRef: string | null = "acct-42") {
  return {
    connect: {
      ownerRef,
      wifi: [{ band: "5G", ssid: "Fake guest", password: PASSWORD }],
    },
    rawSnapshot: { password: PASSWORD },
  } as unknown as RouterInventory;
}
function snapshot(ownerRef = "acct-42") {
  const protectedWire = protectConnectInventory(ID, ownerRef, wire());
  return {
    routerId: ID,
    payload: protectedWire.inventory,
    connectSecretCiphertext: protectedWire.ciphertext,
    createdAt: new Date(NOW.getTime() + 1000),
  } as typeof routerInventorySnapshots.$inferSelect;
}

describe("owner-bound confidential Connect persistence", () => {
  it("strips passwords before persistence and encrypts only matching wire ownership", () => {
    const result = protectConnectInventory(ID, "acct-42", wire());
    expect(JSON.stringify(result)).not.toContain(PASSWORD);
    expect(result.inventory.connect?.wifi).toEqual([
      { band: "5G", ssid: "Fake guest" },
    ]);
    expect(result.ciphertext).toBeTruthy();
    expect(protectConnectInventory(ID, "other", wire()).ciphertext).toBeNull();
    expect(
      protectConnectInventory(ID, "acct-42", wire(null)).ciphertext,
    ).toBeNull();
  });
  it("hydrates only the currently claimed matching owner, router and snapshot", () => {
    const row = snapshot();
    expect(hydrateConnectWifi(router, row)?.[0]).toMatchObject({
      password: PASSWORD,
    });
    for (const changed of [
      { ...router, ownerRef: "other" },
      { ...router, releasedAt: NOW },
      { ...router, id: "other-device" },
      { ...router, claimedAt: new Date(NOW.getTime() + 2000) },
    ])
      expect(JSON.stringify(hydrateConnectWifi(changed, row))).not.toContain(
        PASSWORD,
      );
    expect(
      JSON.stringify(
        hydrateConnectWifi(router, {
          ...row,
          connectSecretCiphertext: "corrupt",
        }),
      ),
    ).not.toContain(PASSWORD);
    expect(
      JSON.stringify(
        hydrateConnectWifi(router, {
          ...row,
          payload: wire(),
          connectSecretCiphertext: null,
        }),
      ),
    ).not.toContain(PASSWORD);
  });
  it("encrypts set_wifi job params and hydrates only authenticated matching delivery", () => {
    const payload = {
      origin: "partner_action",
      actionId: "action-test",
      ownerRef: "acct-42",
      action: "set_wifi",
      ...protectPartnerParams(
        "set_wifi",
        {
          ssid: "Fake guest",
          password: PASSWORD,
        },
        { routerId: ID, ownerRef: "acct-42", actionId: "action-test" },
      ),
    };
    expect(JSON.stringify(payload)).not.toContain(PASSWORD);
    const job = {
      id: "action-test",
      routerId: ID,
      payload,
    } as unknown as typeof jobs.$inferSelect;
    expect(hydratePartnerJobPayload(job, "acct-42")).toEqual({
      origin: "partner_action",
      actionId: "action-test",
      ownerRef: "acct-42",
      action: "set_wifi",
      params: { ssid: "Fake guest", password: PASSWORD },
    });
    expect(() => hydratePartnerJobPayload(job, "other")).toThrow(
      "owner mismatch",
    );
    expect(() =>
      hydratePartnerJobPayload({ ...job, id: "other-action" }, "acct-42"),
    ).toThrow("owner mismatch");
    expect(() =>
      hydratePartnerJobPayload(
        { ...job, payload: { ...payload, paramsCiphertext: "corrupt" } },
        "acct-42",
      ),
    ).toThrow();
  });
  it("rejects valid ciphertext transplanted to a different job, router, owner or action and unbound legacy envelopes", async () => {
    const { encryptJson } = await import("./secrets");
    const context = { routerId: ID, ownerRef: "acct-42", actionId: "original" };
    const secured = protectPartnerParams(
      "set_wifi",
      { ssid: "Fake guest", password: PASSWORD },
      context,
    );
    const job = {
      id: context.actionId,
      routerId: ID,
      payload: {
        origin: "partner_action",
        actionId: context.actionId,
        ownerRef: context.ownerRef,
        action: "set_wifi",
        ...secured,
      },
    } as unknown as typeof jobs.$inferSelect;
    const variants = [
      { ...job, id: "second", payload: { ...job.payload, actionId: "second" } },
      { ...job, routerId: "other-router" },
      { ...job, payload: { ...job.payload, ownerRef: "other-owner" } },
      { ...job, payload: { ...job.payload, action: "reboot" } },
      {
        ...job,
        payload: {
          ...job.payload,
          paramsCiphertext: encryptJson({
            ssid: "Fake guest",
            password: PASSWORD,
          }),
        },
      },
      {
        ...job,
        payload: {
          ...job.payload,
          paramsCiphertext: undefined,
          params: { ssid: "Fake guest", password: PASSWORD },
        },
      },
    ];
    for (const changed of variants)
      expect(() =>
        hydratePartnerJobPayload(changed, changed.payload.ownerRef as string),
      ).toThrow();
    expect(hydratePartnerJobPayload(job, "acct-42")).toMatchObject({
      params: { password: PASSWORD },
    });
  });
});
