import { describe, expect, it } from "vitest";

import { passwallDesiredConfigSchema } from "@vectra/contracts";

import {
  buildUnconfirmedChangesSummary,
  buildLastControllerUpdateAttempt,
  buildLastPasswallUpdateAttempt,
  buildRouterManagementTaskLog,
  mergeCurrentLiveRouterDataIntoDraftConfig,
} from "./editor-surface";
import { buildConfigTrustState } from "./config-trust";

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

    expect(attempt?.summary).toBe("failed to download");
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
            artifactVersion: "0.1.13-r1",
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
            stdout: "controller self-update to 0.1.13-r1 installed",
          },
        }),
      ],
      installedControllerVersion: "0.1.12-r11",
    });

    expect(attempt).toMatchObject({
      jobState: "succeeded",
      resultStatus: "success",
      artifactVersion: "0.1.13-r1",
      summary: "controller self-update to 0.1.13-r1 installed",
    });
  });

  it("does not let harmless success stderr hide controller self-update success", () => {
    const attempt = buildLastControllerUpdateAttempt({
      jobs: [
        createJob({
          id: "controller-terminal-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "controller-self-update",
            artifactVersion: "0.1.13-r5",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "controller-terminal-result",
          jobId: "controller-terminal-job",
          status: "success",
          payload: {
            stdout: "controller self-update to 0.1.13-r5 installed",
            stderr:
              "Collected errors:\n * resolve_conffiles: Existing conffile is different",
          },
        }),
      ],
    });

    expect(attempt?.summary).toBe(
      "controller self-update to 0.1.13-r5 installed",
    );
  });

  it("keeps terminal self-update LuCI failures red even when the agent version converged", () => {
    const attempt = buildLastControllerUpdateAttempt({
      jobs: [
        createJob({
          id: "controller-terminal-job",
          type: "run_terminal_command",
          state: "failed",
          payload: {
            purpose: "controller-self-update",
            artifactVersion: "0.1.13-r5",
          },
        }),
      ],
      results: [
        createJobResult({
          id: "controller-terminal-result",
          jobId: "controller-terminal-job",
          status: "failure",
          payload: {
            stderr:
              "Collected errors:\n * check_data_file_clashes: Package luci-app-vectra-controller wants to install file /._usr",
          },
        }),
      ],
      installedControllerVersion: "0.1.13-r5",
    });

    expect(attempt).toMatchObject({
      resultStatus: "failure",
      summary:
        "check_data_file_clashes: Package luci-app-vectra-controller wants to install file /._usr",
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

describe("buildUnconfirmedChangesSummary", () => {
  it("shows exact router-side changes for pending imported revisions", () => {
    const importedConfig = structuredClone(baseConfig);
    importedConfig.basicSettings.log.level = "warning";
    importedConfig.ruleManage.autoUpdate = false;

    const summary = buildUnconfirmedChangesSummary({
      importState: "out_of_sync",
      configTrust: buildConfigTrustState({
        routerReachable: true,
        lastCheckInAt: new Date("2026-04-19T12:00:00Z"),
        authoritativeDigest: "digest-a",
        snapshotDigest: "digest-b",
        revisions: [
          {
            configDigest: "digest-b",
            createdAt: new Date("2026-04-19T11:59:00Z"),
            origin: "router_import",
          },
        ],
        hasAuthoritativeConfig: true,
      }),
      activeRevisionId: "rev-active",
      importedRevisionId: "rev-imported",
      latestDraftId: "rev-active",
      authoritativeConfig: baseConfig,
      importedConfig,
      draftConfig: baseConfig,
    });

    expect(summary.router).toMatchObject({
      status: "pending-import-review",
      exact: true,
      revisionId: "rev-imported",
    });
    expect(summary.router.changeCount).toBeGreaterThan(0);
    expect(summary.router.changedSections).toEqual(
      expect.arrayContaining(["Журнал", "Управление правилами"]),
    );
    expect(summary.router.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          path: "basicSettings.log.level",
          before: "error",
          after: "warning",
        }),
      ]),
    );
  });

  it("shows exact router-side changes for first import review without authoritative baseline", () => {
    const importedConfig = structuredClone(baseConfig);
    importedConfig.basicSettings.log.level = "warning";

    const summary = buildUnconfirmedChangesSummary({
      importState: "import_review",
      configTrust: buildConfigTrustState({
        routerReachable: true,
        lastCheckInAt: new Date("2026-04-19T12:00:00Z"),
        authoritativeDigest: null,
        snapshotDigest: "digest-b",
        revisions: [
          {
            configDigest: "digest-b",
            createdAt: new Date("2026-04-19T11:59:00Z"),
            origin: "router_import",
          },
        ],
        hasAuthoritativeConfig: false,
      }),
      activeRevisionId: null,
      importedRevisionId: "rev-imported",
      latestDraftId: null,
      authoritativeConfig: null,
      importedConfig,
      draftConfig: importedConfig,
    });

    expect(summary.router).toMatchObject({
      status: "pending-import-review",
      exact: true,
      revisionId: "rev-imported",
    });
    expect(summary.router.changeCount).toBeGreaterThan(0);
    expect(summary.router.summary).toContain("Это первый import с роутера");
    expect(summary.router.changedSections).toContain("Журнал");
    expect(summary.router.items[0]).toMatchObject({
      before: "Не задано",
    });
  });

  it("shows digest-only router drift when re-import is needed but exact diff is unknown", () => {
    const summary = buildUnconfirmedChangesSummary({
      importState: "approved",
      configTrust: buildConfigTrustState({
        routerReachable: true,
        lastCheckInAt: new Date("2026-04-19T12:00:00Z"),
        authoritativeDigest: "digest-a",
        snapshotDigest: "digest-b",
        revisions: [
          {
            configDigest: "digest-a",
            createdAt: new Date("2026-04-19T11:00:00Z"),
            origin: "router_import",
          },
        ],
        hasAuthoritativeConfig: true,
      }),
      activeRevisionId: "rev-active",
      importedRevisionId: null,
      latestDraftId: "rev-active",
      authoritativeConfig: baseConfig,
      importedConfig: null,
      draftConfig: baseConfig,
    });

    expect(summary.router).toMatchObject({
      status: "reimport-needed",
      exact: false,
      changeCount: 0,
      items: [],
    });
  });

  it("shows saved panel changes that are not yet confirmed on router", () => {
    const draftConfig = structuredClone(baseConfig);
    draftConfig.basicSettings.main.clientProxy = false;
    draftConfig.subscriptions.keepList = ["domain:example.com"];

    const summary = buildUnconfirmedChangesSummary({
      importState: "approved",
      configTrust: buildConfigTrustState({
        routerReachable: true,
        lastCheckInAt: new Date("2026-04-19T12:00:00Z"),
        authoritativeDigest: "digest-a",
        snapshotDigest: "digest-a",
        revisions: [
          {
            configDigest: "digest-a",
            createdAt: new Date("2026-04-19T11:59:00Z"),
            origin: "router_import",
          },
        ],
        hasAuthoritativeConfig: true,
      }),
      activeRevisionId: "rev-active",
      importedRevisionId: null,
      latestDraftId: "rev-draft",
      authoritativeConfig: baseConfig,
      importedConfig: null,
      draftConfig,
    });

    expect(summary.panel).toMatchObject({
      status: "saved-draft-pending-apply",
      exact: true,
      revisionId: "rev-draft",
    });
    expect(summary.panel.changeCount).toBeGreaterThan(0);
    expect(summary.panel.changedSections).toEqual(
      expect.arrayContaining(["Основные настройки", "Подписки"]),
    );
    expect(summary.panel.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          path: "basicSettings.main.clientProxy",
          before: "Да",
          after: "Нет",
        }),
      ]),
    );
  });
});

describe("buildRouterManagementTaskLog", () => {
  it("includes controller update jobs with failure details", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [createJob()],
      results: [
        createJobResult({
          payload: {
            error: "opkg update: exit status 7",
            stderr: "Collected errors:\n * failed",
          },
        }),
      ],
    });

    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      kind: "controller-update",
      label: "Обновление controller",
      resultStatus: "failure",
      summary: "opkg update: exit status 7",
      error: "opkg update: exit status 7",
      stderr: "Collected errors:\n * failed",
      artifactVersion: "0.1.12-r2",
    });
  });

  it("treats terminal self-update as controller task", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "terminal-controller-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "controller-self-update",
            artifactVersion: "0.1.13-r1",
            command: "opkg install --force-reinstall ...",
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "terminal-controller-job",
          status: "success",
          payload: {
            stdout: "controller self-update to 0.1.13-r1 installed",
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "controller-self-update",
      label: "Self-update controller",
      resultStatus: "success",
      command: "opkg install --force-reinstall ...",
      summary: "controller self-update to 0.1.13-r1 installed",
    });
  });

  it("surfaces terminal self-update LuCI package failures as actionable errors", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "terminal-controller-job",
          type: "run_terminal_command",
          state: "failed",
          payload: {
            purpose: "controller-self-update",
            artifactVersion: "0.1.13-r4",
          },
        }),
      ],
      installedControllerVersion: "0.1.13-r4",
      results: [
        createJobResult({
          jobId: "terminal-controller-job",
          status: "failure",
          payload: {
            stderr:
              "Collected errors:\n * check_data_file_clashes: Package luci-app-vectra-controller wants to install file /._usr\n * opkg_install_cmd: Cannot install package luci-app-vectra-controller.",
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "controller-self-update",
      resultStatus: "failure",
      summary:
        "check_data_file_clashes: Package luci-app-vectra-controller wants to install file /._usr",
    });
  });

  it("treats router reboot as a dedicated task-log item", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "terminal-reboot-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "router-reboot",
            command:
              "set -eu; (sleep 5; /sbin/reboot) >/tmp/vectra-router-reboot.log 2>&1 &; printf 'router reboot scheduled\\n'",
            timeoutSeconds: 15,
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "terminal-reboot-job",
          status: "success",
          payload: {
            stdout: "router reboot scheduled",
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "router-reboot",
      label: "Перезагрузка роутера",
      resultStatus: "success",
      command:
        "set -eu; (sleep 5; /sbin/reboot) >/tmp/vectra-router-reboot.log 2>&1 &; printf 'router reboot scheduled\\n'",
      summary: "router reboot scheduled",
    });
  });

  it("treats router hostname update as a dedicated task-log item", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "terminal-hostname-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "router-hostname-update",
            hostname: "andrey-livingroom",
            command:
              'uci set system.@system[0].hostname="$new_hostname"\nuci commit system',
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "terminal-hostname-job",
          status: "success",
          payload: {
            hostnameAfter: "andrey-livingroom",
            stdout: "hostname updated to andrey-livingroom",
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "router-hostname-update",
      label: "Смена OpenWrt hostname",
      resultStatus: "success",
      command:
        'uci set system.@system[0].hostname="$new_hostname"\nuci commit system',
      summary: "hostname updated to andrey-livingroom",
    });
  });

  it("treats PassWall Clear IPSET/NFTSet as a dedicated task-log item", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "terminal-clear-ipsets-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "passwall-clear-ipsets",
            command:
              "uci -q set passwall2.@global[0].flush_set='1'\n/etc/init.d/passwall2 restart",
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "terminal-clear-ipsets-job",
          status: "success",
          payload: {
            stdout:
              "PassWall2 IPSET/NFTSet clear requested; passwall2 restarted",
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "passwall-clear-ipsets",
      label: "Clear IPSET/NFTSet",
      resultStatus: "success",
      command:
        "uci -q set passwall2.@global[0].flush_set='1'\n/etc/init.d/passwall2 restart",
      summary: "PassWall2 IPSET/NFTSet clear requested; passwall2 restarted",
    });
  });

  it("suppresses stale controller failure in task log when installed version already converged", () => {
    const items = buildRouterManagementTaskLog({
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

    expect(items[0]).toMatchObject({
      resultStatus: "success",
      summary:
        "controller уже на 0.1.12-r11; старый failure-result после self-update больше не актуален",
    });
  });

  it("includes managed-stack passwall jobs with per-package details", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "pw-managed-job",
          type: "update_passwall_packages",
          state: "failed",
          payload: {
            targetVersion: "26.4.10-1",
            packageTargetVersion: "26.4.10-r1",
            updateScope: "managed-stack",
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "pw-managed-job",
          status: "failure",
          payload: {
            packageResults: [
              {
                package: "v2ray-geoip",
                status: "storage-blocked",
                error: "overlay free space too low",
              },
            ],
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "passwall-update",
      label: "Обновление PassWall stack",
      resultStatus: "failure",
      targetVersion: "26.4.10-1",
      packageTargetVersion: "26.4.10-r1",
      summary: "v2ray-geoip: package path пропущен из-за места (overlay free space too low)",
    });
    expect(items[0]?.packageResults).toHaveLength(1);
  });

  it("includes scoped passwall package jobs in management history", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "pw-scoped-job",
          type: "update_passwall_packages",
          state: "succeeded",
          payload: {
            targetVersion: "26.3.27-r1",
            packageTargetVersion: "26.3.27-r1",
            updateScope: "scoped-package",
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "pw-scoped-job",
          status: "success",
          payload: {
            packageResults: [
              {
                package: "xray-core",
                status: "runtime-only-converged",
                pathUsed: "built-in-updater",
              },
            ],
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      kind: "passwall-update",
      updateScope: "scoped-package",
      label: "Точечное обновление PassWall",
    });
  });

  it("propagates delivery-blocked state into task log", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "pw-delivery-job",
          type: "update_passwall_packages",
          state: "queued",
          payload: {
            targetVersion: "26.4.10-1",
            updateScope: "managed-stack",
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "pw-delivery-job",
          status: "accepted",
          payload: {
            deliveryBlocked: true,
            deliveryBlockedReason: "db write probe failed",
          },
        }),
      ],
    });

    expect(items[0]).toMatchObject({
      deliveryBlocked: true,
      deliveryBlockedReason: "db write probe failed",
      summary:
        "job поставлен в очередь, но сервер сейчас не сохраняет check-in: db write probe failed",
    });
  });

  it("keeps null result status when there is no confirmed non-accepted result", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "controller-no-result-job",
          state: "succeeded",
        }),
      ],
      results: [],
    });

    expect(items[0]).toMatchObject({
      resultStatus: null,
      summary: "обновление завершилось без подробностей",
    });
  });

  it("filters out non-management terminal jobs", () => {
    const items = buildRouterManagementTaskLog({
      jobs: [
        createJob({
          id: "generic-terminal-job",
          type: "run_terminal_command",
          state: "succeeded",
          payload: {
            purpose: "generic-terminal",
            command: "ubus call system board",
          },
        }),
      ],
      results: [
        createJobResult({
          jobId: "generic-terminal-job",
          status: "success",
          payload: {
            stdout: "ok",
          },
        }),
      ],
    });

    expect(items).toHaveLength(0);
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

  it("rebinds node references by label when live runtime node ids rotate", () => {
    const draftConfig = structuredClone(baseConfig);
    draftConfig.basicSettings.main.selectedNodeId = "old-eu";
    draftConfig.basicSettings.socks = [
      {
        id: "managed-socks",
        enabled: true,
        nodeId: "old-eu",
        port: 2080,
        bindLocal: true,
        autoswitchBackupNodeIds: ["old-us"],
        extras: {},
      },
    ];
    draftConfig.basicSettings.shuntRules = [
      {
        id: "route_us",
        label: "US route",
        outboundNodeId: "old-us",
        domainRules: [],
        ipRules: [],
        extras: {},
      },
    ];
    draftConfig.ruleManage.shuntRules = structuredClone(
      draftConfig.basicSettings.shuntRules,
    );
    draftConfig.nodes = [
      {
        id: "myshunt",
        label: "myshunt",
        protocol: "shunt",
        enabled: true,
        group: "default",
        tags: [],
        extras: {
          default_node: "old-eu",
          route_us_proxy_tag: "old-us",
        },
      },
      {
        id: "old-eu",
        label: "Europe",
        protocol: "vless",
        enabled: true,
        group: "Managed",
        address: "eu-old.example.invalid",
        port: 443,
        transport: "ws",
        tls: true,
        tags: [],
        extras: {
          add_mode: "2",
        },
      },
      {
        id: "old-us",
        label: "United States",
        protocol: "vless",
        enabled: true,
        group: "Managed",
        address: "us-old.example.invalid",
        port: 443,
        transport: "ws",
        tls: true,
        tags: [],
        extras: {
          add_mode: "2",
        },
      },
    ];
    draftConfig.subscriptions.items = [
      {
        id: "@subscribe_list[0]",
        remark: "Managed",
        url: "https://managed.example.invalid/sub",
        enabled: true,
        addMode: "2",
        metadata: {},
        extras: {
          to_node: "old-us",
        },
      },
    ];

    const currentLiveConfig = structuredClone(baseConfig);
    currentLiveConfig.basicSettings.main.selectedNodeId = "new-eu";
    currentLiveConfig.nodes = [
      currentLiveConfig.nodes[0]!,
      {
        id: "new-eu",
        label: "Europe",
        protocol: "vless",
        enabled: true,
        group: "Managed",
        address: "eu-new.example.invalid",
        port: 443,
        transport: "xhttp",
        tls: true,
        tags: [],
        extras: {
          add_mode: "2",
        },
      },
      {
        id: "new-us",
        label: "United States",
        protocol: "vless",
        enabled: true,
        group: "Managed",
        address: "us-new.example.invalid",
        port: 443,
        transport: "xhttp",
        tls: true,
        tags: [],
        extras: {
          add_mode: "2",
        },
      },
    ];
    currentLiveConfig.subscriptions.items = [
      {
        id: "vectra_sub_managed",
        remark: "Managed",
        url: "https://managed.example.invalid/sub",
        enabled: true,
        addMode: "2",
        metadata: {},
        extras: {},
      },
    ];

    const merged = mergeCurrentLiveRouterDataIntoDraftConfig({
      draftConfig,
      currentLiveConfig,
    });

    expect(merged.nodes.map((node) => node.id)).toEqual([
      "myshunt",
      "new-eu",
      "new-us",
    ]);
    expect(merged.basicSettings.main.selectedNodeId).toBe("new-eu");
    expect(merged.basicSettings.socks[0]).toMatchObject({
      nodeId: "new-eu",
      autoswitchBackupNodeIds: ["new-us"],
    });
    expect(merged.basicSettings.shuntRules[0]?.outboundNodeId).toBe("new-us");
    expect(merged.ruleManage.shuntRules[0]?.outboundNodeId).toBe("new-us");
    expect(merged.nodes[0]!.extras.default_node).toBe("new-eu");
    expect(merged.nodes[0]!.extras.route_us_proxy_tag).toBe("new-us");
    expect(merged.subscriptions.items[0]).toMatchObject({
      id: "vectra_sub_managed",
      extras: {
        to_node: "new-us",
      },
    });
  });

  it("keeps live shunt outbound tags when they are opaque runtime ids", () => {
    const draftConfig = structuredClone(baseConfig);
    draftConfig.basicSettings.shuntRules = [
      {
        id: "WorldProxy",
        label: "WorldProxy",
        outboundNodeId: "draft-runtime-tag",
        domainRules: [],
        ipRules: [],
        extras: {},
      },
    ];
    draftConfig.ruleManage.shuntRules = structuredClone(
      draftConfig.basicSettings.shuntRules,
    );

    const currentLiveConfig = structuredClone(baseConfig);
    currentLiveConfig.basicSettings.shuntRules = [
      {
        id: "WorldProxy",
        label: "WorldProxy",
        outboundNodeId: "live-runtime-tag",
        domainRules: [],
        ipRules: [],
        extras: {},
      },
    ];
    currentLiveConfig.ruleManage.shuntRules = structuredClone(
      currentLiveConfig.basicSettings.shuntRules,
    );

    const merged = mergeCurrentLiveRouterDataIntoDraftConfig({
      draftConfig,
      currentLiveConfig,
    });

    expect(merged.basicSettings.shuntRules[0]?.outboundNodeId).toBe(
      "live-runtime-tag",
    );
    expect(merged.ruleManage.shuntRules[0]?.outboundNodeId).toBe(
      "live-runtime-tag",
    );
  });
});
