export const AX3000T_BASELINE_PATH =
  "/api/install/ax3000t-passwall2-baseline.uci";
export const AX3000T_BOOTSTRAP_PATH = "/install/ax3000t-bootstrap.sh";
export const AX3000T_SHUNT_REBIND_PATH = "/install/ax3000t-myshunt-rebind.sh";
export const DEFAULT_CONTROL_DOMAIN = "https://router.vectra-pro.net";
export const DEFAULT_ROUTER_API_BASE_URL = "https://api.vectra-pro.net";
export const DEFAULT_ARTIFACT_BASE_URL =
  "https://api.vectra-pro.net/artifacts";
export const DEFAULT_PASSWALL2_RELEASE_TAG = "26.4.5-1";
const DEFAULT_PASSWALL2_RELEASE_URL = `https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/download/${DEFAULT_PASSWALL2_RELEASE_TAG}`;

export type Ax3000tMirroredPackageName =
  | "luci-app-passwall2"
  | "tcping"
  | "xray-core"
  | "geoview"
  | "v2ray-geoip"
  | "v2ray-geosite"
  | "chinadns-ng"
  | "sing-box"
  | "hysteria";

export type Ax3000tManagedPackageRole =
  | "passwall-app"
  | "utility"
  | "proxy-core"
  | "geo-view"
  | "geo-data"
  | "dns-helper";

export type Ax3000tManagedPackageClass = "required" | "optional";

export type Ax3000tBootstrapInstallStage =
  | "managed-passwall"
  | "managed-passwall-finalize";

export type Ax3000tBootstrapClassification =
  | "fresh install"
  | "upgrade existing PassWall stack"
  | "repair drifted managed packages";

export type Ax3000tManagedPackageDefinition = {
  name: Ax3000tMirroredPackageName;
  filename: string;
  role: Ax3000tManagedPackageRole;
  installClass: Ax3000tManagedPackageClass;
  targetVersion: string;
  downloadSizeBytes: number;
  installedSizeBytes: number;
  upgradeOrder: number;
};

export type PasswallMirrorManifestEntry = {
  name: Ax3000tMirroredPackageName;
  filename: string;
  version: string;
  downloadSizeBytes: number;
  installedSizeBytes: number;
};

export type PasswallMirrorManifest = {
  tag: string;
  arch: string;
  requiredPackages: ReadonlyArray<PasswallMirrorManifestEntry>;
  optionalPackages: ReadonlyArray<PasswallMirrorManifestEntry>;
  sourceUrls: {
    release: string;
    luciAppPackage: string;
    packageBundle: string;
  };
};

export type Ax3000tManagedPackageState = {
  installed: boolean;
  version: string | null;
  installedSizeBytes?: number | null;
  unexpectedDependents?: readonly string[];
};

export type Ax3000tManagedPackagePlanAction =
  | "skip"
  | "install"
  | "replace"
  | "reinstall";

export type Ax3000tManagedPackagePlanStep = {
  packageName: Ax3000tMirroredPackageName;
  stage: Ax3000tBootstrapInstallStage;
  action: Ax3000tManagedPackagePlanAction;
  currentVersion: string | null;
  targetVersion: string;
  currentInstalledSizeBytes: number;
  targetInstalledSizeBytes: number;
};

export type Ax3000tManagedPackageStorageCheck = {
  ok: boolean;
  reason: "stage" | "overlay" | "unexpected_dependents" | null;
  blockingPackageName: Ax3000tMirroredPackageName | null;
  availableBytes: number;
  requiredBytes: number;
  reclaimBytes: number;
  unexpectedDependents: readonly string[];
  message: string;
};

export type Ax3000tManagedPackagePlan = {
  classification: Ax3000tBootstrapClassification;
  passwallAppRemovalRequired: boolean;
  requiredStageBytes: number;
  steps: ReadonlyArray<Ax3000tManagedPackagePlanStep>;
  storageCheck: Ax3000tManagedPackageStorageCheck;
};

export const AX3000T_MANAGED_PACKAGE_CATALOG: Record<
  Ax3000tMirroredPackageName,
  Ax3000tManagedPackageDefinition
> = {
  "luci-app-passwall2": {
    name: "luci-app-passwall2",
    filename: "luci-app-passwall2_26.4.5-r1_all.ipk",
    role: "passwall-app",
    installClass: "required",
    targetVersion: "26.4.5-r1",
    downloadSizeBytes: 325772,
    installedSizeBytes: 1300480,
    upgradeOrder: 90,
  },
  tcping: {
    name: "tcping",
    filename: "tcping_0.3-r1_aarch64_cortex-a53.ipk",
    role: "utility",
    installClass: "required",
    targetVersion: "0.3-r1",
    downloadSizeBytes: 4339,
    installedSizeBytes: 71680,
    upgradeOrder: 70,
  },
  "xray-core": {
    name: "xray-core",
    filename: "xray-core_26.3.27-r1_aarch64_cortex-a53.ipk",
    role: "proxy-core",
    installClass: "required",
    targetVersion: "26.3.27-r1",
    downloadSizeBytes: 10777362,
    installedSizeBytes: 30320640,
    upgradeOrder: 10,
  },
  geoview: {
    name: "geoview",
    filename: "geoview_0.2.5-r1_aarch64_cortex-a53.ipk",
    role: "geo-view",
    installClass: "required",
    targetVersion: "0.2.5-r1",
    downloadSizeBytes: 2740538,
    installedSizeBytes: 7208960,
    upgradeOrder: 40,
  },
  "v2ray-geoip": {
    name: "v2ray-geoip",
    filename: "v2ray-geoip_202603260032.1_all.ipk",
    role: "geo-data",
    installClass: "required",
    targetVersion: "202603260032.1",
    downloadSizeBytes: 4040459,
    installedSizeBytes: 19773440,
    upgradeOrder: 20,
  },
  "v2ray-geosite": {
    name: "v2ray-geosite",
    filename: "v2ray-geosite_202603292224.1_all.ipk",
    role: "geo-data",
    installClass: "required",
    targetVersion: "202603292224.1",
    downloadSizeBytes: 3456591,
    installedSizeBytes: 10536960,
    upgradeOrder: 30,
  },
  "chinadns-ng": {
    name: "chinadns-ng",
    filename: "chinadns-ng_2025.08.09-r1_aarch64_cortex-a53.ipk",
    role: "dns-helper",
    installClass: "required",
    targetVersion: "2025.08.09-r1",
    downloadSizeBytes: 269754,
    installedSizeBytes: 522240,
    upgradeOrder: 60,
  },
  "sing-box": {
    name: "sing-box",
    filename: "sing-box_1.13.5-r1_aarch64_cortex-a53.ipk",
    role: "proxy-core",
    installClass: "optional",
    targetVersion: "1.13.5-r1",
    downloadSizeBytes: 15947069,
    installedSizeBytes: 45209600,
    upgradeOrder: 50,
  },
  hysteria: {
    name: "hysteria",
    filename: "hysteria_2.8.1-r1_aarch64_cortex-a53.ipk",
    role: "proxy-core",
    installClass: "optional",
    targetVersion: "2.8.1-r1",
    downloadSizeBytes: 7012046,
    installedSizeBytes: 19077120,
    upgradeOrder: 55,
  },
} as const;

export const AX3000T_PASSWALL_PACKAGE_FILES = Object.fromEntries(
  Object.values(AX3000T_MANAGED_PACKAGE_CATALOG).map((pkg) => [
    pkg.name,
    pkg.filename,
  ]),
) as Record<Ax3000tMirroredPackageName, string>;

export type Ax3000tShuntRebindTarget = {
  slot: "WorldProxy" | "YouTube" | "Special" | "Tiktok";
  remark: string;
};

function buildMirrorManifestEntries<TPackageName extends Ax3000tMirroredPackageName>(
  packageNames: readonly TPackageName[],
) {
  return packageNames.map((name) => {
    const pkg = AX3000T_MANAGED_PACKAGE_CATALOG[name];
    return {
      name: pkg.name,
      filename: pkg.filename,
      version: pkg.targetVersion,
      downloadSizeBytes: pkg.downloadSizeBytes,
      installedSizeBytes: pkg.installedSizeBytes,
    };
  });
}

export const AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES = [
  "tcping",
  "xray-core",
  "geoview",
  "v2ray-geoip",
  "v2ray-geosite",
  "chinadns-ng",
  "luci-app-passwall2",
] as const;

export const AX3000T_OPTIONAL_MIRRORED_PACKAGE_NAMES = [
  "sing-box",
  "hysteria",
] as const;

export const AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER = [
  ...AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES,
].sort(
  (left, right) =>
    AX3000T_MANAGED_PACKAGE_CATALOG[left].upgradeOrder -
    AX3000T_MANAGED_PACKAGE_CATALOG[right].upgradeOrder,
);

export const AX3000T_REQUIRED_MIRRORED_PACKAGES =
  buildMirrorManifestEntries(AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES);

export const AX3000T_OPTIONAL_MIRRORED_PACKAGES =
  buildMirrorManifestEntries(AX3000T_OPTIONAL_MIRRORED_PACKAGE_NAMES);

const AX3000T_INTERNAL_DEPENDENT_NAMES = new Set<string>([
  ...AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES,
  ...AX3000T_OPTIONAL_MIRRORED_PACKAGE_NAMES,
]);

type NormalizedAx3000tManagedPackageState = {
  installed: boolean;
  version: string | null;
  installedSizeBytes: number;
  unexpectedDependents: readonly string[];
};

function normalizeManagedPackageState(
  state: Ax3000tManagedPackageState | undefined,
): NormalizedAx3000tManagedPackageState {
  return {
    installed: state?.installed ?? false,
    version: state?.version ?? null,
    installedSizeBytes: Math.max(0, state?.installedSizeBytes ?? 0),
    unexpectedDependents: state?.unexpectedDependents ?? [],
  };
}

function buildManagedStorageMessage(
  reason: Ax3000tManagedPackageStorageCheck["reason"],
  packageName: Ax3000tMirroredPackageName | null,
  availableBytes: number,
  requiredBytes: number,
  reclaimBytes: number,
  unexpectedDependents: readonly string[],
) {
  if (reason === "unexpected_dependents") {
    const dependents = unexpectedDependents.join(", ");
    return `Автоматический reclaim для ${packageName ?? "managed package"} запрещён: найдены внешние зависимости (${dependents}).`;
  }

  if (reason === "stage") {
    return `Недостаточно staging-space для ${packageName ?? "managed package"}: доступно ${availableBytes} B, требуется ${requiredBytes} B.`;
  }

  if (reason === "overlay") {
    const reclaimSuffix =
      reclaimBytes > 0
        ? ` Даже после reclaim доступно только ${availableBytes + reclaimBytes} B при требуемых ${requiredBytes} B.`
        : "";
    return `Недостаточно места на /overlay для ${packageName ?? "managed package"}: доступно ${availableBytes} B, требуется ${requiredBytes} B.${reclaimSuffix}`;
  }

  return "Storage preflight пройден.";
}

export function planAx3000tManagedPackageOperations(args: {
  overlayFreeBytes: number;
  stageFreeBytes: number;
  packageStates: Partial<
    Record<Ax3000tMirroredPackageName, Ax3000tManagedPackageState>
  >;
  stageMarginBytes?: number;
}): Ax3000tManagedPackagePlan {
  const overlayFreeBytes = Math.max(0, args.overlayFreeBytes);
  const stageFreeBytes = Math.max(0, args.stageFreeBytes);
  const stageMarginBytes = Math.max(0, args.stageMarginBytes ?? 1_048_576);

  const normalizedStates = Object.fromEntries(
    (Object.keys(AX3000T_MANAGED_PACKAGE_CATALOG) as Ax3000tMirroredPackageName[]).map(
      (name) => [name, normalizeManagedPackageState(args.packageStates[name])],
    ),
  ) as Record<
    Ax3000tMirroredPackageName,
    NormalizedAx3000tManagedPackageState
  >;

  const installedRequiredPackages = AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES.filter(
    (name) => normalizedStates[name].installed,
  );
  const outdatedRequiredPackages = AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES.filter(
    (name) =>
      normalizedStates[name].installed &&
      normalizedStates[name].version !==
        AX3000T_MANAGED_PACKAGE_CATALOG[name].targetVersion,
  );
  const missingRequiredPackages = AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES.filter(
    (name) => !normalizedStates[name].installed,
  );

  let classification: Ax3000tBootstrapClassification;
  if (installedRequiredPackages.length === 0) {
    classification = "fresh install";
  } else if (outdatedRequiredPackages.length > 0) {
    classification = "upgrade existing PassWall stack";
  } else if (missingRequiredPackages.length > 0) {
    classification = "repair drifted managed packages";
  } else {
    classification = "upgrade existing PassWall stack";
  }

  const steps: Ax3000tManagedPackagePlanStep[] = [];
  const requiredStageBytes = AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER.reduce(
    (maxBytes, name) => {
      const state = normalizedStates[name];
      const target = AX3000T_MANAGED_PACKAGE_CATALOG[name];
      if (state.installed && state.version === target.targetVersion) {
        return maxBytes;
      }
      return Math.max(maxBytes, target.downloadSizeBytes + stageMarginBytes);
    },
    0,
  );

  if (stageFreeBytes < requiredStageBytes) {
    const blockingPackage =
      AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER.find((name) => {
        const state = normalizedStates[name];
        const target = AX3000T_MANAGED_PACKAGE_CATALOG[name];
        return (
          (!state.installed || state.version !== target.targetVersion) &&
          stageFreeBytes < target.downloadSizeBytes + stageMarginBytes
        );
      }) ?? null;
    return {
      classification,
      passwallAppRemovalRequired: false,
      requiredStageBytes,
      steps,
      storageCheck: {
        ok: false,
        reason: "stage",
        blockingPackageName: blockingPackage,
        availableBytes: stageFreeBytes,
        requiredBytes: blockingPackage
          ? AX3000T_MANAGED_PACKAGE_CATALOG[blockingPackage].downloadSizeBytes +
            stageMarginBytes
          : requiredStageBytes,
        reclaimBytes: 0,
        unexpectedDependents: [],
        message: buildManagedStorageMessage(
          "stage",
          blockingPackage,
          stageFreeBytes,
          blockingPackage
            ? AX3000T_MANAGED_PACKAGE_CATALOG[blockingPackage].downloadSizeBytes +
                stageMarginBytes
            : requiredStageBytes,
          0,
          [],
        ),
      },
    };
  }

  const passwallAppState = normalizedStates["luci-app-passwall2"];
  const replacementPackages = AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER.filter(
    (name) => {
      if (name === "luci-app-passwall2") {
        return false;
      }
      const state = normalizedStates[name];
      const target = AX3000T_MANAGED_PACKAGE_CATALOG[name];
      return state.installed && state.version !== target.targetVersion;
    },
  );

  const dependencyFailure = replacementPackages.find((name) => {
    const unexpectedDependents = normalizedStates[name].unexpectedDependents.filter(
      (dependent) => !AX3000T_INTERNAL_DEPENDENT_NAMES.has(dependent),
    );
    return unexpectedDependents.length > 0;
  });

  if (dependencyFailure) {
    const unexpectedDependents =
      normalizedStates[dependencyFailure].unexpectedDependents.filter(
        (dependent) => !AX3000T_INTERNAL_DEPENDENT_NAMES.has(dependent),
      );
    return {
      classification,
      passwallAppRemovalRequired: false,
      requiredStageBytes,
      steps,
      storageCheck: {
        ok: false,
        reason: "unexpected_dependents",
        blockingPackageName: dependencyFailure,
        availableBytes: overlayFreeBytes,
        requiredBytes:
          AX3000T_MANAGED_PACKAGE_CATALOG[dependencyFailure].installedSizeBytes,
        reclaimBytes: normalizedStates[dependencyFailure].installedSizeBytes,
        unexpectedDependents,
        message: buildManagedStorageMessage(
          "unexpected_dependents",
          dependencyFailure,
          overlayFreeBytes,
          AX3000T_MANAGED_PACKAGE_CATALOG[dependencyFailure].installedSizeBytes,
          normalizedStates[dependencyFailure].installedSizeBytes,
          unexpectedDependents,
        ),
      },
    };
  }

  const passwallAppNeedsRefresh =
    passwallAppState.installed &&
    passwallAppState.version !==
      AX3000T_MANAGED_PACKAGE_CATALOG["luci-app-passwall2"].targetVersion;
  const passwallAppRemovalRequired =
    passwallAppNeedsRefresh || replacementPackages.length > 0;

  let simulatedOverlayFreeBytes = overlayFreeBytes;

  if (passwallAppRemovalRequired && passwallAppState.installed) {
    simulatedOverlayFreeBytes += passwallAppState.installedSizeBytes;
  }

  for (const packageName of AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER) {
    const currentState = normalizedStates[packageName];
    const target = AX3000T_MANAGED_PACKAGE_CATALOG[packageName];

    if (
      packageName === "luci-app-passwall2" &&
      passwallAppRemovalRequired &&
      passwallAppState.installed
    ) {
      steps.push({
        packageName,
        stage: "managed-passwall-finalize",
        action: "reinstall",
        currentVersion: currentState.version,
        targetVersion: target.targetVersion,
        currentInstalledSizeBytes: currentState.installedSizeBytes,
        targetInstalledSizeBytes: target.installedSizeBytes,
      });
      continue;
    }

    if (currentState.installed && currentState.version === target.targetVersion) {
      steps.push({
        packageName,
        stage:
          packageName === "luci-app-passwall2"
            ? "managed-passwall-finalize"
            : "managed-passwall",
        action: "skip",
        currentVersion: currentState.version,
        targetVersion: target.targetVersion,
        currentInstalledSizeBytes: currentState.installedSizeBytes,
        targetInstalledSizeBytes: target.installedSizeBytes,
      });
      continue;
    }

    const reclaimBytes = currentState.installed
      ? currentState.installedSizeBytes
      : 0;
    const availableBytes = simulatedOverlayFreeBytes;
    const requiredBytes = target.installedSizeBytes;

    if (availableBytes + reclaimBytes < requiredBytes) {
      return {
        classification,
        passwallAppRemovalRequired,
        requiredStageBytes,
        steps,
        storageCheck: {
          ok: false,
          reason: "overlay",
          blockingPackageName: packageName,
          availableBytes,
          requiredBytes,
          reclaimBytes,
          unexpectedDependents: [],
          message: buildManagedStorageMessage(
            "overlay",
            packageName,
            availableBytes,
            requiredBytes,
            reclaimBytes,
            [],
          ),
        },
      };
    }

    simulatedOverlayFreeBytes = availableBytes + reclaimBytes - requiredBytes;
    steps.push({
      packageName,
      stage:
        packageName === "luci-app-passwall2"
          ? "managed-passwall-finalize"
          : "managed-passwall",
      action: currentState.installed ? "replace" : "install",
      currentVersion: currentState.version,
      targetVersion: target.targetVersion,
      currentInstalledSizeBytes: currentState.installedSizeBytes,
      targetInstalledSizeBytes: target.installedSizeBytes,
    });
  }

  return {
    classification,
    passwallAppRemovalRequired,
    requiredStageBytes,
    steps,
    storageCheck: {
      ok: true,
      reason: null,
      blockingPackageName: null,
      availableBytes: simulatedOverlayFreeBytes,
      requiredBytes: 0,
      reclaimBytes: 0,
      unexpectedDependents: [],
      message: buildManagedStorageMessage(null, null, simulatedOverlayFreeBytes, 0, 0, []),
    },
  };
}

export const AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES = [
  "libc",
  "coreutils",
  "coreutils-base64",
  "coreutils-nohup",
  "curl",
  "ip-full",
  "libuci-lua",
  "lua",
  "luci-compat",
  "luci-lib-jsonc",
  "resolveip",
  "unzip",
  "luci-lua-runtime",
] as const;

export const AX3000T_REQUIRED_OPENWRT_RUNTIME_PACKAGES = [
  "dnsmasq-full",
  "kmod-nft-socket",
  "kmod-nft-tproxy",
] as const;

export const AX3000T_OPTIONAL_OPENWRT_RUNTIME_PACKAGES = [
  "kmod-nft-nat",
] as const;

export const AX3000T_PASSWALL_MIRROR_MANIFEST: PasswallMirrorManifest = {
  tag: DEFAULT_PASSWALL2_RELEASE_TAG,
  arch: "aarch64_cortex-a53",
  requiredPackages: AX3000T_REQUIRED_MIRRORED_PACKAGES,
  optionalPackages: AX3000T_OPTIONAL_MIRRORED_PACKAGES,
  sourceUrls: {
    release: `https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/tag/${DEFAULT_PASSWALL2_RELEASE_TAG}`,
    luciAppPackage: `${DEFAULT_PASSWALL2_RELEASE_URL}/${AX3000T_PASSWALL_PACKAGE_FILES["luci-app-passwall2"]}`,
    packageBundle: `${DEFAULT_PASSWALL2_RELEASE_URL}/passwall_packages_ipk_aarch64_cortex-a53.zip`,
  },
};

export const AX3000T_SOURCE_SHUNT_REMARK = "Маршрутизатор BloopCat";

export const AX3000T_SHUNT_REBIND_TARGETS = [
  {
    slot: "WorldProxy",
    remark: "🇩🇪⚡Германия YouTube 🚫Ad🚫",
  },
  {
    slot: "YouTube",
    remark: "🇷🇺⚡Россия YouTube 🚫Ad🚫",
  },
  {
    slot: "Special",
    remark: "🇫🇮 ⚡⚡ Финляндия Xhttp Gaming",
  },
  {
    slot: "Tiktok",
    remark: "🇧🇾 Беларусь",
  },
] as const satisfies readonly Ax3000tShuntRebindTarget[];

export const ax3000tEnrollmentPreset = {
  architecture: AX3000T_PASSWALL_MIRROR_MANIFEST.arch,
  selectedNodeId: "myshunt",
  shuntRuleCount: 5,
  sourceShuntRemark: AX3000T_SOURCE_SHUNT_REMARK,
  sourceShuntTargets: AX3000T_SHUNT_REBIND_TARGETS,
  removedSubscriptions: 1,
  removedProxyNodes: 14,
  passwallReleaseTag: DEFAULT_PASSWALL2_RELEASE_TAG,
  passwallAppPackage: "luci-app-passwall2",
  requiredMirroredPackages: AX3000T_REQUIRED_MIRRORED_PACKAGES.map(
    ({ name }) => name,
  ),
  optionalMirroredPackages: AX3000T_OPTIONAL_MIRRORED_PACKAGES.map(
    ({ name }) => name,
  ),
  openWrtFeedProvidedDependencies: [
    ...AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES,
  ],
  requiredOpenWrtPackages: [...AX3000T_REQUIRED_OPENWRT_RUNTIME_PACKAGES],
  optionalOpenWrtPackages: [...AX3000T_OPTIONAL_OPENWRT_RUNTIME_PACKAGES],
  controllerPackages: [
    "vectra-controller-agent",
    "luci-app-vectra-controller",
  ],
} as const;

function withTrailingSlash(value: string | undefined, fallback: string) {
  const resolved = value ?? fallback;
  return resolved.endsWith("/") ? resolved : `${resolved}/`;
}

function buildMirroredPackageUrls(
  artifactBase: string | undefined,
): Record<Ax3000tMirroredPackageName, string> {
  const mirrorUrl = buildAx3000tPasswallMirrorUrl(artifactBase);
  return Object.fromEntries(
    Object.entries(AX3000T_PASSWALL_PACKAGE_FILES).map(([pkg, filename]) => [
      pkg,
      new URL(filename, mirrorUrl).toString(),
    ]),
  ) as Record<Ax3000tMirroredPackageName, string>;
}

function formatShellArguments(values: readonly string[]) {
  return values.join(" ");
}

function buildManagedPackageShellFieldCases(
  passwallPackageUrls: Record<Ax3000tMirroredPackageName, string>,
) {
  return AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES.flatMap((name) => {
    const pkg = AX3000T_MANAGED_PACKAGE_CATALOG[name];
    return [
      `    ${name})`,
      `      case "$field" in`,
      `        version) printf '%s\\n' '${pkg.targetVersion}' ;;`,
      `        download_size_bytes) printf '%s\\n' '${pkg.downloadSizeBytes}' ;;`,
      `        installed_size_bytes) printf '%s\\n' '${pkg.installedSizeBytes}' ;;`,
      `        url) printf '%s\\n' '${passwallPackageUrls[name]}' ;;`,
      "        *) return 1 ;;",
      "      esac",
      "      ;;",
    ];
  });
}

export function resolveAbsoluteUrl(baseUrl: string | undefined, path: string) {
  return new URL(path, withTrailingSlash(baseUrl, DEFAULT_CONTROL_DOMAIN)).toString();
}

export function buildAx3000tFeedUrl(artifactBase: string | undefined) {
  return new URL(
    `openwrt/stable/${ax3000tEnrollmentPreset.architecture}/`,
    withTrailingSlash(artifactBase, DEFAULT_ARTIFACT_BASE_URL),
  ).toString();
}

export function buildAx3000tPasswallMirrorUrl(artifactBase: string | undefined) {
  return new URL(
    `bootstrap/passwall2/${ax3000tEnrollmentPreset.passwallReleaseTag}/${ax3000tEnrollmentPreset.architecture}/`,
    withTrailingSlash(artifactBase, DEFAULT_ARTIFACT_BASE_URL),
  ).toString();
}

export function buildAx3000tBaselineUrl(controlDomain: string | undefined) {
  return resolveAbsoluteUrl(controlDomain, AX3000T_BASELINE_PATH);
}

export function buildAx3000tBootstrapScriptUrl(controlDomain: string | undefined) {
  return resolveAbsoluteUrl(controlDomain, AX3000T_BOOTSTRAP_PATH);
}

export function buildAx3000tShuntRebindScriptUrl(
  controlDomain: string | undefined,
) {
  return resolveAbsoluteUrl(controlDomain, AX3000T_SHUNT_REBIND_PATH);
}

export function buildAx3000tBootstrapCommand(controlDomain: string | undefined) {
  const scriptUrl = buildAx3000tBootstrapScriptUrl(controlDomain);
  return `wget -O /tmp/vectra-ax3000t-bootstrap.sh '${scriptUrl}' && sh /tmp/vectra-ax3000t-bootstrap.sh`;
}

export function buildAx3000tShuntRebindCommand(
  controlDomain: string | undefined,
) {
  const scriptUrl = buildAx3000tShuntRebindScriptUrl(controlDomain);
  return `wget -O /tmp/vectra-ax3000t-myshunt-rebind.sh '${scriptUrl}' && sh /tmp/vectra-ax3000t-myshunt-rebind.sh`;
}

export function buildAx3000tShuntRebindScript(args?: {
  controlDomain?: string;
}) {
  const controlDomain = args?.controlDomain ?? DEFAULT_CONTROL_DOMAIN;
  const applySlotCalls = AX3000T_SHUNT_REBIND_TARGETS.map(
    ({ slot, remark }) => `apply_slot '${slot}' '${remark}'`,
  );

  return [
    "#!/bin/sh",
    "set -eu",
    "",
    "SHUNT_ID='myshunt'",
    `PANEL_URL='${controlDomain}'`,
    "",
    "log() {",
    "  printf '%s\\n' \"$*\"",
    "}",
    "",
    "require_cmd() {",
    "  command -v \"$1\" >/dev/null 2>&1 || {",
    "    log \"Не найдена команда: $1\"",
    "    exit 1",
    "  }",
    "}",
    "",
    "require_cmd uci",
    "require_cmd awk",
    "",
    "if [ \"$(uci -q get \"passwall2.$SHUNT_ID.protocol\" || true)\" != '_shunt' ]; then",
    "  log \"Не найден shunt-узел $SHUNT_ID в /etc/config/passwall2\"",
    "  exit 1",
    "fi",
    "",
    "normalize_remark() {",
    "  printf '%s\\n' \"$1\" | awk '{$1=$1; print}'",
    "}",
    "",
    "find_node_by_remark() {",
    "  wanted=\"$1\"",
    "  wanted_normalized=\"$(normalize_remark \"$wanted\")\"",
    "  for id in $(uci show passwall2 2>/dev/null | awk -F'[.=]' '/=nodes$/{print $2}'); do",
    "    [ \"$id\" = \"$SHUNT_ID\" ] && continue",
    "    protocol=\"$(uci -q get \"passwall2.$id.protocol\" || true)\"",
    "    [ \"$protocol\" = '_shunt' ] && continue",
    "    remark=\"$(uci -q get \"passwall2.$id.remarks\" || true)\"",
    "    remark_normalized=\"$(normalize_remark \"$remark\")\"",
    "    if [ \"$remark_normalized\" = \"$wanted_normalized\" ]; then",
    "      printf '%s\\n' \"$id\"",
    "      return 0",
    "    fi",
    "  done",
    "  return 1",
    "}",
    "",
    "changes_made='0'",
    "missing='0'",
    "",
    "apply_slot() {",
    "  slot=\"$1\"",
    "  wanted=\"$2\"",
    "  node_id=\"$(find_node_by_remark \"$wanted\" || true)\"",
    "  if [ -n \"$node_id\" ]; then",
    "    uci set \"passwall2.$SHUNT_ID.$slot=$node_id\"",
    "    changes_made='1'",
    "    log \"[$slot] восстановлен по remark: $wanted -> $node_id\"",
    "  else",
    "    missing=$((missing + 1))",
    "    log \"[$slot] не найден. Ожидался remark: $wanted\"",
    "  fi",
    "}",
    "",
    "log 'Ищу ваши реальные PassWall-ноды и восстанавливаю привязки myshunt...' ",
    ...applySlotCalls,
    "",
    "if [ \"$changes_made\" = '1' ]; then",
    "  uci commit passwall2",
    "  if [ -x /etc/init.d/passwall2 ]; then",
    "    /etc/init.d/passwall2 restart >/dev/null 2>&1 || /etc/init.d/passwall2 start >/dev/null 2>&1 || true",
    "  fi",
    "  log 'Привязки myshunt обновлены и PassWall2 перезапущен.'",
    "else",
    "  log 'Ни один slot не обновлён. Возможно, подписка ещё не импортирована или remarks отличаются.'",
    "fi",
    "",
    "if [ \"$missing\" -gt 0 ]; then",
    "  log \"Если у ваших нод другие remarks, откройте $PANEL_URL и привяжите slots вручную.\"",
    "fi",
  ].join("\n");
}

export function buildAx3000tBootstrapScript(args: {
  controlDomain?: string;
  routerApiBase?: string;
  artifactBase?: string;
}) {
  const controlDomain = args.controlDomain ?? DEFAULT_CONTROL_DOMAIN;
  const routerApiBase = args.routerApiBase ?? DEFAULT_ROUTER_API_BASE_URL;
  const baselineUrl = buildAx3000tBaselineUrl(controlDomain);
  const feedUrl = buildAx3000tFeedUrl(args.artifactBase);
  const passwallMirrorUrl = buildAx3000tPasswallMirrorUrl(args.artifactBase);
  const passwallPackageUrls = buildMirroredPackageUrls(args.artifactBase);
  const requiredMirrorPreflight = AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES.map(
    (pkg) => `  require_remote_ipk ${pkg} '${passwallPackageUrls[pkg]}'`,
  );
  const managedPackageFieldCases =
    buildManagedPackageShellFieldCases(passwallPackageUrls);
  const requiredManagedPackages = formatShellArguments(
    AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER,
  );
  const allowedInternalDependents = formatShellArguments([
    ...AX3000T_REQUIRED_MIRRORED_PACKAGE_NAMES,
    ...AX3000T_OPTIONAL_MIRRORED_PACKAGE_NAMES,
  ]);
  const requiredRuntimePackages = formatShellArguments(
    AX3000T_REQUIRED_OPENWRT_RUNTIME_PACKAGES,
  );
  const requiredRuntimePackagesWithoutDnsmasq = formatShellArguments(
    AX3000T_REQUIRED_OPENWRT_RUNTIME_PACKAGES.filter(
      (pkg) => pkg !== "dnsmasq-full",
    ),
  );
  const optionalRuntimePackages = formatShellArguments(
    AX3000T_OPTIONAL_OPENWRT_RUNTIME_PACKAGES,
  );
  const openWrtFeedPrereqs = formatShellArguments(
    AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES,
  );
  const controllerPackages = formatShellArguments(
    ax3000tEnrollmentPreset.controllerPackages,
  );

  return [
    "#!/bin/sh",
    "set -eu",
    "",
    `EXPECTED_ARCH='${ax3000tEnrollmentPreset.architecture}'`,
    `PANEL_URL='${controlDomain}'`,
    `CONTROL_URL='${routerApiBase}'`,
    `FEED_URL='${feedUrl}'`,
    `BASELINE_URL='${baselineUrl}'`,
    `PASSWALL_RELEASE_TAG='${ax3000tEnrollmentPreset.passwallReleaseTag}'`,
    `PASSWALL_MIRROR_URL='${passwallMirrorUrl}'`,
    "STAGE_MARGIN_BYTES='1048576'",
    "OVERLAY_PATH='/overlay'",
    "WORKDIR='/tmp/vectra-bootstrap-work'",
    "BACKUP_ROOT='/root/vectra-bootstrap-backup'",
    "OPKG_TMP_DIR=\"$WORKDIR/opkg-tmp\"",
    `REQUIRED_MANAGED_PACKAGES='${requiredManagedPackages}'`,
    `ALLOWED_INTERNAL_DEPENDENTS='${allowedInternalDependents}'`,
    `REQUIRED_RUNTIME_PACKAGES='${requiredRuntimePackages}'`,
    `OPTIONAL_RUNTIME_PACKAGES='${optionalRuntimePackages}'`,
    `CONTROLLER_PACKAGES='${controllerPackages}'`,
    `OPENWRT_FEED_PREREQS='${openWrtFeedPrereqs}'`,
    "",
    "log() {",
    "  printf '%s\\n' \"$*\"",
    "}",
    "",
    "bytes_or_zero() {",
    "  value=\"$1\"",
    "  case \"$value\" in",
    "    ''|*[!0-9]*) printf '0\\n' ;;",
    "    *) printf '%s\\n' \"$value\" ;;",
    "  esac",
    "}",
    "",
    "run_opkg() {",
    "  opkg -t \"$OPKG_TMP_DIR\" \"$@\"",
    "}",
    "",
    "require_cmd() {",
    "  command -v \"$1\" >/dev/null 2>&1 || {",
    "    log \"Не найдена команда: $1\"",
    "    exit 1",
    "  }",
    "}",
    "",
    "require_cmd wget",
    "require_cmd opkg",
    "require_cmd opkg-key",
    "require_cmd uci",
    "",
    "ARCH=\"$(awk -F\"'\" '/^DISTRIB_ARCH=/{print $2; exit}' /etc/openwrt_release 2>/dev/null || true)\"",
    "",
    "CURRENT_ARCH='unknown'",
    "if [ -n \"$ARCH\" ]; then",
    "  CURRENT_ARCH=\"$ARCH\"",
    "fi",
    "",
    "if [ \"$ARCH\" != \"$EXPECTED_ARCH\" ]; then",
    "  log \"Скрипт подготовлен для $EXPECTED_ARCH. Текущая архитектура: $CURRENT_ARCH\"",
    "  exit 1",
    "fi",
    "",
    "mkdir -p \"$WORKDIR\" \"$BACKUP_ROOT\" \"$OPKG_TMP_DIR\"",
    "cleanup() {",
    "  rm -rf \"$WORKDIR\"",
    "}",
    "trap cleanup EXIT INT TERM",
    "",
    "STAMP=\"$(date +%Y%m%d-%H%M%S 2>/dev/null || echo now)\"",
    "BACKUP_DIR=\"$BACKUP_ROOT/$STAMP\"",
    "mkdir -p \"$BACKUP_DIR\"",
    "[ -f /etc/config/passwall2 ] && cp /etc/config/passwall2 \"$BACKUP_DIR/passwall2\" || true",
    "[ -f /etc/config/passwall2_server ] && cp /etc/config/passwall2_server \"$BACKUP_DIR/passwall2_server\" || true",
    "[ -f /etc/config/vectra-controller ] && cp /etc/config/vectra-controller \"$BACKUP_DIR/vectra-controller\" || true",
    "[ -f /etc/config/dhcp ] && cp /etc/config/dhcp \"$BACKUP_DIR/dhcp\" || true",
    "[ -f /etc/opkg/customfeeds.conf ] && cp /etc/opkg/customfeeds.conf \"$BACKUP_DIR/customfeeds.conf\" || true",
    "log \"Бэкап сохранён в $BACKUP_DIR\"",
    "",
    "wget -qO \"$WORKDIR/vectra.pub\" \"$FEED_URL/vectra.pub\"",
    "opkg-key add \"$WORKDIR/vectra.pub\" >/dev/null 2>&1 || true",
    "",
    "if [ -f /etc/opkg/customfeeds.conf ]; then",
    "  grep -v '/artifacts/openwrt/' /etc/opkg/customfeeds.conf > \"$WORKDIR/customfeeds.conf\" || true",
    "else",
    "  : > \"$WORKDIR/customfeeds.conf\"",
    "fi",
    "printf '%s\\n' \"src/gz vectra $FEED_URL\" >> \"$WORKDIR/customfeeds.conf\"",
    "cat \"$WORKDIR/customfeeds.conf\" > /etc/opkg/customfeeds.conf",
    "",
    "run_opkg update",
    "",
    "package_status_field() {",
    "  pkg=\"$1\"",
    "  field=\"$2\"",
    "  opkg status \"$pkg\" 2>/dev/null | awk -F': ' -v field=\"$field\" '$1 == field { print $2; exit }'",
    "}",
    "",
    "package_installed() {",
    "  status=\"$(package_status_field \"$1\" Status || true)\"",
    "  printf '%s\\n' \"$status\" | grep -Eq '(^| )installed($| )'",
    "}",
    "",
    "package_version() {",
    "  package_status_field \"$1\" Version || true",
    "}",
    "",
    "package_installed_size_bytes() {",
    "  bytes_or_zero \"$(package_status_field \"$1\" Installed-Size || true)\"",
    "}",
    "",
    "package_available() {",
    "  run_opkg list \"$1\" 2>/dev/null | grep -q \"^$1 - \"",
    "}",
    "",
    "get_free_bytes() {",
    "  path=\"$1\"",
    "  bytes=\"$(df -kP \"$path\" 2>/dev/null | awk 'NR == 2 { print $4 * 1024; exit }')\"",
    "  bytes_or_zero \"$bytes\"",
    "}",
    "",
    "download_file() {",
    "  url=\"$1\"",
    "  destination=\"$2\"",
    "  wget -qO \"$destination\" \"$url\" || {",
    "    log \"Не удалось скачать $url\"",
    "    exit 1",
    "  }",
    "}",
    "",
    "package_index_field() {",
    "  pkg=\"$1\"",
    "  field=\"$2\"",
    "  awk -F': ' -v pkg=\"$pkg\" -v field=\"$field\" '",
    "    $1 == \"Package\" {",
    "      if (match_pkg && value != \"\") {",
    "        print value",
    "        exit",
    "      }",
    "      match_pkg = ($2 == pkg)",
    "      value = \"\"",
    "      next",
    "    }",
    "    match_pkg && $1 == field && value == \"\" {",
    "      value = $2",
    "    }",
    "    END {",
    "      if (value != \"\") {",
    "        print value",
    "      }",
    "    }",
    "  ' \"$PACKAGES_FILE\"",
    "}",
    "",
    "feed_package_field() {",
    "  pkg=\"$1\"",
    "  field=\"$2\"",
    "  run_opkg info \"$pkg\" 2>/dev/null | awk -F': ' -v pkg=\"$pkg\" -v field=\"$field\" '",
    "    $1 == \"Package\" {",
    "      if (match_pkg && value != \"\") {",
    "        print value",
    "        exit",
    "      }",
    "      match_pkg = ($2 == pkg)",
    "      value = \"\"",
    "      next",
    "    }",
    "    match_pkg && $1 == field && value == \"\" {",
    "      value = $2",
    "    }",
    "    END {",
    "      if (value != \"\") {",
    "        print value",
    "      }",
    "    }",
    "  '",
    "}",
    "",
    "vectra_package_field() {",
    "  package_index_field \"$1\" \"$2\"",
    "}",
    "",
    "managed_package_field() {",
    "  pkg=\"$1\"",
    "  field=\"$2\"",
    "  case \"$pkg\" in",
    ...managedPackageFieldCases,
    "    *) return 1 ;;",
    "  esac",
    "}",
    "",
    "managed_package_at_target() {",
    "  package_installed \"$1\" && [ \"$(package_version \"$1\")\" = \"$(managed_package_field \"$1\" version)\" ]",
    "}",
    "",
    "managed_package_needs_replacement() {",
    "  package_installed \"$1\" && [ \"$(package_version \"$1\")\" != \"$(managed_package_field \"$1\" version)\" ]",
    "}",
    "",
    "runtime_package_needs_install() {",
    "  pkg=\"$1\"",
    "  case \"$pkg\" in",
    "    dnsmasq-full)",
    "      ! package_installed dnsmasq-full",
    "      ;;",
    "    *)",
    "      ! package_installed \"$pkg\"",
    "      ;;",
    "  esac",
    "}",
    "",
    "PACKAGES_FILE=\"$WORKDIR/Packages\"",
    "download_file \"$FEED_URL/Packages\" \"$PACKAGES_FILE\"",
    "",
    "vectra_package_version() {",
    "  vectra_package_field \"$1\" Version",
    "}",
    "",
    "vectra_package_installed_size_bytes() {",
    "  bytes_or_zero \"$(vectra_package_field \"$1\" Installed-Size)\"",
    "}",
    "",
    "vectra_package_download_size_bytes() {",
    "  bytes_or_zero \"$(vectra_package_field \"$1\" Size)\"",
    "}",
    "",
    "controller_package_at_target() {",
    "  pkg=\"$1\"",
    "  target_version=\"$(vectra_package_version \"$pkg\")\"",
    "  package_installed \"$pkg\" && [ \"$(package_version \"$pkg\")\" = \"$target_version\" ]",
    "}",
    "",
    "list_unexpected_dependents() {",
    "  pkg=\"$1\"",
    "  opkg whatdepends \"$pkg\" 2>/dev/null |",
    "    awk '/^[[:alnum:]_.+-]+[[:space:]]/ { print $1 }' |",
    "    awk '!seen[$0]++' |",
    "    while IFS= read -r dependent; do",
    "      [ -n \"$dependent\" ] || continue",
    "      [ \"$dependent\" = \"$pkg\" ] && continue",
    "      case \" $ALLOWED_INTERNAL_DEPENDENTS \" in",
    "        *\" $dependent \"*) ;;",
    "        *) printf '%s\\n' \"$dependent\" ;;",
    "      esac",
    "    done",
    "}",
    "",
    "package_depended_on_by_passwall_app() {",
    "  pkg=\"$1\"",
    "  opkg whatdepends \"$pkg\" 2>/dev/null | awk '/^[[:alnum:]_.+-]+[[:space:]]/ { print $1 }' | grep -qx 'luci-app-passwall2'",
    "}",
    "",
    "install_optional_openwrt_packages() {",
    "  for pkg in \"$@\"; do",
    "    if ! package_available \"$pkg\"; then",
    "      continue",
    "    fi",
    "    if package_installed \"$pkg\"; then",
    "      continue",
    "    fi",
    "    run_opkg install \"$pkg\" || true",
    "  done",
    "}",
    "",
    "install_dnsmasq_full() {",
    "  if package_installed dnsmasq-full; then",
    "    rm -f /etc/config/dhcp-opkg",
    "    log '[dnsmasq-full] уже установлен; пропускаю.'",
    "    return 0",
    "  fi",
    "  dhcp_backup=''",
    "  if [ -f /etc/config/dhcp ]; then",
    "    dhcp_backup=\"$WORKDIR/dhcp.before-dnsmasq-full\"",
    "    cp /etc/config/dhcp \"$dhcp_backup\"",
    "  fi",
    "  if package_installed dnsmasq; then",
    "    log 'Заменяю штатный dnsmasq на dnsmasq-full для PassWall2...'",
    "    run_opkg remove dnsmasq || {",
    "      log 'Не удалось удалить штатный dnsmasq перед установкой dnsmasq-full'",
    "      exit 1",
    "    }",
    "  fi",
    "  rm -f /etc/config/dhcp /etc/config/dhcp-opkg",
    "  run_opkg install dnsmasq-full || {",
    "    log 'Не удалось установить dnsmasq-full'",
    "    exit 1",
    "  }",
    "  if [ -n \"$dhcp_backup\" ] && [ -f \"$dhcp_backup\" ]; then",
    "    cp \"$dhcp_backup\" /etc/config/dhcp",
    "  fi",
    "  rm -f /etc/config/dhcp-opkg",
    "  /etc/init.d/dnsmasq restart >/dev/null 2>&1 || true",
    "}",
    "",
    "require_feed_package() {",
    "  pkg=\"$1\"",
    "  package_available \"$pkg\" || {",
    "    log \"В доступных OpenWrt feeds не найден обязательный пакет: $pkg\"",
    "    exit 1",
    "  }",
    "  installed_size=\"$(bytes_or_zero \"$(feed_package_field \"$pkg\" Installed-Size)\")\"",
    "  download_size=\"$(bytes_or_zero \"$(feed_package_field \"$pkg\" Size)\")\"",
    "  [ \"$installed_size\" -gt 0 ] || {",
    "    log \"Не удалось определить Installed-Size для OpenWrt пакета $pkg. Storage-aware preflight остановлен.\"",
    "    exit 1",
    "  }",
    "  [ \"$download_size\" -gt 0 ] || {",
    "    log \"Не удалось определить Size для OpenWrt пакета $pkg. Storage-aware preflight остановлен.\"",
    "    exit 1",
    "  }",
    "}",
    "",
    "require_feed_packages() {",
    "  for pkg in \"$@\"; do",
    "    require_feed_package \"$pkg\"",
    "  done",
    "}",
    "",
    "require_remote_ipk() {",
    "  pkg=\"$1\"",
    "  url=\"$2\"",
    "  wget -q --spider \"$url\" || {",
    "    log \"В зеркале PassWall2 не найден обязательный пакет: $pkg ($url)\"",
    "    exit 1",
    "  }",
    "}",
    "",
    "require_vectra_package() {",
    "  pkg=\"$1\"",
    "  version=\"$(vectra_package_version \"$pkg\" || true)\"",
    "  installed_size=\"$(vectra_package_installed_size_bytes \"$pkg\")\"",
    "  download_size=\"$(vectra_package_download_size_bytes \"$pkg\")\"",
    "  [ -n \"$version\" ] || {",
    "    log \"Не удалось найти $pkg в подписанном feed Vectra\"",
    "    exit 1",
    "  }",
    "  [ \"$installed_size\" -gt 0 ] || {",
    "    log \"Не удалось определить Installed-Size для пакета Vectra $pkg. Storage-aware preflight остановлен.\"",
    "    exit 1",
    "  }",
    "  [ \"$download_size\" -gt 0 ] || {",
    "    log \"Не удалось определить Size для пакета Vectra $pkg. Storage-aware preflight остановлен.\"",
    "    exit 1",
    "  }",
    "}",
    "",
    "detect_bootstrap_classification() {",
    "  installed_required='0'",
    "  outdated_required='0'",
    "  missing_required='0'",
    "  for pkg in $REQUIRED_MANAGED_PACKAGES; do",
    "    if package_installed \"$pkg\"; then",
    "      installed_required=$((installed_required + 1))",
    "      if ! managed_package_at_target \"$pkg\"; then",
    "        outdated_required=$((outdated_required + 1))",
    "      fi",
    "    else",
    "      missing_required=$((missing_required + 1))",
    "    fi",
    "  done",
    "  if [ \"$installed_required\" -eq 0 ]; then",
    "    printf '%s\\n' 'fresh install'",
    "  elif [ \"$outdated_required\" -gt 0 ]; then",
    "    printf '%s\\n' 'upgrade existing PassWall stack'",
    "  elif [ \"$missing_required\" -gt 0 ]; then",
    "    printf '%s\\n' 'repair drifted managed packages'",
    "  else",
    "    printf '%s\\n' 'upgrade existing PassWall stack'",
    "  fi",
    "}",
    "",
    "update_max_stage_bytes() {",
    "  candidate=\"$1\"",
    "  if [ \"$candidate\" -gt \"$MAX_STAGE_REQUIRED_BYTES\" ]; then",
    "    MAX_STAGE_REQUIRED_BYTES=\"$candidate\"",
    "  fi",
    "}",
    "",
    "overlay_fail() {",
    "  pkg=\"$1\"",
    "  available_bytes=\"$2\"",
    "  required_bytes=\"$3\"",
    "  reclaim_bytes=\"$4\"",
    "  log \"Недостаточно места на /overlay для $pkg: доступно ${available_bytes} B, требуется ${required_bytes} B.\"",
    "  if [ \"$reclaim_bytes\" -gt 0 ]; then",
    "    log \"Даже после reclaim $pkg доступно только $((available_bytes + reclaim_bytes)) B.\"",
    "  fi",
    "  log \"Режим bootstrap: $BOOTSTRAP_CLASSIFICATION\"",
    "  log 'Bootstrap прерван до любых изменений.'",
    "  exit 1",
    "}",
    "",
    "simulate_overlay_step() {",
    "  pkg=\"$1\"",
    "  required_bytes=\"$2\"",
    "  reclaim_bytes=\"$3\"",
    "  available_bytes=\"$SIMULATED_OVERLAY_FREE_BYTES\"",
    "  [ \"$required_bytes\" -gt 0 ] || {",
    "    log \"Не удалось определить storage budget для $pkg.\"",
    "    log 'Bootstrap прерван до любых изменений.'",
    "    exit 1",
    "  }",
    "  if [ $((available_bytes + reclaim_bytes)) -lt \"$required_bytes\" ]; then",
    "    overlay_fail \"$pkg\" \"$available_bytes\" \"$required_bytes\" \"$reclaim_bytes\"",
    "  fi",
    "  SIMULATED_OVERLAY_FREE_BYTES=$((available_bytes + reclaim_bytes - required_bytes))",
    "}",
    "",
    "run_preflight_checks() {",
    "  log 'Проверяю prerequisites bootstrap...'",
    ...requiredMirrorPreflight,
    "  require_feed_packages $OPENWRT_FEED_PREREQS",
    "  require_feed_packages $REQUIRED_RUNTIME_PACKAGES",
    `  require_vectra_package ${ax3000tEnrollmentPreset.controllerPackages[0]}`,
    `  require_vectra_package ${ax3000tEnrollmentPreset.controllerPackages[1]}`,
    "  CURRENT_STAGE_FREE_BYTES=\"$(get_free_bytes \"$WORKDIR\")\"",
    "  CURRENT_OVERLAY_FREE_BYTES=\"$(get_free_bytes \"$OVERLAY_PATH\")\"",
    "  BOOTSTRAP_CLASSIFICATION=\"$(detect_bootstrap_classification)\"",
    "  MAX_STAGE_REQUIRED_BYTES='0'",
    "  for pkg in $REQUIRED_MANAGED_PACKAGES; do",
    "    if ! managed_package_at_target \"$pkg\"; then",
    "      download_bytes=\"$(managed_package_field \"$pkg\" download_size_bytes)\"",
    "      update_max_stage_bytes $((download_bytes + STAGE_MARGIN_BYTES))",
    "    fi",
    "  done",
    "  for pkg in $REQUIRED_RUNTIME_PACKAGES; do",
    "    if runtime_package_needs_install \"$pkg\"; then",
    "      download_bytes=\"$(bytes_or_zero \"$(feed_package_field \"$pkg\" Size)\")\"",
    "      update_max_stage_bytes $((download_bytes + STAGE_MARGIN_BYTES))",
    "    fi",
    "  done",
    "  for pkg in $CONTROLLER_PACKAGES; do",
    "    if ! controller_package_at_target \"$pkg\"; then",
    "      download_bytes=\"$(vectra_package_download_size_bytes \"$pkg\")\"",
    "      update_max_stage_bytes $((download_bytes + STAGE_MARGIN_BYTES))",
    "    fi",
    "  done",
    "  if [ \"$CURRENT_STAGE_FREE_BYTES\" -lt \"$MAX_STAGE_REQUIRED_BYTES\" ]; then",
    "    blocking_stage_pkg='unknown'",
    "    for pkg in $REQUIRED_MANAGED_PACKAGES; do",
    "      if ! managed_package_at_target \"$pkg\"; then",
    "        candidate_bytes=$(( $(managed_package_field \"$pkg\" download_size_bytes) + STAGE_MARGIN_BYTES ))",
    "        if [ \"$CURRENT_STAGE_FREE_BYTES\" -lt \"$candidate_bytes\" ]; then",
    "          blocking_stage_pkg=\"$pkg\"",
    "          break",
    "        fi",
    "      fi",
    "    done",
    "    if [ \"$blocking_stage_pkg\" = 'unknown' ]; then",
    "      for pkg in $REQUIRED_RUNTIME_PACKAGES; do",
    "        if runtime_package_needs_install \"$pkg\"; then",
    "          candidate_bytes=$(( $(bytes_or_zero \"$(feed_package_field \"$pkg\" Size)\") + STAGE_MARGIN_BYTES ))",
    "          if [ \"$CURRENT_STAGE_FREE_BYTES\" -lt \"$candidate_bytes\" ]; then",
    "            blocking_stage_pkg=\"$pkg\"",
    "            break",
    "          fi",
    "        fi",
    "      done",
    "    fi",
    "    if [ \"$blocking_stage_pkg\" = 'unknown' ]; then",
    "      for pkg in $CONTROLLER_PACKAGES; do",
    "        if ! controller_package_at_target \"$pkg\"; then",
    "          candidate_bytes=$(( $(vectra_package_download_size_bytes \"$pkg\") + STAGE_MARGIN_BYTES ))",
    "          if [ \"$CURRENT_STAGE_FREE_BYTES\" -lt \"$candidate_bytes\" ]; then",
    "            blocking_stage_pkg=\"$pkg\"",
    "            break",
    "          fi",
    "        fi",
    "      done",
    "    fi",
    "    log \"Недостаточно staging-space в $WORKDIR: доступно ${CURRENT_STAGE_FREE_BYTES} B, требуется не меньше ${MAX_STAGE_REQUIRED_BYTES} B. Блокирующий пакет: $blocking_stage_pkg.\"",
    "    log \"Режим bootstrap: $BOOTSTRAP_CLASSIFICATION\"",
    "    log 'Bootstrap прерван до любых изменений.'",
    "    exit 1",
    "  fi",
    "  PASSWALL_APP_REMOVAL_REQUIRED='0'",
    "  for pkg in $REQUIRED_MANAGED_PACKAGES; do",
    "    [ \"$pkg\" = 'luci-app-passwall2' ] && continue",
    "    if managed_package_needs_replacement \"$pkg\"; then",
    "      unexpected=\"$(list_unexpected_dependents \"$pkg\" | tr '\\n' ' ' | awk '{$1=$1; print}')\"",
    "      if [ -n \"$unexpected\" ]; then",
    "        log \"Автоматический reclaim для $pkg запрещён: внешние зависимости -> $unexpected\"",
    "        log 'Bootstrap прерван до любых изменений.'",
    "        exit 1",
    "      fi",
    "      if package_depended_on_by_passwall_app \"$pkg\"; then",
    "        PASSWALL_APP_REMOVAL_REQUIRED='1'",
    "      fi",
    "    fi",
    "  done",
    "  if package_installed luci-app-passwall2 && ! managed_package_at_target luci-app-passwall2; then",
    "    PASSWALL_APP_REMOVAL_REQUIRED='1'",
    "  fi",
    "  SIMULATED_OVERLAY_FREE_BYTES=\"$CURRENT_OVERLAY_FREE_BYTES\"",
    "  if runtime_package_needs_install dnsmasq-full; then",
    "    dnsmasq_target_bytes=\"$(bytes_or_zero \"$(feed_package_field dnsmasq-full Installed-Size)\")\"",
    "    dnsmasq_reclaim_bytes='0'",
    "    if package_installed dnsmasq; then",
    "      dnsmasq_reclaim_bytes=\"$(package_installed_size_bytes dnsmasq)\"",
    "    fi",
    "    simulate_overlay_step dnsmasq-full \"$dnsmasq_target_bytes\" \"$dnsmasq_reclaim_bytes\"",
    "  fi",
    `  for pkg in ${requiredRuntimePackagesWithoutDnsmasq}; do`,
    "    if runtime_package_needs_install \"$pkg\"; then",
    "      target_bytes=\"$(bytes_or_zero \"$(feed_package_field \"$pkg\" Installed-Size)\")\"",
    "      simulate_overlay_step \"$pkg\" \"$target_bytes\" '0'",
    "    fi",
    "  done",
    "  if [ \"$PASSWALL_APP_REMOVAL_REQUIRED\" = '1' ] && package_installed luci-app-passwall2; then",
    "    SIMULATED_OVERLAY_FREE_BYTES=$((SIMULATED_OVERLAY_FREE_BYTES + $(package_installed_size_bytes luci-app-passwall2)))",
    "  fi",
    "  for pkg in $REQUIRED_MANAGED_PACKAGES; do",
    "    [ \"$pkg\" = 'luci-app-passwall2' ] && continue",
    "    if managed_package_at_target \"$pkg\"; then",
    "      continue",
    "    fi",
    "    reclaim_bytes='0'",
    "    if package_installed \"$pkg\"; then",
    "      reclaim_bytes=\"$(package_installed_size_bytes \"$pkg\")\"",
    "    fi",
    "    target_bytes=\"$(managed_package_field \"$pkg\" installed_size_bytes)\"",
    "    simulate_overlay_step \"$pkg\" \"$target_bytes\" \"$reclaim_bytes\"",
    "  done",
    "  if [ \"$PASSWALL_APP_REMOVAL_REQUIRED\" = '1' ] || ! managed_package_at_target luci-app-passwall2; then",
    "    target_bytes=\"$(managed_package_field luci-app-passwall2 installed_size_bytes)\"",
    "    simulate_overlay_step luci-app-passwall2 \"$target_bytes\" '0'",
    "  fi",
    "  for pkg in $CONTROLLER_PACKAGES; do",
    "    if controller_package_at_target \"$pkg\"; then",
    "      continue",
    "    fi",
    "    reclaim_bytes='0'",
    "    if package_installed \"$pkg\"; then",
    "      reclaim_bytes=\"$(package_installed_size_bytes \"$pkg\")\"",
    "    fi",
    "    target_bytes=\"$(vectra_package_installed_size_bytes \"$pkg\")\"",
    "    simulate_overlay_step \"$pkg\" \"$target_bytes\" \"$reclaim_bytes\"",
    "  done",
    "  log \"Режим bootstrap: $BOOTSTRAP_CLASSIFICATION\"",
    "  log \"Свободно в staging ($WORKDIR): ${CURRENT_STAGE_FREE_BYTES} B\"",
    "  log \"Свободно на /overlay: ${CURRENT_OVERLAY_FREE_BYTES} B\"",
    "  log \"Максимальный staging budget: ${MAX_STAGE_REQUIRED_BYTES} B\"",
    "  if [ \"$PASSWALL_APP_REMOVAL_REQUIRED\" = '1' ]; then",
    "    log 'Для успешного upgrade скрипт временно снимет luci-app-passwall2 и затем поставит его обратно последним шагом.'",
    "  fi",
    "}",
    "",
    "log \"Использую зеркальные пакеты PassWall2 $PASSWALL_RELEASE_TAG из $PASSWALL_MIRROR_URL\"",
    "",
    "run_preflight_checks",
    "",
    "log 'Подготавливаю dnsmasq-full для PassWall2...'",
    "install_dnsmasq_full",
    "",
    "install_required_openwrt_packages() {",
    "  for pkg in \"$@\"; do",
    "    if package_installed \"$pkg\"; then",
    "      log \"[$pkg] уже установлен; пропускаю.\"",
    "      continue",
    "    fi",
    "    log \"[$pkg] устанавливаю из OpenWrt feed...\"",
    "    run_opkg install \"$pkg\" || {",
    "      log \"Не удалось установить обязательный OpenWrt пакет $pkg\"",
    "      exit 1",
    "    }",
    "  done",
    "}",
    "",
    "log 'Устанавливаю обязательные OpenWrt runtime-пакеты для PassWall2...'",
    `install_required_openwrt_packages ${requiredRuntimePackagesWithoutDnsmasq}`,
    "",
    "download_and_install_managed_ipk() {",
    "  pkg=\"$1\"",
    "  url=\"$(managed_package_field \"$pkg\" url)\"",
    "  destination=\"$WORKDIR/$pkg.ipk\"",
    "  download_file \"$url\" \"$destination\"",
    "  run_opkg install \"$destination\" || {",
    "    rm -f \"$destination\"",
    "    log \"Не удалось установить $pkg из $url\"",
    "    exit 1",
    "  }",
    "  rm -f \"$destination\"",
    "}",
    "",
    "append_preserved_passwall_sections() {",
    "  source=\"$1\"",
    "  destination=\"$2\"",
    "  [ -f \"$source\" ] || return 0",
    "  awk -v allowed_types='nodes subscribe_list' -f - \"$source\" >> \"$destination\" <<'AWK'",
    "function is_allowed(type, list, idx, count) {",
    "  count = split(allowed_types, list, \" \")",
    "  for (idx = 1; idx <= count; idx++) {",
    "    if (list[idx] == type) {",
    "      return 1",
    "    }",
    "  }",
    "  return 0",
    "}",
    "function flush_block() {",
    "  if (capture && block != \"\") {",
    "    printf \"\\n%s\", block",
    "  }",
    "  block = \"\"",
    "}",
    "/^config[ \\t]+/ {",
    "  flush_block()",
    "  block = $0 \"\\n\"",
    "  section_type = $2",
    "  section_name = \"\"",
    "  if (match($0, /'[^']+'/)) {",
    "    section_name = substr($0, RSTART + 1, RLENGTH - 2)",
    "  }",
    "  capture = is_allowed(section_type) && !(section_type == \"nodes\" && section_name == \"myshunt\")",
    "  next",
    "}",
    "{",
    "  if (block != \"\") {",
    "    block = block $0 \"\\n\"",
    "  }",
    "}",
    "END {",
    "  flush_block()",
    "}",
    "AWK",
    "}",
    "",
    "apply_passwall_baseline() {",
    "  mkdir -p \"$WORKDIR/uci\"",
    "  wget -qO \"$WORKDIR/uci/passwall2\" \"$BASELINE_URL\"",
    "  if ! uci -c \"$WORKDIR/uci\" show passwall2 >/dev/null 2>\"$WORKDIR/baseline-validate.stderr\"; then",
    "    log 'Не удалось проверить baseline PassWall2 перед применением в /etc/config/passwall2'",
    "    [ -s \"$WORKDIR/baseline-validate.stderr\" ] && cat \"$WORKDIR/baseline-validate.stderr\"",
    "    exit 1",
    "  fi",
    "  if [ -f \"$BACKUP_DIR/passwall2\" ]; then",
    "    log 'Сохраняю существующие подписки и ноды PassWall2 из текущего конфига...'",
    "    append_preserved_passwall_sections \"$BACKUP_DIR/passwall2\" \"$WORKDIR/uci/passwall2\"",
    "    if ! uci -c \"$WORKDIR/uci\" show passwall2 >/dev/null 2>\"$WORKDIR/preserved-passwall-validate.stderr\"; then",
    "      log 'Не удалось объединить baseline PassWall2 с существующими подписками и нодами'",
    "      [ -s \"$WORKDIR/preserved-passwall-validate.stderr\" ] && cat \"$WORKDIR/preserved-passwall-validate.stderr\"",
    "      exit 1",
    "    fi",
    "  fi",
    "  cp \"$WORKDIR/uci/passwall2\" /etc/config/passwall2",
    "}",
    "",
    "refresh_passwall_managed_stack() {",
    "  if [ \"$PASSWALL_APP_REMOVAL_REQUIRED\" = '1' ] && package_installed luci-app-passwall2; then",
    "    log 'Освобождаю место: временно снимаю luci-app-passwall2 перед обновлением PassWall stack...'",
    "    if [ -x /etc/init.d/passwall2 ]; then",
    "      /etc/init.d/passwall2 stop >/dev/null 2>&1 || true",
    "    fi",
    "    run_opkg remove luci-app-passwall2 || {",
    "      log 'Не удалось временно снять luci-app-passwall2 перед reclaim-upgrade'",
    "      exit 1",
    "    }",
    "  fi",
    "  for pkg in $REQUIRED_MANAGED_PACKAGES; do",
    "    [ \"$pkg\" = 'luci-app-passwall2' ] && continue",
    "    if managed_package_at_target \"$pkg\"; then",
    "      log \"[$pkg] версия $(managed_package_field \"$pkg\" version) уже установлена; пропускаю.\"",
    "      continue",
    "    fi",
    "    if package_installed \"$pkg\"; then",
    "      log \"[$pkg] снимаю $(package_version \"$pkg\") перед установкой $(managed_package_field \"$pkg\" version)...\"",
    "      run_opkg remove \"$pkg\" || {",
    "        log \"Не удалось снять старую версию $pkg\"",
    "        exit 1",
    "      }",
    "    fi",
    "    log \"[$pkg] устанавливаю $(managed_package_field \"$pkg\" version) из зеркала PassWall2...\"",
    "    download_and_install_managed_ipk \"$pkg\"",
    "  done",
    "  if [ \"$PASSWALL_APP_REMOVAL_REQUIRED\" = '1' ] || ! managed_package_at_target luci-app-passwall2; then",
    "    if package_installed luci-app-passwall2; then",
    "      run_opkg remove luci-app-passwall2 || {",
    "        log 'Не удалось снять старую версию luci-app-passwall2'",
    "        exit 1",
    "      }",
    "    fi",
    "    log \"[luci-app-passwall2] устанавливаю $(managed_package_field luci-app-passwall2 version) последним шагом...\"",
    "    download_and_install_managed_ipk luci-app-passwall2",
    "  else",
    "    log '[luci-app-passwall2] версия уже актуальна; пропускаю.'",
    "  fi",
    "}",
    "",
    "log 'Устанавливаю и обновляю managed stack PassWall2 с учётом реального места на overlay...'",
    "refresh_passwall_managed_stack",
    "",
    "log 'Устанавливаю доступные дополнительные OpenWrt-пакеты...'",
    `install_optional_openwrt_packages ${optionalRuntimePackages}`,
    "",
    "apply_passwall_baseline",
    "",
    "/etc/init.d/passwall2 enable >/dev/null 2>&1 || true",
    "lua /usr/share/passwall2/rule_update.lua log geoip,geosite || true",
    "/etc/init.d/passwall2 running >/dev/null 2>&1 && /etc/init.d/passwall2 restart || /etc/init.d/passwall2 start",
    "",
    "install_controller_packages() {",
    "  for pkg in $CONTROLLER_PACKAGES; do",
    "    target_version=\"$(vectra_package_version \"$pkg\")\"",
    "    if controller_package_at_target \"$pkg\"; then",
    "      log \"[$pkg] версия $target_version уже установлена; пропускаю.\"",
    "      continue",
    "    fi",
    "    if [ \"$pkg\" = 'vectra-controller-agent' ] && [ -x /etc/init.d/vectra-controller ]; then",
    "      /etc/init.d/vectra-controller stop >/dev/null 2>&1 || true",
    "    fi",
    "    if package_installed \"$pkg\"; then",
    "      log \"[$pkg] снимаю $(package_version \"$pkg\") перед установкой $target_version...\"",
    "      run_opkg remove \"$pkg\" || {",
    "        log \"Не удалось снять старую версию $pkg\"",
    "        exit 1",
    "      }",
    "    fi",
    "    log \"[$pkg] устанавливаю $target_version из подписанного feed Vectra...\"",
    "    run_opkg install \"$pkg\" || {",
    "      log \"Не удалось установить пакет Vectra $pkg\"",
    "      exit 1",
    "    }",
    "  done",
    "}",
    "",
    "log 'Устанавливаю пакеты контроллера Vectra по одному...'",
    "install_controller_packages",
    "",
    "uci batch <<'EOF'",
    "set vectra-controller.main.enabled='1'",
    `set vectra-controller.main.control_url='${routerApiBase}'`,
    `set vectra-controller.main.panel_url='${controlDomain}'`,
    "set vectra-controller.main.poll_interval='45s'",
    "set vectra-controller.main.request_timeout='10s'",
    "set vectra-controller.main.state_path='/etc/vectra-controller/state.json'",
    "set vectra-controller.main.status_path='/var/run/vectra-controller/status.json'",
    "set vectra-controller.main.config_render_path='/var/run/vectra-controller/config.json'",
    "commit vectra-controller",
    "EOF",
    "",
    "/etc/init.d/vectra-controller enable >/dev/null 2>&1 || true",
    "/etc/init.d/vectra-controller restart",
    "",
    "log 'Готово.'",
    "log \"1. Локальный baseline PassWall2 применён из $BASELINE_URL\"",
    "log \"2. Контроллер подключён к $CONTROL_URL\"",
    "log \"3. После первого check-in откройте веб-панель и примите импорт как эталон\"",
  ].join("\n");
}
