export type RouterImportState =
  | "awaiting_import"
  | "import_review"
  | "approved"
  | "out_of_sync";

type ResolvePersistedConfigDigestArgs = {
  previousDigest: string | null;
  reportedDigest: string | null | undefined;
  hasPasswallImport: boolean;
};

type ShouldRequestImportOnCheckInArgs = {
  importState: RouterImportState;
  /** A Vectra Connect owner's router on the xray engine (isConnectOwnedXrayRouter). */
  connectOwned?: boolean;
  hasPasswallImport: boolean;
  reportedDigest: string | null | undefined;
  authoritativeDigest: string | null | undefined;
};

type ResolveImportedConfigDigestArgs = {
  importedDigest: string | null | undefined;
  fallbackDigest: string;
};

export function resolvePersistedConfigDigest(
  args: ResolvePersistedConfigDigestArgs
) {
  if (!args.hasPasswallImport) {
    return args.previousDigest;
  }

  return args.reportedDigest ?? args.previousDigest;
}

export function shouldRequestImportOnCheckIn(
  args: ShouldRequestImportOnCheckInArgs
) {
  // Its config is the owner's, run by vctl: there is no PassWall baseline to
  // ask for, and a digest that moves is the owner's own change.
  if (args.connectOwned) {
    return false;
  }
  if (args.importState === "awaiting_import") {
    return !args.hasPasswallImport;
  }

  return (
    args.importState === "approved" &&
    !args.hasPasswallImport &&
    Boolean(args.reportedDigest) &&
    args.reportedDigest !== (args.authoritativeDigest ?? null)
  );
}

export function resolveImportedConfigDigest(
  args: ResolveImportedConfigDigestArgs
) {
  const importedDigest = args.importedDigest?.trim();
  return importedDigest && importedDigest.length > 0
    ? importedDigest
    : args.fallbackDigest;
}

/**
 * A router a Vectra Connect owner holds and the panel runs on the xray engine.
 * A PassWall baseline from it can only come from a legacy agent that woke up
 * on it (a dead-man hand-back): it is not the router's config, and must never
 * stop the owner's actions — partner jobs are delivered only while the router
 * is "approved".
 */
export function isConnectOwnedXrayRouter(router: {
  ownerRef: string | null;
  releasedAt: Date | null;
  engineMode: string;
}) {
  return Boolean(router.ownerRef) && !router.releasedAt && router.engineMode === "xray-direct";
}
