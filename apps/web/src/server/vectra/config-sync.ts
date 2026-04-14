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
