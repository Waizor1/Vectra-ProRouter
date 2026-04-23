import {
  AX3000T_OPTIONAL_MIRRORED_PACKAGES,
  AX3000T_REQUIRED_MIRRORED_PACKAGES,
  DEFAULT_ARTIFACT_BASE_URL,
  buildAx3000tPasswallMirrorUrl,
} from "~/app/enrollment/install-presets";

export const PASSWALL_RECOVERY_DEPENDENCY_PACKAGES = [
  "dnsmasq-full",
  "kmod-nft-socket",
  "kmod-nft-tproxy",
  "kmod-nft-nat",
] as const;

export const PASSWALL_OPTIONAL_COMPONENT_PACKAGES = [
  "sing-box",
  "hysteria",
] as const;

export const PASSWALL_HEAVY_COMPONENT_PACKAGES = [
  "xray-core",
  "geoview",
  ...PASSWALL_OPTIONAL_COMPONENT_PACKAGES,
] as const;

export const PASSWALL_MANAGED_STACK_REQUIRED_PACKAGES = [
  ...AX3000T_REQUIRED_MIRRORED_PACKAGES.map((entry) => entry.name),
  ...PASSWALL_RECOVERY_DEPENDENCY_PACKAGES,
] as const;

export const PASSWALL_MANAGED_INSTALL_ORDER = [
  "xray-core",
  "v2ray-geoip",
  "v2ray-geosite",
  "geoview",
  "sing-box",
  "hysteria",
  "chinadns-ng",
  "tcping",
  "dnsmasq-full",
  "kmod-nft-socket",
  "kmod-nft-tproxy",
  "kmod-nft-nat",
  "luci-app-passwall2",
] as const;

export const PASSWALL_PACKAGE_TARGET_ROWS = [
  {
    key: "passwall2",
    label: "PassWall2",
    packages: ["luci-app-passwall2"] as const,
    managedStack: true,
  },
  {
    key: "xray",
    label: "Xray",
    packages: ["xray-core"] as const,
    managedStack: false,
  },
  {
    key: "sing-box",
    label: "sing-box",
    packages: ["sing-box"] as const,
    managedStack: false,
  },
  {
    key: "hysteria",
    label: "Hysteria",
    packages: ["hysteria"] as const,
    managedStack: false,
  },
  {
    key: "geoview",
    label: "Geoview",
    packages: ["geoview"] as const,
    managedStack: false,
  },
] as const;

export type PasswallArtifactOrigin = "vectra" | "upstream";
export type PasswallFallbackPolicy =
  | "package-only"
  | "adaptive-component-fallback";
export type PasswallUpdateScope = "managed-stack" | "scoped-package";
export type PasswallPackageUpdateStatus =
  | "updated"
  | "package-updated"
  | "already-current"
  | "runtime-updated"
  | "runtime-only-converged"
  | "storage-blocked"
  | "delivery-blocked"
  | "failed";
export type PasswallPackagePathUsed =
  | "package"
  | "built-in-updater"
  | "xray-binary-payload"
  | "not-needed";

export type PasswallPackageArtifactDescriptor = {
  name: string;
  artifactUrl: string;
  artifactVersion: string;
  sha256: string;
  signatureUrl: string | null;
  downloadSizeBytes: number | null;
  installedSizeBytes: number | null;
  required: boolean;
  source: PasswallArtifactOrigin;
};

export type PasswallRuntimeTargetDescriptor = {
  componentName: string;
  remoteVersion: string;
  releaseUrl: string | null;
  assetName: string | null;
  assetUrl: string | null;
  assetSizeBytes: number | null;
};

export type PasswallBundleMetadata = {
  source: PasswallArtifactOrigin;
  releaseTag: string;
  manifestUrl: string | null;
  releaseUrl: string | null;
  packageBundleUrl: string | null;
  runtimeTargets: Readonly<Record<string, PasswallRuntimeTargetDescriptor>>;
  requiredPackages: ReadonlyArray<{
    name: string;
    version: string;
    filename: string;
    downloadSizeBytes: number | null;
    installedSizeBytes: number | null;
  }>;
  optionalPackages: ReadonlyArray<{
    name: string;
    version: string;
    filename: string;
    downloadSizeBytes: number | null;
    installedSizeBytes: number | null;
  }>;
  packageArtifacts: ReadonlyArray<PasswallPackageArtifactDescriptor>;
  managedPackageList: ReadonlyArray<string>;
  recoveryDependencies: ReadonlyArray<string>;
  installOrder: ReadonlyArray<string>;
};

type ArtifactRowLike = {
  id?: string;
  type: string;
  channel: string;
  name: string;
  version: string;
  architecture: string | null;
  boardName?: string | null;
  layoutFamily?: string | null;
  downloadUrl: string;
  checksumSha256: string;
  signatureUrl: string | null;
  metadata?: Record<string, unknown> | null;
  publishedAt?: Date | string | null;
};

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function readString(value: unknown) {
  return typeof value === "string" && value.trim().length > 0 ? value : null;
}

function readNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function parsePackageEntry(value: unknown) {
  if (!isObject(value)) {
    return null;
  }

  const name = readString(value.name);
  const version = readString(value.version);
  const filename = readString(value.filename);
  if (!name || !version || !filename) {
    return null;
  }

  return {
    name,
    version,
    filename,
    downloadSizeBytes: readNumber(value.downloadSizeBytes),
    installedSizeBytes: readNumber(value.installedSizeBytes),
  };
}

function parsePackageArtifactDescriptor(
  value: unknown,
): PasswallPackageArtifactDescriptor | null {
  if (!isObject(value)) {
    return null;
  }

  const name = readString(value.name);
  const artifactUrl = readString(value.artifactUrl);
  const artifactVersion = readString(value.artifactVersion);
  const sha256 = readString(value.sha256);
  if (!name || !artifactUrl || !artifactVersion || !sha256) {
    return null;
  }

  const source = value.source === "upstream" ? "upstream" : "vectra";

  return {
    name,
    artifactUrl,
    artifactVersion,
    sha256,
    signatureUrl: readString(value.signatureUrl),
    downloadSizeBytes: readNumber(value.downloadSizeBytes),
    installedSizeBytes: readNumber(value.installedSizeBytes),
    required: value.required !== false,
    source,
  };
}

function parseBundlePackageEntries(
  value: unknown,
): ReadonlyArray<
  ReturnType<typeof parsePackageEntry> extends infer T
    ? Exclude<T, null>
    : never
> {
  if (!Array.isArray(value)) {
    return [];
  }

  return value
    .map((entry) => parsePackageEntry(entry))
    .filter((entry): entry is Exclude<typeof entry, null> => entry !== null);
}

function parseBundlePackageArtifacts(value: unknown) {
  if (!Array.isArray(value)) {
    return [];
  }

  return value
    .map((entry) => parsePackageArtifactDescriptor(entry))
    .filter(
      (entry): entry is PasswallPackageArtifactDescriptor => entry !== null,
    );
}

function parseRuntimeTargetDescriptor(
  value: unknown,
): PasswallRuntimeTargetDescriptor | null {
  if (!isObject(value)) {
    return null;
  }

  const componentName = readString(value.componentName);
  const remoteVersion = readString(value.remoteVersion);
  if (!componentName || !remoteVersion) {
    return null;
  }

  return {
    componentName,
    remoteVersion,
    releaseUrl: readString(value.releaseUrl),
    assetName: readString(value.assetName),
    assetUrl: readString(value.assetUrl),
    assetSizeBytes: readNumber(value.assetSizeBytes),
  };
}

function parseRuntimeTargets(
  value: unknown,
): Readonly<Record<string, PasswallRuntimeTargetDescriptor>> {
  if (!isObject(value)) {
    return {};
  }

  const entries = Object.entries(value)
    .map(([packageName, descriptor]) => {
      const parsed = parseRuntimeTargetDescriptor(descriptor);
      if (!parsed || packageName.trim().length === 0) {
        return null;
      }
      return [packageName, parsed] as const;
    })
    .filter(
      (entry): entry is readonly [string, PasswallRuntimeTargetDescriptor] =>
        entry !== null,
    );

  return Object.fromEntries(entries);
}

export function sortPasswallPackageList(packages: readonly string[]) {
  const unique = Array.from(new Set(packages));
  const order = new Map<string, number>(
    PASSWALL_MANAGED_INSTALL_ORDER.map((name, index) => [name, index]),
  );

  return [...unique].sort((left, right) => {
    const leftOrder = order.get(left) ?? Number.MAX_SAFE_INTEGER;
    const rightOrder = order.get(right) ?? Number.MAX_SAFE_INTEGER;
    if (leftOrder !== rightOrder) {
      return leftOrder - rightOrder;
    }
    return left.localeCompare(right);
  });
}

export function isOptionalPasswallManagedPackage(packageName: string) {
  return PASSWALL_OPTIONAL_COMPONENT_PACKAGES.includes(
    packageName as (typeof PASSWALL_OPTIONAL_COMPONENT_PACKAGES)[number],
  );
}

export function isHeavyPasswallComponent(packageName: string) {
  return PASSWALL_HEAVY_COMPONENT_PACKAGES.includes(
    packageName as (typeof PASSWALL_HEAVY_COMPONENT_PACKAGES)[number],
  );
}

export function packageNameToRuntimeKey(packageName: string) {
  switch (packageName) {
    case "luci-app-passwall2":
      return "passwall2";
    case "xray-core":
      return "xray";
    case "sing-box":
      return "sing-box";
    case "hysteria":
      return "hysteria";
    case "geoview":
      return "geoview";
    default:
      return packageName;
  }
}

export function buildFallbackPasswallArtifactDescriptors(args?: {
  artifactBaseUrl?: string;
}) {
  const artifactBaseUrl = args?.artifactBaseUrl ?? DEFAULT_ARTIFACT_BASE_URL;
  const mirrorBaseUrl = buildAx3000tPasswallMirrorUrl(artifactBaseUrl);

  return [
    ...AX3000T_REQUIRED_MIRRORED_PACKAGES.map((entry) => ({
      name: entry.name,
      artifactUrl: new URL(entry.filename, mirrorBaseUrl).toString(),
      artifactVersion: entry.version,
      sha256: "",
      signatureUrl: null,
      downloadSizeBytes: entry.downloadSizeBytes,
      installedSizeBytes: entry.installedSizeBytes,
      required: true,
      source: "vectra" as const,
    })),
    ...AX3000T_OPTIONAL_MIRRORED_PACKAGES.map((entry) => ({
      name: entry.name,
      artifactUrl: new URL(entry.filename, mirrorBaseUrl).toString(),
      artifactVersion: entry.version,
      sha256: "",
      signatureUrl: null,
      downloadSizeBytes: entry.downloadSizeBytes,
      installedSizeBytes: entry.installedSizeBytes,
      required: false,
      source: "vectra" as const,
    })),
  ];
}

export function findPasswallRuntimeTarget(
  bundleMetadata: PasswallBundleMetadata,
  packageName: string,
) {
  return bundleMetadata.runtimeTargets[packageName] ?? null;
}

export function buildFallbackPasswallBundleMetadata(args?: {
  artifactBaseUrl?: string;
}) {
  const artifactBaseUrl = args?.artifactBaseUrl ?? DEFAULT_ARTIFACT_BASE_URL;
  const mirrorBaseUrl = buildAx3000tPasswallMirrorUrl(artifactBaseUrl);
  const releaseTag = mirrorBaseUrl.replace(/\/+$/, "").split("/").at(-2);

  return {
    source: "vectra" as const,
    releaseTag: releaseTag ?? "",
    manifestUrl: new URL("manifest.json", mirrorBaseUrl).toString(),
    releaseUrl: null,
    packageBundleUrl: null,
    runtimeTargets: {},
    requiredPackages: AX3000T_REQUIRED_MIRRORED_PACKAGES,
    optionalPackages: AX3000T_OPTIONAL_MIRRORED_PACKAGES,
    packageArtifacts: buildFallbackPasswallArtifactDescriptors({
      artifactBaseUrl,
    }),
    managedPackageList: sortPasswallPackageList(
      PASSWALL_MANAGED_STACK_REQUIRED_PACKAGES,
    ),
    recoveryDependencies: [...PASSWALL_RECOVERY_DEPENDENCY_PACKAGES],
    installOrder: [...PASSWALL_MANAGED_INSTALL_ORDER],
  } satisfies PasswallBundleMetadata;
}

export function parsePasswallBundleMetadata(
  metadata: Record<string, unknown> | null | undefined,
): PasswallBundleMetadata | null {
  if (!metadata) {
    return null;
  }

  const requiredPackages = parseBundlePackageEntries(metadata.requiredPackages);
  const optionalPackages = parseBundlePackageEntries(metadata.optionalPackages);
  const packageArtifacts = parseBundlePackageArtifacts(
    metadata.packageArtifacts,
  );
  const releaseTag =
    readString(metadata.releaseTag) ?? readString(metadata.tag);

  if (!releaseTag || packageArtifacts.length === 0) {
    return null;
  }

  const managedPackageList = Array.isArray(metadata.managedPackageList)
    ? metadata.managedPackageList.filter(
        (entry): entry is string =>
          typeof entry === "string" && entry.trim().length > 0,
      )
    : sortPasswallPackageList([
        ...requiredPackages.map((entry) => entry.name),
        ...PASSWALL_RECOVERY_DEPENDENCY_PACKAGES,
      ]);
  const recoveryDependencies = Array.isArray(metadata.recoveryDependencies)
    ? metadata.recoveryDependencies.filter(
        (entry): entry is string =>
          typeof entry === "string" && entry.trim().length > 0,
      )
    : [...PASSWALL_RECOVERY_DEPENDENCY_PACKAGES];
  const installOrder = Array.isArray(metadata.installOrder)
    ? metadata.installOrder.filter(
        (entry): entry is string =>
          typeof entry === "string" && entry.trim().length > 0,
      )
    : [...PASSWALL_MANAGED_INSTALL_ORDER];

  return {
    source: metadata.source === "upstream" ? "upstream" : "vectra",
    releaseTag,
    manifestUrl: readString(metadata.manifestUrl),
    releaseUrl: readString(metadata.releaseUrl),
    packageBundleUrl: readString(metadata.packageBundleUrl),
    runtimeTargets: parseRuntimeTargets(metadata.runtimeTargets),
    requiredPackages,
    optionalPackages,
    packageArtifacts,
    managedPackageList: sortPasswallPackageList(managedPackageList),
    recoveryDependencies,
    installOrder,
  };
}

export function buildLatestPasswallArtifactMap<
  TArtifact extends ArtifactRowLike,
>(artifacts: readonly TArtifact[]) {
  const latestByName = new Map<string, TArtifact>();
  for (const artifact of artifacts) {
    if (!latestByName.has(artifact.name)) {
      latestByName.set(artifact.name, artifact);
    }
  }
  return latestByName;
}

export function resolvePasswallPackageArtifactsFromRows<
  TArtifact extends ArtifactRowLike,
>(artifacts: readonly TArtifact[]) {
  return artifacts
    .filter((artifact) => artifact.type === "passwall_package")
    .map((artifact) => ({
      name: artifact.name,
      artifactUrl: artifact.downloadUrl,
      artifactVersion: artifact.version,
      sha256: artifact.checksumSha256,
      signatureUrl: artifact.signatureUrl ?? null,
      downloadSizeBytes: readNumber(artifact.metadata?.downloadSizeBytes),
      installedSizeBytes: readNumber(artifact.metadata?.installedSizeBytes),
      required: artifact.metadata?.required !== false,
      source:
        artifact.metadata?.source === "upstream"
          ? ("upstream" as const)
          : ("vectra" as const),
    }))
    .filter((artifact) => artifact.sha256.length > 0);
}

export function buildPasswallBundleMetadataFromArtifact<
  TArtifact extends ArtifactRowLike,
>(artifact: TArtifact | null | undefined) {
  if (!artifact) {
    return null;
  }

  return parsePasswallBundleMetadata(artifact.metadata ?? null);
}

export function resolveInstalledOptionalPasswallPackages(payload: unknown) {
  if (!isObject(payload)) {
    return [];
  }

  const packageVersions = isObject(payload.packageVersions)
    ? payload.packageVersions
    : null;

  return PASSWALL_OPTIONAL_COMPONENT_PACKAGES.filter((packageName) => {
    const version = packageVersions?.[packageName];
    return typeof version === "string" && version.trim().length > 0;
  });
}

export function formatPasswallArtifactSourceLabel(
  source: PasswallArtifactOrigin,
) {
  return source === "upstream" ? "upstream" : "Vectra mirror";
}
