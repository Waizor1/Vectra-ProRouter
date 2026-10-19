import { describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";
import { verifyEnrollmentToken } from "~/server/vectra/router-auth-integrity";

import { fleetRouter } from "./fleet";

// Fake key; the procedure gets its database from the caller's context.
const SECRETS_KEY = vi.hoisted(() => "fleet-enrollment-token-test-key-0123456789");
vi.mock("~/env", () => ({ env: { NODE_ENV: "test", VECTRA_SECRETS_KEY: SECRETS_KEY } }));
vi.mock("~/server/db", () => ({ db: {} }));

const DEVICE = "vectra-0a1b2c3d4e5f";

function createMockDb() {
  const inserted: Array<Record<string, unknown>> = [];
  return {
    inserted,
    db: {
      insert() {
        return {
          values(value: Record<string, unknown>) {
            inserted.push(value);
            return Promise.resolve([]);
          },
        };
      },
    },
  };
}

type MintCaller = {
  mintEnrollmentToken: (input: { deviceIdentifier: string; ttlHours?: number }) => Promise<{
    token: string;
    expiresAt: string;
  }>;
};

function caller(db: unknown, operatorSession: unknown = { subject: "operator" }) {
  return createCallerFactory(fleetRouter as never)({
    db: db as never,
    operatorSession: operatorSession as never,
    headers: new Headers(),
  }) as unknown as MintCaller;
}

describe("fleet.mintEnrollmentToken", () => {
  it("mints a 24 h token for the device and records the device and expiry, never the token", async () => {
    const mock = createMockDb();
    const before = Math.floor(Date.now() / 1000);

    const minted = await caller(mock.db).mintEnrollmentToken({ deviceIdentifier: `  ${DEVICE} ` });

    const expiresAtUnix = Date.parse(minted.expiresAt) / 1000;
    expect(expiresAtUnix - before).toBeGreaterThanOrEqual(24 * 3600);
    expect(expiresAtUnix - before).toBeLessThanOrEqual(24 * 3600 + 5);
    expect(minted.token).toBe(`vpt1.${expiresAtUnix}.${minted.token.split(".")[2]}`);
    expect(
      verifyEnrollmentToken({
        secretsKey: SECRETS_KEY,
        token: minted.token,
        deviceIdentifier: DEVICE,
        nowUnix: before,
      }),
    ).toBe("valid");
    expect(mock.inserted).toHaveLength(1);
    expect(Object.keys(mock.inserted[0] ?? {}).sort()).toEqual(["message", "metadata", "severity", "type"]);
    expect(mock.inserted[0]).toMatchObject({ type: "router.enrollment.token_minted", severity: "info" });
    expect(mock.inserted[0]?.metadata).toEqual({ deviceIdentifier: DEVICE, expiresAt: minted.expiresAt });
    expect(JSON.stringify(mock.inserted)).not.toContain(minted.token.split(".")[2]);
  });

  it("takes 1..168 hours and nothing else", async () => {
    const mock = createMockDb();
    const week = await caller(mock.db).mintEnrollmentToken({ deviceIdentifier: DEVICE, ttlHours: 168 });
    expect(
      verifyEnrollmentToken({
        secretsKey: SECRETS_KEY,
        token: week.token,
        deviceIdentifier: DEVICE,
        nowUnix: Math.floor(Date.now() / 1000),
      }),
    ).toBe("valid");

    for (const ttlHours of [0, 169, 2.5]) {
      await expect(caller(mock.db).mintEnrollmentToken({ deviceIdentifier: DEVICE, ttlHours })).rejects.toMatchObject({
        code: "BAD_REQUEST",
      });
    }
    await expect(caller(mock.db).mintEnrollmentToken({ deviceIdentifier: "   " })).rejects.toMatchObject({
      code: "BAD_REQUEST",
    });
    expect(mock.inserted).toHaveLength(1);
  });

  it("is operator-only", async () => {
    const mock = createMockDb();
    await expect(caller(mock.db, null).mintEnrollmentToken({ deviceIdentifier: DEVICE })).rejects.toMatchObject({
      code: "UNAUTHORIZED",
    });
    expect(mock.inserted).toEqual([]);
  });
});
