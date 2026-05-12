import { describe, expect, it } from "vitest";

import {
  buildSyntheticRecoveryTransitions,
  isControlPlaneRecoveryIncident,
  resolveReportedRouterHostname,
  resolveRescueReason,
  selectDeliverableJobsForCheckIn,
  shouldPromotePostApplyImport,
} from "./router-control";

describe("resolveRescueReason", () => {
  it("clears stale rescue reason after proxy recovery", () => {
    expect(
      resolveRescueReason(
        "proxy",
        undefined,
        "Subscription expired or upstream proxy unavailable",
      ),
    ).toBeNull();
  });

  it("keeps the reported direct-mode reason when controller is in direct mode", () => {
    expect(
      resolveRescueReason(
        "direct",
        "Оператор принудительно включил прямой режим из LuCI",
        null,
      ),
    ).toBe("Оператор принудительно включил прямой режим из LuCI");
  });

  it("keeps previous direct-mode reason until a new direct reason arrives", () => {
    expect(
      resolveRescueReason(
        "direct",
        undefined,
        "Subscription expired or upstream proxy unavailable",
      ),
    ).toBe("Subscription expired or upstream proxy unavailable");
  });
});

describe("shouldPromotePostApplyImport", () => {
  it("promotes check-in imports that confirm a server-applied revision", () => {
    expect(
      shouldPromotePostApplyImport({
        approvedAt: new Date("2026-04-07T00:00:00.000Z"),
        importSource: "check_in",
        reportedAppliedRevisionId: "revision-applied",
        activeRevisionId: "revision-applied",
        lastAppliedRevisionId: "revision-applied",
      }),
    ).toBe(true);
  });

  it("does not promote arbitrary live drift after approval", () => {
    expect(
      shouldPromotePostApplyImport({
        approvedAt: new Date("2026-04-07T00:00:00.000Z"),
        importSource: "check_in",
        reportedAppliedRevisionId: null,
        activeRevisionId: "revision-active",
        lastAppliedRevisionId: "revision-applied",
      }),
    ).toBe(false);
  });

  it("keeps operator-requested re-imports in the review lane", () => {
    expect(
      shouldPromotePostApplyImport({
        approvedAt: new Date("2026-04-07T00:00:00.000Z"),
        importSource: "operator_reimport",
        reportedAppliedRevisionId: "revision-applied",
        activeRevisionId: "revision-applied",
        lastAppliedRevisionId: "revision-applied",
      }),
    ).toBe(false);
  });
});

describe("resolveReportedRouterHostname", () => {
  it("extracts the applied hostname from router-hostname-update terminal jobs", () => {
    expect(
      resolveReportedRouterHostname({
        jobType: "run_terminal_command",
        jobPayload: {
          purpose: "router-hostname-update",
          hostname: "andrey-livingroom",
        },
        resultPayload: {
          hostnameAfter: "andrey-livingroom",
        },
      }),
    ).toBe("andrey-livingroom");
  });

  it("ignores generic terminal jobs", () => {
    expect(
      resolveReportedRouterHostname({
        jobType: "run_terminal_command",
        jobPayload: {
          purpose: "generic-terminal",
          hostname: "andrey-livingroom",
        },
        resultPayload: {
          hostnameAfter: "andrey-livingroom",
        },
      }),
    ).toBeNull();
  });
});

describe("selectDeliverableJobsForCheckIn", () => {
  it("treats controller self-update terminal jobs as exclusive", () => {
    const deliverable = selectDeliverableJobsForCheckIn("approved", [
      {
        id: "apply-job",
        type: "apply_passwall_config",
        state: "queued",
        payload: {},
      },
      {
        id: "controller-legacy-job",
        type: "run_terminal_command",
        state: "queued",
        payload: {
          purpose: "controller-self-update",
          artifactVersion: "0.1.13-r1",
          command: "opkg install --force-reinstall ...",
          timeoutSeconds: 120,
        },
      },
    ] as never);

    expect(deliverable).toHaveLength(1);
    expect(deliverable[0]?.id).toBe("controller-legacy-job");
  });

  it("treats compat controller self-update terminal jobs as exclusive", () => {
    const deliverable = selectDeliverableJobsForCheckIn("approved", [
      {
        id: "apply-job",
        type: "apply_passwall_config",
        state: "queued",
        payload: {},
      },
      {
        id: "controller-compat-job",
        type: "run_terminal_command",
        state: "queued",
        payload: {
          purpose: "controller-self-update-compat",
          artifactVersion: "0.1.13-r20",
          command: "opkg install --force-reinstall ...",
          timeoutSeconds: 120,
        },
      },
    ] as never);

    expect(deliverable).toHaveLength(1);
    expect(deliverable[0]?.id).toBe("controller-compat-job");
  });

  it("treats rescue repair jobs as exclusive", () => {
    const deliverable = selectDeliverableJobsForCheckIn("approved", [
      {
        id: "apply-job",
        type: "apply_passwall_config",
        state: "queued",
        payload: {},
      },
      {
        id: "repair-job",
        type: "run_rescue_repair",
        state: "queued",
        payload: {
          actions: ["restart_passwall", "reconnect_proxy"],
          timeoutSeconds: 90,
          requestedBy: "auto_rescue",
        },
      },
    ] as never);

    expect(deliverable).toHaveLength(1);
    expect(deliverable[0]?.id).toBe("repair-job");
  });

  it("treats router reboot terminal jobs as exclusive", () => {
    const deliverable = selectDeliverableJobsForCheckIn("approved", [
      {
        id: "apply-job",
        type: "apply_passwall_config",
        state: "queued",
        payload: {},
      },
      {
        id: "reboot-job",
        type: "run_terminal_command",
        state: "queued",
        payload: {
          purpose: "router-reboot",
          command:
            "set -eu; (sleep 5; /sbin/reboot) >/tmp/vectra-router-reboot.log 2>&1 &; printf 'router reboot scheduled\\n'",
          timeoutSeconds: 15,
        },
      },
    ] as never);

    expect(deliverable).toHaveLength(1);
    expect(deliverable[0]?.id).toBe("reboot-job");
  });

  it("treats PassWall Clear IPSET/NFTSet terminal jobs as exclusive", () => {
    const deliverable = selectDeliverableJobsForCheckIn("approved", [
      {
        id: "apply-job",
        type: "apply_passwall_config",
        state: "queued",
        payload: {},
      },
      {
        id: "clear-ipsets-job",
        type: "run_terminal_command",
        state: "queued",
        payload: {
          purpose: "passwall-clear-ipsets",
          command:
            "uci -q set passwall2.@global[0].flush_set='1'\n/etc/init.d/passwall2 restart",
          timeoutSeconds: 90,
        },
      },
    ] as never);

    expect(deliverable).toHaveLength(1);
    expect(deliverable[0]?.id).toBe("clear-ipsets-job");
  });
});

describe("buildSyntheticRecoveryTransitions", () => {
  it("opens server_unreachable when controller recovery is waiting on panel return", () => {
    const transitions = buildSyntheticRecoveryTransitions({
      health: {
        currentMode: "proxy",
        publicConnectivityFailures: 0,
        directConnectivitySuccesses: 0,
        proxyConnectivitySuccesses: 0,
        serverReachable: false,
        recoveryPhase: "controller_restart_wait",
        lastRecoveryAction:
          "Control plane unreachable for over one hour; scheduled local vectra-controller restart.",
        awaitingOperator: false,
      },
      inventory: {
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "vectra-test",
        devicePublicKey: "pub",
        controllerVersion: "0.1.13-r3",
        model: "AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        passwallEnabled: true,
        nodeCount: 1,
        subscriptionCount: 1,
        packageVersions: {},
        binaryVersions: {},
        rulesAssets: {},
        resources: {
          memoryTotalMb: 256,
          memoryAvailableMb: 128,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
        serviceHealth: {
          controller: "running",
          passwall: "running",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
        panelReachability: {
          reachable: false,
          checkedAt: "2026-04-22T10:00:00.000Z",
          status: "blocked",
          reachableCount: 0,
          totalCount: 1,
          checks: [],
        },
      },
      openIncident: null,
    });

    expect(transitions).toHaveLength(1);
    expect(transitions[0]?.type).toBe("server_unreachable");
    expect(transitions[0]?.reason).toContain(
      "scheduled local vectra-controller restart",
    );
    expect(transitions[0]?.metadata.origin).toBe("control-plane-recovery");
  });

  it("opens proxy_outage when router is waiting for operator after failed foreign recovery", () => {
    const transitions = buildSyntheticRecoveryTransitions({
      health: {
        currentMode: "direct",
        publicConnectivityFailures: 0,
        directConnectivitySuccesses: 0,
        proxyConnectivitySuccesses: 0,
        serverReachable: true,
        recoveryPhase: "operator_attention",
        lastRecoveryAction:
          "After auto-reboot and PassWall retry, foreign resources are still unavailable; router left in direct mode.",
        awaitingOperator: true,
      },
      inventory: {
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "vectra-test",
        devicePublicKey: "pub",
        controllerVersion: "0.1.13-r3",
        model: "AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        passwallEnabled: false,
        nodeCount: 1,
        subscriptionCount: 1,
        packageVersions: {},
        binaryVersions: {},
        rulesAssets: {},
        resources: {
          memoryTotalMb: 256,
          memoryAvailableMb: 128,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
        serviceHealth: {
          controller: "running",
          passwall: "stopped",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
        foreignReachability: {
          reachable: false,
          checkedAt: "2026-04-22T10:10:00.000Z",
          status: "blocked",
          reachableCount: 0,
          totalCount: 3,
          checks: [],
        },
      },
      openIncident: null,
    });

    expect(transitions).toHaveLength(1);
    expect(transitions[0]?.type).toBe("proxy_outage");
    expect(transitions[0]?.reason).toContain(
      "foreign resources are still unavailable",
    );
  });

  it("resolves open recovery incidents once router returns to idle recovery phase", () => {
    const transitions = buildSyntheticRecoveryTransitions({
      health: {
        currentMode: "proxy",
        publicConnectivityFailures: 0,
        directConnectivitySuccesses: 0,
        proxyConnectivitySuccesses: 2,
        serverReachable: true,
        recoveryPhase: "idle",
        lastRecoveryAction: null,
        awaitingOperator: false,
      },
      inventory: {
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "vectra-test",
        devicePublicKey: "pub",
        controllerVersion: "0.1.13-r3",
        model: "AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        passwallEnabled: true,
        nodeCount: 1,
        subscriptionCount: 1,
        packageVersions: {},
        binaryVersions: {},
        rulesAssets: {},
        resources: {
          memoryTotalMb: 256,
          memoryAvailableMb: 128,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
        serviceHealth: {
          controller: "running",
          passwall: "running",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
      },
      openIncident: {
        id: "incident-1",
        routerId: "router-1",
        type: "proxy_outage",
        state: "open",
        reason: "Proxy path still degraded",
        metadata: {
          origin: "control-plane-recovery",
        },
        openedAt: new Date("2026-04-22T10:00:00.000Z"),
        resolvedAt: null,
      },
    });

    expect(transitions).toHaveLength(1);
    expect(transitions[0]?.type).toBe("recovered");
    expect(transitions[0]?.state).toBe("resolved");
  });

  it("does not open a synthetic recovery incident while another incident is already open", () => {
    const transitions = buildSyntheticRecoveryTransitions({
      health: {
        currentMode: "proxy",
        publicConnectivityFailures: 0,
        directConnectivitySuccesses: 0,
        proxyConnectivitySuccesses: 0,
        serverReachable: false,
        recoveryPhase: "controller_restart_wait",
        lastRecoveryAction:
          "Control plane unreachable for over one hour; scheduled local vectra-controller restart.",
        awaitingOperator: false,
      },
      inventory: {
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "vectra-test",
        devicePublicKey: "pub",
        controllerVersion: "0.1.13-r3",
        model: "AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        passwallEnabled: true,
        nodeCount: 1,
        subscriptionCount: 1,
        packageVersions: {},
        binaryVersions: {},
        rulesAssets: {},
        resources: {
          memoryTotalMb: 256,
          memoryAvailableMb: 128,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
        serviceHealth: {
          controller: "running",
          passwall: "running",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
      },
      openIncident: {
        id: "incident-2",
        routerId: "router-1",
        type: "subscription_degraded",
        state: "open",
        reason: "Subscription refresh failed.",
        metadata: {},
        openedAt: new Date("2026-04-22T10:00:00.000Z"),
        resolvedAt: null,
      },
    });

    expect(transitions).toHaveLength(0);
  });

  it("does not resolve unrelated incidents when recovery returns to idle", () => {
    const transitions = buildSyntheticRecoveryTransitions({
      health: {
        currentMode: "proxy",
        publicConnectivityFailures: 0,
        directConnectivitySuccesses: 0,
        proxyConnectivitySuccesses: 2,
        serverReachable: true,
        recoveryPhase: "idle",
        lastRecoveryAction: null,
        awaitingOperator: false,
      },
      inventory: {
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "vectra-test",
        devicePublicKey: "pub",
        controllerVersion: "0.1.13-r3",
        model: "AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        passwallEnabled: true,
        nodeCount: 1,
        subscriptionCount: 1,
        packageVersions: {},
        binaryVersions: {},
        rulesAssets: {},
        resources: {
          memoryTotalMb: 256,
          memoryAvailableMb: 128,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 32,
          tmpFreeMb: 64,
        },
        serviceHealth: {
          controller: "running",
          passwall: "running",
          passwallServer: "unknown",
          dnsmasq: "running",
        },
      },
      openIncident: {
        id: "incident-3",
        routerId: "router-1",
        type: "subscription_degraded",
        state: "open",
        reason: "Subscription refresh failed.",
        metadata: {},
        openedAt: new Date("2026-04-22T10:00:00.000Z"),
        resolvedAt: null,
      },
    });

    expect(transitions).toHaveLength(0);
  });
});

describe("isControlPlaneRecoveryIncident", () => {
  it("matches only supervisor-owned proxy and server incidents", () => {
    expect(
      isControlPlaneRecoveryIncident({
        type: "proxy_outage",
        metadata: { origin: "control-plane-recovery" },
      }),
    ).toBe(true);
    expect(
      isControlPlaneRecoveryIncident({
        type: "subscription_degraded",
        metadata: { origin: "control-plane-recovery" },
      }),
    ).toBe(false);
    expect(
      isControlPlaneRecoveryIncident({
        type: "server_unreachable",
        metadata: {},
      }),
    ).toBe(false);
  });
});
