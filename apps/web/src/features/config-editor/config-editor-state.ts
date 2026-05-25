import type { PasswallDesiredConfig } from "@vectra/contracts";

export type ConfigSectionProps = {
  config: PasswallDesiredConfig;
  onChange: (next: PasswallDesiredConfig) => void;
  disabled?: boolean;
};

type Main = PasswallDesiredConfig["basicSettings"]["main"];
type Dns = PasswallDesiredConfig["basicSettings"]["dns"];
type Log = PasswallDesiredConfig["basicSettings"]["log"];
type Maintenance = PasswallDesiredConfig["basicSettings"]["maintenance"];
type ShuntRule = PasswallDesiredConfig["basicSettings"]["shuntRules"][number];

export function patchMain(
  config: PasswallDesiredConfig,
  patch: Partial<Main>,
): PasswallDesiredConfig {
  return {
    ...config,
    basicSettings: {
      ...config.basicSettings,
      main: { ...config.basicSettings.main, ...patch },
    },
  };
}

export function patchDns(
  config: PasswallDesiredConfig,
  patch: Partial<Dns>,
): PasswallDesiredConfig {
  return {
    ...config,
    basicSettings: {
      ...config.basicSettings,
      dns: { ...config.basicSettings.dns, ...patch },
    },
  };
}

export function patchLog(
  config: PasswallDesiredConfig,
  patch: Partial<Log>,
): PasswallDesiredConfig {
  return {
    ...config,
    basicSettings: {
      ...config.basicSettings,
      log: { ...config.basicSettings.log, ...patch },
    },
  };
}

export function patchMaintenance(
  config: PasswallDesiredConfig,
  patch: Partial<Maintenance>,
): PasswallDesiredConfig {
  return {
    ...config,
    basicSettings: {
      ...config.basicSettings,
      maintenance: { ...config.basicSettings.maintenance, ...patch },
    },
  };
}

// ruleManage.shuntRules mirrors basicSettings.shuntRules (see syncShuntRules in
// router-editor-state.ts); keep both in lockstep on every shunt-rule edit.
function withShuntRulesSynced(
  config: PasswallDesiredConfig,
  shuntRules: ShuntRule[],
): PasswallDesiredConfig {
  return {
    ...config,
    basicSettings: { ...config.basicSettings, shuntRules },
    ruleManage: {
      ...config.ruleManage,
      shuntRules: structuredClone(shuntRules),
    },
  };
}

export function setShuntRuleField(
  config: PasswallDesiredConfig,
  ruleId: string,
  patch: Partial<Pick<ShuntRule, "label" | "outboundNodeId" | "domainRules" | "ipRules">>,
): PasswallDesiredConfig {
  const shuntRules = config.basicSettings.shuntRules.map((rule) =>
    rule.id === ruleId ? { ...rule, ...patch } : rule,
  );
  return withShuntRulesSynced(config, shuntRules);
}

// PassWall2 stores per-rule options (network/protocol/inbound/source/port/invert)
// on the shunt_rules section; the Vectra importer keeps everything beyond
// remarks/domain_list/ip_list in `rule.extras` under the native UCI key. These
// readers/encoders mirror the legacy editor so values round-trip through
// import.go/apply.go unchanged.
type ExtrasRecord = ShuntRule["extras"];

function tokenizeExtra(value: string): string[] {
  return value
    .split(/\s+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

export function getExtraString(
  extras: ExtrasRecord | undefined,
  key: string,
): string | undefined;
export function getExtraString(
  extras: ExtrasRecord | undefined,
  key: string,
  fallback: string,
): string;
export function getExtraString(
  extras: ExtrasRecord | undefined,
  key: string,
  fallback?: string,
): string | undefined {
  const value = extras?.[key];
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number") {
    return String(value);
  }
  if (typeof value === "boolean") {
    return value ? "1" : "0";
  }
  if (Array.isArray(value)) {
    return (value[0] as string | undefined) ?? fallback;
  }
  return fallback;
}

export function getExtraBoolean(
  extras: ExtrasRecord | undefined,
  key: string,
  fallback = false,
): boolean {
  const value = extras?.[key];
  if (typeof value === "boolean") {
    return value;
  }
  if (typeof value === "number") {
    return value !== 0;
  }
  if (typeof value === "string") {
    const normalized = value.toLowerCase();
    if (["1", "true", "yes", "on"].includes(normalized)) {
      return true;
    }
    if (["0", "false", "no", "off", ""].includes(normalized)) {
      return false;
    }
  }
  return fallback;
}

export function getExtraTokens(
  extras: ExtrasRecord | undefined,
  key: string,
): string[] {
  const value = extras?.[key];
  if (Array.isArray(value)) {
    return value.flatMap((entry) =>
      typeof entry === "string" ? tokenizeExtra(entry) : [String(entry)],
    );
  }
  if (typeof value === "string") {
    return tokenizeExtra(value);
  }
  if (typeof value === "number") {
    return [String(value)];
  }
  return [];
}

export function encodeExtraTokens(values: string[]): string | undefined {
  const next = values.map((value) => value.trim()).filter(Boolean);
  return next.length > 0 ? next.join(" ") : undefined;
}
