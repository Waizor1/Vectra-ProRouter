export type OnboardingBaseline =
  | "standard-non-hh"
  | "hh-exempt"
  | "subscription-only";

export type OnboardingVerifyPolicy = "route-smoke" | "services-only";

export type OnboardingRunStatus = string | null | undefined;

export function normalizeOnboardingVerifyPolicyForBaseline(
  baseline: OnboardingBaseline,
  verifyPolicy: OnboardingVerifyPolicy,
): OnboardingVerifyPolicy {
  return baseline === "standard-non-hh" ? verifyPolicy : "services-only";
}

export function shouldEnableOnboardingAdvance(args: {
  profileEnabled: boolean | null | undefined;
  canRunJobs: boolean;
  busy: boolean;
  runStatus: OnboardingRunStatus;
}) {
  return (
    Boolean(args.profileEnabled) &&
    args.canRunJobs &&
    !args.busy &&
    args.runStatus !== "done" &&
    args.runStatus !== "paused" &&
    args.runStatus !== "blocked" &&
    args.runStatus !== "failed"
  );
}

export function shouldEnableOnboardingRetry(args: {
  profilePresent: boolean;
  canRunJobs: boolean;
  busy: boolean;
  runStatus: OnboardingRunStatus;
}) {
  return (
    args.profilePresent &&
    args.canRunJobs &&
    !args.busy &&
    (args.runStatus === "blocked" ||
      args.runStatus === "failed" ||
      args.runStatus === "paused")
  );
}

export function getOnboardingDoneBannerCopy() {
  return "Run завершён и больше не перезапускается автоматически. Сохранение профиля меняет только сохранённые поля; новый onboarding здесь не запускается.";
}

export function buildOnboardingSaveProfileInput(args: {
  routerId: string;
  targetHostname: string;
  displayName: string;
  subscriptionUrl: string;
  subscriptionRemark: string;
  baseline: OnboardingBaseline;
  runtimePolicy: "auto-minimal-passwall-xray" | "controller-only";
  verifyPolicy: OnboardingVerifyPolicy;
  notes: string;
}) {
  return {
    routerId: args.routerId,
    targetHostname: args.targetHostname.trim() || null,
    displayName: args.displayName.trim() || null,
    subscriptionUrl: args.subscriptionUrl.trim() || undefined,
    subscriptionRemark: args.subscriptionRemark.trim() || null,
    baseline: args.baseline,
    runtimePolicy: args.runtimePolicy,
    verifyPolicy: normalizeOnboardingVerifyPolicyForBaseline(
      args.baseline,
      args.verifyPolicy,
    ),
    notes: args.notes.trim() || null,
  };
}
