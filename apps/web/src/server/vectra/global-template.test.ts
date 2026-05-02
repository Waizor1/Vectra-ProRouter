import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { passwallDesiredConfigSchema } from "@vectra/contracts";
import { describe, expect, it } from "vitest";

import {
  mergeGlobalTemplateIntoRouterConfig,
  validateInstallBaselineUci,
  validateRolloutTemplateConfig,
} from "./global-template";

const baselinePath = fileURLToPath(
  new URL("../../../public/install/ax3000t-passwall2-baseline.uci", import.meta.url),
);
const rolloutSeedPath = fileURLToPath(
  new URL(
    "../../app/enrollment/__fixtures__/ax3000t-global-rollout-template.seed.json",
    import.meta.url,
  ),
);

function loadRolloutSeed() {
  return passwallDesiredConfigSchema.parse(
    JSON.parse(readFileSync(rolloutSeedPath, "utf8")) as unknown,
  );
}

describe("validateInstallBaselineUci", () => {
  it("accepts the checked-in sanitized AX3000T baseline", () => {
    const baseline = readFileSync(baselinePath, "utf8");

    expect(validateInstallBaselineUci(baseline)).toEqual([]);
  });

  it("rejects a baseline when Default or Direct FakeDNS drifts back to enabled", () => {
    const baseline = readFileSync(baselinePath, "utf8")
      .replace("option default_fakedns '0'", "option default_fakedns '1'")
      .replace("option direct_fakedns '0'", "option direct_fakedns '1'");

    expect(validateInstallBaselineUci(baseline)).toEqual([
      "Для Default в install baseline должен оставаться FakeDNS = 0.",
      "Для Direct в install baseline должен оставаться FakeDNS = 0.",
    ]);
  });

  it("rejects a baseline when remote DNS drifts back to Yandex DoH", () => {
    const baseline = readFileSync(baselinePath, "utf8")
      .replace("option remote_dns_protocol 'udp'", "option remote_dns_protocol 'doh'")
      .replace("option remote_dns '8.8.8.8'", "option remote_dns '1.1.1.1'")
      .replace(
        "option remote_dns_detour 'direct'",
        "option remote_dns_doh 'common.dot.dns.yandex.net'\n\toption remote_dns_detour 'direct'",
      )
      .replace(
        "dns.google.com 8.8.8.8\ncloudflare-dns.com 1.1.1.1",
        "common.dot.dns.yandex.net 77.88.8.8\ncommon.dot.dns.yandex.net 77.88.8.1",
      );

    expect(validateInstallBaselineUci(baseline)).toEqual([
      "Install baseline должен использовать UDP remote DNS.",
      "Install baseline должен использовать 8.8.8.8 как remote DNS.",
      "Install baseline не должен хранить remote_dns_doh при UDP DNS.",
      "Install baseline не должен пиновать legacy Yandex DNS host.",
    ]);
  });
});

describe("validateRolloutTemplateConfig", () => {
  it("rejects subscription items and real proxy nodes", () => {
    const config = loadRolloutSeed();
    config.subscriptions.items.push({
      id: "subscription-1",
      remark: "Private sub",
      url: "https://example.invalid/sub",
      enabled: true,
      addMode: "1",
      metadata: {},
      extras: {},
    });
    config.nodes.push({
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

    expect(validateRolloutTemplateConfig(config)).toEqual([
      "Fleet-template не должен хранить subscription items: ссылки остаются локальными для каждого роутера.",
      "Fleet-template не должен хранить реальные proxy-node секции: разрешены только template-managed shunt nodes.",
    ]);
  });

  it("rejects unsafe remote DNS defaults", () => {
    const config = loadRolloutSeed();
    config.basicSettings.dns.remoteDnsProtocol = "doh";
    config.basicSettings.dns.remoteDns = "1.1.1.1";
    config.basicSettings.dns.remoteDnsDoh = "common.dot.dns.yandex.net";
    config.basicSettings.dns.remoteDnsDetour = "remote";
    config.basicSettings.dns.dnsHosts = [
      "common.dot.dns.yandex.net 77.88.8.8",
      "common.dot.dns.yandex.net 77.88.8.1",
    ];

    expect(validateRolloutTemplateConfig(config)).toEqual([
      "Fleet-template должен использовать UDP remote DNS.",
      "Fleet-template должен использовать 8.8.8.8 как remote DNS.",
      "Fleet-template не должен хранить remoteDnsDoh при UDP DNS.",
      "Fleet-template должен отправлять remote DNS напрямую.",
      "Fleet-template не должен пиновать legacy Yandex DNS host.",
    ]);
  });
});

describe("mergeGlobalTemplateIntoRouterConfig", () => {
  it("preserves router-local nodes, selected node, socks and subscriptions", () => {
    const template = loadRolloutSeed();
    const routerConfig = loadRolloutSeed();

    routerConfig.basicSettings.main.selectedNodeId = "private-node";
    routerConfig.basicSettings.socks = [
      {
        id: "local-socks",
        enabled: true,
        nodeId: "private-node",
        port: 2080,
        bindLocal: false,
        autoswitchBackupNodeIds: ["backup-node"],
        extras: {
          testFlag: "1",
        },
      },
    ];
    routerConfig.subscriptions.items = [
      {
        id: "subscription-1",
        remark: "Private sub",
        url: "https://example.invalid/sub",
        enabled: true,
        addMode: "1",
        metadata: {},
        extras: {
          token: "private",
        },
      },
    ];
    routerConfig.nodes = [
      {
        ...routerConfig.nodes[0]!,
        label: "Router-local myshunt that must be replaced by template",
      },
      {
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
      },
    ];
    template.basicSettings.main.selectedNodeId = "myshunt";
    template.appUpdate.updateStrategy = "expert-fallback";

    const merged = mergeGlobalTemplateIntoRouterConfig({
      template,
      routerConfig,
    });

    expect(merged.basicSettings.main.selectedNodeId).toBe("private-node");
    expect(merged.basicSettings.socks).toEqual(routerConfig.basicSettings.socks);
    expect(merged.subscriptions.items).toEqual(routerConfig.subscriptions.items);
    expect(merged.appUpdate).toEqual(template.appUpdate);
    expect(merged.ruleManage).toEqual(template.ruleManage);
    expect(merged.nodes.map((node) => node.id)).toEqual(["myshunt", "private-node"]);
    expect(merged.nodes[0]?.label).toBe(template.nodes[0]?.label);
    expect(merged.nodes[1]?.label).toBe("Private node");
  });
});
