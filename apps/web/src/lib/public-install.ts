export const INSTALL_HELPER_BASE_URL = "http://127.0.0.1:38471";
export const INSTALL_HELPER_DOWNLOAD_BASE_PATH = "/install-helper";

export const installStageOrder = [
  "helper detected",
  "router found",
  "ssh authenticated",
  "bootstrap downloaded",
  "packages installed",
  "controller running",
  "passwall verified",
  "completed",
] as const;

export type InstallStage = (typeof installStageOrder)[number];

export type InstallEventState = "pending" | "running" | "success" | "failure";

export type HelperFingerprintState = "new" | "trusted" | "mismatch" | "unknown";

export type HelperCredentialProfile = {
  id: string;
  label: string;
  username: string;
  lastUsedAt: string | null;
};

export type HelperCapabilities = {
  scan: boolean;
  install: boolean;
  events: boolean;
  secureStorage: boolean;
};

export type HelperHealthResponse = {
  service: string;
  version: string;
  sessionToken: string;
  capabilities: HelperCapabilities;
  savedCredentialProfiles: HelperCredentialProfile[];
};

export type HelperDesktopPlatform = "macos" | "windows" | "linux" | "unknown";

export type HelperDownloadOption = {
  id: string;
  family: Exclude<HelperDesktopPlatform, "unknown">;
  label: string;
  shortLabel: string;
  url: string;
  launcher: string;
};

export type HelperScanCandidate = {
  ip: string;
  source: "default_gateway" | "known_ip";
  sshReachable: boolean;
  fingerprintState: HelperFingerprintState;
  hostKeyFingerprint: string | null;
  recommended: boolean;
};

export type HelperScanResponse = {
  recommendedTargetIp: string | null;
  candidates: HelperScanCandidate[];
};

export type InstallChecklistItem = {
  id: string;
  label: string;
  status: "pending" | "success" | "failure";
  details?: string | null;
};

export type HelperInstallResponse = {
  sessionId: string;
};

export type HelperInstallEvent = {
  stage: InstallStage;
  state: InstallEventState;
  message: string;
  timestamp: string;
  copyableLogChunk?: string | null;
  checklistDelta?: InstallChecklistItem[] | null;
  code?: "auth_failed" | "host_key_mismatch" | "router_not_found" | "bootstrap_failed" | "verification_failed" | "internal_error";
};

export function isProbablyMobileUserAgent(userAgent: string) {
  return /android|iphone|ipad|ipod|mobile|windows phone/i.test(userAgent);
}

const helperDownloadOptions: HelperDownloadOption[] = [
  {
    id: "macos-apple-silicon",
    family: "macos",
    label: "macOS (Apple Silicon)",
    shortLabel: "Mac Apple Silicon",
    url: `${INSTALL_HELPER_DOWNLOAD_BASE_PATH}/vectra-install-helper-macos-apple-silicon.zip`,
    launcher: "Vectra Install Helper.app",
  },
  {
    id: "macos-intel",
    family: "macos",
    label: "macOS (Intel)",
    shortLabel: "Mac Intel",
    url: `${INSTALL_HELPER_DOWNLOAD_BASE_PATH}/vectra-install-helper-macos-intel.zip`,
    launcher: "Vectra Install Helper.app",
  },
  {
    id: "windows-x64",
    family: "windows",
    label: "Windows (x64)",
    shortLabel: "Windows",
    url: `${INSTALL_HELPER_DOWNLOAD_BASE_PATH}/vectra-install-helper-windows-x64.zip`,
    launcher: "start-vectra-install-helper.cmd",
  },
  {
    id: "linux-x64",
    family: "linux",
    label: "Linux (x64)",
    shortLabel: "Linux",
    url: `${INSTALL_HELPER_DOWNLOAD_BASE_PATH}/vectra-install-helper-linux-x64.zip`,
    launcher: "start-vectra-install-helper.sh",
  },
];

export function detectHelperDesktopPlatform(
  userAgent: string,
  platform = "",
): HelperDesktopPlatform {
  const joined = `${userAgent} ${platform}`.toLowerCase();

  if (joined.includes("mac")) {
    return "macos";
  }

  if (joined.includes("win")) {
    return "windows";
  }

  if (joined.includes("linux") || joined.includes("x11")) {
    return "linux";
  }

  return "unknown";
}

export function getHelperDownloadOptions(
  platform: HelperDesktopPlatform,
): HelperDownloadOption[] {
  if (platform === "unknown") {
    return helperDownloadOptions;
  }

  const preferred = helperDownloadOptions.filter(
    (option) => option.family === platform,
  );
  const fallback = helperDownloadOptions.filter(
    (option) => option.family !== platform,
  );

  return [...preferred, ...fallback];
}

export function selectRecommendedCandidate(scan: HelperScanResponse) {
  const preferred =
    scan.candidates.find((candidate) => candidate.recommended) ??
    scan.candidates.find(
      (candidate) =>
        candidate.sshReachable && candidate.fingerprintState !== "mismatch",
    ) ??
    scan.candidates.find((candidate) => candidate.sshReachable) ??
    null;

  return preferred;
}

export function mergeChecklistDelta(
  current: InstallChecklistItem[],
  delta: InstallChecklistItem[] | null | undefined,
) {
  if (!delta || delta.length === 0) {
    return current;
  }

  const next = new Map(current.map((item) => [item.id, item] as const));
  for (const item of delta) {
    next.set(item.id, item);
  }
  return Array.from(next.values());
}

export async function fetchHelperHealth() {
  const response = await fetch(`${INSTALL_HELPER_BASE_URL}/health`, {
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(`helper-health-${response.status}`);
  }

  const payload = (await response.json()) as Partial<HelperHealthResponse>;

  return {
    service: payload.service ?? "vectra-install-helper",
    version: payload.version ?? "unknown",
    sessionToken: payload.sessionToken ?? "",
    capabilities: payload.capabilities ?? {
      scan: false,
      install: false,
      events: false,
      secureStorage: false,
    },
    savedCredentialProfiles: Array.isArray(payload.savedCredentialProfiles)
      ? payload.savedCredentialProfiles
      : [],
  };
}

export async function runHelperScan(sessionToken: string) {
  const response = await fetch(`${INSTALL_HELPER_BASE_URL}/scan`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-vectra-install-session": sessionToken,
    },
    body: JSON.stringify({}),
  });

  if (!response.ok) {
    throw new Error(`helper-scan-${response.status}`);
  }

  return (await response.json()) as HelperScanResponse;
}

export async function startHelperInstall(args: {
  sessionToken: string;
  targetIp: string;
  credentialProfileId?: string | null;
  password?: string;
  saveProfile?: boolean;
}) {
  const response = await fetch(`${INSTALL_HELPER_BASE_URL}/install`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-vectra-install-session": args.sessionToken,
    },
    body: JSON.stringify({
      targetIp: args.targetIp,
      credentialProfileId: args.credentialProfileId ?? undefined,
      password: args.password ?? undefined,
      saveProfile: args.saveProfile ?? false,
    }),
  });

  if (!response.ok) {
    throw new Error(`helper-install-${response.status}`);
  }

  return (await response.json()) as HelperInstallResponse;
}
