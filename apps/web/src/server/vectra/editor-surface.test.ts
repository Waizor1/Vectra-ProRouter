import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import {
  buildLastControllerUpdateAttempt,
  buildLastPasswallUpdateAttempt,
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

  it("suppresses stale failure when installed controller already converged", () => {
    const attempt = buildLastControllerUpdateAttempt({
      jobs: [
        createJob({
          payload: { artifactVersion: "0.1.12-r11" },
        }),
      ],
      results: [
        createJobResult({
          payload: {
            error: "opkg install vectra-controller-agent: signal: killed",
          },
        }),
      ],
      installedControllerVersion: "0.1.12-r11",
    });

    expect(attempt).toMatchObject({
      jobState: "failed",
      resultStatus: "success",
      artifactVersion: "0.1.12-r11",
      summary:
        "controller уже на 0.1.12-r11; старый failure-result после self-update больше не актуален",
    });
  });

  it("accepts terminal compatibility jobs for controller self-update history", () => {
    const attempt = buildLastControllerUpdateAttempt({
      jobs: [
        createJob({
          id: "controller-terminal-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "controller-self-update",
            artifactVersion: "0.1.12-r13",
            command: "opkg install --force-reinstall ...",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "controller-terminal-result",
          jobId: "controller-terminal-job",
          status: "success",
          payload: {
            stdout: "controller self-update to 0.1.12-r13 queued",
          },
        }),
      ],
      installedControllerVersion: "0.1.12-r11",
    });

    expect(attempt).toMatchObject({
      jobState: "succeeded",
      resultStatus: "success",
      artifactVersion: "0.1.12-r13",
      summary: "controller self-update to 0.1.12-r13 queued",
    });
  });
});

describe("buildLastPasswallUpdateAttempt", () => {
  it("returns null when passwall update history is absent", () => {
    expect(
      buildLastPasswallUpdateAttempt({
        jobs: [],
        results: [],
      }),
    ).toBeNull();
  });

  it("summarizes runtime-only convergence and drift from per-package results", () => {
    const attempt = buildLastPasswallUpdateAttempt({
      jobs: [
        createJob({
          id: "pw-job-1",
          type: "update_passwall_packages",
          state: "succeeded",
          payload: {
            targetVersion: "26.4.10-1",
            originSource: "vectra",
            updateScope: "managed-stack",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "pw-result-1",
          jobId: "pw-job-1",
          status: "success",
          payload: {
            targetVersion: "26.4.10-1",
            driftDetected: true,
            packageResults: [
              {
                package: "xray-core",
                targetVersion: "26.3.27-r1",
                status: "runtime-only-converged",
                pathUsed: "built-in-updater",
                packageVersionAfter: "25.10.15-r1",
                runtimeVersionAfter: "Xray 26.4.15",
                driftDetected: true,
              },
            ],
          },
        }),
      ],
    });

    expect(attempt).toMatchObject({
      jobState: "succeeded",
      resultStatus: "success",
      strategy: null,
      targetVersion: "26.4.10-1",
      packageTargetVersion: null,
      runtimeTargetVersion: null,
      originSource: "vectra",
      updateScope: "managed-stack",
      driftDetected: true,
      deliveryBlocked: false,
      deliveryBlockedReason: null,
      fallbackSummary:
        "xray-core: built-in updater уже держал runtime Xray 26.4.15; запись пакета осталась 25.10.15-r1",
      summary:
        "xray-core: built-in updater уже держал runtime Xray 26.4.15; запись пакета осталась 25.10.15-r1",
    });
  });

  it("surfaces the first failed package when managed-stack payload contains per-package errors", () => {
    const attempt = buildLastPasswallUpdateAttempt({
      jobs: [
        createJob({
          id: "pw-job-2",
          type: "update_passwall_packages",
          state: "failed",
          payload: {
            targetVersion: "26.3.27-r1",
            originSource: "vectra",
            updateScope: "managed-stack",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "pw-result-2",
          jobId: "pw-job-2",
          status: "failure",
          payload: {
            packageResults: [
              {
                package: "xray-core",
                targetVersion: "26.3.27-r1",
                status: "failed",
                pathUsed: "package",
                error: "artifact checksum mismatch",
                driftDetected: false,
              },
            ],
          },
        }),
      ],
    });

    expect(attempt?.summary).toBe("xray-core: artifact checksum mismatch");
    expect(attempt?.driftDetected).toBe(false);
  });

  it("ignores newer scoped-package jobs when resolving the last stack attempt", () => {
    const attempt = buildLastPasswallUpdateAttempt({
      jobs: [
        createJob({
          id: "pw-managed",
          type: "update_passwall_packages",
          state: "failed",
          createdAt: new Date("2026-04-18T09:34:00Z"),
          payload: {
            targetVersion: "26.4.10-1",
            originSource: "vectra",
            updateScope: "managed-stack",
          },
        }),
        createJob({
          id: "pw-scoped",
          type: "update_passwall_packages",
          state: "succeeded",
          createdAt: new Date("2026-04-18T09:36:00Z"),
          payload: {
            targetVersion: "26.3.27-r1",
            originSource: "vectra",
            updateScope: "scoped-package",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "pw-managed-result",
          jobId: "pw-managed",
          status: "failure",
          reportedAt: new Date("2026-04-18T09:35:00Z"),
          payload: {
            packageResults: [
              {
                package: "v2ray-geoip",
                targetVersion: "202603260032.1",
                status: "storage-blocked",
                pathUsed: "package",
                error:
                  "v2ray-geoip package path skipped: not enough overlay space",
                driftDetected: false,
              },
            ],
          },
        }),
        createJobResult({
          id: "pw-scoped-result",
          jobId: "pw-scoped",
          status: "success",
          reportedAt: new Date("2026-04-18T09:37:00Z"),
          payload: {
            packageResults: [
              {
                package: "xray-core",
                targetVersion: "26.3.27-r1",
                status: "runtime-only-converged",
                pathUsed: "not-needed",
                driftDetected: true,
              },
            ],
          },
        }),
      ],
    });

    expect(attempt).toMatchObject({
      jobState: "failed",
      resultStatus: "failure",
      updateScope: "managed-stack",
      summary:
        "v2ray-geoip: package path пропущен из-за места (v2ray-geoip package path skipped: not enough overlay space)",
    });
  });

  it("surfaces storage-blocked package paths honestly", () => {
    const attempt = buildLastPasswallUpdateAttempt({
      jobs: [
        createJob({
          id: "pw-job-3",
          type: "update_passwall_packages",
          state: "failed",
          payload: {
            strategy: "managed-stack-package-first",
            targetVersion: "26.4.10-1",
            packageTargetVersion: "26.4.10-r1",
            originSource: "vectra",
            updateScope: "managed-stack",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "pw-result-3",
          jobId: "pw-job-3",
          status: "failure",
          payload: {
            packageResults: [
              {
                package: "xray-core",
                targetVersion: "26.3.27-r1",
                packageTargetVersion: "26.3.27-r1",
                status: "storage-blocked",
                pathUsed: "package",
                packageVersionBefore: "25.10.15-r1",
                packageVersionAfter: "25.10.15-r1",
                runtimeVersionBefore: "Xray 26.4.15",
                runtimeVersionAfter: "Xray 26.4.15",
                driftDetected: false,
                error: "xray-core package path skipped: not enough overlay space",
              },
            ],
          },
        }),
      ],
    });

    expect(attempt?.summary).toBe(
      "xray-core: package path пропущен из-за места (xray-core package path skipped: not enough overlay space)",
    );
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
