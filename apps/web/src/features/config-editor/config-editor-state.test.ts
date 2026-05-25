import { describe, expect, it } from "vitest";

import { addShuntRule, createDraftFixture } from "~/components/router-editor-state";

import { updateShuntRuleExtra } from "~/components/router-editor-state";

import {
  encodeExtraTokens,
  getExtraBoolean,
  getExtraString,
  getExtraTokens,
  patchDns,
  patchLog,
  patchMain,
  patchMaintenance,
  setShuntRuleField,
} from "./config-editor-state";

describe("config-editor-state", () => {
  it("patchMain updates a field without mutating the source", () => {
    const config = createDraftFixture();
    expect(config.basicSettings.main.mainSwitch).toBe(true);

    const next = patchMain(config, { mainSwitch: false, nodeSocksPort: 1090 });

    expect(next.basicSettings.main.mainSwitch).toBe(false);
    expect(next.basicSettings.main.nodeSocksPort).toBe(1090);
    expect(config.basicSettings.main.mainSwitch).toBe(true);
    expect(next).not.toBe(config);
  });

  it("patchDns updates remote DNS fields immutably", () => {
    const config = createDraftFixture();
    const next = patchDns(config, { remoteDns: "8.8.8.8", remoteFakeDns: true });

    expect(next.basicSettings.dns.remoteDns).toBe("8.8.8.8");
    expect(next.basicSettings.dns.remoteFakeDns).toBe(true);
    expect(config.basicSettings.dns.remoteDns).toBe("1.1.1.1");
  });

  it("patchLog updates log level immutably", () => {
    const config = createDraftFixture();
    const next = patchLog(config, { level: "error", enableNodeLog: false });

    expect(next.basicSettings.log.level).toBe("error");
    expect(next.basicSettings.log.enableNodeLog).toBe(false);
    expect(config.basicSettings.log.level).toBe("warning");
  });

  it("patchMaintenance replaces backup paths immutably", () => {
    const config = createDraftFixture();
    const next = patchMaintenance(config, { backupPaths: ["/etc/config/passwall2"] });

    expect(next.basicSettings.maintenance.backupPaths).toEqual([
      "/etc/config/passwall2",
    ]);
    expect(config.basicSettings.maintenance.backupPaths.length).toBeGreaterThan(1);
  });

  it("setShuntRuleField edits a rule and keeps ruleManage.shuntRules in sync", () => {
    const config = addShuntRule(createDraftFixture());
    const ruleId = config.basicSettings.shuntRules[0]!.id;

    const next = setShuntRuleField(config, ruleId, {
      label: "Только YouTube",
      domainRules: ["geosite:youtube"],
    });

    const edited = next.basicSettings.shuntRules[0]!;
    expect(edited.label).toBe("Только YouTube");
    expect(edited.domainRules).toEqual(["geosite:youtube"]);
    expect(next.ruleManage.shuntRules[0]).toEqual(edited);
    expect(config.basicSettings.shuntRules[0]!.label).toBe("Новое правило");
  });

  it("reads and writes per-rule extras (network/protocol/source/invert) round-trip", () => {
    const config = addShuntRule(createDraftFixture());
    const ruleId = config.basicSettings.shuntRules[0]!.id;

    let next = updateShuntRuleExtra(config, ruleId, "network", "udp");
    next = updateShuntRuleExtra(
      next,
      ruleId,
      "protocol",
      encodeExtraTokens(["http", "tls"]),
    );
    next = updateShuntRuleExtra(next, ruleId, "invert", "1");

    const extras = next.basicSettings.shuntRules[0]!.extras;
    expect(getExtraString(extras, "network", "tcp,udp")).toBe("udp");
    expect(getExtraTokens(extras, "protocol")).toEqual(["http", "tls"]);
    expect(getExtraBoolean(extras, "invert")).toBe(true);
    // ruleManage mirror stays in sync
    expect(next.ruleManage.shuntRules[0]!.extras).toEqual(extras);
  });

  it("getExtraString falls back and encodeExtraTokens drops empties", () => {
    expect(getExtraString({}, "network", "tcp,udp")).toBe("tcp,udp");
    expect(getExtraTokens({}, "protocol")).toEqual([]);
    expect(getExtraBoolean({}, "invert")).toBe(false);
    expect(encodeExtraTokens([" http ", "", "tls"])).toBe("http tls");
    expect(encodeExtraTokens([" ", ""])).toBeUndefined();
  });
});
