import { beforeEach, describe, expect, it, vi } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

const { notifyVendorAccessWithDb } = vi.hoisted(() => ({
  notifyVendorAccessWithDb: vi.fn(async (..._args: unknown[]) => null),
}));
vi.mock("~/server/vectra/vendor-access", () => ({ notifyVendorAccessWithDb }));

import { terminalRouter } from "./terminal";

beforeEach(() => {
  notifyVendorAccessWithDb.mockClear();
});

const ROUTER_ID = "0e7d2b52-e2d5-4e95-95c2-a193070dc0b9";

function routerRow(engineMode: "passwall" | "xray-direct") {
  return {
    id: ROUTER_ID,
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    engineMode,
    ownerRef: "bc_1",
    partnerId: "bloopcat",
  };
}

function snapshotRow(extra: Record<string, unknown> = {}) {
  return {
    id: "snapshot-1",
    routerId: ROUTER_ID,
    createdAt: new Date("2026-10-03T09:10:00.000Z"),
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      layoutFamily: "stock-layout",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      packageVersions: {},
      ...extra,
    },
  };
}

function createMockDb(selectResponses: unknown[][]) {
  let selectIndex = 0;
  const inserted: unknown[] = [];
  const chain = () => ({
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
      return Promise.resolve(selectResponses[selectIndex++] ?? []);
    },
  });
  return {
    db: {
      select: chain,
      insert() {
        return {
          values(value: Record<string, unknown>) {
            inserted.push(value);
            return {
              returning: () => Promise.resolve([{ id: "job-1", ...value }]),
            };
          },
        };
      },
    },
    inserted,
  };
}

function caller(db: unknown) {
  return createCallerFactory(terminalRouter)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  });
}

describe("terminal.queueCommand on xray-direct (vctl)", () => {
  it("queues when vctl's last check-in reports remoteShell: true", async () => {
    const mock = createMockDb([[routerRow("xray-direct")], [snapshotRow({ engineMode: "xray-direct", remoteShell: true })], []]);
    const job = await caller(mock.db).queueCommand({ routerId: ROUTER_ID, command: "uptime" });
    expect(job).toMatchObject({ type: "run_terminal_command", state: "queued" });
    expect(mock.inserted).toHaveLength(1);
  });

  it.each([
    ["false", { engineMode: "xray-direct", remoteShell: false }, "выключил доступ поддержки"],
    ["absent", { engineMode: "xray-direct" }, "ещё не сообщил"],
  ])("refuses up front when remoteShell is %s", async (_label, extra, reason) => {
    const mock = createMockDb([[routerRow("xray-direct")], [snapshotRow(extra)]]);
    const error: unknown = await caller(mock.db)
      .queueCommand({ routerId: ROUTER_ID, command: "uptime" })
      .catch((caught: unknown) => caught);
    expect(error).toMatchObject({ code: "PRECONDITION_FAILED" });
    expect(String((error as Error).message)).toContain(reason);
    expect(mock.inserted).toHaveLength(0);
  });

  it("refuses when the router has no snapshot yet", async () => {
    const mock = createMockDb([[routerRow("xray-direct")], []]);
    await expect(caller(mock.db).queueCommand({ routerId: ROUTER_ID, command: "uptime" })).rejects.toMatchObject({
      code: "PRECONDITION_FAILED",
    });
  });

  it("leaves PassWall routers as they were", async () => {
    const mock = createMockDb([[routerRow("passwall")], [snapshotRow()], []]);
    const job = await caller(mock.db).queueCommand({ routerId: ROUTER_ID, command: "uptime" });
    expect(job).toMatchObject({ type: "run_terminal_command" });
  });
});

describe("terminal.queueCommand and the partner's vendor-access notice", () => {
  it("tells the router's partner once a new command is queued", async () => {
    const mock = createMockDb([[routerRow("passwall")], [snapshotRow()], []]);
    await caller(mock.db).queueCommand({ routerId: ROUTER_ID, command: "uptime" });

    expect(notifyVendorAccessWithDb).toHaveBeenCalledTimes(1);
    expect(notifyVendorAccessWithDb).toHaveBeenCalledWith(
      mock.db,
      expect.objectContaining({ id: ROUTER_ID, ownerRef: "bc_1", partnerId: "bloopcat" }),
      { kind: "terminal", by: "operator" },
    );
  });

  it("says nothing when an active command is returned instead of a new one", async () => {
    const active = { id: "job-0", type: "run_terminal_command", state: "running" };
    const mock = createMockDb([[routerRow("passwall")], [snapshotRow()], [active]]);
    await caller(mock.db).queueCommand({ routerId: ROUTER_ID, command: "uptime" });

    expect(mock.inserted).toHaveLength(0);
    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });

  it("says nothing when the command is refused", async () => {
    const mock = createMockDb([
      [routerRow("xray-direct")],
      [snapshotRow({ engineMode: "xray-direct", remoteShell: false })],
    ]);
    await caller(mock.db)
      .queueCommand({ routerId: ROUTER_ID, command: "uptime" })
      .catch(() => null);

    expect(notifyVendorAccessWithDb).not.toHaveBeenCalled();
  });
});
