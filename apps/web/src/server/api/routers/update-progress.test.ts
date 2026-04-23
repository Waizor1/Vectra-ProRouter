import { describe, expect, it } from "vitest";

import { createCallerFactory } from "~/server/api/trpc";

import { updateRouter } from "./update";

const ROUTER_A_ID = "11111111-1111-4111-8111-111111111111";
const ROUTER_B_ID = "22222222-2222-4222-8222-222222222222";
const PASSWALL_JOB_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const REBOOT_JOB_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";

function createRouterRow(args: {
  id: string;
  displayName?: string | null;
  hostname?: string | null;
  deviceIdentifier: string;
}) {
  return {
    id: args.id,
    deviceIdentifier: args.deviceIdentifier,
    displayName: args.displayName ?? null,
    hostname: args.hostname ?? null,
    panelDomain: null,
    model: "AX3000T",
    boardName: "xiaomi,mi-router-ax3000t",
    target: "mediatek/filogic",
    architecture: "aarch64_cortex-a53",
    openwrtRelease: "24.10.6",
    status: "active",
    importState: "approved",
    controllerChannel: "stable",
    rolloutGroupId: null,
    pendingImportRevisionId: null,
    activeRevisionId: null,
    lastAppliedRevisionId: null,
    lastConfigDigest: null,
    approvedAt: new Date("2026-04-21T08:00:00.000Z"),
    lastSeenAt: new Date("2026-04-21T09:00:00.000Z"),
    lastCheckInAt: new Date("2026-04-21T09:00:00.000Z"),
    lastDirectModeAt: null,
    lastRescueReason: null,
    createdAt: new Date("2026-04-20T08:00:00.000Z"),
    updatedAt: new Date("2026-04-21T09:00:00.000Z"),
  };
}

function createSnapshotRow(args: { routerId: string; hostname?: string | null }) {
  return {
    id: `snapshot-${args.routerId}`,
    routerId: args.routerId,
    source: "check_in",
    payload: {
      boardName: "xiaomi,mi-router-ax3000t",
      target: "mediatek/filogic",
      architecture: "aarch64_cortex-a53",
      openwrtRelease: "24.10.6",
      hostname: args.hostname ?? null,
      packageVersions: {
        "luci-app-passwall2": "26.4.10-r1",
      },
      binaryVersions: {
        xray: "26.4.15",
      },
    },
    passwallEnabled: true,
    selectedNodeId: "myshunt",
    nodeCount: 12,
    subscriptionCount: 1,
    controllerVersion: "0.1.13-r1",
    passwallAppVersion: "26.4.10-r1",
    createdAt: new Date("2026-04-21T09:00:00.000Z"),
  };
}

function createPasswallJob() {
  return {
    id: PASSWALL_JOB_ID,
    routerId: ROUTER_A_ID,
    type: "update_passwall_packages",
    state: "running",
    payload: {
      updateScope: "managed-stack",
      targetVersion: "26.4.20-1",
      packageTargetVersion: "26.4.20-r1",
    },
    desiredRevisionId: null,
    dedupeKey: "update_passwall_packages:test",
    deliverAfter: null,
    deliveredAt: new Date("2026-04-21T09:00:05.000Z"),
    completedAt: null,
    createdAt: new Date("2026-04-21T09:00:00.000Z"),
  };
}

function createRebootJob() {
  return {
    id: REBOOT_JOB_ID,
    routerId: ROUTER_B_ID,
    type: "run_terminal_command",
    state: "succeeded",
    payload: {
      command: "(sleep 5; /sbin/reboot) &",
      timeoutSeconds: 15,
      purpose: "router-reboot",
    },
    desiredRevisionId: null,
    dedupeKey: "router_reboot:test",
    deliverAfter: null,
    deliveredAt: new Date("2026-04-21T09:00:10.000Z"),
    completedAt: new Date("2026-04-21T09:00:14.000Z"),
    createdAt: new Date("2026-04-21T09:00:10.000Z"),
  };
}

function createRebootResult() {
  return {
    id: "result-reboot-1",
    jobId: REBOOT_JOB_ID,
    routerId: ROUTER_B_ID,
    status: "success",
    payload: {
      command: "(sleep 5; /sbin/reboot) &",
      timeoutSeconds: 15,
      startedAt: "2026-04-21T09:00:10.000Z",
      completedAt: "2026-04-21T09:00:11.000Z",
      durationMs: 1000,
      exitCode: 0,
      timedOut: false,
      stdout: "reboot scheduled",
      stderr: "",
      stdoutTruncated: false,
      stderrTruncated: false,
      error: null,
    },
    reportedAt: new Date("2026-04-21T09:00:11.000Z"),
  };
}

function createMockDb(selectResponses: unknown[][]) {
  let selectIndex = 0;

  const nextSelectResult = () => selectResponses[selectIndex++] ?? [];

  const makeSelectChain = () => {
    const chain = {
      from() {
        return chain;
      },
      where() {
        return chain;
      },
      orderBy() {
        return chain;
      },
      limit() {
        return Promise.resolve(nextSelectResult());
      },
      then<TResult1 = unknown, TResult2 = never>(
        onfulfilled?:
          | ((value: unknown[]) => TResult1 | PromiseLike<TResult1>)
          | null,
        onrejected?:
          | ((reason: unknown) => TResult2 | PromiseLike<TResult2>)
          | null,
      ) {
        return Promise.resolve(nextSelectResult()).then(onfulfilled, onrejected);
      },
    };

    return chain;
  };

  return {
    select() {
      return makeSelectChain();
    },
  };
}

function createProtectedCaller(db: unknown) {
  return createCallerFactory(updateRouter as never)({
    db: db as never,
    operatorSession: { subject: "operator" } as never,
    headers: new Headers(),
  }) as {
    launchProgress: (input: {
      jobIds: string[];
    }) => Promise<{
      items: Array<{
        jobId: string;
        routerId: string;
        displayName: string;
        kind: string;
        jobState: string;
        resultStatus: string | null;
      }>;
    }>;
  };
}

describe("update.launchProgress", () => {
  it("returns normalized monitoring items for the requested job ids", async () => {
    const caller = createProtectedCaller(
      createMockDb([
        [createPasswallJob(), createRebootJob()],
        [
          createRouterRow({
            id: ROUTER_A_ID,
            displayName: "Pilot Router A",
            hostname: "pilot-a",
            deviceIdentifier: "pilot-router-a",
          }),
          createRouterRow({
            id: ROUTER_B_ID,
            displayName: null,
            hostname: null,
            deviceIdentifier: "pilot-router-b",
          }),
        ],
        [
          createSnapshotRow({ routerId: ROUTER_A_ID, hostname: "pilot-a" }),
          createSnapshotRow({ routerId: ROUTER_B_ID, hostname: "router-b" }),
        ],
        [createRebootResult()],
      ]),
    );

    const result = await caller.launchProgress({
      jobIds: [PASSWALL_JOB_ID, REBOOT_JOB_ID],
    });

    expect(result.items).toHaveLength(2);
    expect(result.items[0]).toMatchObject({
      jobId: PASSWALL_JOB_ID,
      routerId: ROUTER_A_ID,
      displayName: "Pilot Router A",
      kind: "passwall-update",
      jobState: "running",
      resultStatus: null,
    });
    expect(result.items[1]).toMatchObject({
      jobId: REBOOT_JOB_ID,
      routerId: ROUTER_B_ID,
      displayName: "router-b",
      kind: "router-reboot",
      jobState: "succeeded",
      resultStatus: "success",
    });
  });
});
