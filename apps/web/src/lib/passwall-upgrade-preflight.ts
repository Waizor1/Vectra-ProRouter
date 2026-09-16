/**
 * Preflight gates for a PassWall package upgrade.
 *
 * Both gates exist because the fleet has already been broken by them, in the
 * same week, on routers whose upgrade job reported success:
 *
 *  - PassWall 26.7.16+ sends the subscription hardware id only when
 *    `option hwid '1'` is set. Upgrading a router that lacks it turns the gate
 *    on, the provider answers the next nightly request with a placeholder, and
 *    the cron replaces every real node with it.
 *  - PassWall 26.8.10 emits a config that xray refuses below 26.3.27. The
 *    passwall feed ships an older xray-core, so a router whose package is behind
 *    ends up with PassWall in no-proxy mode: a running xray process and no
 *    traffic at all.
 *
 * Both are judged from what the router itself reported, and both fail open when
 * that report is missing — absence of evidence must not freeze the fleet.
 */

/** First PassWall release that requires the HWID option for subscriptions. */
const HWID_GATE_VERSION = [26, 7, 16] as const;

/** Oldest xray-core package that survives the 26.8.10 config contract. */
const XRAY_PACKAGE_FLOOR = [26, 2, 6] as const;

export type PasswallUpgradePreflightInput = {
  subscriptionHealth?: {
    hwidEnabled: boolean;
    scheduleEnabled: boolean;
    placeholderNodes: number;
  } | null;
  /** `opkg` version of xray-core, or null when it is not a tracked package. */
  xrayPackageVersion?: string | null;
  targetVersion: string;
};

export type PasswallUpgradePreflightResult = {
  blocked: boolean;
  reasons: string[];
};

function parseVersion(value: string | null | undefined) {
  if (!value) {
    return null;
  }
  const match = /(\d+)\.(\d+)\.(\d+)/.exec(value);
  if (!match) {
    return null;
  }
  return [Number(match[1]), Number(match[2]), Number(match[3])] as const;
}

function isAtLeast(
  version: readonly [number, number, number],
  floor: readonly [number, number, number],
) {
  for (let index = 0; index < 3; index += 1) {
    const left = version[index] ?? 0;
    const right = floor[index] ?? 0;
    if (left !== right) {
      return left > right;
    }
  }
  return true;
}

export function checkPasswallUpgradePreflight(
  input: PasswallUpgradePreflightInput,
): PasswallUpgradePreflightResult {
  const reasons: string[] = [];

  const target = parseVersion(input.targetVersion);
  const targetEnablesHwidGate =
    target !== null && isAtLeast(target, HWID_GATE_VERSION);

  // Only meaningful when the router actually told us; an older controller that
  // reports nothing is unknown, not unsafe.
  if (
    targetEnablesHwidGate &&
    input.subscriptionHealth &&
    !input.subscriptionHealth.hwidEnabled
  ) {
    reasons.push(
      `На роутере не выставлен hwid, а ${input.targetVersion} включает HWID-гейт: ` +
        "первая же ночная подписка получит заглушку и сотрёт узлы. " +
        "Сначала `uci set passwall2.<секция>.hwid=1 && uci commit passwall2`.",
    );
  }

  // A router with no xray-core package runs a manually placed binary that opkg
  // cannot see, so there is nothing to compare and nothing to block on.
  const xray = parseVersion(input.xrayPackageVersion);
  if (xray !== null && !isAtLeast(xray, XRAY_PACKAGE_FLOOR)) {
    reasons.push(
      `Пакет xray-core ${input.xrayPackageVersion} ниже ${XRAY_PACKAGE_FLOOR.join(".")}: ` +
        "апгрейд PassWall снесёт xray и оставит роутер без прокси. " +
        "Сначала обновить xray-core.",
    );
  }

  return { blocked: reasons.length > 0, reasons };
}
