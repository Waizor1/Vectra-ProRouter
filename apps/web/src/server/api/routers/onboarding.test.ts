import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { onboardingRouter } from "./onboarding";

const ROUTER_ID = "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9";

function createProfileRow(overrides: Record<string, unknown> = {}) {
  return {
    id: "profile-1",
    routerId: ROUTER_ID,
    enabled: true,
    targetHostname: "yuranrod-msk",
    displayName: "YuranRod-msk",
    subscriptionSecretCiphertext: "encrypted-secret-payload",
    subscriptionUrlHash:
      "56b13c6db79b2e4e0f4af3e1ac2e4c656f0deffdb3d7d85772a0ccf02f5f2a65",
    subscriptionRemark: "StarMY",
    baseline: "standard-non-hh",
    runtimePolicy: "auto-minimal-passwall-xray",
    verifyPolicy: "route-smoke",
    notes: "pilot onboarding",
    createdAt: new Date("2026-05-14T18:00:00.000Z"),
    updatedAt: new Date("2026-05-14T18:00:00.000Z"),
    ...overrides,
  };
}

function createMockDb({
  selectResponses,
  insertResponses = [],
}: {
  selectResponses: unknown[][];
  insertResponses?: unknown[][];
}) {
  let selectIndex = 0;
  let insertIndex = 0;
  const insertedValues: unknown[] = [];

  const nextSelectResult = () => selectResponses[selectIndex++] ?? [];
  const nextInsertResult = () => insertResponses[insertIndex++] ?? [];

  const makeSelectChain = () => ({
    from() {
      return this;
    },
    where() {
      return this;
    },
    orderBy() {
      return this;
    },
    limit() {
      return Promise.resolve(nextSelectResult());
    },
  });

  return {
    db: {
      select() {
        return makeSelectChain();
      },
      insert() {
        return {
          values(value: unknown) {
            insertedValues.push(value);
            const result = nextInsertResult();
            return {
              onConflictDoUpdate() {
                return {
                  returning() {
                    return Promise.resolve(result);
                  },
                };
              },
              returning() {
                return Promise.resolve(result);
              },
              then<TResult1 = unknown, TResult2 = never>(
                onfulfilled?:
                  | ((value: unknown[]) => TResult1 | PromiseLike<TResult1>)
                  | null,
                onrejected?:
                  | ((reason: unknown) => TResult2 | PromiseLike<TResult2>)
                  | null,
              ) {
                return Promise.resolve(result).then(onfulfilled, onrejected);
              },
            };
          },
        };
      },
    },
    insertedValues() {
      return insertedValues;
    },
  };
}

function createProtectedCaller(db: unknown) {
  return createCallerFactory(onboardingRouter as never)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  }) as {
    saveProfile: (input: {
      routerId: string;
      targetHostname: string;
      displayName: string;
      subscriptionUrl: string;
      subscriptionRemark: string;
      baseline: "standard-non-hh";
      runtimePolicy: "auto-minimal-passwall-xray";
      verifyPolicy: "route-smoke";
      notes: string;
    }) => Promise<{
      profile: {
        hasSubscription: boolean;
        subscriptionUrlHash: string | null;
      } | null;
      run: unknown;
    }>;
  };
}

describe("onboarding router", () => {
  it("saves a profile without returning raw subscription secrets", async () => {
    const profile = createProfileRow();
    const mock = createMockDb({
      insertResponses: [[profile], []],
      selectResponses: [[profile], []],
    });
    const caller = createProtectedCaller(mock.db);
    const subscriptionUrl = "https://sub.example.invalid/api/sub/secret-token";

    const result = await caller.saveProfile({
      routerId: ROUTER_ID,
      targetHostname: "YuranRod-msk",
      displayName: "YuranRod-msk",
      subscriptionUrl,
      subscriptionRemark: "StarMY",
      baseline: "standard-non-hh",
      runtimePolicy: "auto-minimal-passwall-xray",
      verifyPolicy: "route-smoke",
      notes: "pilot onboarding",
    });

    expect(result.profile).toMatchObject({
      hasSubscription: true,
      subscriptionUrlHash: profile.subscriptionUrlHash,
    });
    const serialized = JSON.stringify(result);
    expect(serialized).not.toContain(subscriptionUrl);
    expect(serialized).not.toContain("secret-token");

    const [profileInsert, eventInsert] = mock.insertedValues() as Array<
      Record<string, unknown>
    >;
    expect(profileInsert?.subscriptionSecretCiphertext).toBeTypeOf("string");
    expect(profileInsert?.subscriptionUrlHash).toBeTypeOf("string");
    expect(JSON.stringify(eventInsert)).not.toContain(subscriptionUrl);
    expect(JSON.stringify(eventInsert)).not.toContain("secret-token");
  });
});
