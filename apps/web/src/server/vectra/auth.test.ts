import { routerCredentials, routers } from "@vectra/db";
import { afterEach, describe, expect, it, vi } from "vitest";

import * as dbModule from "~/server/db";

import { authenticateRouter } from "./auth";
import type { FakeDb } from "./testing/fake-db";

vi.mock("~/server/db", async () => {
  const { createFakeDb } = await import("./testing/fake-db");
  const fake = createFakeDb();
  return { db: fake.db, __fake: fake };
});
const fake = (dbModule as unknown as { __fake: FakeDb }).__fake;

function headers(routerId: string) {
  return new Headers({ "x-vectra-router-id": routerId, "x-vectra-router-token": "token" });
}

function scriptAuth(credentialId: string, routerId: string, times: number) {
  fake.reset({
    selects: [
      [routerCredentials, Array.from({ length: times }, () => [{ id: credentialId, routerId }])],
      [routers, Array.from({ length: times }, () => [{ id: routerId }])],
    ],
  });
}

describe("authenticateRouter lastUsedAt bookkeeping", () => {
  afterEach(() => vi.useRealTimers());

  it("writes lastUsedAt at most once a minute per credential, and still authenticates every request", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-05T10:00:00Z"));
    scriptAuth("cred-a", "router-a", 4);

    // Three check-ins 45 s apart (the polling interval) and one more.
    expect(await authenticateRouter(headers("router-a"))).not.toBeNull();
    vi.advanceTimersByTime(45_000);
    expect(await authenticateRouter(headers("router-a"))).not.toBeNull();
    vi.advanceTimersByTime(45_000);
    expect(await authenticateRouter(headers("router-a"))).not.toBeNull();
    expect(fake.updates(routerCredentials)).toHaveLength(2);

    vi.advanceTimersByTime(10_000);
    expect(await authenticateRouter(headers("router-a"))).not.toBeNull();
    expect(fake.updates(routerCredentials)).toHaveLength(2);
  });

  it("throttles per credential, not fleet-wide", async () => {
    fake.reset({
      selects: [
        [routerCredentials, [[{ id: "cred-b", routerId: "router-b" }], [{ id: "cred-c", routerId: "router-c" }]]],
        [routers, [[{ id: "router-b" }], [{ id: "router-c" }]]],
      ],
    });
    await authenticateRouter(headers("router-b"));
    await authenticateRouter(headers("router-c"));
    expect(fake.updates(routerCredentials)).toHaveLength(2);
  });

  it("does not touch lastUsedAt for a rejected token", async () => {
    fake.reset({ selects: [[routerCredentials, [[]]]] });
    expect(await authenticateRouter(headers("router-d"))).toBeNull();
    expect(fake.updates(routerCredentials)).toHaveLength(0);
  });
});
