import { compareLooseSemverVersions } from "~/lib/passwall-update-summary";

export const PASSWALL_FEATURE_MIN_VERSIONS = {
  shuntQuicProtocol: "26.5.1",
  xrayMkcpMtu: "26.5.1",
  xrayTlsPinSha256: "26.5.1",
  subscriptionDomainResolver: "26.4.20",
} as const;

export type PasswallFeatureGate = {
  supported: boolean;
  minimumVersion: string;
  currentVersion: string | null;
  reason: string | null;
};

export function getPasswallFeatureGate(
  currentVersion: string | null | undefined,
  minimumVersion: string,
): PasswallFeatureGate {
  const trimmedCurrent = currentVersion?.trim();
  const normalizedCurrent =
    trimmedCurrent && trimmedCurrent.length > 0 ? trimmedCurrent : null;
  const comparison = compareLooseSemverVersions(
    normalizedCurrent,
    minimumVersion,
  );
  const supported = comparison !== null && comparison >= 0;

  return {
    supported,
    minimumVersion,
    currentVersion: normalizedCurrent,
    reason: supported
      ? null
      : normalizedCurrent
        ? `Работает только с PassWall2 ${minimumVersion}+; на этом роутере ${normalizedCurrent}.`
        : `Работает только с PassWall2 ${minimumVersion}+; версия PassWall2 на роутере неизвестна.`,
  };
}
