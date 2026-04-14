import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import {
  buildLastControllerUpdateAttempt,
  mergeCurrentLiveRouterDataIntoDraftConfig,
} from "./editor-surface";

type BuildArgs = Parameters<typeof buildLastControllerUpdateAttempt>[0];
type JobRow = BuildArgs["jobs"][number];
type JobResultRow = BuildArgs["results"][number];

const baseConfig = passwallDesiredConfigSchema.parse({
  basicSettings: {
    main: {
      mainSwitch: true,
      selectedNodeId: "myshunt",
      localhostProxy: true,
      clientProxy: true,
      nodeSocksPort: 1070,
      nodeSocksBindLocal: true,
      socksMainSwitch: false,
      extras: {},
    },
    dns: {
      directQueryStrategy: "UseIP",
      remoteDnsProtocol: "tcp",
      remoteDns: "1.1.1.1",
      remoteDnsDoh: "https://1.1.1.1/dns-query",
      remoteDnsDetour: "remote",
      remoteFakeDns: false,
      remoteDnsQueryStrategy: "UseIPv4",
      dnsHosts: [],
      dnsRedirect: true,
      extras: {},
    },
    log: {
      enableNodeLog: false,
      level: "error",
      extras: {},
    },
    maintenance: {
      backupPaths: [],
      extras: {},
    },
    socks: [],
    shuntRules: [],
  },
  nodes: [
    {
      id: "myshunt",
      label: "myshunt",
      protocol: "shunt",
      enabled: true,
      group: "default",
      tags: [],
      extras: {},
    },
  ],
  subscriptions: {
    filterKeywordMode: "0",
    discardList: [],
    keepList: [],
    typePreferences: {},
    domainStrategy: "auto",
    items: [],
  },
  appUpdate: {
    binaryPaths: {
      xray: "/usr/bin/xray",
      singBox: "/usr/bin/sing-box",
      hysteria: "/usr/bin/hysteria",
      geoview: "/usr/bin/geoview",
    },
    updateStrategy: "package-preferred",
    targetVersions: {},
    extras: {},
  },
  ruleManage: {
    geoipUrl: "https://example.com/geoip.dat",
    geositeUrl: "https://example.com/geosite.dat",
    assetDirectory: "/usr/share/v2ray/",
    autoUpdate: true,
    scheduleMode: "daily",
    enabledAssets: ["geoip", "geosite"],
    shuntRules: [],
    extras: {},
  },
});

function createJob(overrides: Partial<JobRow> = {}): JobRow {
  return {
    id: "job-1",
    routerId: "router-1",
    type: "update_controller",
    state: "failed",
    payload: {
      artifactVersion: "0.1.12-r2",
    },
    desiredRevisionId: null,
    dedupeKey: null,
    deliverAfter: null,
    deliveredAt: null,
    completedAt: new Date("2026-04-09T10:00:00Z"),
    createdAt: new Date("2026-04-09T09:55:00Z"),
    ...overrides,
  };
}

function createJobResult(overrides: Partial<JobResultRow> = {}): JobResultRow {
  return {
    id: "result-1",
    jobId: "job-1",
    routerId: "router-1",
    status: "failure",
    payload: {},
    reportedAt: new Date("2026-04-09T10:00:00Z"),
    ...overrides,
  };
}

describe("buildLastControllerUpdateAttempt", () => {
  it("returns null when controller update history is absent", () => {
    expect(
      buildLastControllerUpdateAttempt({
        jobs: [],
        results: [],
      }),
    ).toBeNull();
  });

  it("prefers failure over accepted and uses payload.error as summary", () => {
    const attempt = buildLastControllerUpdateAttempt({
      jobs: [createJob()],
      results: [
        createJobResult({
          id: "accepted",
          status: "accepted",
          reportedAt: new Date("2026-04-09T09:56:00Z"),
          payload: { message: "job accepted" },
        }),
        createJobResult({
          id: "failure",
          status: "failure",
          reportedAt: new Date("2026-04-09T10:00:00Z"),
          payload: {
            error: "opkg update: exit status 7",
            stderr: "Collected errors:\n * opkg_download: failed",
          },
        }),
      ],
    });

    expect(attempt).toMatchObject({
      jobState: "failed",
      resultStatus: "failure",
      artifactVersion: "0.1.12-r2",
      summary: "opkg update: exit status 7",
    });
    expect(attempt?.reportedAt).toEqual(new Date("2026-04-09T10:00:00Z"));
  });

  it("falls back to stderr when error is absent", () => {
    const attempt = buildLastControllerUpdateAttempt({
      jobs: [createJob({ state: "failed" })],
      results: [
        createJobResult({
          payload: {
            stderr: "\nCollected errors:\n* failed to download",
          },
        }),
      ],
    });

    expect(attempt?.summary).toBe("Collected errors:");
  });

  it("falls back to stdout and running copy when needed", () => {
    const stdoutAttempt = buildLastControllerUpdateAttempt({
      jobs: [createJob({ state: "failed" })],
      results: [
        createJobResult({
          payload: {
            stdout: "\nDownloading Packages.gz\nUpdated list",
          },
        }),
      ],
    });
    const runningAttempt = buildLastControllerUpdateAttempt({
      jobs: [
        createJob({
          state: "running",
          payload: { artifactVersion: "0.1.12-r2" },
        }),
      ],
      results: [],
    });

    expect(stdoutAttempt?.summary).toBe("Downloading Packages.gz");
    expect(runningAttempt?.summary).toBe("обновление ещё выполняется");
    expect(runningAttempt?.resultStatus).toBeNull();
  });
});

describe("mergeCurrentLiveRouterDataIntoDraftConfig", () => {
  it("keeps live-only subscriptions, nodes and socks in the editable draft base", () => {
    const draftConfig = structuredClone(baseConfig);
    const currentLiveConfig = structuredClone(baseConfig);

    currentLiveConfig.basicSettings.main.selectedNodeId = "private-node";
    currentLiveConfig.basicSettings.socks.push({
      id: "router-socks",
      enabled: true,
      nodeId: "private-node",
      port: 2080,
      bindLocal: true,
      autoswitchBackupNodeIds: [],
      extras: {},
    });
    currentLiveConfig.nodes.push({
      id: "private-node",
      label: "Private node",
      protocol: "vmess",
      enabled: true,
      group: "default",
      address: "example.invalid",
      port: 443,
      transport: "tcp",
      tls: true,
      tags: [],
      extras: {},
    });
    currentLiveConfig.subscriptions.items.push({
      id: "manual-sub",
      remark: "Manual subscription",
      url: "https://example.invalid/sub",
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {},
    });

    const merged = mergeCurrentLiveRouterDataIntoDraftConfig({
      draftConfig,
      currentLiveConfig,
    });

    expect(merged.basicSettings.main.selectedNodeId).toBe("private-node");
    expect(merged.basicSettings.socks.map((entry) => entry.id)).toEqual([
      "router-socks",
    ]);
    expect(merged.nodes.map((node) => node.id)).toEqual([
      "myshunt",
      "private-node",
    ]);
    expect(merged.subscriptions.items.map((item) => item.id)).toEqual([
      "manual-sub",
    ]);
  });

  it("does not overwrite existing draft-managed entries with live data", () => {
    const draftConfig = structuredClone(baseConfig);
    draftConfig.basicSettings.main.selectedNodeId = "myshunt";
    draftConfig.subscriptions.items.push({
      id: "known-sub",
      remark: "Draft remark",
      url: "https://draft.invalid/sub",
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {},
    });

    const currentLiveConfig = structuredClone(baseConfig);
    currentLiveConfig.subscriptions.items.push({
      id: "known-sub",
      remark: "Live remark",
      url: "https://live.invalid/sub",
      enabled: false,
      addMode: "1",
      metadata: {},
      extras: {},
    });

    const merged = mergeCurrentLiveRouterDataIntoDraftConfig({
      draftConfig,
      currentLiveConfig,
    });

    expect(merged.basicSettings.main.selectedNodeId).toBe("myshunt");
    expect(merged.subscriptions.items).toEqual(draftConfig.subscriptions.items);
  });
});
