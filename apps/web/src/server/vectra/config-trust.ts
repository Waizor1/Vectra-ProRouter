export type ConfigSourceMode =
  | "live-import"
  | "authoritative"
  | "stale-authoritative"
  | "inventory-only";

export type ConfigTrustState = {
  liveConfigAvailable: boolean;
  requiresReimport: boolean;
  digestMismatch: boolean;
  configSourceMode: ConfigSourceMode;
  lastLiveImportAt: Date | null;
  lastCheckInAt: Date | null;
};

type LiveImportRevisionLike = {
  configDigest: string | null | undefined;
  createdAt: Date | null | undefined;
  origin: string | null | undefined;
};

function normalizeDigest(value: string | null | undefined) {
  const trimmed = value?.trim();
  return trimmed && trimmed.length > 0 ? trimmed : null;
}

export function isLiveImportOrigin(origin: string | null | undefined) {
  return origin === "router_import" || origin === "operator_reimport";
}

export function buildConfigTrustState<T extends LiveImportRevisionLike>(args: {
  routerReachable: boolean;
  lastCheckInAt: Date | null;
  authoritativeDigest: string | null | undefined;
  snapshotDigest: string | null | undefined;
  revisions: T[];
  hasAuthoritativeConfig: boolean;
}): ConfigTrustState {
  const snapshotDigest = normalizeDigest(args.snapshotDigest);
  const authoritativeDigest = normalizeDigest(args.authoritativeDigest);
  const liveImportRevisions = args.revisions.filter((revision) =>
    isLiveImportOrigin(revision.origin),
  );
  const matchingLiveImport =
    snapshotDigest === null
      ? null
      : liveImportRevisions.find(
          (revision) => normalizeDigest(revision.configDigest) === snapshotDigest,
        ) ?? null;
  const latestLiveImport = liveImportRevisions[0] ?? null;
  const liveConfigAvailable = matchingLiveImport !== null;
  const digestMismatch =
    snapshotDigest !== null &&
    authoritativeDigest !== null &&
    snapshotDigest !== authoritativeDigest;
  const configSourceMode: ConfigSourceMode = liveConfigAvailable
    ? "live-import"
    : args.hasAuthoritativeConfig
      ? snapshotDigest
        ? "stale-authoritative"
        : "authoritative"
      : "inventory-only";

  return {
    liveConfigAvailable,
    requiresReimport:
      args.routerReachable && snapshotDigest !== null && !liveConfigAvailable,
    digestMismatch,
    configSourceMode,
    lastLiveImportAt: latestLiveImport?.createdAt ?? null,
    lastCheckInAt: args.lastCheckInAt,
  };
}
