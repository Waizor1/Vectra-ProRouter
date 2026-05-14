"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import {
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

import {
  passwallDesiredConfigSchema,
  passwallNodeProtocolSchema,
  passwallTransportSchema,
  summarizePasswallRevisionDiff,
  type PasswallDesiredConfig,
  type PasswallFieldDiff,
  type PasswallOperationPreview,
} from "@vectra/contracts";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import { DisabledFeatureNotice } from "~/components/disabled-feature-notice";
import { ImportReviewActions } from "~/components/import-review-actions";
import {
  MobileCard,
  MobileCardField,
  MobileCardGrid,
  MobileCardList,
} from "~/components/mobile-records";
import { Panel } from "~/components/panel";
import { RescueActions } from "~/components/rescue-actions";
import { RouterManagementTaskLog } from "~/components/router-management-task-log";
import { RouterWatchLogsSection } from "~/components/router-watch-logs-section";
import {
  basicSettingsSecondaryTabs,
  buildRouterConsoleQuery,
  normalizeRouterConsoleSelection,
  routerPrimaryTabs,
  type RouterConsoleSelection,
  type RouterPrimaryTab,
} from "~/components/router-console";
import { StatusTile } from "~/components/status-tile";
import { TabBar } from "~/components/tab-bar";
import {
  compareControllerVersions,
  formatControllerVersion,
  normalizeControllerVersion,
} from "~/lib/controller-version";
import {
  minimumTerminalControllerVersion,
  supportsTerminalFeature,
} from "~/lib/router-terminal-support";
import { describeRouterMemory, getRouterMemoryTone } from "~/lib/router-memory";
import {
  describeConfigTrustState,
  formatConfigSourceModeLabel,
} from "~/lib/router-config-trust";
import {
  PASSWALL_PACKAGE_TARGET_ROWS,
  buildFallbackPasswallBundleMetadata,
  buildPasswallBundleMetadataFromArtifact,
  findPasswallRuntimeTarget,
  packageNameToRuntimeKey,
} from "~/lib/passwall-artifacts";
import {
  formatPasswallAvailableVersion,
  formatPasswallManagedStackAvailableVersion,
  runtimeMeetsOrExceedsTargetVersion,
  summarizePasswallAttempt,
} from "~/lib/passwall-update-summary";
import {
  PASSWALL_FEATURE_MIN_VERSIONS,
  getPasswallFeatureGate,
  type PasswallFeatureGate,
} from "~/lib/passwall-feature-gates";
import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
  isRouterOnboardingPending,
} from "~/lib/router-onboarding";
import {
  formatTelegramReachabilityLabel,
  getTelegramReachabilityChecks,
} from "~/lib/telegram-reachability";
import {
  formatYoutubeReachabilityLabel,
  getYoutubeReachabilityChecks,
} from "~/lib/youtube-reachability";
import type { RouterWorkspaceInventory } from "~/server/vectra/editor-surface";
import {
  addNode,
  addShuntRule,
  addSubscription,
  deleteNode,
  deleteShuntRule,
  deleteSubscription,
  duplicateNode,
  moveNodeToTop,
  moveShuntRuleToTop,
  moveSubscriptionToTop,
  normalizeShuntRuleBindings,
  pruneNodes,
  renameShuntRule,
  selectNode,
  updateShuntRuleExtra,
} from "~/components/router-editor-state";
import { api, type RouterOutputs } from "~/trpc/react";

type DraftConfigInput = PasswallDesiredConfig;
type EditorSurface = RouterOutputs["draft"]["editorSurface"];
type ControlPlaneHealthResponse = {
  ok: boolean;
  checkedAt?: string;
  checks?: {
    dbRead?: boolean;
    dbWriteProbe?: boolean;
    browserPushMonitor?: boolean;
  };
  error?: string | null;
};

type RouterDetailWorkspaceProps = {
  routerId: string;
  initialSurface: EditorSurface;
  routerReachable: boolean;
  directModeActive: boolean;
  needsRecoveryAction: boolean;
};

const routerDetailReimportRefreshMs = 5_000;
const routerDetailDriftRefreshMs = 10_000;

function getRouterDetailSurfaceRefetchInterval(
  surfaceData: EditorSurface | undefined,
) {
  if (!surfaceData) {
    return false;
  }

  if (surfaceData.configTrust.requiresReimport) {
    return routerDetailReimportRefreshMs;
  }

  if (
    surfaceData.unconfirmedChanges.router.status !== "none" ||
    surfaceData.unconfirmedChanges.panel.status !== "none"
  ) {
    return routerDetailDriftRefreshMs;
  }

  return false;
}

function formatFleetPolicyStatus(
  status: EditorSurface["fleetPolicyCompliance"]["status"],
) {
  switch (status) {
    case "compliant":
      return "policy OK";
    case "violation":
      return "policy drift";
    case "exempt":
      return "исключение";
    case "unknown":
      return "policy ?";
  }
}

function getFleetPolicyBadgeClassName(
  status: EditorSurface["fleetPolicyCompliance"]["status"],
) {
  switch (status) {
    case "compliant":
      return "border-emerald-400/25 bg-emerald-500/10 text-emerald-100";
    case "violation":
      return "border-amber-400/30 bg-amber-500/10 text-amber-100";
    case "exempt":
      return "border-white/10 bg-white/5 text-slate-200";
    case "unknown":
      return "border-sky-400/25 bg-sky-500/10 text-sky-100";
  }
}

type UnconfirmedChangeGroup = EditorSurface["unconfirmedChanges"]["router"];

type Option = {
  value: string;
  label: string;
  disabled?: boolean;
  title?: string;
};

const dnsStrategyOptions = [
  { value: "UseIP", label: "UseIP" },
  { value: "UseIPv4", label: "UseIPv4" },
  { value: "UseIPv6", label: "UseIPv6" },
] as const satisfies ReadonlyArray<Option>;

const remoteDnsProtocolOptions = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "doh", label: "DoH" },
  { value: "tls", label: "TLS" },
  { value: "quic", label: "QUIC" },
  { value: "http3", label: "HTTP/3" },
] as const satisfies ReadonlyArray<Option>;

const detourOptions = [
  { value: "remote", label: "Через прокси" },
  { value: "direct", label: "Напрямую" },
] as const satisfies ReadonlyArray<Option>;

const logLevelOptions = [
  { value: "debug", label: "debug" },
  { value: "info", label: "info" },
  { value: "warning", label: "warning" },
  { value: "error", label: "error" },
] as const satisfies ReadonlyArray<Option>;

const subscriptionFilterOptions = [
  { value: "0", label: "Close" },
  { value: "1", label: "Discard List" },
  { value: "2", label: "Keep List" },
  { value: "3", label: "Discard List, but Keep List first" },
  { value: "4", label: "Keep List, but Discard List first" },
] as const satisfies ReadonlyArray<Option>;

const subscriptionItemFilterOptions = [
  ...subscriptionFilterOptions,
  { value: "5", label: "Use global config" },
] as const satisfies ReadonlyArray<Option>;

const domainStrategyOptions = [
  { value: "auto", label: "auto" },
  { value: "prefer_ipv4", label: "prefer_ipv4" },
  { value: "prefer_ipv6", label: "prefer_ipv6" },
  { value: "ipv4_only", label: "ipv4_only" },
  { value: "ipv6_only", label: "ipv6_only" },
] as const satisfies ReadonlyArray<Option>;

const subscriptionItemDomainStrategyOptions = [
  { value: "global", label: "Use global config" },
  { value: "", label: "Auto" },
  { value: "UseIPv4", label: "UseIPv4" },
  { value: "UseIPv6", label: "UseIPv6" },
  { value: "prefer_ipv4", label: "Prefer IPv4" },
  { value: "prefer_ipv6", label: "Prefer IPv6" },
  { value: "ipv4_only", label: "IPv4 Only" },
  { value: "ipv6_only", label: "IPv6 Only" },
] as const satisfies ReadonlyArray<Option>;

const updateStrategyOptions = [
  { value: "package-only", label: "Только пакеты" },
  { value: "package-preferred", label: "Сначала пакеты" },
  { value: "expert-fallback", label: "Резервный экспертный путь" },
] as const satisfies ReadonlyArray<Option>;

const subscriptionAddModeOptions = [
  { value: "1", label: "Обновлять существующие" },
  { value: "2", label: "Полностью перечитать подписку" },
] as const satisfies ReadonlyArray<Option>;

const scheduleModeOptions = [
  { value: "daily", label: "Ежедневно" },
  { value: "weekly", label: "Еженедельно" },
  { value: "interval", label: "Через интервал" },
] as const satisfies ReadonlyArray<Option>;

const scheduleDayOptions = [
  { value: "1", label: "Понедельник" },
  { value: "2", label: "Вторник" },
  { value: "3", label: "Среда" },
  { value: "4", label: "Четверг" },
  { value: "5", label: "Пятница" },
  { value: "6", label: "Суббота" },
  { value: "0", label: "Воскресенье" },
] as const satisfies ReadonlyArray<Option>;

const passwallWeekUpdateOptions = [
  { value: "8", label: "Loop Mode" },
  { value: "7", label: "Every day" },
  { value: "1", label: "Every Monday" },
  { value: "2", label: "Every Tuesday" },
  { value: "3", label: "Every Wednesday" },
  { value: "4", label: "Every Thursday" },
  { value: "5", label: "Every Friday" },
  { value: "6", label: "Every Saturday" },
  { value: "0", label: "Every Sunday" },
] as const satisfies ReadonlyArray<Option>;

const hourOptions = Array.from({ length: 24 }, (_, hour) => ({
  value: String(hour),
  label: `${String(hour).padStart(2, "0")}:00`,
})) satisfies Option[];

const intervalHourOptions = Array.from({ length: 24 }, (_, index) => ({
  value: String(index + 1),
  label: `${index + 1} ч.`,
})) satisfies Option[];

const ruleAssetOptions = [
  { value: "geoip", label: "GeoIP" },
  { value: "geosite", label: "GeoSite" },
] as const satisfies ReadonlyArray<Option>;

const shuntProtocolOptions = [
  { value: "http", label: "http" },
  { value: "tls", label: "tls" },
  { value: "quic", label: "quic" },
  { value: "bittorrent", label: "bittorrent" },
] as const satisfies ReadonlyArray<Option>;

const subscriptionDomainResolverOptions = [
  { value: "", label: "Auto" },
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "https", label: "DoH" },
] as const satisfies ReadonlyArray<Option>;

function gatedOption(option: Option, gate: PasswallFeatureGate): Option {
  if (gate.supported) {
    return option;
  }
  return {
    ...option,
    disabled: true,
    title: gate.reason ?? undefined,
    label: `${option.label} · только ${gate.minimumVersion}+`,
  };
}

function buildShuntProtocolOptions(passwallVersion: string | null | undefined) {
  const quicGate = getPasswallFeatureGate(
    passwallVersion,
    PASSWALL_FEATURE_MIN_VERSIONS.shuntQuicProtocol,
  );

  return shuntProtocolOptions.map((option) =>
    option.value === "quic" ? gatedOption(option, quicGate) : option,
  );
}

function buildSubscriptionItemDomainStrategyOptions(
  passwallVersion: string | null | undefined,
) {
  const resolverGate = getPasswallFeatureGate(
    passwallVersion,
    PASSWALL_FEATURE_MIN_VERSIONS.subscriptionDomainResolver,
  );

  return subscriptionItemDomainStrategyOptions.map((option) =>
    option.value === "UseIPv4" || option.value === "UseIPv6"
      ? gatedOption(option, resolverGate)
      : option,
  );
}

const shuntInboundOptions = [
  { value: "tproxy", label: "Transparent proxy" },
  { value: "socks", label: "Socks" },
] as const satisfies ReadonlyArray<Option>;

const shuntNetworkOptions = [
  { value: "tcp,udp", label: "TCP UDP" },
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
] as const satisfies ReadonlyArray<Option>;

const accessModeOptions = [
  { value: "", label: "Auto" },
  { value: "direct", label: "Direct Connection" },
  { value: "proxy", label: "Proxy" },
] as const satisfies ReadonlyArray<Option>;

const chainProxyOptions = [
  { value: "", label: "Close (Not use)" },
  { value: "1", label: "Preproxy Node" },
  { value: "2", label: "Landing Node" },
] as const satisfies ReadonlyArray<Option>;

const shadowsocksTypeOptions = [
  { value: "global", label: "Use global config" },
  { value: "shadowsocks-libev", label: "shadowsocks-libev" },
  { value: "shadowsocks-rust", label: "shadowsocks-rust" },
  { value: "sing-box", label: "sing-box" },
  { value: "xray", label: "xray" },
] as const satisfies ReadonlyArray<Option>;

const xraySingBoxTypeOptions = [
  { value: "global", label: "Use global config" },
  { value: "sing-box", label: "sing-box" },
  { value: "xray", label: "xray" },
] as const satisfies ReadonlyArray<Option>;

const hysteriaTypeOptions = [
  ...xraySingBoxTypeOptions,
  { value: "hysteria2", label: "hysteria2" },
] as const satisfies ReadonlyArray<Option>;

const shuntTargetBaseOptions = [
  { value: "", label: "Close (Not use)" },
  { value: "_default", label: "Use default node" },
  { value: "_direct", label: "Direct Connection" },
  { value: "_blackhole", label: "Blackhole (Block)" },
] as const satisfies ReadonlyArray<Option>;

const shuntDefaultTargetOptions = shuntTargetBaseOptions.filter(
  (option) => option.value !== "_default",
);

const nodeProtocolOptions = passwallNodeProtocolSchema.options.map((value) => ({
  value,
  label: value,
}));

const transportOptions = passwallTransportSchema.options.map((value) => ({
  value,
  label: value,
}));

const disabledTabsExplanation =
  "Серые PassWall-вкладки оставлены для узнаваемой структуры, но ещё не реализованы в Vectra Stable V1.";

const minimumWatchLogsControllerVersion = "0.1.12-r1";

export function RouterDetailWorkspace({
  routerId,
  initialSurface,
  routerReachable,
  directModeActive,
  needsRecoveryAction,
}: RouterDetailWorkspaceProps) {
  const utils = api.useUtils();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const surface = api.draft.editorSurface.useQuery(
    { routerId },
    {
      initialData: initialSurface,
      refetchInterval: (query) =>
        getRouterDetailSurfaceRefetchInterval(query.state.data),
      refetchIntervalInBackground: false,
      refetchOnMount: "always",
      refetchOnReconnect: "always",
      refetchOnWindowFocus: "always",
    },
  );
  const [routerHostnameDraft, setRouterHostnameDraft] = useState(
    initialSurface.routerRuntimeSummary.hostname ?? "",
  );
  const [draft, setDraft] = useState<DraftConfigInput | null>(null);
  const [note, setNote] = useState("");
  const [loadedRevisionId, setLoadedRevisionId] = useState<string | null>(null);
  const [savedRevisionId, setSavedRevisionId] = useState<string | null>(null);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [selectedSubscriptionId, setSelectedSubscriptionId] = useState<
    string | null
  >(null);
  const [selectedRuleId, setSelectedRuleId] = useState<string | null>(null);
  const [selectedSocksId, setSelectedSocksId] = useState<string | null>(null);
  const deferredDraft = useDeferredValue(draft);

  useEffect(() => {
    setRouterHostnameDraft(surface.data?.routerRuntimeSummary.hostname ?? "");
  }, [surface.data?.routerRuntimeSummary.hostname]);

  useEffect(() => {
    if (!surface.data?.draftConfig) {
      return;
    }

    const revisionId =
      surface.data.workspaceRevisionId ??
      surface.data.latestDraftId ??
      surface.data.activeRevisionId ??
      surface.data.importedRevisionId ??
      "live";

    if (loadedRevisionId === revisionId) {
      return;
    }

    const nextDraft = passwallDesiredConfigSchema.parse(
      surface.data.draftConfig,
    );
    const nextEditableNodeIds =
      surface.data.subscriptionRuntime?.editableNodeIds ??
      nextDraft.nodes.map((node) => node.id);
    const nextEditableSubscriptionIds =
      surface.data.subscriptionRuntime?.editableSubscriptionIds ??
      nextDraft.subscriptions.items.map((item) => item.id);
    setDraft(nextDraft);
    setLoadedRevisionId(revisionId);
    setSavedRevisionId(surface.data.latestDraftId ?? null);
    setSelectedNodeId(nextEditableNodeIds[0] ?? null);
    setSelectedSubscriptionId(nextEditableSubscriptionIds[0] ?? null);
    setSelectedRuleId(nextDraft.basicSettings.shuntRules[0]?.id ?? null);
    setSelectedSocksId(nextDraft.basicSettings.socks[0]?.id ?? null);
  }, [loadedRevisionId, surface.data]);

  const inventory = surface.data?.inventory ?? initialSurface.inventory;
  const currentSearchParams = useMemo(
    () => new URLSearchParams(searchParams.toString()),
    [searchParams],
  );
  const normalizedConsoleSelection = useMemo(
    () =>
      normalizeRouterConsoleSelection(
        searchParams.get("tab"),
        searchParams.get("section"),
      ),
    [searchParams],
  );
  const [consoleSelection, setConsoleSelection] =
    useState<RouterConsoleSelection>(normalizedConsoleSelection);
  const pendingTabScrollYRef = useRef<number | null>(null);

  const replaceRouterDetailUrl = useCallback(
    (query: URLSearchParams) => {
      const nextQuery = query.toString();
      const nextUrl = nextQuery ? `${pathname}?${nextQuery}` : pathname;
      window.history.replaceState(window.history.state, "", nextUrl);
    },
    [pathname],
  );
  const updateConsoleSelection = useCallback(
    (nextSelection: RouterConsoleSelection) => {
      pendingTabScrollYRef.current = window.scrollY;
      setConsoleSelection((currentSelection) =>
        currentSelection.primaryTab === nextSelection.primaryTab &&
        currentSelection.secondaryTab === nextSelection.secondaryTab
          ? currentSelection
          : nextSelection,
      );

      const query = buildRouterConsoleQuery({
        existing: new URLSearchParams(window.location.search),
        primaryTab: nextSelection.primaryTab,
        secondaryTab: nextSelection.secondaryTab,
      });
      replaceRouterDetailUrl(query);
    },
    [replaceRouterDetailUrl],
  );

  const primaryTab = consoleSelection.primaryTab;
  const secondaryTab = consoleSelection.secondaryTab;
  const watchLogsSupported = supportsControllerFeature(
    inventory.controllerVersion,
    minimumWatchLogsControllerVersion,
  );
  const effectivePrimaryTab =
    primaryTab === "watch-logs" && !watchLogsSupported
      ? "basic-settings"
      : primaryTab;

  useEffect(() => {
    const currentTab = searchParams.get("tab");
    const currentSection = searchParams.get("section");
    const nextPrimary = normalizedConsoleSelection.primaryTab;
    const nextSecondary = normalizedConsoleSelection.secondaryTab;
    const shouldReplace =
      currentTab !== nextPrimary ||
      (nextPrimary === "basic-settings"
        ? currentSection !== nextSecondary
        : currentSection !== null);

    if (!shouldReplace) {
      return;
    }

    const query = buildRouterConsoleQuery({
      existing: new URLSearchParams(currentSearchParams),
      primaryTab: nextPrimary,
      secondaryTab: nextSecondary,
    });
    replaceRouterDetailUrl(query);
  }, [
    currentSearchParams,
    normalizedConsoleSelection.primaryTab,
    normalizedConsoleSelection.secondaryTab,
    replaceRouterDetailUrl,
    searchParams,
  ]);
  useEffect(() => {
    setConsoleSelection((currentSelection) =>
      currentSelection.primaryTab === normalizedConsoleSelection.primaryTab &&
      currentSelection.secondaryTab === normalizedConsoleSelection.secondaryTab
        ? currentSelection
        : normalizedConsoleSelection,
    );
  }, [normalizedConsoleSelection]);
  useEffect(() => {
    const preservedScrollY = pendingTabScrollYRef.current;

    if (preservedScrollY === null) {
      return;
    }

    pendingTabScrollYRef.current = null;
    window.scrollTo({ top: preservedScrollY });
    window.requestAnimationFrame(() => {
      window.scrollTo({ top: preservedScrollY });
      window.requestAnimationFrame(() => {
        window.scrollTo({ top: preservedScrollY });
      });
    });
  }, [consoleSelection.primaryTab, consoleSelection.secondaryTab]);

  useSelectionSync(
    surface.data?.subscriptionRuntime?.editableNodeIds ??
      draft?.nodes.map((node) => node.id),
    selectedNodeId,
    setSelectedNodeId,
  );
  useSelectionSync(
    surface.data?.subscriptionRuntime?.editableSubscriptionIds ??
      draft?.subscriptions.items.map((item) => item.id),
    selectedSubscriptionId,
    setSelectedSubscriptionId,
  );
  useSelectionSync(
    draft?.basicSettings.shuntRules.map((rule) => rule.id),
    selectedRuleId,
    setSelectedRuleId,
  );
  useSelectionSync(
    draft?.basicSettings.socks.map((entry) => entry.id),
    selectedSocksId,
    setSelectedSocksId,
  );

  const normalizedDeferredDraft = deferredDraft
    ? normalizeShuntRuleBindings(deferredDraft)
    : null;
  const validation = normalizedDeferredDraft
    ? passwallDesiredConfigSchema.safeParse(normalizedDeferredDraft)
    : null;
  const validDraft = validation?.success ? validation.data : null;
  const validationMessage =
    validation && !validation.success
      ? `${validation.error.issues[0]?.path.join(".") ?? "config"}: ${
          validation.error.issues[0]?.message ?? "Некорректная конфигурация"
        }`
      : null;

  const preview =
    surface.data && validDraft
      ? summarizePasswallRevisionDiff(
          surface.data.currentConfigFreshness === "live"
            ? surface.data.currentLiveConfig
            : (surface.data.authoritativeConfig ??
                surface.data.currentLiveConfig),
          validDraft,
        )
      : null;
  const visibleFieldDiffs = preview
    ? filterOperatorVisibleFieldDiffs(preview.fieldDiffs)
    : [];

  const saveMutation = api.draft.save.useMutation({
    onSuccess: async (revision) => {
      setSavedRevisionId(revision?.id ?? null);
      setLoadedRevisionId(null);
      setNote("");
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.draft.list.invalidate(),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });

  const queueMutation = api.draft.queueApply.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.draft.list.invalidate(),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });
  const discardDraftMutation = api.draft.discard.useMutation({
    onSuccess: async () => {
      setSavedRevisionId(null);
      setLoadedRevisionId(null);
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.draft.workspace.invalidate({ routerId }),
        utils.draft.list.invalidate(),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });

  const deleteRouterMutation = api.fleet.deleteRouter.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.list.invalidate(),
        utils.fleet.monitoring.invalidate(),
        utils.fleet.overview.invalidate(),
        utils.fleet.pendingImportReviews.invalidate(),
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.draft.list.invalidate(),
      ]);
      router.replace("/fleet");
    },
  });
  const renameRouterMutation = api.fleet.renameRouter.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.draft.workspace.invalidate({ routerId }),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.list.invalidate(),
        utils.fleet.monitoring.invalidate(),
        utils.update.versionDriftWorkspace.invalidate(),
      ]);
      router.refresh();
    },
  });

  const handleDeleteRouter = () => {
    const routerName = surface.data?.routerRuntimeSummary.name ?? routerId;
    const confirmed = window.confirm(
      `Удалить роутер "${routerName}" из системы?\n\n` +
        "Будут удалены черновики, задания, снапшоты и связанные записи из панели. " +
        "На самом роутере пакеты не удаляются. Если контроллер снова зарегистрируется, устройство может появиться заново.",
    );

    if (!confirmed) {
      return;
    }

    deleteRouterMutation.mutate({ routerId });
  };

  const handleRenameRouter = async () => {
    await renameRouterMutation.mutateAsync({
      routerId,
      hostname: routerHostnameDraft,
    });
  };

  if (surface.isLoading || !surface.data || !draft) {
    return (
      <div className="rounded-md border border-white/10 bg-[var(--vectra-panel)] px-4 py-4 text-sm text-slate-300">
        Загружаю рабочую поверхность роутера...
      </div>
    );
  }

  const editor = surface.data;
  const editableNodeIds = new Set(editor.subscriptionRuntime.editableNodeIds);
  const editableSubscriptionIds = new Set(
    editor.subscriptionRuntime.editableSubscriptionIds,
  );
  const currentDraftFingerprint = JSON.stringify(validDraft ?? draft);
  const loadedDraftFingerprint = JSON.stringify(editor.draftConfig);
  const hasUnsavedChanges = currentDraftFingerprint !== loadedDraftFingerprint;
  const selectedNode =
    draft.nodes.find(
      (node) => node.id === selectedNodeId && editableNodeIds.has(node.id),
    ) ?? null;
  const selectedSubscription =
    draft.subscriptions.items.find(
      (item) =>
        item.id === selectedSubscriptionId &&
        editableSubscriptionIds.has(item.id),
    ) ?? null;
  const selectedRule =
    draft.basicSettings.shuntRules.find((rule) => rule.id === selectedRuleId) ??
    null;
  const selectedSocks =
    draft.basicSettings.socks.find((entry) => entry.id === selectedSocksId) ??
    null;
  const canQueueApply =
    Boolean(savedRevisionId ?? editor.latestDraftId) &&
    !editor.approvalRequired &&
    editor.routerRuntimeSummary.destructiveActionsAllowed;
  const canApplyCurrentDraft =
    Boolean(validDraft) &&
    !editor.approvalRequired &&
    editor.routerRuntimeSummary.destructiveActionsAllowed;
  const savedDraftExists = Boolean(savedRevisionId ?? editor.latestDraftId);
  const saveDisabledReason = !validDraft
    ? "Сохранение недоступно, пока не исправлены ошибки в форме."
    : saveMutation.isPending
      ? "Черновик сохраняется в панели."
      : hasUnsavedChanges
        ? "Несохранённые изменения останутся только в текущей форме, пока вы не сохраните черновик."
        : savedDraftExists
          ? "Форма уже совпадает с последним сохранённым черновиком в панели."
          : "Можно сохранить текущую конфигурацию как черновик в панели без применения на роутер.";
  const applyDisabledReason = !validDraft
    ? "Исправьте ошибки в форме."
    : editor.approvalRequired
      ? "Сначала завершите подключение и подтвердите стартовую базу."
      : !editor.routerRuntimeSummary.destructiveActionsAllowed
        ? "Для этого роутера применение отключено."
        : hasUnsavedChanges || !savedDraftExists
          ? "Сохранит текущие поля как новую ревизию и только потом поставит применение в очередь."
          : "Поставит применение последнего сохранённого черновика. Controller перепишет управляемые PassWall-секции из этой ревизии.";

  const handleSaveDraft = async () => {
    if (!validDraft) {
      return;
    }

    await saveMutation.mutateAsync({
      routerId,
      note: note.trim() || undefined,
      config: validDraft,
    });
  };

  const handleSaveAndApply = async () => {
    if (!validDraft) {
      return;
    }

    let desiredRevisionId = savedRevisionId ?? editor.latestDraftId ?? null;

    if (hasUnsavedChanges || !desiredRevisionId) {
      const revision = await saveMutation.mutateAsync({
        routerId,
        note: note.trim() || undefined,
        config: validDraft,
      });
      desiredRevisionId = revision?.id ?? null;
    }

    if (!desiredRevisionId) {
      return;
    }

    await queueMutation.mutateAsync({
      routerId,
      desiredRevisionId,
    });
  };

  const handlePersistNormalizedSubscriptionRuntime = async () => {
    if (!validDraft) {
      return;
    }

    await saveMutation.mutateAsync({
      routerId,
      note: note.trim() || "Принять текущий live-список нод с роутера",
      config: validDraft,
    });
  };

  const handleDiscardSavedDraft = async () => {
    const revisionId =
      editor.unconfirmedChanges.panel.revisionId ??
      savedRevisionId ??
      editor.latestDraftId ??
      null;

    if (!revisionId) {
      return;
    }

    const confirmed = window.confirm(
      "Отбросить сохранённый черновик?\n\n" +
        "Черновик будет скрыт из применения и не изменит роутер. " +
        "Если по нему уже была доставлена задача применения, панель не даст отбросить её этим действием.",
    );

    if (!confirmed) {
      return;
    }

    await discardDraftMutation.mutateAsync({ routerId, revisionId });
  };

  const primaryItems = routerPrimaryTabs.map((tab) => {
    const watchLogsDisabled = tab.id === "watch-logs" && !watchLogsSupported;
    const disabled = ("disabled" in tab && tab.disabled) || watchLogsDisabled;

    return {
      id: tab.id,
      label: tab.label,
      disabled,
      active: tab.id === effectivePrimaryTab,
      onSelect: () => {
        if (disabled) {
          return;
        }

        updateConsoleSelection({
          primaryTab: tab.id as RouterPrimaryTab,
          secondaryTab: tab.id === "basic-settings" ? "main" : null,
        });
      },
    };
  });

  const secondaryItems = basicSettingsSecondaryTabs.map((tab) => ({
    id: tab.id,
    label: tab.label,
    active: secondaryTab === tab.id,
    onSelect: () => {
      updateConsoleSelection({
        primaryTab: "basic-settings",
        secondaryTab: tab.id,
      });
    },
  }));

  let tabContent: ReactNode = null;
  if (effectivePrimaryTab === "basic-settings") {
    switch (secondaryTab ?? "main") {
      case "main":
        tabContent = (
          <MainTabSection
            draft={draft}
            surface={editor}
            selectedSocks={selectedSocks}
            selectedSocksId={selectedSocksId}
            setSelectedSocksId={setSelectedSocksId}
            setDraft={setDraft}
          />
        );
        break;
      case "shunt-rule":
        tabContent = (
          <ShuntRulesSection
            draft={draft}
            selectedRule={selectedRule}
            selectedRuleId={selectedRuleId}
            setSelectedRuleId={setSelectedRuleId}
            setDraft={setDraft}
            passwallVersion={inventory.passwallVersion ?? null}
            title="Shunt Rule"
            description="Список правил сначала, редактор выбранной строки ниже."
          />
        );
        break;
      case "dns":
        tabContent = (
          <DnsTabSection draft={draft} surface={editor} setDraft={setDraft} />
        );
        break;
      case "log":
        tabContent = (
          <LogTabSection draft={draft} surface={editor} setDraft={setDraft} />
        );
        break;
      case "maintain":
        tabContent = (
          <MaintainTabSection
            draft={draft}
            surface={editor}
            setDraft={setDraft}
            routerId={routerId}
          />
        );
        break;
      default:
        tabContent = null;
        break;
    }
  } else if (effectivePrimaryTab === "node-list") {
    tabContent = (
      <NodeListSection
        draft={draft}
        surface={editor}
        selectedNode={selectedNode}
        selectedNodeId={selectedNodeId}
        setSelectedNodeId={setSelectedNodeId}
        setDraft={setDraft}
        setNote={setNote}
        savePending={saveMutation.isPending}
        queuePending={queueMutation.isPending}
        canPersistNormalizedRuntime={Boolean(validDraft)}
        handlePersistNormalizedRuntime={
          handlePersistNormalizedSubscriptionRuntime
        }
        passwallVersion={inventory.passwallVersion ?? null}
      />
    );
  } else if (effectivePrimaryTab === "node-subscribe") {
    tabContent = (
      <SubscriptionSection
        routerId={routerId}
        draft={draft}
        surface={editor}
        routerReachable={routerReachable}
        selectedSubscription={selectedSubscription}
        selectedSubscriptionId={selectedSubscriptionId}
        setSelectedSubscriptionId={setSelectedSubscriptionId}
        setDraft={setDraft}
        canRunJobs={editor.routerRuntimeSummary.destructiveActionsAllowed}
        passwallVersion={inventory.passwallVersion ?? null}
      />
    );
  } else if (effectivePrimaryTab === "app-update") {
    tabContent = (
      <AppUpdateSection
        routerId={routerId}
        draft={draft}
        surface={editor}
        inventory={inventory}
        setDraft={setDraft}
        canRunJobs={editor.routerRuntimeSummary.updateActionsAllowed ?? false}
        routerReachable={routerReachable}
      />
    );
  } else if (effectivePrimaryTab === "rule-manage") {
    tabContent = (
      <RuleManageSection
        routerId={routerId}
        draft={draft}
        surface={editor}
        selectedRule={selectedRule}
        selectedRuleId={selectedRuleId}
        setSelectedRuleId={setSelectedRuleId}
        setDraft={setDraft}
        canRunJobs={editor.routerRuntimeSummary.destructiveActionsAllowed}
        passwallVersion={inventory.passwallVersion ?? null}
      />
    );
  } else if (effectivePrimaryTab === "geo-view") {
    tabContent = <GeoViewSection inventory={inventory} />;
  } else if (effectivePrimaryTab === "watch-logs") {
    tabContent = (
      <RouterWatchLogsSection
        routerId={routerId}
        routerReachable={routerReachable}
        controllerVersion={inventory.controllerVersion}
        minimumTerminalControllerVersion={minimumTerminalControllerVersion}
      />
    );
  }

  const currentModeLabel = formatProxyMode({
    routerReachable,
    directModeActive,
    passwallEnabled: editor.routerRuntimeSummary.passwallEnabled,
  });
  const configTrust = describeConfigTrustState({
    trust: editor.configTrust,
    offline: !routerReachable,
    directMode: directModeActive,
  });
  const onboarding = describeRouterOnboarding(
    editor.routerRuntimeSummary.importState,
    editor.configTrust,
  );
  const onboardingPending = isRouterOnboardingPending(
    editor.routerRuntimeSummary.importState,
    editor.configTrust,
  );
  const watchLogsHref = (() => {
    const query = buildRouterConsoleQuery({
      existing: new URLSearchParams(currentSearchParams),
      primaryTab: "watch-logs",
    });

    return query.toString() ? `${pathname}?${query.toString()}` : pathname;
  })();
  const telegramChecks = getTelegramReachabilityChecks(
    inventory.telegramReachability,
  );
  const youtubeChecks = getYoutubeReachabilityChecks(
    inventory.youtubeReachability,
  );
  const memoryStatus = describeRouterMemory(inventory.resources ?? null);
  const safetyEvents = (inventory.safetyEvents ?? []).filter(
    (event) => event.severity === "critical" || event.severity === "warning",
  );
  const hasUnconfirmedChanges =
    editor.unconfirmedChanges.router.status !== "none" ||
    editor.unconfirmedChanges.panel.status !== "none";
  const trustDetailsOpen =
    editor.configTrust.requiresReimport ||
    editor.fleetPolicyCompliance.status === "violation" ||
    !routerReachable ||
    directModeActive;
  const supportMeta = formatSupportMeta(
    editor.routerRuntimeSummary.supportState,
  );
  const currentHostname = editor.routerRuntimeSummary.hostname ?? "";
  const normalizedCurrentHostname = currentHostname.trim().toLowerCase();
  const normalizedHostnameDraft = routerHostnameDraft.trim().toLowerCase();
  const renameDirty = normalizedHostnameDraft !== normalizedCurrentHostname;
  const hostnameRenameSupported =
    editor.routerRuntimeSummary.destructiveActionsAllowed &&
    supportsTerminalFeature(
      inventory.controllerVersion ?? null,
      minimumTerminalControllerVersion,
    );
  const routerIdentityMeta = [
    editor.routerRuntimeSummary.hostname
      ? `hostname: ${editor.routerRuntimeSummary.hostname}`
      : null,
    `ID: ${editor.routerRuntimeSummary.deviceIdentifier}`,
    editor.routerRuntimeSummary.boardName ?? "board n/a",
    editor.routerRuntimeSummary.layoutFamily ?? "layout n/a",
  ]
    .filter((value): value is string => Boolean(value))
    .join(" · ");

  return (
    <div className="space-y-4 xl:space-y-5">
      <section className="rounded-[1.6rem] border border-white/10 bg-[rgba(8,11,17,0.88)] px-4 py-4 shadow-[var(--vectra-shadow-md)] sm:px-5 sm:py-5">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0">
            <p className="vectra-kicker text-[var(--vectra-accent)]">
              Router Console
            </p>
            <h2 className="mt-1 text-xl font-semibold tracking-[-0.03em] text-white sm:text-2xl">
              {editor.routerRuntimeSummary.name}
            </h2>
            <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-400">
              Рабочая страница роутера: поменяли настройки, сохранили и
              применили на устройство.
            </p>
          </div>

          <div className="flex flex-wrap gap-2 text-xs text-slate-300">
            <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1">
              {formatRouterImportStateLabel(
                editor.routerRuntimeSummary.importState,
              )}
            </span>
            <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1">
              {routerReachable ? "контроллер на связи" : "нет свежей связи"}
            </span>
            <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1">
              {currentModeLabel}
            </span>
          </div>
        </div>

        <div className="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-5">
          <SummaryCell
            label="Роутер"
            value={editor.routerRuntimeSummary.name}
            meta={routerIdentityMeta}
          />
          <SummaryCell
            label="Связь"
            value={formatDateTime(editor.routerRuntimeSummary.lastSeenAt)}
            meta={routerReachable ? "контроллер на связи" : "свежей связи нет"}
          />
          <SummaryCell
            label="Выбранная нода"
            value={
              editor.routerRuntimeSummary.selectedNodeLabel ??
              draft.basicSettings.main.selectedNodeId ??
              "не выбрана"
            }
            meta={`в очереди: ${editor.routerRuntimeSummary.pendingChanges}`}
          />
          <SummaryCell
            label="Поддержка"
            value={editor.routerRuntimeSummary.supportTitle}
            meta={supportMeta}
          />
          <SummaryCell
            label="Режим"
            value={currentModeLabel}
            meta={onboardingPending ? onboarding.badge : "рабочий режим"}
          />
        </div>

        <form
          onSubmit={(event) => {
            event.preventDefault();
            void handleRenameRouter();
          }}
          className="mt-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3"
        >
          <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
            <label className="block min-w-0 flex-1">
              <span className="vectra-kicker text-slate-500">
                Hostname роутера
              </span>
              <input
                name="router-hostname"
                maxLength={63}
                pattern="[A-Za-z0-9](?:[A-Za-z0-9\\-]{0,61}[A-Za-z0-9])?"
                className="vectra-field mt-2 px-3 py-2 text-sm text-white"
                value={routerHostnameDraft}
                onChange={(event) => setRouterHostnameDraft(event.target.value)}
                placeholder="andrey-livingroom"
              />
            </label>

            <div className="flex flex-wrap gap-2">
              <button
                type="submit"
                disabled={
                  !hostnameRenameSupported ||
                  !renameDirty ||
                  renameRouterMutation.isPending
                }
                className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
              >
                {renameRouterMutation.isPending
                  ? "Отправляю hostname..."
                  : "Применить hostname"}
              </button>
              {renameDirty ? (
                <button
                  type="button"
                  onClick={() =>
                    setRouterHostnameDraft(
                      editor.routerRuntimeSummary.hostname ?? "",
                    )
                  }
                  className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
                >
                  Сбросить форму
                </button>
              ) : null}
            </div>
          </div>

          <p className="mt-2 text-sm leading-6 text-slate-400">
            Панель ставит в очередь реальную смену OpenWrt hostname через
            controller-agent. Меняется именно `system.@system[0].hostname` на
            самом роутере. Допустимы латиница, цифры и дефис.
          </p>
          <p className="mt-2 text-sm leading-6 text-slate-400">
            {hostnameRenameSupported
              ? routerReachable
                ? "Если роутер на связи, задача уйдёт сразу. После успешного выполнения новое имя подтянется в панель на ближайшем check-in."
                : "Роутер сейчас не на связи. Задача останется в очереди и выполнится на следующем check-in."
              : `Нужен controller-agent ${minimumTerminalControllerVersion} или новее на поддерживаемом роутере.`}
          </p>
          {renameRouterMutation.error ? (
            <p className="mt-2 text-sm text-rose-200">
              {renameRouterMutation.error.message}
            </p>
          ) : null}
        </form>

        <div className="vectra-stat-grid mt-3">
          <StatusTile
            label="Состояние PassWall2"
            value={
              editor.routerRuntimeSummary.passwallEnabled
                ? "PassWall2 включён"
                : "PassWall2 выключен"
            }
            tone={
              !routerReachable
                ? "warning"
                : editor.routerRuntimeSummary.passwallEnabled
                  ? "good"
                  : "default"
            }
            hint={`Сервис: ${formatServiceState(inventory.serviceHealth?.passwall)}`}
            compact
            emphasis={editor.routerRuntimeSummary.passwallEnabled}
          />
          <StatusTile
            label="Связь контроллера"
            value={routerReachable ? "свежая" : "устарела"}
            tone={routerReachable ? "good" : "warning"}
            hint={`Последний check-in: ${formatDateTime(editor.routerRuntimeSummary.lastSeenAt)}`}
            compact
            emphasis={routerReachable}
          />
          <StatusTile
            label="RAM свободно"
            value={memoryStatus.summary}
            tone={getRouterMemoryTone(memoryStatus.level)}
            hint={memoryStatus.detail}
            compact
            emphasis={memoryStatus.level === "critical"}
          />
          <StatusTile
            label="Нода и режим"
            value={
              editor.routerRuntimeSummary.selectedNodeLabel
                ? `${editor.routerRuntimeSummary.selectedNodeLabel} / ${currentModeLabel}`
                : currentModeLabel
            }
            tone={directModeActive ? "warning" : "default"}
            hint={`Черновик: ${draft.basicSettings.main.selectedNodeId ?? "не задана"}`}
            compact
          />
          <StatusTile
            label="Подключение"
            value={formatRouterImportStateLabel(
              editor.routerRuntimeSummary.importState,
            )}
            tone={
              editor.approvalRequired || directModeActive
                ? "warning"
                : "default"
            }
            hint={
              directModeActive
                ? (editor.routerRuntimeSummary.lastRescueReason ??
                  "прямой режим активен")
                : onboarding.cardHint
            }
            compact
          />
        </div>

        {safetyEvents.length > 0 ? (
          <details className="mt-3 rounded-2xl border border-amber-300/20 bg-amber-500/[0.07] px-3 py-3">
            <summary className="min-h-11 cursor-pointer list-none">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="vectra-kicker text-amber-200">
                    События безопасности
                  </p>
                  <p className="mt-2 text-sm font-medium text-white">
                    Контроллер заметил риск для PassWall/Xray или ресурсов
                    роутера
                  </p>
                </div>
                <span className="text-xs text-amber-100">
                  {safetyEvents.length}
                </span>
              </div>
            </summary>
            <div className="mt-3 grid gap-2 md:grid-cols-2">
              {safetyEvents.slice(0, 6).map((event) => (
                <div
                  key={`${event.type}-${event.component ?? "router"}-${event.observedAt}`}
                  className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2"
                >
                  <div className="flex items-center justify-between gap-3">
                    <p className="text-sm font-medium text-white">
                      {event.component ?? event.type}
                    </p>
                    <span
                      className={`text-xs ${
                        event.severity === "critical"
                          ? "text-rose-200"
                          : "text-amber-100"
                      }`}
                    >
                      {event.severity === "critical"
                        ? "критично"
                        : "предупреждение"}
                    </span>
                  </div>
                  <p className="mt-2 text-xs leading-5 text-slate-300">
                    {event.message}
                  </p>
                  {event.evidence ? (
                    <p className="mt-1 text-[11px] leading-5 text-slate-500">
                      {event.evidence}
                    </p>
                  ) : null}
                  <p className="mt-1 text-[11px] leading-5 text-slate-500">
                    Источник {event.source ?? "inventory"} ·{" "}
                    {formatDateTime(event.observedAt)}
                  </p>
                </div>
              ))}
            </div>
          </details>
        ) : null}

        {telegramChecks.length > 0 ? (
          <details className="mt-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
            <summary className="min-h-11 cursor-pointer list-none">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="vectra-kicker text-slate-500">
                    Проверки Telegram
                  </p>
                  <p className="mt-2 text-sm font-medium text-white">
                    {formatTelegramReachabilityLabel(
                      inventory.telegramReachability,
                    )}
                  </p>
                </div>
                <span className="text-xs text-slate-400">
                  {telegramChecks.length} цели
                </span>
              </div>
            </summary>
            <div className="mt-3 grid gap-2 md:grid-cols-2">
              {telegramChecks.map((check) => (
                <div
                  key={`${check.label}-${check.checkedAt ?? "na"}`}
                  className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2"
                >
                  <div className="flex items-center justify-between gap-3">
                    <p className="text-sm font-medium text-white">
                      {check.label}
                    </p>
                    <span
                      className={`text-xs ${
                        check.reachable ? "text-emerald-100" : "text-rose-200"
                      }`}
                    >
                      {check.reachable ? "доступно" : "недоступно"}
                    </span>
                  </div>
                  <p className="mt-2 text-xs leading-5 text-slate-400">
                    {check.detail}
                  </p>
                  <p className="mt-1 text-[11px] leading-5 text-slate-500">
                    Проверка {formatDateTime(check.checkedAt)}
                  </p>
                </div>
              ))}
            </div>
          </details>
        ) : null}

        {youtubeChecks.length > 0 ? (
          <details className="mt-3 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
            <summary className="min-h-11 cursor-pointer list-none">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="vectra-kicker text-slate-500">
                    Проверки YouTube
                  </p>
                  <p className="mt-2 text-sm font-medium text-white">
                    {formatYoutubeReachabilityLabel(
                      inventory.youtubeReachability,
                    )}
                  </p>
                </div>
                <span className="text-xs text-slate-400">
                  {youtubeChecks.length} цели
                </span>
              </div>
            </summary>
            <div className="mt-3 grid gap-2 md:grid-cols-2">
              {youtubeChecks.map((check) => (
                <div
                  key={`${check.label}-${check.checkedAt ?? "na"}`}
                  className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2"
                >
                  <div className="flex items-center justify-between gap-3">
                    <p className="text-sm font-medium text-white">
                      {check.label}
                    </p>
                    <span
                      className={`text-xs ${
                        check.reachable ? "text-emerald-100" : "text-rose-200"
                      }`}
                    >
                      {check.reachable ? "доступно" : "недоступно"}
                    </span>
                  </div>
                  <p className="mt-2 text-xs leading-5 text-slate-400">
                    {check.detail}
                  </p>
                  <p className="mt-1 text-[11px] leading-5 text-slate-500">
                    Проверка {formatDateTime(check.checkedAt)}
                  </p>
                </div>
              ))}
            </div>
          </details>
        ) : null}
      </section>

      <RouterActionRail
        routerId={routerId}
        importedRevisionId={editor.importedRevisionId}
        importState={editor.routerRuntimeSummary.importState}
        configTrust={editor.configTrust}
        unconfirmedChanges={editor.unconfirmedChanges}
        validDraft={Boolean(validDraft)}
        validationMessage={validationMessage}
        note={note}
        setNote={setNote}
        savePending={saveMutation.isPending}
        queuePending={queueMutation.isPending}
        discardDraftPending={discardDraftMutation.isPending}
        deletePending={deleteRouterMutation.isPending}
        canApplyCurrentDraft={canApplyCurrentDraft}
        canQueueApply={canQueueApply}
        hasUnsavedChanges={hasUnsavedChanges}
        savedDraftExists={savedDraftExists}
        hasUnconfirmedChanges={hasUnconfirmedChanges}
        saveDisabledReason={saveDisabledReason}
        applyDisabledReason={applyDisabledReason}
        handleSaveDraft={handleSaveDraft}
        handleSaveAndApply={handleSaveAndApply}
        handleDiscardSavedDraft={handleDiscardSavedDraft}
        watchLogsSupported={watchLogsSupported}
        watchLogsHref={watchLogsHref}
        minimumWatchLogsControllerVersion={minimumWatchLogsControllerVersion}
        needsRecoveryAction={needsRecoveryAction}
        directModeActive={directModeActive}
        routerReachable={routerReachable}
        handleDeleteRouter={handleDeleteRouter}
      />

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.85fr)]">
        <div className="min-w-0">
          <Panel
            eyebrow="PassWall workspace"
            title="Вкладки и редакторы"
            tone="muted"
          >
            <TabBar
              items={primaryItems}
              ariaLabel="Основные вкладки PassWall"
            />
            <div className="mt-2">
              <DisabledFeatureNotice
                text={
                  watchLogsSupported
                    ? disabledTabsExplanation
                    : `${disabledTabsExplanation} Watch Logs включится после обновления controller-agent до ${minimumWatchLogsControllerVersion} или новее.`
                }
              />
            </div>
            {effectivePrimaryTab === "basic-settings" ? (
              <div className="mt-3">
                <TabBar
                  items={secondaryItems}
                  ariaLabel="Подразделы Basic Settings"
                  variant="secondary"
                />
              </div>
            ) : null}

            <div className="mt-4 min-w-0">{tabContent}</div>
          </Panel>
        </div>

        <div className="min-w-0 space-y-4">
          <Panel
            eyebrow="Предпросмотр применения"
            title="Что реально изменится"
            tone="muted"
          >
            <div className="vectra-stat-grid">
              <StatusTile
                label="Правки"
                value={`${visibleFieldDiffs.length}`}
                compact
              />
              <StatusTile
                label="Перезапуск"
                value={preview?.requiresRestart ? "нужен" : "нет"}
                compact
              />
              <StatusTile
                label="Подписки"
                value={
                  preview?.refreshSubscriptions ? "обновить" : "без изменений"
                }
                compact
              />
              <StatusTile
                label="Правила"
                value={preview?.refreshRules ? "обновить" : "без изменений"}
                compact
              />
              <StatusTile
                label="Пакеты"
                value={preview?.packageInstall ? "затронуты" : "нет"}
                compact
              />
            </div>
            <ActualChangesList fieldDiffs={visibleFieldDiffs} />
            <div className="mt-4 space-y-2">
              {preview?.operationPreview.length ? (
                preview.operationPreview.map((operation) => (
                  <OperationRow
                    key={`${operation.kind}-${operation.section}-${operation.description}`}
                    operation={operation}
                  />
                ))
              ) : (
                <EmptyState text="Точный предпросмотр появится после валидного черновика." />
              )}
            </div>
          </Panel>

          <details
            open={trustDetailsOpen ? true : undefined}
            className={`rounded-2xl border px-4 py-3 ${configTrust.badgeClassName}`}
          >
            <summary className="min-h-11 cursor-pointer list-none">
              <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                <div className="min-w-0">
                  <p className="vectra-kicker text-current/80">
                    Сверка состояния
                  </p>
                  <h3 className="mt-2 text-sm font-semibold text-white sm:text-base">
                    {editor.configTrust.requiresReimport
                      ? "Подробные настройки ещё обновляются"
                      : configTrust.title}
                  </h3>
                  <p className="mt-2 max-w-3xl text-sm leading-6 text-current/90">
                    {editor.configTrust.requiresReimport
                      ? "Связь с роутером свежая, но подробные разделы PassWall2 ещё подтягиваются. Если настройки не меняли вне панели, обычно ничего делать не нужно."
                      : configTrust.detail}
                  </p>
                </div>
                <span className="rounded-full border border-white/10 bg-white/8 px-3 py-1 text-xs font-medium text-current">
                  {configTrust.badge}
                </span>
                <span
                  className={`rounded-full border px-3 py-1 text-xs font-medium ${getFleetPolicyBadgeClassName(
                    editor.fleetPolicyCompliance.status,
                  )}`}
                  title="Отдельно от configTrust: соответствие общему fleet server package."
                >
                  {formatFleetPolicyStatus(editor.fleetPolicyCompliance.status)}
                </span>
              </div>
            </summary>
            <p className="mt-3 text-xs leading-6 text-current/80">
              Источник настроек:{" "}
              {formatConfigSourceModeLabel(editor.configTrust.configSourceMode)}{" "}
              · последнее чтение{" "}
              {formatDateTime(editor.configTrust.lastLiveImportAt)} · последний
              check-in {formatDateTime(editor.configTrust.lastCheckInAt)}
            </p>
            {editor.fleetPolicyCompliance.status === "violation" ? (
              <div className="mt-3 rounded-xl border border-amber-400/20 bg-amber-500/10 px-3 py-3 text-sm leading-6 text-amber-50">
                <p className="font-semibold">
                  Fleet package не совпадает с live ShuntRules
                </p>
                <p className="mt-1">
                  {editor.fleetPolicyCompliance.summary}
                </p>
                {editor.fleetPolicyCompliance.mismatches.length > 0 ? (
                  <ul className="mt-2 list-disc space-y-1 pl-5">
                    {editor.fleetPolicyCompliance.mismatches
                      .slice(0, 5)
                      .map((mismatch) => (
                        <li key={`${mismatch.slot}-${mismatch.reason}`}>
                          {mismatch.slot}: ожидается {mismatch.expected}, сейчас{" "}
                          {mismatch.actual}
                        </li>
                      ))}
                  </ul>
                ) : null}
              </div>
            ) : null}
          </details>
        </div>
      </div>
    </div>
  );
}

function RouterActionRail({
  routerId,
  importedRevisionId,
  importState,
  configTrust,
  unconfirmedChanges,
  validDraft,
  validationMessage,
  note,
  setNote,
  savePending,
  queuePending,
  discardDraftPending,
  deletePending,
  canApplyCurrentDraft,
  canQueueApply,
  hasUnsavedChanges,
  savedDraftExists,
  hasUnconfirmedChanges,
  saveDisabledReason,
  applyDisabledReason,
  handleSaveDraft,
  handleSaveAndApply,
  handleDiscardSavedDraft,
  watchLogsSupported,
  watchLogsHref,
  minimumWatchLogsControllerVersion,
  needsRecoveryAction,
  directModeActive,
  routerReachable,
  handleDeleteRouter,
}: {
  routerId: string;
  importedRevisionId: string | null;
  importState: string;
  configTrust: EditorSurface["configTrust"];
  unconfirmedChanges: EditorSurface["unconfirmedChanges"];
  validDraft: boolean;
  validationMessage: string | null;
  note: string;
  setNote: Dispatch<SetStateAction<string>>;
  savePending: boolean;
  queuePending: boolean;
  discardDraftPending: boolean;
  deletePending: boolean;
  canApplyCurrentDraft: boolean;
  canQueueApply: boolean;
  hasUnsavedChanges: boolean;
  savedDraftExists: boolean;
  hasUnconfirmedChanges: boolean;
  saveDisabledReason: string;
  applyDisabledReason: string;
  handleSaveDraft: () => Promise<void>;
  handleSaveAndApply: () => Promise<void>;
  handleDiscardSavedDraft: () => Promise<void>;
  watchLogsSupported: boolean;
  watchLogsHref: string;
  minimumWatchLogsControllerVersion: string;
  needsRecoveryAction: boolean;
  directModeActive: boolean;
  routerReachable: boolean;
  handleDeleteRouter: () => void;
}) {
  const hasRouterChanges = unconfirmedChanges.router.status !== "none";
  const hasPanelChanges = unconfirmedChanges.panel.status !== "none";

  return (
    <Panel
      eyebrow="Следующее безопасное действие"
      title="Правки и применение на роутер"
      tone="muted"
    >
      <div className="space-y-4">
        <ImportReviewActions
          routerId={routerId}
          revisionId={importedRevisionId}
          importState={importState}
          configTrust={configTrust}
        />

        {hasUnconfirmedChanges ? (
          <details
            open={hasRouterChanges || hasPanelChanges ? true : undefined}
            className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3"
          >
            <summary className="min-h-11 cursor-pointer list-none">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="vectra-kicker text-slate-500">
                    Неподтверждённые изменения
                  </p>
                  <p className="mt-1 text-sm font-medium text-white">
                    {hasRouterChanges && hasPanelChanges
                      ? "Есть расхождение на роутере и в панели"
                      : hasRouterChanges
                        ? "Есть расхождение со стороны роутера"
                        : "В панели есть изменения, которые ещё ждут применения"}
                  </p>
                </div>
                <span className="text-xs text-slate-400">раскрыть</span>
              </div>
            </summary>

            <div className="mt-3">
              <UnconfirmedChangesPanel
                routerChanges={unconfirmedChanges.router}
                panelChanges={unconfirmedChanges.panel}
                compact
              />
            </div>
            {unconfirmedChanges.panel.status === "saved-draft-pending-apply" &&
            unconfirmedChanges.panel.revisionId ? (
              <div className="mt-3 rounded-2xl border border-amber-400/20 bg-amber-500/10 px-4 py-3">
                <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                  <div className="min-w-0">
                    <p className="vectra-kicker text-amber-200">
                      Безопасная развилка
                    </p>
                    <p className="mt-2 text-sm leading-6 text-amber-50/90">
                      Этот черновик ещё не применён. На роутер уйдёт именно
                      сохранённая ревизия панели, и она перепишет управляемые
                      PassWall-секции. Если это старый эксперимент — отбросьте
                      его перед следующими правками.
                    </p>
                  </div>
                  <button
                    type="button"
                    disabled={discardDraftPending || queuePending}
                    onClick={() => {
                      void handleDiscardSavedDraft();
                    }}
                    className="vectra-button-secondary px-3 py-2 text-sm font-medium text-amber-100 transition hover:border-amber-300/40 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {discardDraftPending
                      ? "Отбрасываю..."
                      : "Отбросить черновик"}
                  </button>
                </div>
              </div>
            ) : null}
          </details>
        ) : null}

        <div className="grid gap-3 md:grid-cols-2">
          <InlineStateCard
            eyebrow="Черновик"
            title={
              validationMessage
                ? "Нужно исправить форму"
                : hasUnsavedChanges
                  ? "Есть несохранённые изменения"
                  : savedDraftExists
                    ? "Черновик синхронизирован с панелью"
                    : "Форма готова к сохранению"
            }
            tone={
              validationMessage
                ? "danger"
                : hasUnsavedChanges
                  ? "warning"
                  : "good"
            }
            description={validationMessage ?? saveDisabledReason}
          />
          <InlineStateCard
            eyebrow="Следующий шаг"
            title={
              !canApplyCurrentDraft
                ? "Применение сейчас недоступно"
                : hasUnsavedChanges
                  ? "Сохранить и применить текущие правки"
                  : "Применить последнюю сохранённую ревизию"
            }
            tone={
              !canApplyCurrentDraft
                ? "warning"
                : hasUnsavedChanges
                  ? "good"
                  : "default"
            }
            description={
              canQueueApply && !hasUnsavedChanges
                ? "Если ничего не менялось, на роутер уйдёт уже сохранённый черновик из панели; управляемые PassWall-секции будут переписаны из этой ревизии."
                : applyDisabledReason
            }
          />
        </div>

        <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(320px,0.95fr)]">
          <label className="block min-w-0">
            <span className="vectra-kicker text-slate-500">
              Комментарий к черновику
            </span>
            <input
              name="draft-note"
              className="vectra-field mt-2 px-3 py-2 text-sm text-white"
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder="Что меняется в этой ревизии"
            />
          </label>
          <div className="rounded-md border border-white/10 bg-black/10 px-3 py-2 text-sm leading-6 text-slate-300">
            {validationMessage ? (
              <span className="text-rose-200">{validationMessage}</span>
            ) : (
              "На роутер отправляется сохранённая ревизия из панели, а не несохранённое состояние формы. При выполнении controller перепишет управляемые PassWall-секции из этой ревизии."
            )}
          </div>
        </div>

        <ActionStrip justify="start" dense>
          <button
            type="button"
            disabled={
              !validDraft ||
              savePending ||
              (!hasUnsavedChanges && canQueueApply)
            }
            onClick={() => {
              void handleSaveDraft();
            }}
            className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            {savePending ? "Сохраняю..." : "Сохранить черновик"}
          </button>
          <button
            type="button"
            disabled={!canApplyCurrentDraft || queuePending || savePending}
            onClick={() => {
              void handleSaveAndApply();
            }}
            className="vectra-button-primary px-3 py-2 text-sm font-medium transition hover:bg-[color-mix(in_oklab,var(--vectra-accent)_85%,white)] disabled:cursor-not-allowed disabled:opacity-50"
          >
            {queuePending
              ? "Отправляю на роутер..."
              : savePending
                ? "Сохраняю..."
                : hasUnsavedChanges || !savedDraftExists
                  ? "Сохранить и применить на роутере"
                  : "Применить сохранённый черновик"}
          </button>
          <Link
            href={`/drafts?routerId=${routerId}`}
            className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
          >
            Экспертный JSON
          </Link>
          {needsRecoveryAction || directModeActive || !routerReachable ? (
            <RescueActions
              routerId={routerId}
              needsRecoveryAction={needsRecoveryAction}
              directModeActive={directModeActive}
              routerReachable={routerReachable}
            />
          ) : null}
        </ActionStrip>

        <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-3">
          <summary className="min-h-11 cursor-pointer list-none">
            <div className="flex flex-col items-start justify-between gap-2 sm:flex-row sm:items-center">
              <div>
                <p className="vectra-kicker text-slate-500">
                  Вторичные действия
                </p>
                <p className="mt-1 text-sm font-medium text-white">
                  Журналы, recovery и удаление роутера
                </p>
              </div>
              <span className="text-xs text-slate-400">раскрыть</span>
            </div>
          </summary>

          <div className="mt-3 space-y-3">
            <ActionStrip justify="start" dense>
              {!needsRecoveryAction && !directModeActive && routerReachable ? (
                <RescueActions
                  routerId={routerId}
                  needsRecoveryAction={needsRecoveryAction}
                  directModeActive={directModeActive}
                  routerReachable={routerReachable}
                />
              ) : null}
              {watchLogsSupported ? (
                <Link
                  href={watchLogsHref}
                  className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
                >
                  Открыть Watch Logs
                </Link>
              ) : (
                <span className="rounded-xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-400">
                  Watch Logs включится после controller{" "}
                  {minimumWatchLogsControllerVersion}
                </span>
              )}
            </ActionStrip>

            <div className="rounded-2xl border border-rose-400/20 bg-rose-500/10 px-4 py-4">
              <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                <div>
                  <p className="vectra-kicker text-rose-200">Опасная зона</p>
                  <p className="mt-2 text-sm font-semibold text-white">
                    Панель забудет этот роутер, но не удалит пакеты на
                    устройстве
                  </p>
                  <p className="mt-2 text-sm leading-6 text-rose-50/90">
                    Удаляются черновики, задачи, снапшоты и связанные записи
                    панели. Если контроллер снова зарегистрируется, устройство
                    появится заново.
                  </p>
                </div>
                <button
                  type="button"
                  disabled={deletePending}
                  onClick={handleDeleteRouter}
                  className="vectra-button-danger px-3 py-2 text-sm font-medium transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {deletePending
                    ? "Удаляю роутер..."
                    : "Удалить роутер из системы"}
                </button>
              </div>
            </div>
          </div>
        </details>
      </div>
    </Panel>
  );
}

function MainTabSection({
  draft,
  surface,
  selectedSocks,
  selectedSocksId,
  setSelectedSocksId,
  setDraft,
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedSocks: DraftConfigInput["basicSettings"]["socks"][number] | null;
  selectedSocksId: string | null;
  setSelectedSocksId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
}) {
  const selectedNodeOptions = draft.nodes.map((node) => ({
    value: node.id,
    label: `${node.label} (${node.id})`,
  }));

  return (
    <div className="space-y-4">
      <FieldGrid>
        <BooleanControl
          label="Главный переключатель"
          value={draft.basicSettings.main.mainSwitch}
          onChange={(value) =>
            updatePathValue(setDraft, "basicSettings.main.mainSwitch", value)
          }
          diff={getDiff(surface, "basicSettings.main.mainSwitch")}
        />
        <SelectControl
          label="Выбранная нода"
          value={draft.basicSettings.main.selectedNodeId}
          options={selectedNodeOptions}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.main.selectedNodeId",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.main.selectedNodeId")}
        />
        <BooleanControl
          label="Проксировать localhost"
          value={draft.basicSettings.main.localhostProxy}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.main.localhostProxy",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.main.localhostProxy")}
        />
        <BooleanControl
          label="Проксировать клиентов"
          value={draft.basicSettings.main.clientProxy}
          onChange={(value) =>
            updatePathValue(setDraft, "basicSettings.main.clientProxy", value)
          }
          diff={getDiff(surface, "basicSettings.main.clientProxy")}
        />
        <NumberControl
          label="SOCKS-порт"
          value={draft.basicSettings.main.nodeSocksPort}
          onChange={(value) =>
            updatePathValue(setDraft, "basicSettings.main.nodeSocksPort", value)
          }
          diff={getDiff(surface, "basicSettings.main.nodeSocksPort")}
        />
        <BooleanControl
          label="SOCKS только localhost"
          value={draft.basicSettings.main.nodeSocksBindLocal}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.main.nodeSocksBindLocal",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.main.nodeSocksBindLocal")}
        />
        <BooleanControl
          label="Глобальный SOCKS-переключатель"
          value={draft.basicSettings.main.socksMainSwitch}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.main.socksMainSwitch",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.main.socksMainSwitch")}
        />
      </FieldGrid>

      <ActionStrip justify="start">
        <span className="vectra-chip text-slate-400">SOCKS-профили</span>
        <button
          type="button"
          onClick={() => {
            const entry = createSocksDraft(draft.nodes[0]?.id ?? "");
            setDraft((previous) =>
              previous
                ? updateConfig(previous, (current) => {
                    current.basicSettings.socks.push(entry);
                  })
                : previous,
            );
            setSelectedSocksId(entry.id);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white"
        >
          Добавить
        </button>
        <button
          type="button"
          disabled={!selectedSocksId}
          onClick={() => {
            if (!selectedSocksId) {
              return;
            }
            setDraft((previous) =>
              previous
                ? updateConfig(previous, (current) => {
                    current.basicSettings.socks =
                      current.basicSettings.socks.filter(
                        (item) => item.id !== selectedSocksId,
                      );
                  })
                : previous,
            );
          }}
          className="rounded-md border border-rose-400/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-100 transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Удалить
        </button>
      </ActionStrip>

      <MobileCardList title="SOCKS-профили" hint="Телефонный режим">
        {draft.basicSettings.socks.length > 0 ? (
          draft.basicSettings.socks.map((item) => {
            const selected = item.id === selectedSocksId;

            return (
              <MobileCard key={item.id} tone={selected ? "accent" : "default"}>
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-white">
                      {item.id}
                    </p>
                    <p className="mt-1 text-xs leading-5 text-slate-400">
                      {resolveNodeLabel(draft, item.nodeId)}
                    </p>
                  </div>
                  {selected ? <SelectedPill /> : null}
                </div>

                <div className="mt-3">
                  <MobileCardGrid>
                    <MobileCardField
                      label="Нода"
                      value={resolveNodeLabel(draft, item.nodeId)}
                    />
                    <MobileCardField
                      label="Порты"
                      value={`${item.port}${item.httpPort ? ` / HTTP ${item.httpPort}` : ""}`}
                    />
                    <MobileCardField
                      label="Состояние"
                      value={item.enabled ? "включён" : "выключен"}
                    />
                  </MobileCardGrid>
                </div>

                <div className="mt-3">
                  <MobileSelectButton
                    selected={selected}
                    onClick={() => setSelectedSocksId(item.id)}
                    label="Открыть профиль"
                    selectedLabel="Профиль открыт"
                  />
                </div>
              </MobileCard>
            );
          })
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">
              SOCKS-профили пока не заданы.
            </p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "id", label: "Профиль" },
            { key: "node", label: "Нода" },
            { key: "ports", label: "Порты" },
            { key: "state", label: "Состояние" },
          ]}
        >
          {draft.basicSettings.socks.length > 0 ? (
            draft.basicSettings.socks.map((item) => (
              <tr
                key={item.id}
                className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                  item.id === selectedSocksId
                    ? "bg-[var(--vectra-accent-soft)] ring-1 ring-[var(--vectra-line-strong)] ring-inset"
                    : ""
                }`}
                onClick={() => setSelectedSocksId(item.id)}
              >
                <td className="px-3 py-2 font-medium text-white">
                  <div className="flex items-center gap-2">
                    <span>{item.id}</span>
                    {item.id === selectedSocksId ? <SelectedPill /> : null}
                  </div>
                </td>
                <td className="px-3 py-2">
                  {resolveNodeLabel(draft, item.nodeId)}
                </td>
                <td className="px-3 py-2">
                  {item.port}
                  {item.httpPort ? ` / HTTP ${item.httpPort}` : ""}
                </td>
                <td className="px-3 py-2">
                  {item.enabled ? "включён" : "выключен"}
                </td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={4}>
              SOCKS-профили пока не заданы.
            </DataTableEmpty>
          )}
        </DataTable>
      </div>

      {selectedSocks ? (
        <SectionBox title="Редактор выбранного SOCKS-профиля">
          <FieldGrid>
            <TextControl
              label="ID"
              value={selectedSocks.id}
              onChange={(value) =>
                updateSocksField(
                  setDraft,
                  selectedSocks.id,
                  "id",
                  value ?? selectedSocks.id,
                )
              }
            />
            <SelectControl
              label="Нода"
              value={selectedSocks.nodeId}
              options={selectedNodeOptions}
              onChange={(value) =>
                updateSocksField(
                  setDraft,
                  selectedSocks.id,
                  "nodeId",
                  value ?? "",
                )
              }
            />
            <NumberControl
              label="SOCKS port"
              value={selectedSocks.port}
              onChange={(value) =>
                updateSocksField(
                  setDraft,
                  selectedSocks.id,
                  "port",
                  typeof value === "number" ? value : selectedSocks.port,
                )
              }
            />
            <NumberControl
              label="HTTP port"
              value={selectedSocks.httpPort}
              optional
              onChange={(value) =>
                updateSocksField(setDraft, selectedSocks.id, "httpPort", value)
              }
            />
            <BooleanControl
              label="Включён"
              value={selectedSocks.enabled}
              onChange={(value) =>
                updateSocksField(setDraft, selectedSocks.id, "enabled", value)
              }
            />
            <BooleanControl
              label="Bind localhost"
              value={selectedSocks.bindLocal}
              onChange={(value) =>
                updateSocksField(setDraft, selectedSocks.id, "bindLocal", value)
              }
            />
            <TextAreaControl
              label="Backup node IDs"
              rows={4}
              value={selectedSocks.autoswitchBackupNodeIds}
              onChange={(value) =>
                updateSocksField(
                  setDraft,
                  selectedSocks.id,
                  "autoswitchBackupNodeIds",
                  value,
                )
              }
            />
          </FieldGrid>
        </SectionBox>
      ) : null}
    </div>
  );
}

function DnsTabSection({
  draft,
  surface,
  setDraft,
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
}) {
  return (
    <div className="space-y-4">
      <FieldGrid>
        <SelectControl
          label="Стратегия прямого DNS"
          value={draft.basicSettings.dns.directQueryStrategy}
          options={dnsStrategyOptions}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.directQueryStrategy",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.dns.directQueryStrategy")}
        />
        <SelectControl
          label="Протокол удалённого DNS"
          value={draft.basicSettings.dns.remoteDnsProtocol}
          options={remoteDnsProtocolOptions}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.remoteDnsProtocol",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.dns.remoteDnsProtocol")}
        />
        <TextControl
          label="Удалённый DNS"
          value={draft.basicSettings.dns.remoteDns}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.remoteDns",
              value ?? "",
            )
          }
          diff={getDiff(surface, "basicSettings.dns.remoteDns")}
        />
        <TextControl
          label="URL DoH"
          value={draft.basicSettings.dns.remoteDnsDoh}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.remoteDnsDoh",
              value ?? "",
            )
          }
          diff={getDiff(surface, "basicSettings.dns.remoteDnsDoh")}
        />
        <TextControl
          label="EDNS Client IP"
          value={draft.basicSettings.dns.remoteDnsClientIp}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.remoteDnsClientIp",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.dns.remoteDnsClientIp")}
        />
        <SelectControl
          label="Маршрут DNS"
          value={draft.basicSettings.dns.remoteDnsDetour}
          options={detourOptions}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.remoteDnsDetour",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.dns.remoteDnsDetour")}
        />
        <BooleanControl
          label="Использовать FakeDNS"
          value={draft.basicSettings.dns.remoteFakeDns}
          onChange={(value) =>
            updatePathValue(setDraft, "basicSettings.dns.remoteFakeDns", value)
          }
          diff={getDiff(surface, "basicSettings.dns.remoteFakeDns")}
        />
        <SelectControl
          label="Стратегия удалённого DNS"
          value={draft.basicSettings.dns.remoteDnsQueryStrategy}
          options={dnsStrategyOptions}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.dns.remoteDnsQueryStrategy",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.dns.remoteDnsQueryStrategy")}
        />
        <BooleanControl
          label="Перехватывать DNS"
          value={draft.basicSettings.dns.dnsRedirect}
          onChange={(value) =>
            updatePathValue(setDraft, "basicSettings.dns.dnsRedirect", value)
          }
          diff={getDiff(surface, "basicSettings.dns.dnsRedirect")}
        />
        <TextAreaControl
          label="DNS hosts"
          rows={6}
          value={draft.basicSettings.dns.dnsHosts}
          onChange={(value) =>
            updatePathValue(setDraft, "basicSettings.dns.dnsHosts", value)
          }
          diff={getDiff(surface, "basicSettings.dns.dnsHosts")}
        />
      </FieldGrid>
    </div>
  );
}

function LogTabSection({
  draft,
  surface,
  setDraft,
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
}) {
  return (
    <FieldGrid>
      <BooleanControl
        label="Логировать ноду"
        value={draft.basicSettings.log.enableNodeLog}
        onChange={(value) =>
          updatePathValue(setDraft, "basicSettings.log.enableNodeLog", value)
        }
        diff={getDiff(surface, "basicSettings.log.enableNodeLog")}
      />
      <SelectControl
        label="Уровень логирования"
        value={draft.basicSettings.log.level}
        options={logLevelOptions}
        onChange={(value) =>
          updatePathValue(setDraft, "basicSettings.log.level", value)
        }
        diff={getDiff(surface, "basicSettings.log.level")}
      />
    </FieldGrid>
  );
}

function MaintainTabSection({
  draft,
  surface,
  setDraft,
  routerId,
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  routerId: string;
}) {
  return (
    <div className="space-y-4">
      <FieldGrid>
        <TextAreaControl
          label="Пути резервных копий"
          rows={6}
          value={draft.basicSettings.maintenance.backupPaths}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "basicSettings.maintenance.backupPaths",
              value,
            )
          }
          diff={getDiff(surface, "basicSettings.maintenance.backupPaths")}
        />
      </FieldGrid>

      <ActionStrip justify="start">
        <span className="text-sm text-slate-300">
          Скрытых полей: {surface.maskedFields.length}. Для нестандартных правок
          держите резервный путь через JSON-редактор.
        </span>
        <Link
          href={`/drafts?routerId=${routerId}`}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm font-medium text-slate-200 transition hover:border-white/20 hover:text-white"
        >
          Открыть экспертный JSON
        </Link>
      </ActionStrip>
    </div>
  );
}

function NodeListSection({
  draft,
  surface,
  selectedNode,
  selectedNodeId,
  setSelectedNodeId,
  setDraft,
  setNote,
  savePending,
  queuePending,
  canPersistNormalizedRuntime,
  handlePersistNormalizedRuntime,
  passwallVersion,
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedNode: DraftConfigInput["nodes"][number] | null;
  selectedNodeId: string | null;
  setSelectedNodeId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  setNote: Dispatch<SetStateAction<string>>;
  savePending: boolean;
  queuePending: boolean;
  canPersistNormalizedRuntime: boolean;
  handlePersistNormalizedRuntime: () => Promise<void>;
  passwallVersion: string | null;
}) {
  const editableNodeIds = new Set(surface.subscriptionRuntime.editableNodeIds);
  const editableNodes = draft.nodes.filter((node) =>
    editableNodeIds.has(node.id),
  );
  const managedNodeCount = surface.subscriptionRuntime.managedNodes.length;
  const panelOnlyCount = surface.subscriptionRuntime.panelOnlyNodes.length;
  const cleanupNodeIds = surface.subscriptionRuntime.cleanupNodes.map(
    (node) => node.id,
  );
  const orphanNodeIds = surface.subscriptionRuntime.orphanNodes.map(
    (node) => node.id,
  );
  const cleanupCount = cleanupNodeIds.length;
  const orphanCount = orphanNodeIds.length;
  const routerOnlyNodeIds = [...new Set([...cleanupNodeIds, ...orphanNodeIds])];
  const routerOnlyCount = cleanupCount + orphanCount;
  const needsAttentionCount = panelOnlyCount + routerOnlyCount;
  const hasPanelOnlyNodes = panelOnlyCount > 0;
  const hasCleanupNodes = cleanupCount > 0;
  const hasOrphanNodes = orphanNodeIds.length > 0;
  const mkcpMtuGate = getPasswallFeatureGate(
    passwallVersion,
    PASSWALL_FEATURE_MIN_VERSIONS.xrayMkcpMtu,
  );
  const tlsPinSha256Gate = getPasswallFeatureGate(
    passwallVersion,
    PASSWALL_FEATURE_MIN_VERSIONS.xrayTlsPinSha256,
  );
  const cleanupTitle =
    routerOnlyCount > 0
      ? "Список нод ещё не выровнен"
      : "В панели остался старый список нод";
  const cleanupDescription =
    routerOnlyCount > 0
      ? "Ниже есть ноды, которые всё ещё лежат на роутере, хотя текущий preview подписки их уже не отдаёт или они не помечены как managed. Кнопка подготовит чистый draft по текущему live-списку; потом останется нажать «Сохранить» или «Сохранить и применить»."
      : "На роутере список уже актуальный. Кнопка ниже создаст в панели новый черновик по этому live-списку и не применит его сама.";
  const cleanupButtonLabel =
    routerOnlyCount > 0
      ? "Подготовить чистый draft"
      : savePending
        ? "Сохраняю..."
        : "Синхронизировать панель с роутером";

  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-3">
        <InlineStateCard
          eyebrow="Редактируете"
          title={`${editableNodes.length}`}
          description="Ручные ноды, которыми можно управлять и сохранять из панели."
        />
        <InlineStateCard
          eyebrow="На роутере"
          title={`${managedNodeCount}`}
          description="Ноды, которые PassWall сейчас реально держит по подписке на этом роутере."
        />
        <InlineStateCard
          eyebrow="Разобрать"
          title={`${needsAttentionCount}`}
          tone={needsAttentionCount > 0 ? "warning" : "good"}
          description={
            needsAttentionCount > 0
              ? "Лишние ноды только в панели или всё ещё импортированы на роутере. Основной рабочий список выше."
              : "Лишних нод сейчас нет: панель и live-роутер по списку подписки совпадают."
          }
        />
      </div>

      <RuntimeNodeTable
        title="Ноды, которые сейчас пришли по подписке"
        description="Это реальный список нод с живого роутера. Именно на него нужно ориентироваться как на основной рабочий список PassWall."
        nodes={surface.subscriptionRuntime.managedNodes}
        emptyText="Сейчас роутер не показывает нод из подписки."
      />

      {hasPanelOnlyNodes || routerOnlyCount > 0 ? (
        <ActionGroup
          eyebrow="Синхронизация списка нод"
          title={cleanupTitle}
          tone={routerOnlyCount > 0 ? "warning" : "default"}
          description={cleanupDescription}
        >
          <div className="grid gap-2 md:grid-cols-2">
            {hasPanelOnlyNodes ? (
              <InlineStateCard
                eyebrow="Лишнее только в панели"
                title={`${panelOnlyCount} нод`}
                tone="warning"
                description="Эти ноды когда-то были сохранены в панели, но сейчас уже не подтверждаются live-списком с роутера."
              />
            ) : null}
            {hasCleanupNodes ? (
              <InlineStateCard
                eyebrow="Лишнее по preview подписки"
                title={`${cleanupCount} нод`}
                tone="warning"
                description="Эти ноды всё ещё импортированы на роутере, но текущий preview PassWall с этого же роутера их уже не отдаёт."
              />
            ) : null}
            {hasOrphanNodes ? (
              <InlineStateCard
                eyebrow="Лишнее без managed-метки"
                title={`${orphanCount} нод`}
                tone="warning"
                description="Эти ноды лежат в группе подписки на роутере, но не отмечены как текущий managed-результат подписки."
              />
            ) : null}
          </div>
          <ActionStrip justify="start" dense>
            <button
              type="button"
              disabled={
                routerOnlyCount > 0
                  ? savePending || queuePending
                  : !canPersistNormalizedRuntime || savePending || queuePending
              }
              onClick={() => {
                if (routerOnlyCount > 0) {
                  setDraft(pruneNodes(draft, routerOnlyNodeIds));
                  setNote((current) =>
                    current.trim().length > 0
                      ? current
                      : "Привести draft к текущему live-списку нод с роутера",
                  );
                  return;
                }

                void handlePersistNormalizedRuntime();
              }}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
            >
              {cleanupButtonLabel}
            </button>
          </ActionStrip>
          <p className="text-xs leading-5 text-slate-400">
            {routerOnlyCount > 0
              ? "Кнопка ничего не применяет сама: она только готовит чистый draft. После этого используйте обычное «Сохранить» или «Сохранить и применить»."
              : "Это действие только создаст новую ревизию-черновик в панели на основе текущего live-списка с роутера. Apply нужно запускать отдельно."}
          </p>
        </ActionGroup>
      ) : null}

      {needsAttentionCount > 0 ? (
        <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
          <summary className="min-h-11 cursor-pointer list-none">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <p className="vectra-kicker text-amber-200">
                  Подробности cleanup
                </p>
                <p className="mt-2 text-sm font-medium text-white">
                  Лишние ноды в панели или на роутере: {needsAttentionCount}
                </p>
                <p className="mt-1 text-sm leading-6 text-slate-300">
                  Этот блок нужен только для разбора лишних записей. Основной
                  рабочий список нод показан выше.
                </p>
              </div>
              <span className="text-xs text-slate-400">Открыть детали</span>
            </div>
          </summary>
          <div className="mt-4 space-y-4">
            {hasPanelOnlyNodes ? (
              <RuntimeNodeTable
                title="Лишние ноды только в панели"
                description="Они сохранились в draft или истории панели, но сейчас не подтверждаются live-списком нод с роутера."
                nodes={surface.subscriptionRuntime.panelOnlyNodes}
                emptyText="Лишних нод только в панели сейчас нет."
              />
            ) : null}

            {hasCleanupNodes ? (
              <RuntimeNodeTable
                title="Лишние ноды по текущему preview подписки"
                description="Они ещё импортированы на роутере, но текущий preview PassWall с этого же роутера их уже не отдаёт."
                nodes={surface.subscriptionRuntime.cleanupNodes}
                emptyText="Лишних нод по preview подписки сейчас нет."
              />
            ) : null}

            {hasOrphanNodes ? (
              <RuntimeNodeTable
                title="Лишние локальные ноды без managed-метки"
                description="Они реально лежат в подписочной группе на роутере, но не отмечены как текущий managed-результат подписки."
                nodes={surface.subscriptionRuntime.orphanNodes}
                emptyText="Лишних локальных нод на роутере сейчас нет."
              />
            ) : null}
          </div>
        </details>
      ) : null}

      <ActionStrip justify="start">
        <button
          type="button"
          onClick={() => {
            const next = addNode(draft);
            setDraft(next);
            setSelectedNodeId(next.nodes[next.nodes.length - 1]?.id ?? null);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white"
        >
          Добавить
        </button>
        <button
          type="button"
          disabled={!selectedNode}
          onClick={() => {
            if (!selectedNode) {
              return;
            }
            const index = draft.nodes.findIndex(
              (node) => node.id === selectedNode.id,
            );
            const next = duplicateNode(draft, index);
            setDraft(next);
            setSelectedNodeId(next.nodes[index + 1]?.id ?? null);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          Дублировать
        </button>
        <button
          type="button"
          disabled={!selectedNode}
          onClick={() => {
            if (!selectedNode) {
              return;
            }
            const index = draft.nodes.findIndex(
              (node) => node.id === selectedNode.id,
            );
            const next = deleteNode(draft, index);
            setDraft(next);
            setSelectedNodeId(next.nodes[0]?.id ?? null);
          }}
          className="rounded-md border border-rose-400/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-100 transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Удалить
        </button>
        <button
          type="button"
          disabled={!selectedNode}
          onClick={() => {
            if (!selectedNode) {
              return;
            }
            const index = draft.nodes.findIndex(
              (node) => node.id === selectedNode.id,
            );
            setDraft(moveNodeToTop(draft, index));
            setSelectedNodeId(selectedNode.id);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          В начало
        </button>
        <button
          type="button"
          disabled={!selectedNode}
          onClick={() => {
            if (!selectedNode) {
              return;
            }
            setDraft(selectNode(draft, selectedNode.id));
          }}
          className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          Назначить выбранной
        </button>
      </ActionStrip>

      <MobileCardList title="Ручные ноды" hint="Телефонный режим">
        {editableNodes.length > 0 ? (
          editableNodes.map((node) => {
            const selected = node.id === selectedNodeId;
            const state = node.enabled
              ? draft.basicSettings.main.selectedNodeId === node.id
                ? "selected"
                : "enabled"
              : "disabled";

            return (
              <MobileCard key={node.id} tone={selected ? "accent" : "default"}>
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-white">
                      {node.label}
                    </p>
                    <p className="mt-1 text-xs leading-5 break-all text-slate-400">
                      {node.id}
                    </p>
                  </div>
                  {selected ? <SelectedPill /> : null}
                </div>

                <div className="mt-3">
                  <MobileCardGrid>
                    <MobileCardField label="Протокол" value={node.protocol} />
                    <MobileCardField
                      label="Endpoint"
                      value={`${node.address ?? "n/a"}${node.port ? `:${node.port}` : ""}`}
                    />
                    <MobileCardField label="Состояние" value={state} />
                    <MobileCardField label="Группа" value={node.group || "—"} />
                  </MobileCardGrid>
                </div>

                <div className="mt-3">
                  <MobileSelectButton
                    selected={selected}
                    onClick={() => setSelectedNodeId(node.id)}
                    label="Открыть ноду"
                    selectedLabel="Нода открыта"
                  />
                </div>
              </MobileCard>
            );
          })
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">
              Ручных нод пока нет.
            </p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "label", label: "Нода" },
            { key: "protocol", label: "Протокол" },
            { key: "endpoint", label: "Endpoint" },
            { key: "state", label: "Состояние" },
          ]}
        >
          {editableNodes.length > 0 ? (
            editableNodes.map((node) => (
              <tr
                key={node.id}
                className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                  node.id === selectedNodeId
                    ? "bg-[var(--vectra-accent-soft)] ring-1 ring-[var(--vectra-line-strong)] ring-inset"
                    : ""
                }`}
                onClick={() => setSelectedNodeId(node.id)}
              >
                <td className="px-3 py-2">
                  <div className="flex items-center gap-2 font-medium text-white">
                    <span>{node.label}</span>
                    {node.id === selectedNodeId ? <SelectedPill /> : null}
                  </div>
                  <div className="text-xs text-slate-500">{node.id}</div>
                </td>
                <td className="px-3 py-2">{node.protocol}</td>
                <td className="px-3 py-2">
                  {node.address ?? "n/a"}
                  {node.port ? `:${node.port}` : ""}
                </td>
                <td className="px-3 py-2">
                  {node.enabled
                    ? draft.basicSettings.main.selectedNodeId === node.id
                      ? "selected"
                      : "enabled"
                    : "disabled"}
                </td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={4}>Ручных нод пока нет.</DataTableEmpty>
          )}
        </DataTable>
      </div>

      {selectedNode ? (
        <SectionBox title="Редактор выбранной ноды">
          <FieldGrid>
            <TextControl
              label="ID"
              value={selectedNode.id}
              onChange={(value) =>
                updateNodeField(
                  setDraft,
                  selectedNode.id,
                  "id",
                  value ?? selectedNode.id,
                )
              }
            />
            <TextControl
              label="Label"
              value={selectedNode.label}
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "label", value ?? "")
              }
            />
            <SelectControl
              label="Протокол"
              value={selectedNode.protocol}
              options={nodeProtocolOptions}
              onChange={(value) =>
                updateNodeField(
                  setDraft,
                  selectedNode.id,
                  "protocol",
                  value ?? selectedNode.protocol,
                )
              }
            />
            <TextControl
              label="Группа"
              value={selectedNode.group}
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "group", value ?? "")
              }
            />
            <TextControl
              label="Адрес"
              value={selectedNode.address}
              optional
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "address", value)
              }
            />
            <NumberControl
              label="Port"
              value={selectedNode.port}
              optional
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "port", value)
              }
            />
            <TextControl
              label="Username"
              value={selectedNode.username}
              optional
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "username", value)
              }
            />
            <TextControl
              label="Password"
              value={selectedNode.password}
              optional
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "password", value)
              }
            />
            <SelectControl
              label="Transport"
              value={selectedNode.transport}
              optional
              options={transportOptions}
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "transport", value)
              }
            />
            <BooleanControl
              label="TLS"
              value={Boolean(selectedNode.tls)}
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "tls", value)
              }
            />
            <BooleanControl
              label="Включена"
              value={selectedNode.enabled}
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "enabled", value)
              }
            />
            <TextAreaControl
              label="Tags"
              rows={4}
              value={selectedNode.tags}
              onChange={(value) =>
                updateNodeField(setDraft, selectedNode.id, "tags", value)
              }
            />
            <BooleanControl
              label="Mux"
              value={getExtraBoolean(selectedNode.extras, "mux")}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedNode.id,
                  "mux",
                  boolExtra(value),
                )
              }
            />
            <NumberControl
              label="Mux concurrency"
              value={getExtraNumber(selectedNode.extras, "mux_concurrency")}
              optional
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedNode.id,
                  "mux_concurrency",
                  value === "" ? undefined : value,
                )
              }
            />
            <NumberControl
              label="XUDP concurrency"
              value={getExtraNumber(selectedNode.extras, "xudp_concurrency")}
              optional
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedNode.id,
                  "xudp_concurrency",
                  value === "" ? undefined : value,
                )
              }
            />
            <TextControl
              label="Packet encoding"
              value={getExtraString(selectedNode.extras, "packet_encoding")}
              optional
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedNode.id,
                  "packet_encoding",
                  value,
                )
              }
            />
            <NumberControl
              label="mKCP MTU"
              value={getExtraNumber(selectedNode.extras, "mkcp_mtu")}
              optional
              disabled={!mkcpMtuGate.supported}
              hint={mkcpMtuGate.reason ?? undefined}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedNode.id,
                  "mkcp_mtu",
                  value === "" ? undefined : value,
                )
              }
            />
            <TextControl
              label="TLS pinSHA256"
              value={getExtraString(selectedNode.extras, "tls_pinSHA256")}
              optional
              disabled={!tlsPinSha256Gate.supported}
              hint={tlsPinSha256Gate.reason ?? undefined}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedNode.id,
                  "tls_pinSHA256",
                  value,
                )
              }
            />
          </FieldGrid>
          <p className="mt-3 text-xs leading-5 text-slate-400">
            Эти поля пишутся в PassWall node extras и нужны для
            Xray/Mux/XUDP/KCP/TLS тюнинга, включая Discord voice. Новые поля
            остаются неактивными на старых версиях PassWall2, а остальные
            импортированные extras не теряются при apply и видны в предпросмотре
            технических UCI-команд.
          </p>
        </SectionBox>
      ) : null}
    </div>
  );
}

function SubscriptionSection({
  routerId,
  draft,
  surface,
  routerReachable,
  selectedSubscription,
  selectedSubscriptionId,
  setSelectedSubscriptionId,
  setDraft,
  canRunJobs,
  passwallVersion,
}: {
  routerId: string;
  draft: DraftConfigInput;
  surface: EditorSurface;
  routerReachable: boolean;
  selectedSubscription:
    | DraftConfigInput["subscriptions"]["items"][number]
    | null;
  selectedSubscriptionId: string | null;
  setSelectedSubscriptionId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  canRunJobs: boolean;
  passwallVersion: string | null;
}) {
  const router = useRouter();
  const utils = api.useUtils();
  const inspectMutation = api.update.queueSubscriptionsInspect.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });
  const refreshMutation = api.update.queueSubscriptionsRefresh.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });
  const autoInspectStartedRef = useRef(false);

  const editableSubscriptionIds = new Set(
    surface.subscriptionRuntime.editableSubscriptionIds,
  );
  const editableSubscriptions = draft.subscriptions.items.filter((item) =>
    editableSubscriptionIds.has(item.id),
  );
  const subscriptionDomainResolverGate = getPasswallFeatureGate(
    passwallVersion,
    PASSWALL_FEATURE_MIN_VERSIONS.subscriptionDomainResolver,
  );
  const subscriptionItemDomainStrategyOptionsForRouter =
    buildSubscriptionItemDomainStrategyOptions(passwallVersion);
  const previewNodeCount = surface.subscriptionRuntime.previews.reduce(
    (sum, preview) => sum + (preview.resolvedPayloadNodeCount ?? 0),
    0,
  );
  const cleanupPreviewCount = surface.subscriptionRuntime.cleanupNodes.length;
  const orphanRuntimeCount = surface.subscriptionRuntime.orphanNodes.length;
  const previewState = surface.subscriptionRuntime.previewState.status;
  const previewAvailable = previewState === "fresh";
  const needsAttentionCount =
    surface.subscriptionRuntime.panelOnlyNodes.length +
    cleanupPreviewCount +
    orphanRuntimeCount;
  const previewTitle = previewAvailable ? `${previewNodeCount}` : "недоступно";
  const previewDescription =
    previewState === "fresh"
      ? "Сколько нод роутер PassWall сейчас реально получает по текущей live подписке."
      : previewState === "pending"
        ? "Роутер сейчас выполняет безопасный preview. Как только job завершится, здесь появится реальный count."
        : previewState === "stale"
          ? "Старый preview больше не подходит к текущей live подписке. Панель ждёт новый безопасный preview с роутера."
          : previewState === "failed"
            ? "Последняя попытка preview на роутере завершилась ошибкой. Панель не подставляет server-side эвристику."
            : "Для текущей live подписки ещё нет свежего router preview. Панель не делает вид, что знает реальный count.";
  const previewSummary =
    previewState === "fresh"
      ? cleanupPreviewCount > 0
        ? `Preview с роутера показывает меньше нод, чем ещё импортировано локально. Это drift для cleanup, а не ошибка payload.`
        : "Router preview свежий: count берётся с самого роутера в контуре PassWall, а не с серверной эвристики."
      : previewState === "pending"
        ? "Роутер сейчас сам перепроверяет, сколько нод реально даёт текущая подписка."
        : previewState === "stale"
          ? "Последний preview устарел или относится к другому digest live-подписки, поэтому count сейчас не используется."
          : previewState === "failed"
            ? "Preview на роутере не удалось получить. Панель честно показывает только live runtime и статус ошибки."
            : "Preview ещё не запускался для текущей live-подписки. Ниже остаётся только фактический live runtime роутера.";

  useEffect(() => {
    if (!routerReachable || !canRunJobs) {
      return;
    }
    if (!["missing", "stale", "failed"].includes(previewState)) {
      return;
    }
    if (inspectMutation.isPending || autoInspectStartedRef.current) {
      return;
    }

    autoInspectStartedRef.current = true;
    inspectMutation.mutate({ routerId });
  }, [canRunJobs, inspectMutation, previewState, routerId, routerReachable]);

  return (
    <div className="space-y-4">
      <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
        <summary className="min-h-11 cursor-pointer list-none">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <p className="vectra-kicker text-slate-500">
                Техническая диагностика
              </p>
              <p className="mt-2 text-sm font-medium text-white">
                Router preview подписки, live runtime и панель
              </p>
              <p className="mt-1 text-sm leading-6 text-slate-300">
                {previewSummary}
              </p>
            </div>
            <span className="text-xs text-slate-400">
              {surface.subscriptionRuntime.previews.length} подписок
            </span>
          </div>
        </summary>

        <div className="mt-4 space-y-4">
          <div className="grid gap-3 md:grid-cols-3">
            <InlineStateCard
              eyebrow="Подписка сейчас отдаёт"
              title={previewTitle}
              description={previewDescription}
            />
            <InlineStateCard
              eyebrow="На роутере импортировано"
              title={`${surface.subscriptionRuntime.managedNodes.length}`}
              description="Сколько подписочных нод PassWall сейчас реально держит на роутере."
            />
            <InlineStateCard
              eyebrow="Разобрать"
              title={`${needsAttentionCount}`}
              tone={needsAttentionCount > 0 ? "warning" : "good"}
              description={
                needsAttentionCount > 0
                  ? "Лишние ноды только в панели или всё ещё импортированы на роутере сверх текущего preview."
                  : "Лишних подписочных нод сейчас нет."
              }
            />
          </div>

          <SubscriptionPreviewTable
            previews={surface.subscriptionRuntime.previews}
          />
        </div>
      </details>

      <FieldGrid>
        <SelectControl
          label="Режим фильтрации"
          value={draft.subscriptions.filterKeywordMode}
          options={subscriptionFilterOptions}
          onChange={(value) =>
            updatePathValue(setDraft, "subscriptions.filterKeywordMode", value)
          }
          diff={getDiff(surface, "subscriptions.filterKeywordMode")}
        />
        <SelectControl
          label="Стратегия доменов"
          value={draft.subscriptions.domainStrategy}
          options={domainStrategyOptions}
          onChange={(value) =>
            updatePathValue(setDraft, "subscriptions.domainStrategy", value)
          }
          diff={getDiff(surface, "subscriptions.domainStrategy")}
        />
        <TextAreaControl
          label="Discard list"
          rows={5}
          value={draft.subscriptions.discardList}
          onChange={(value) =>
            updatePathValue(setDraft, "subscriptions.discardList", value)
          }
          diff={getDiff(surface, "subscriptions.discardList")}
        />
        <TextAreaControl
          label="Keep list"
          rows={5}
          value={draft.subscriptions.keepList}
          onChange={(value) =>
            updatePathValue(setDraft, "subscriptions.keepList", value)
          }
          diff={getDiff(surface, "subscriptions.keepList")}
        />
        <TextControl
          label="Pref. Shadowsocks"
          value={draft.subscriptions.typePreferences.shadowsocks}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "subscriptions.typePreferences.shadowsocks",
              value,
            )
          }
        />
        <TextControl
          label="Pref. Trojan"
          value={draft.subscriptions.typePreferences.trojan}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "subscriptions.typePreferences.trojan",
              value,
            )
          }
        />
        <TextControl
          label="Pref. VMess"
          value={draft.subscriptions.typePreferences.vmess}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "subscriptions.typePreferences.vmess",
              value,
            )
          }
        />
        <TextControl
          label="Pref. VLESS"
          value={draft.subscriptions.typePreferences.vless}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "subscriptions.typePreferences.vless",
              value,
            )
          }
        />
        <TextControl
          label="Pref. Hysteria2"
          value={draft.subscriptions.typePreferences.hysteria2}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "subscriptions.typePreferences.hysteria2",
              value,
            )
          }
        />
      </FieldGrid>

      <ActionStrip justify="start">
        <button
          type="button"
          disabled={!canRunJobs || inspectMutation.isPending}
          onClick={() => inspectMutation.mutate({ routerId })}
          className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {inspectMutation.isPending
            ? "Проверяю подписку..."
            : "Проверить подписку на роутере"}
        </button>
        <button
          type="button"
          disabled={!canRunJobs || refreshMutation.isPending}
          onClick={() => refreshMutation.mutate({ routerId })}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          {refreshMutation.isPending
            ? "Ставлю обновление..."
            : "Обновить подписки сейчас"}
        </button>
        <button
          type="button"
          onClick={() => {
            const next = addSubscription(draft);
            setDraft(next);
            setSelectedSubscriptionId(
              next.subscriptions.items[next.subscriptions.items.length - 1]
                ?.id ?? null,
            );
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white"
        >
          Добавить
        </button>
        <button
          type="button"
          disabled={!selectedSubscription}
          onClick={() => {
            if (!selectedSubscription) {
              return;
            }
            const index = draft.subscriptions.items.findIndex(
              (item) => item.id === selectedSubscription.id,
            );
            const next = deleteSubscription(draft, index);
            setDraft(next);
            setSelectedSubscriptionId(next.subscriptions.items[0]?.id ?? null);
          }}
          className="rounded-md border border-rose-400/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-100 transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Удалить
        </button>
        <button
          type="button"
          disabled={!selectedSubscription}
          onClick={() => {
            if (!selectedSubscription) {
              return;
            }
            const index = draft.subscriptions.items.findIndex(
              (item) => item.id === selectedSubscription.id,
            );
            setDraft(moveSubscriptionToTop(draft, index));
            setSelectedSubscriptionId(selectedSubscription.id);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          В начало
        </button>
      </ActionStrip>

      <MobileCardList title="Подписки" hint="Телефонный режим">
        {editableSubscriptions.length > 0 ? (
          editableSubscriptions.map((item) => {
            const selected = item.id === selectedSubscriptionId;

            return (
              <MobileCard key={item.id} tone={selected ? "accent" : "default"}>
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-white">
                      {item.remark}
                    </p>
                    <p className="mt-1 text-xs leading-5 break-all text-slate-400">
                      {item.id}
                    </p>
                  </div>
                  {selected ? <SelectedPill /> : null}
                </div>

                <div className="mt-3">
                  <MobileCardGrid columns={1}>
                    <MobileCardField label="URL" value={item.url} mono />
                    <MobileCardField
                      label="Режим"
                      value={
                        item.addMode === "1"
                          ? "обновлять существующие"
                          : "полное чтение"
                      }
                    />
                    <MobileCardField
                      label="Состояние"
                      value={item.enabled ? "enabled" : "disabled"}
                    />
                  </MobileCardGrid>
                </div>

                <div className="mt-3">
                  <MobileSelectButton
                    selected={selected}
                    onClick={() => setSelectedSubscriptionId(item.id)}
                    label="Открыть подписку"
                    selectedLabel="Подписка открыта"
                  />
                </div>
              </MobileCard>
            );
          })
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">
              Подписок пока нет.
            </p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "remark", label: "Подписка" },
            { key: "url", label: "URL" },
            { key: "mode", label: "Режим" },
            { key: "state", label: "Состояние" },
          ]}
        >
          {editableSubscriptions.length > 0 ? (
            editableSubscriptions.map((item) => (
              <tr
                key={item.id}
                className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                  item.id === selectedSubscriptionId
                    ? "bg-[var(--vectra-accent-soft)] ring-1 ring-[var(--vectra-line-strong)] ring-inset"
                    : ""
                }`}
                onClick={() => setSelectedSubscriptionId(item.id)}
              >
                <td className="px-3 py-2">
                  <div className="flex items-center gap-2 font-medium text-white">
                    <span>{item.remark}</span>
                    {item.id === selectedSubscriptionId ? (
                      <SelectedPill />
                    ) : null}
                  </div>
                  <div className="text-xs text-slate-500">{item.id}</div>
                </td>
                <td className="px-3 py-2">{item.url}</td>
                <td className="px-3 py-2">
                  {item.addMode === "1"
                    ? "обновлять существующие"
                    : "полное чтение"}
                </td>
                <td className="px-3 py-2">
                  {item.enabled ? "enabled" : "disabled"}
                </td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={4}>Подписок пока нет.</DataTableEmpty>
          )}
        </DataTable>
      </div>

      {selectedSubscription ? (
        <SectionBox title="Редактор выбранной подписки">
          <FieldGrid>
            <TextControl
              label="ID"
              value={selectedSubscription.id}
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "id",
                  value ?? selectedSubscription.id,
                )
              }
            />
            <TextControl
              label="Remark"
              value={selectedSubscription.remark}
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "remark",
                  value ?? "",
                )
              }
            />
            <TextControl
              label="URL"
              value={selectedSubscription.url}
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "url",
                  value ?? "",
                )
              }
            />
            <SelectControl
              label="Add mode"
              value={selectedSubscription.addMode}
              options={subscriptionAddModeOptions}
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "addMode",
                  value ?? selectedSubscription.addMode,
                )
              }
            />
            <BooleanControl
              label="Включена"
              value={selectedSubscription.enabled}
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "enabled",
                  value,
                )
              }
            />
            <SelectControl
              label="Domain DNS Resolve"
              value={getExtraString(
                selectedSubscription.extras,
                "domain_resolver",
                "",
              )}
              options={subscriptionDomainResolverOptions}
              disabled={!subscriptionDomainResolverGate.supported}
              hint={subscriptionDomainResolverGate.reason ?? undefined}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "domain_resolver",
                  value,
                )
              }
            />
            <TextControl
              label="Domain resolver DNS"
              value={getExtraString(
                selectedSubscription.extras,
                "domain_resolver_dns",
              )}
              optional
              disabled={!subscriptionDomainResolverGate.supported}
              hint={subscriptionDomainResolverGate.reason ?? undefined}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "domain_resolver_dns",
                  value,
                )
              }
            />
            <TextControl
              label="Domain resolver DoH"
              value={getExtraString(
                selectedSubscription.extras,
                "domain_resolver_dns_https",
              )}
              optional
              disabled={!subscriptionDomainResolverGate.supported}
              hint={subscriptionDomainResolverGate.reason ?? undefined}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "domain_resolver_dns_https",
                  value,
                )
              }
            />
            <TextControl
              label="Remaining traffic"
              value={selectedSubscription.metadata.remainingTraffic}
              optional
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "metadata.remainingTraffic",
                  value,
                )
              }
            />
            <TextControl
              label="Expires at"
              value={selectedSubscription.metadata.expiresAt}
              optional
              onChange={(value) =>
                updateSubscriptionField(
                  setDraft,
                  selectedSubscription.id,
                  "metadata.expiresAt",
                  value,
                )
              }
            />
            <BooleanControl
              label="allowInsecure"
              value={getExtraBoolean(
                selectedSubscription.extras,
                "allowInsecure",
              )}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "allowInsecure",
                  boolExtra(value),
                )
              }
            />
            <SelectControl
              label="Filter keyword mode"
              value={getExtraString(
                selectedSubscription.extras,
                "filter_keyword_mode",
                "5",
              )}
              options={subscriptionItemFilterOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "filter_keyword_mode",
                  value ?? "5",
                )
              }
            />
            <TextAreaControl
              label="Discard List"
              rows={4}
              value={getExtraList(
                selectedSubscription.extras,
                "filter_discard_list",
              )}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "filter_discard_list",
                  value,
                )
              }
            />
            <TextAreaControl
              label="Keep List"
              rows={4}
              value={getExtraList(
                selectedSubscription.extras,
                "filter_keep_list",
              )}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "filter_keep_list",
                  value,
                )
              }
            />
            <SelectControl
              label="Shadowsocks use type"
              value={getExtraString(
                selectedSubscription.extras,
                "ss_type",
                "global",
              )}
              options={shadowsocksTypeOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "ss_type",
                  value ?? "global",
                )
              }
            />
            <SelectControl
              label="Trojan use type"
              value={getExtraString(
                selectedSubscription.extras,
                "trojan_type",
                "global",
              )}
              options={xraySingBoxTypeOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "trojan_type",
                  value ?? "global",
                )
              }
            />
            <SelectControl
              label="VMess use type"
              value={getExtraString(
                selectedSubscription.extras,
                "vmess_type",
                "global",
              )}
              options={xraySingBoxTypeOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "vmess_type",
                  value ?? "global",
                )
              }
            />
            <SelectControl
              label="VLESS use type"
              value={getExtraString(
                selectedSubscription.extras,
                "vless_type",
                "global",
              )}
              options={xraySingBoxTypeOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "vless_type",
                  value ?? "global",
                )
              }
            />
            <SelectControl
              label="Hysteria2 use type"
              value={getExtraString(
                selectedSubscription.extras,
                "hysteria2_type",
                "global",
              )}
              options={hysteriaTypeOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "hysteria2_type",
                  value ?? "global",
                )
              }
            />
            <SelectControl
              label="Domain strategy"
              value={getExtraString(
                selectedSubscription.extras,
                "domain_strategy",
                "global",
              )}
              options={subscriptionItemDomainStrategyOptionsForRouter}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "domain_strategy",
                  value ?? "global",
                )
              }
            />
            <BooleanControl
              label="Update once on boot"
              value={getExtraBoolean(
                selectedSubscription.extras,
                "boot_update",
              )}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "boot_update",
                  boolExtra(value),
                )
              }
            />
            <BooleanControl
              label="Enable auto update"
              value={getExtraBoolean(
                selectedSubscription.extras,
                "auto_update",
              )}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "auto_update",
                  boolExtra(value),
                )
              }
            />
            {getExtraBoolean(selectedSubscription.extras, "auto_update") ? (
              <>
                <SelectControl
                  label="Update mode"
                  value={getExtraString(
                    selectedSubscription.extras,
                    "week_update",
                    "7",
                  )}
                  options={passwallWeekUpdateOptions}
                  onChange={(value) =>
                    updateSubscriptionExtra(
                      setDraft,
                      selectedSubscription.id,
                      "week_update",
                      value ?? "7",
                    )
                  }
                />
                {getExtraString(
                  selectedSubscription.extras,
                  "week_update",
                  "7",
                ) === "8" ? (
                  <SelectControl
                    label="Update interval"
                    value={getExtraString(
                      selectedSubscription.extras,
                      "interval_update",
                      "2",
                    )}
                    options={intervalHourOptions}
                    onChange={(value) =>
                      updateSubscriptionExtra(
                        setDraft,
                        selectedSubscription.id,
                        "interval_update",
                        value ?? "2",
                      )
                    }
                  />
                ) : (
                  <SelectControl
                    label="Update time"
                    value={getExtraString(
                      selectedSubscription.extras,
                      "time_update",
                      "0",
                    )}
                    options={hourOptions}
                    onChange={(value) =>
                      updateSubscriptionExtra(
                        setDraft,
                        selectedSubscription.id,
                        "time_update",
                        value ?? "0",
                      )
                    }
                  />
                )}
              </>
            ) : null}
            <SelectControl
              label="Subscribe URL access"
              value={getExtraString(
                selectedSubscription.extras,
                "access_mode",
                "",
              )}
              options={accessModeOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "access_mode",
                  value ?? "",
                )
              }
            />
            <TextControl
              label="User-Agent"
              value={getExtraString(
                selectedSubscription.extras,
                "user_agent",
                "v2rayN/9.99",
              )}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "user_agent",
                  value ?? "",
                )
              }
            />
            <SelectControl
              label="Chain Proxy"
              value={getExtraString(
                selectedSubscription.extras,
                "chain_proxy",
                "",
              )}
              options={chainProxyOptions}
              onChange={(value) =>
                updateSubscriptionExtra(
                  setDraft,
                  selectedSubscription.id,
                  "chain_proxy",
                  value ?? "",
                )
              }
            />
            {getExtraString(selectedSubscription.extras, "chain_proxy", "") ===
            "1" ? (
              <SelectControl
                label="Preproxy Node"
                value={getExtraString(
                  selectedSubscription.extras,
                  "preproxy_node",
                )}
                optional
                options={draft.nodes.map((node) => ({
                  value: node.id,
                  label: node.label,
                }))}
                onChange={(value) =>
                  updateSubscriptionExtra(
                    setDraft,
                    selectedSubscription.id,
                    "preproxy_node",
                    value,
                  )
                }
              />
            ) : null}
            {getExtraString(selectedSubscription.extras, "chain_proxy", "") ===
            "2" ? (
              <SelectControl
                label="Landing Node"
                value={getExtraString(selectedSubscription.extras, "to_node")}
                optional
                options={draft.nodes.map((node) => ({
                  value: node.id,
                  label: node.label,
                }))}
                onChange={(value) =>
                  updateSubscriptionExtra(
                    setDraft,
                    selectedSubscription.id,
                    "to_node",
                    value,
                  )
                }
              />
            ) : null}
          </FieldGrid>
        </SectionBox>
      ) : null}
    </div>
  );
}

function ShuntRulesSection({
  draft,
  selectedRule,
  selectedRuleId,
  setSelectedRuleId,
  setDraft,
  passwallVersion,
  title,
  description,
}: {
  draft: DraftConfigInput;
  selectedRule: DraftConfigInput["basicSettings"]["shuntRules"][number] | null;
  selectedRuleId: string | null;
  setSelectedRuleId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  passwallVersion: string | null;
  title: string;
  description?: string;
}) {
  const selectedShuntNode = getSelectedShuntNode(draft);
  const ruleTargetOptions = buildShuntTargetOptions(draft, true);
  const defaultTargetOptions = buildShuntTargetOptions(draft, false);
  const visibleRules = draft.basicSettings.shuntRules;
  const shuntProtocolOptionsForRouter =
    buildShuntProtocolOptions(passwallVersion);

  return (
    <div className="space-y-4">
      {selectedShuntNode ? (
        <SectionBox title="Default">
          <FieldGrid>
            <SelectControl
              label="Default node"
              value={getExtraString(
                selectedShuntNode.extras,
                "default_node",
                "_direct",
              )}
              options={defaultTargetOptions}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedShuntNode.id,
                  "default_node",
                  value ?? "_direct",
                )
              }
            />
            <SelectControl
              label="Default preproxy"
              value={getExtraString(
                selectedShuntNode.extras,
                "default_proxy_tag",
              )}
              optional
              options={draft.nodes.map((node) => ({
                value: node.id,
                label: node.label,
              }))}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedShuntNode.id,
                  "default_proxy_tag",
                  value,
                )
              }
            />
            <BooleanControl
              label="FakeDNS main switch"
              value={getExtraBoolean(selectedShuntNode.extras, "fakedns")}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedShuntNode.id,
                  "fakedns",
                  boolExtra(value),
                )
              }
            />
            <BooleanControl
              label="Default FakeDNS"
              value={getExtraBoolean(
                selectedShuntNode.extras,
                "default_fakedns",
              )}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedShuntNode.id,
                  "default_fakedns",
                  boolExtra(value),
                )
              }
            />
            <BooleanControl
              label="Direct DNS to IPSet"
              value={getExtraBoolean(
                selectedShuntNode.extras,
                "write_ipset_direct",
                true,
              )}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedShuntNode.id,
                  "write_ipset_direct",
                  boolExtra(value),
                )
              }
            />
            <BooleanControl
              label="Enable GeoIP Data Parsing"
              value={getExtraBoolean(
                selectedShuntNode.extras,
                "enable_geoview_ip",
                true,
              )}
              onChange={(value) =>
                updateNodeExtra(
                  setDraft,
                  selectedShuntNode.id,
                  "enable_geoview_ip",
                  boolExtra(value),
                )
              }
            />
          </FieldGrid>
        </SectionBox>
      ) : (
        <EmptyState text="Shunt Default и FakeDNS-настройки появятся, когда в Main будет выбрана shunt-нода. Список правил ниже можно редактировать уже сейчас." />
      )}

      <ActionStrip justify="start">
        <span className="text-sm text-slate-300">{description}</span>
        <button
          type="button"
          onClick={() => {
            const next = addShuntRule(draft);
            setDraft(next);
            setSelectedRuleId(
              next.basicSettings.shuntRules[
                next.basicSettings.shuntRules.length - 1
              ]?.id ?? null,
            );
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white"
        >
          Добавить
        </button>
        <button
          type="button"
          disabled={!selectedRule}
          onClick={() => {
            if (!selectedRule) {
              return;
            }
            const index = draft.basicSettings.shuntRules.findIndex(
              (rule) => rule.id === selectedRule.id,
            );
            const next = deleteShuntRule(draft, index);
            setDraft(next);
            setSelectedRuleId(next.basicSettings.shuntRules[0]?.id ?? null);
          }}
          className="rounded-md border border-rose-400/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-100 transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Удалить
        </button>
        <button
          type="button"
          disabled={!selectedRule}
          onClick={() => {
            if (!selectedRule) {
              return;
            }
            const index = draft.basicSettings.shuntRules.findIndex(
              (rule) => rule.id === selectedRule.id,
            );
            setDraft(moveShuntRuleToTop(draft, index));
            setSelectedRuleId(selectedRule.id);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          В начало
        </button>
      </ActionStrip>

      <MobileCardList title={title} hint="Телефонный режим">
        {visibleRules.length > 0 ? (
          visibleRules.map((rule) => {
            const selected = rule.id === selectedRuleId;

            return (
              <MobileCard key={rule.id} tone={selected ? "accent" : "default"}>
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-white">
                      {rule.label}
                    </p>
                    <p className="mt-1 text-xs leading-5 break-all text-slate-400">
                      {rule.id}
                    </p>
                  </div>
                  {selected ? <SelectedPill /> : null}
                </div>

                <div className="mt-3">
                  <MobileCardGrid>
                    <MobileCardField
                      label="Target"
                      value={formatShuntTargetLabel(draft, rule.outboundNodeId)}
                    />
                    <MobileCardField
                      label="Protocol"
                      value={formatExtraSelection(
                        rule.extras,
                        "protocol",
                        shuntProtocolOptionsForRouter,
                      )}
                    />
                    <MobileCardField
                      label="Inbound"
                      value={formatExtraSelection(
                        rule.extras,
                        "inbound",
                        shuntInboundOptions,
                      )}
                    />
                    <MobileCardField
                      label="Network"
                      value={getExtraOptionLabel(
                        rule.extras,
                        "network",
                        shuntNetworkOptions,
                        "TCP UDP",
                      )}
                    />
                    <MobileCardField
                      label="Port"
                      value={getExtraString(rule.extras, "port") ?? "не задано"}
                    />
                    <MobileCardField
                      label="FakeDNS"
                      value={
                        selectedShuntNode &&
                        getExtraBoolean(
                          selectedShuntNode.extras,
                          `${rule.id}_fakedns`,
                        )
                          ? "on"
                          : "off"
                      }
                    />
                    <MobileCardField
                      label="Preproxy"
                      value={
                        selectedShuntNode
                          ? resolveNodeLabel(
                              draft,
                              getExtraString(
                                selectedShuntNode.extras,
                                `${rule.id}_proxy_tag`,
                              ),
                            )
                          : "не задано"
                      }
                    />
                    <MobileCardField
                      label="Домены"
                      value={`${rule.domainRules.length}`}
                    />
                    <MobileCardField
                      label="IP"
                      value={`${rule.ipRules.length}`}
                    />
                  </MobileCardGrid>
                </div>

                <div className="mt-3">
                  <MobileSelectButton
                    selected={selected}
                    onClick={() => setSelectedRuleId(rule.id)}
                    label="Открыть правило"
                    selectedLabel="Правило открыто"
                  />
                </div>
              </MobileCard>
            );
          })
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">
              Shunt-правил пока нет.
            </p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "label", label: title },
            { key: "node", label: "Target" },
            { key: "protocol", label: "Protocol" },
            { key: "inbound", label: "Inbound" },
            { key: "network", label: "Network" },
            { key: "port", label: "Port" },
            { key: "fakedns", label: "FakeDNS" },
            { key: "preproxy", label: "Preproxy" },
            { key: "domains", label: "Домены" },
            { key: "ips", label: "IP" },
          ]}
        >
          {visibleRules.length > 0 ? (
            visibleRules.map((rule) => (
              <tr
                key={rule.id}
                className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                  rule.id === selectedRuleId
                    ? "bg-[var(--vectra-accent-soft)] ring-1 ring-[var(--vectra-line-strong)] ring-inset"
                    : ""
                }`}
                onClick={() => setSelectedRuleId(rule.id)}
              >
                <td className="px-3 py-2">
                  <div className="flex items-center gap-2 font-medium text-white">
                    <span>{rule.label}</span>
                    {rule.id === selectedRuleId ? <SelectedPill /> : null}
                  </div>
                  <div className="text-xs text-slate-500">{rule.id}</div>
                </td>
                <td className="px-3 py-2">
                  {formatShuntTargetLabel(draft, rule.outboundNodeId)}
                </td>
                <td className="px-3 py-2">
                  {formatExtraSelection(
                    rule.extras,
                    "protocol",
                    shuntProtocolOptionsForRouter,
                  )}
                </td>
                <td className="px-3 py-2">
                  {formatExtraSelection(
                    rule.extras,
                    "inbound",
                    shuntInboundOptions,
                  )}
                </td>
                <td className="px-3 py-2">
                  {getExtraOptionLabel(
                    rule.extras,
                    "network",
                    shuntNetworkOptions,
                    "TCP UDP",
                  )}
                </td>
                <td className="px-3 py-2">
                  {getExtraString(rule.extras, "port") ?? "не задано"}
                </td>
                <td className="px-3 py-2">
                  {selectedShuntNode &&
                  getExtraBoolean(
                    selectedShuntNode.extras,
                    `${rule.id}_fakedns`,
                  )
                    ? "on"
                    : "off"}
                </td>
                <td className="px-3 py-2">
                  {selectedShuntNode
                    ? resolveNodeLabel(
                        draft,
                        getExtraString(
                          selectedShuntNode.extras,
                          `${rule.id}_proxy_tag`,
                        ),
                      )
                    : "не задано"}
                </td>
                <td className="px-3 py-2">{rule.domainRules.length}</td>
                <td className="px-3 py-2">{rule.ipRules.length}</td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={10}>Shunt-правил пока нет.</DataTableEmpty>
          )}
        </DataTable>
      </div>

      {selectedRule ? (
        <SectionBox title="Редактор выбранного правила">
          <FieldGrid>
            <TextControl
              label="ID"
              value={selectedRule.id}
              onChange={(value) => {
                const nextId = value?.trim();
                if (
                  !nextId ||
                  draft.basicSettings.shuntRules.some(
                    (rule) => rule.id === nextId && rule.id !== selectedRule.id,
                  )
                ) {
                  return;
                }
                renameRuleId(setDraft, selectedRule.id, nextId);
                setSelectedRuleId(nextId);
              }}
            />
            <TextControl
              label="Название"
              value={selectedRule.label}
              onChange={(value) =>
                updateRuleField(
                  setDraft,
                  selectedRule.id,
                  "label",
                  value ?? selectedRule.label,
                )
              }
            />
            <SelectControl
              label="Target"
              value={selectedRule.outboundNodeId ?? ""}
              options={ruleTargetOptions}
              onChange={(value) =>
                updateRuleField(
                  setDraft,
                  selectedRule.id,
                  "outboundNodeId",
                  value ?? "",
                )
              }
            />
            <CheckboxGroupControl
              label="Protocol"
              options={shuntProtocolOptionsForRouter}
              values={getExtraTokens(selectedRule.extras, "protocol")}
              onChange={(values) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "protocol",
                  encodeExtraTokens(values),
                )
              }
            />
            <CheckboxGroupControl
              label="Inbound Tag"
              options={shuntInboundOptions}
              values={getExtraTokens(selectedRule.extras, "inbound")}
              onChange={(values) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "inbound",
                  encodeExtraTokens(values),
                )
              }
            />
            <SelectControl
              label="Network"
              value={getExtraString(selectedRule.extras, "network", "tcp,udp")}
              options={shuntNetworkOptions}
              onChange={(value) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "network",
                  value ?? "tcp,udp",
                )
              }
            />
            <TextAreaControl
              label="Source"
              rows={4}
              value={getExtraTokens(selectedRule.extras, "source")}
              onChange={(value) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "source",
                  encodeExtraTokens(value),
                )
              }
            />
            <TextControl
              label="Port"
              value={getExtraString(selectedRule.extras, "port")}
              optional
              onChange={(value) =>
                updateRuleExtra(setDraft, selectedRule.id, "port", value)
              }
            />
            <BooleanControl
              label="FakeDNS"
              value={
                selectedShuntNode
                  ? getExtraBoolean(
                      selectedShuntNode.extras,
                      `${selectedRule.id}_fakedns`,
                    )
                  : false
              }
              onChange={(value) =>
                selectedShuntNode
                  ? updateNodeExtra(
                      setDraft,
                      selectedShuntNode.id,
                      `${selectedRule.id}_fakedns`,
                      boolExtra(value),
                    )
                  : undefined
              }
            />
            <SelectControl
              label="Preproxy"
              value={
                selectedShuntNode
                  ? getExtraString(
                      selectedShuntNode.extras,
                      `${selectedRule.id}_proxy_tag`,
                    )
                  : undefined
              }
              optional
              options={draft.nodes.map((node) => ({
                value: node.id,
                label: node.label,
              }))}
              onChange={(value) =>
                selectedShuntNode
                  ? updateNodeExtra(
                      setDraft,
                      selectedShuntNode.id,
                      `${selectedRule.id}_proxy_tag`,
                      value,
                    )
                  : undefined
              }
            />
            <TextAreaControl
              label="Domain rules"
              rows={6}
              value={selectedRule.domainRules}
              onChange={(value) =>
                updateRuleField(setDraft, selectedRule.id, "domainRules", value)
              }
            />
            <TextAreaControl
              label="IP rules"
              rows={6}
              value={selectedRule.ipRules}
              onChange={(value) =>
                updateRuleField(setDraft, selectedRule.id, "ipRules", value)
              }
            />
            <BooleanControl
              label="invert (Sing-Box only)"
              value={getExtraBoolean(selectedRule.extras, "invert")}
              onChange={(value) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "invert",
                  boolExtra(value),
                )
              }
            />
          </FieldGrid>
        </SectionBox>
      ) : null}
    </div>
  );
}

function formatRuntimePresence(value: boolean) {
  return value ? "есть" : "нет";
}

function formatRuntimeBoolean(value: boolean | null) {
  if (value === null) {
    return "не задано";
  }
  return value ? "да" : "нет";
}

function formatRuntimeValue(value: string | number | null | undefined) {
  if (value === null || value === undefined || value === "") {
    return "не задано";
  }
  return String(value);
}

function RuntimeNodeDetails({
  node,
}: {
  node: EditorSurface["subscriptionRuntime"]["managedNodes"][number];
}) {
  const rows = [
    ["ID", node.id],
    ["Протокол", node.protocol],
    ["Группа", node.group],
    ["Адрес", formatRuntimeValue(node.details.address)],
    ["Порт", formatRuntimeValue(node.details.port)],
    ["Транспорт", formatRuntimeValue(node.details.transport)],
    ["TLS", formatRuntimeBoolean(node.details.tls)],
    ["UUID / username", formatRuntimePresence(node.details.usernamePresent)],
    ["Password", formatRuntimePresence(node.details.passwordPresent)],
    ["REALITY", node.details.realityEnabled ? "включён" : "нет"],
    [
      "REALITY public key",
      formatRuntimePresence(node.details.realityPublicKeyPresent),
    ],
    [
      "REALITY short id",
      formatRuntimePresence(node.details.realityShortIdPresent),
    ],
    ["TLS server name", formatRuntimeValue(node.details.tlsServerName)],
    ["gRPC mode", formatRuntimeValue(node.details.grpcMode)],
    ["Flow", formatRuntimeValue(node.details.flow)],
    ["Encryption", formatRuntimeValue(node.details.encryption)],
    ["Fingerprint", formatRuntimeValue(node.details.fingerprint)],
    ["uTLS", formatRuntimeValue(node.details.utls)],
    ["Mux", formatRuntimeValue(node.details.mux)],
    ["Mux concurrency", formatRuntimeValue(node.details.muxConcurrency)],
    ["XUDP concurrency", formatRuntimeValue(node.details.xudpConcurrency)],
    ["Packet encoding", formatRuntimeValue(node.details.packetEncoding)],
  ] as const;

  return (
    <div className="mt-3 rounded-xl border border-white/10 bg-black/10 px-3 py-3">
      <div className="grid gap-x-4 gap-y-2 text-xs sm:grid-cols-2">
        {rows.map(([label, value]) => (
          <div key={label} className="min-w-0">
            <p className="text-slate-500">{label}</p>
            <p className="mt-0.5 font-medium break-words text-slate-200">
              {value}
            </p>
          </div>
        ))}
      </div>
      <div className="mt-3">
        <p className="text-xs text-slate-500">
          Extra keys без значений секретов
        </p>
        <p className="mt-1 text-xs leading-5 break-words text-slate-300">
          {node.details.extraKeys.length > 0
            ? node.details.extraKeys.join(", ")
            : "нет"}
        </p>
      </div>
    </div>
  );
}

function RuntimeNodeDetailsDisclosure({
  node,
}: {
  node: EditorSurface["subscriptionRuntime"]["managedNodes"][number];
}) {
  return (
    <details className="mt-3">
      <summary className="min-h-11 cursor-pointer list-none text-xs font-medium text-sky-200 transition hover:text-sky-100">
        Открыть read-only детали ноды
      </summary>
      <RuntimeNodeDetails node={node} />
    </details>
  );
}

function RuntimeNodeTable({
  title,
  description,
  nodes,
  emptyText,
}: {
  title: string;
  description: string;
  nodes: EditorSurface["subscriptionRuntime"]["managedNodes"];
  emptyText: string;
}) {
  return (
    <SectionBox title={title}>
      <p className="mb-4 text-sm leading-6 text-slate-300">{description}</p>
      <MobileCardList title={title} hint="Телефонный режим">
        {nodes.length > 0 ? (
          nodes.map((node) => (
            <MobileCard
              key={node.id}
              tone={
                node.orphanReason
                  ? "warning"
                  : node.selected
                    ? "accent"
                    : "default"
              }
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-white">
                    {node.label}
                  </p>
                  <p className="mt-1 text-xs leading-5 break-all text-slate-400">
                    {node.id}
                  </p>
                </div>
                <div className="flex flex-col items-end gap-1">
                  {node.selected ? (
                    <span className="rounded-full border border-emerald-400/30 bg-emerald-500/12 px-2 py-0.5 text-[11px] font-medium text-emerald-100">
                      выбрана сейчас
                    </span>
                  ) : null}
                  {node.orphanReason ? (
                    <span className="rounded-full border border-amber-400/25 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-100">
                      {node.orphanReason === "cleanup-needed"
                        ? "лишняя после preview"
                        : "локально в группе"}
                    </span>
                  ) : null}
                </div>
              </div>

              <div className="mt-3">
                <MobileCardGrid>
                  <MobileCardField label="Протокол" value={node.protocol} />
                  <MobileCardField label="Группа" value={node.group} />
                  <MobileCardField label="Endpoint" value={node.endpoint} />
                  <MobileCardField
                    label="REALITY key"
                    value={formatRuntimePresence(
                      node.details.realityPublicKeyPresent,
                    )}
                  />
                  <MobileCardField
                    label="Статус"
                    value={node.enabled ? "enabled" : "disabled"}
                  />
                </MobileCardGrid>
              </div>
              <RuntimeNodeDetailsDisclosure node={node} />
            </MobileCard>
          ))
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">{emptyText}</p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "label", label: "Нода" },
            { key: "protocol", label: "Протокол" },
            { key: "group", label: "Группа" },
            { key: "endpoint", label: "Endpoint" },
            { key: "reality", label: "REALITY key" },
            { key: "state", label: "Статус" },
            { key: "details", label: "Детали" },
          ]}
        >
          {nodes.length > 0 ? (
            nodes.map((node) => (
              <tr
                key={node.id}
                className="border-t border-white/10 text-slate-200"
              >
                <td className="px-3 py-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-white">{node.label}</span>
                    {node.selected ? (
                      <span className="rounded-full border border-emerald-400/30 bg-emerald-500/12 px-2 py-0.5 text-[11px] font-medium text-emerald-100">
                        выбрана сейчас
                      </span>
                    ) : null}
                    {node.orphanReason ? (
                      <span className="rounded-full border border-amber-400/25 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-100">
                        {node.orphanReason === "cleanup-needed"
                          ? "лишняя после preview"
                          : "локально в группе"}
                      </span>
                    ) : null}
                  </div>
                  <div className="text-xs text-slate-500">{node.id}</div>
                </td>
                <td className="px-3 py-2">{node.protocol}</td>
                <td className="px-3 py-2">{node.group}</td>
                <td className="px-3 py-2">{node.endpoint}</td>
                <td className="px-3 py-2">
                  {formatRuntimePresence(node.details.realityPublicKeyPresent)}
                </td>
                <td className="px-3 py-2">
                  {node.enabled ? "enabled" : "disabled"}
                </td>
                <td className="px-3 py-2">
                  <RuntimeNodeDetailsDisclosure node={node} />
                </td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={7}>{emptyText}</DataTableEmpty>
          )}
        </DataTable>
      </div>
    </SectionBox>
  );
}

function SubscriptionPreviewTable({
  previews,
}: {
  previews: EditorSurface["subscriptionRuntime"]["previews"];
}) {
  return (
    <SectionBox title="Подробности preview">
      <p className="mb-4 text-sm leading-6 text-slate-300">
        Этот блок больше не использует server-side fetch как источник истины.
        Count берётся только из read-only preview на самом роутере, в том же
        контуре PassWall. Когда preview недоступен, панель честно показывает
        статус, а не подставляет эвристику сервера.
      </p>
      <MobileCardList title="Подробности preview" hint="Телефонный режим">
        {previews.length > 0 ? (
          previews.map((preview) => (
            <MobileCard
              key={preview.subscriptionKey}
              tone={preview.status === "drift" ? "warning" : "default"}
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-white">
                    {preview.remark}
                  </p>
                  <p className="mt-1 text-xs leading-5 text-slate-400">
                    hash {preview.urlHash.slice(0, 12)} · {preview.payloadMode}
                  </p>
                </div>
                <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-slate-300">
                  {formatSubscriptionComparisonStatus(preview.status)}
                </span>
              </div>

              <div className="mt-3">
                <MobileCardGrid>
                  <MobileCardField
                    label="Preview"
                    value={
                      preview.resolvedPayloadNodeCount !== null
                        ? `${preview.resolvedPayloadNodeCount}`
                        : formatSubscriptionPreviewState(preview.previewState)
                    }
                    detail={`${formatSubscriptionFetchState(preview.fetchState)}${preview.httpStatus ? ` · HTTP ${preview.httpStatus}` : ""}`}
                  />
                  <MobileCardField
                    label="Импортировано"
                    value={`${preview.liveManagedNodeCount}`}
                    detail={`cleanup ${preview.cleanupNodeCount} · orphan ${preview.orphanNodeCount}`}
                  />
                  <MobileCardField
                    label="Panel draft"
                    value={`${preview.panelDraftManagedNodeCount}`}
                    detail={`panel-only ${preview.panelOnlyNodeCount}`}
                  />
                  <MobileCardField
                    label="Состояние"
                    value={formatSubscriptionPreviewState(preview.previewState)}
                    detail={
                      preview.checkedAt
                        ? `preview ${formatDateTime(preview.checkedAt)}`
                        : "без свежего preview"
                    }
                  />
                </MobileCardGrid>
              </div>
            </MobileCard>
          ))
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">
              Подписок для preview сейчас нет.
            </p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "subscription", label: "Подписка" },
            { key: "preview", label: "Preview" },
            { key: "live", label: "Импортировано" },
            { key: "panel", label: "Panel draft" },
            { key: "status", label: "Статус" },
          ]}
        >
          {previews.length > 0 ? (
            previews.map((preview) => (
              <tr
                key={preview.subscriptionKey}
                className="border-t border-white/10 text-slate-200"
              >
                <td className="px-3 py-2">
                  <div className="font-medium text-white">{preview.remark}</div>
                  <div className="text-xs text-slate-500">
                    hash {preview.urlHash.slice(0, 12)} · {preview.payloadMode}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <div>
                    {preview.resolvedPayloadNodeCount ??
                      formatSubscriptionPreviewState(preview.previewState)}
                  </div>
                  <div className="text-xs text-slate-500">
                    {formatSubscriptionFetchState(preview.fetchState)}
                    {preview.httpStatus ? ` · HTTP ${preview.httpStatus}` : ""}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <div>{preview.liveManagedNodeCount}</div>
                  <div className="text-xs text-slate-500">
                    cleanup {preview.cleanupNodeCount} · orphan{" "}
                    {preview.orphanNodeCount}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <div>{preview.panelDraftManagedNodeCount}</div>
                  <div className="text-xs text-slate-500">
                    panel-only {preview.panelOnlyNodeCount}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <div className="font-medium text-white">
                    {formatSubscriptionComparisonStatus(preview.status)}
                  </div>
                  <div className="text-xs text-slate-500">
                    {formatSubscriptionPreviewState(preview.previewState)}
                    {preview.checkedAt
                      ? ` · preview ${formatDateTime(preview.checkedAt)}`
                      : ""}
                  </div>
                </td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={5}>
              Подписок для preview сейчас нет.
            </DataTableEmpty>
          )}
        </DataTable>
      </div>
    </SectionBox>
  );
}

function formatSubscriptionFetchState(
  value: EditorSurface["subscriptionRuntime"]["previews"][number]["fetchState"],
) {
  switch (value) {
    case "ok":
      return "ok";
    case "disabled":
      return "disabled";
    case "http_error":
      return "HTTP error";
    case "network_error":
      return "network error";
    case "parse_error":
      return "parse error";
    default:
      return value;
  }
}

function formatSubscriptionComparisonStatus(
  value: EditorSurface["subscriptionRuntime"]["previews"][number]["status"],
) {
  switch (value) {
    case "in_sync":
      return "в синхроне";
    case "drift":
      return "есть drift";
    case "disabled":
      return "выключена";
    case "unverifiable":
      return "не удалось проверить";
    default:
      return value;
  }
}

function formatSubscriptionPreviewState(
  value: EditorSurface["subscriptionRuntime"]["previews"][number]["previewState"],
) {
  switch (value) {
    case "fresh":
      return "fresh";
    case "pending":
      return "pending";
    case "stale":
      return "stale";
    case "failed":
      return "failed";
    case "missing":
      return "missing";
    case "disabled":
      return "disabled";
    default:
      return value;
  }
}

function AppUpdateSection({
  routerId,
  draft,
  surface,
  inventory,
  setDraft,
  canRunJobs,
  routerReachable,
}: {
  routerId: string;
  draft: DraftConfigInput;
  surface: EditorSurface;
  inventory: RouterWorkspaceInventory;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  canRunJobs: boolean;
  routerReachable: boolean;
}) {
  const router = useRouter();
  const utils = api.useUtils();
  const [pendingPasswallTarget, setPendingPasswallTarget] = useState<
    string | null
  >(null);
  const [rebootConfirmationOpen, setRebootConfirmationOpen] = useState(false);
  const [controlPlaneHealth, setControlPlaneHealth] =
    useState<ControlPlaneHealthResponse | null>(null);
  const [controlPlaneHealthLoading, setControlPlaneHealthLoading] =
    useState(true);
  const passwallUpdateMutation =
    api.update.queuePasswallPackageUpdate.useMutation({
      onSuccess: async () => {
        await Promise.all([
          utils.draft.editorSurface.invalidate({ routerId }),
          utils.fleet.byId.invalidate({ routerId }),
          utils.fleet.monitoring.invalidate(),
        ]);
        router.refresh();
      },
      onSettled: () => {
        setPendingPasswallTarget(null);
      },
    });
  const controllerUpdateMutation = api.update.queueControllerUpdate.useMutation(
    {
      onSuccess: async () => {
        await Promise.all([
          utils.draft.editorSurface.invalidate({ routerId }),
          utils.fleet.byId.invalidate({ routerId }),
          utils.fleet.monitoring.invalidate(),
        ]);
        router.refresh();
      },
    },
  );
  const routerRebootMutation = api.update.queueRouterReboot.useMutation({
    onSuccess: async () => {
      setRebootConfirmationOpen(false);
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });
  const artifactsQuery = api.update.artifacts.useQuery();
  const latestControllerArtifact =
    artifactsQuery.data?.find(
      (artifact) =>
        artifact.type === "controller" &&
        artifact.name === "vectra-controller-agent",
    ) ?? null;
  const latestPasswallBundleArtifact =
    artifactsQuery.data?.find(
      (artifact) => artifact.type === "passwall_bundle",
    ) ?? null;
  const passwallBundleMetadata =
    buildPasswallBundleMetadataFromArtifact(latestPasswallBundleArtifact) ??
    buildFallbackPasswallBundleMetadata();
  const passwallAppArtifact =
    passwallBundleMetadata.packageArtifacts.find(
      (artifact) => artifact.name === "luci-app-passwall2",
    ) ?? null;
  const xrayArtifact =
    passwallBundleMetadata.packageArtifacts.find(
      (artifact) => artifact.name === "xray-core",
    ) ?? null;
  const installedControllerVersion =
    normalizeControllerVersion(inventory.controllerVersion) ?? null;
  const availableControllerVersion = latestControllerArtifact?.version ?? null;
  const controllerVersionComparison = compareControllerVersions(
    installedControllerVersion,
    availableControllerVersion,
  );
  const controllerNeedsUpdate =
    controllerVersionComparison !== null && controllerVersionComparison < 0;
  const controllerAvailableLabel = availableControllerVersion
    ? controllerVersionComparison === null
      ? availableControllerVersion
      : controllerNeedsUpdate
        ? availableControllerVersion
        : `${availableControllerVersion} (актуально)`
    : artifactsQuery.isLoading
      ? "проверяю..."
      : "не опубликовано";
  const controllerAttempt = surface.lastControllerUpdateAttempt ?? null;
  const controllerHint =
    controllerAttempt?.resultStatus === "failure"
      ? `Последняя попытка обновить controller${
          controllerAttempt.artifactVersion
            ? ` до ${controllerAttempt.artifactVersion}`
            : ""
        } завершилась ошибкой: ${controllerAttempt.summary}.`
      : ["queued", "delivered", "running"].includes(
            controllerAttempt?.jobState ?? "",
          ) || controllerAttempt?.resultStatus === "accepted"
        ? "Обновление controller ещё выполняется."
        : installedControllerVersion === null && controllerAttempt === null
          ? "Роутер не прислал установленную версию controller-agent в последнем check-in."
          : null;
  const lastRouterRebootAttempt =
    surface.managementTaskLog.find((item) => item.kind === "router-reboot") ??
    null;
  const routerRebootQueued =
    lastRouterRebootAttempt !== null &&
    (["queued", "delivered", "running"].includes(
      lastRouterRebootAttempt.jobState,
    ) ||
      lastRouterRebootAttempt.resultStatus === "accepted");
  const routerRebootHint = lastRouterRebootAttempt
    ? `Последняя перезагрузка от панели: ${lastRouterRebootAttempt.summary}`
    : null;
  const passwallAttempt = surface.lastPasswallUpdateAttempt ?? null;
  const passwallHint = summarizePasswallAttempt(passwallAttempt);
  const memoryStatus = describeRouterMemory(inventory.resources ?? null);
  const overlayFreeMb = inventory.resources?.overlayFreeMb ?? null;
  const tmpFreeMb = inventory.resources?.tmpFreeMb ?? null;
  const backendDeliveryBlocked =
    controlPlaneHealth !== null &&
    (!controlPlaneHealth.ok ||
      controlPlaneHealth.checks?.dbWriteProbe === false);
  const deliveryWarnings = [
    !routerReachable
      ? `Роутер давно не присылал свежий check-in. Последняя известная связь: ${formatDateTime(
          surface.routerRuntimeSummary.lastSeenAt,
        )}. Job можно поставить в очередь, но он не дойдёт до роутера, пока controller снова не начнёт check-in.`
      : null,
    backendDeliveryBlocked
      ? `Backend write-probe сейчас не подтверждает сохранение router check-in${
          controlPlaneHealth?.error ? `: ${controlPlaneHealth.error}` : ""
        }. Панель может поставить job, но delivery/result path сейчас ненадёжен.`
      : null,
  ].filter((entry): entry is string => entry !== null);

  useEffect(() => {
    const abort = new AbortController();

    async function loadHealth() {
      setControlPlaneHealthLoading(true);
      try {
        const response = await fetch("/api/health", {
          cache: "no-store",
          signal: abort.signal,
        });
        const payload = (await response.json()) as ControlPlaneHealthResponse;
        if (!abort.signal.aborted) {
          setControlPlaneHealth(payload);
        }
      } catch (error) {
        if (abort.signal.aborted) {
          return;
        }
        setControlPlaneHealth({
          ok: false,
          error:
            error instanceof Error
              ? error.message
              : "health probe request failed",
        });
      } finally {
        if (!abort.signal.aborted) {
          setControlPlaneHealthLoading(false);
        }
      }
    }

    void loadHealth();

    return () => {
      abort.abort();
    };
  }, []);

  const formatComponentInstalledVersion = (packageName: string) => {
    const runtimeKey = packageNameToRuntimeKey(packageName);
    const runtimeVersion = inventory.binaryVersions[runtimeKey] ?? null;
    const packageVersion = inventory.packageVersions[packageName] ?? null;

    if (runtimeVersion && packageVersion && runtimeVersion !== packageVersion) {
      return `runtime ${runtimeVersion} / package ${packageVersion}`;
    }
    if (runtimeVersion && packageVersion) {
      return `runtime ${runtimeVersion} / package ${packageVersion}`;
    }
    if (runtimeVersion) {
      return `runtime ${runtimeVersion}`;
    }
    if (packageVersion) {
      return `package ${packageVersion}`;
    }
    return "неизвестно";
  };
  const runtimeTargetForPackage = (packageName: string) =>
    findPasswallRuntimeTarget(passwallBundleMetadata, packageName)
      ?.remoteVersion ??
    passwallBundleMetadata.packageArtifacts.find(
      (artifact) => artifact.name === packageName,
    )?.artifactVersion ??
    null;
  const versionRows = [
    {
      key: "controller",
      name: "Controller",
      installed: formatControllerVersion(installedControllerVersion),
      available: controllerAvailableLabel,
      action: "controller" as const,
      runtimeCurrent: false,
    },
    ...PASSWALL_PACKAGE_TARGET_ROWS.map((target) => ({
      key: target.key,
      name: target.label,
      installed:
        target.key === "passwall2"
          ? inventory.passwallVersion
            ? `app ${inventory.passwallVersion}`
            : "неизвестно"
          : formatComponentInstalledVersion(target.packages[0]),
      available: target.managedStack
        ? formatPasswallManagedStackAvailableVersion(passwallBundleMetadata)
        : formatPasswallAvailableVersion(
            passwallBundleMetadata,
            target.packages[0],
          ),
      action: "passwall" as const,
      packages: [...target.packages],
      managedStack: target.managedStack,
      runtimeCurrent: target.managedStack
        ? false
        : runtimeMeetsOrExceedsTargetVersion(
            inventory.binaryVersions[
              packageNameToRuntimeKey(target.packages[0])
            ] ?? null,
            runtimeTargetForPackage(target.packages[0]),
          ),
    })),
  ];

  return (
    <div className="space-y-4">
      <ActionStrip justify="start">
        <div className="w-full min-w-0 space-y-2 lg:basis-full">
          <p className="text-sm leading-6 break-words text-slate-300">
            Кнопка <code>PassWall2</code> обновляет не только LuCI-приложение, а
            весь managed stack: bundle{" "}
            <code>{passwallBundleMetadata.releaseTag}</code>, app-package{" "}
            <code>{passwallAppArtifact?.artifactVersion ?? "unknown"}</code>,
            recovery deps и post-update repair.
          </p>
          <p className="text-sm leading-6 break-words text-slate-400">
            Сам <code>luci-app-passwall2</code>
            {passwallAppArtifact
              ? ` небольшой: ${formatCompactSize(passwallAppArtifact.downloadSizeBytes)} download / ${formatCompactSize(passwallAppArtifact.installedSizeBytes)} installed.`
              : " небольшой по сравнению со stack-компонентами."}{" "}
            Тяжёлое место занимают <code>xray-core</code>
            {xrayArtifact
              ? ` (${formatCompactSize(xrayArtifact.downloadSizeBytes)} download / ${formatCompactSize(xrayArtifact.installedSizeBytes)} installed)`
              : ""}{" "}
            и recovery-пакеты.
            {overlayFreeMb !== null || tmpFreeMb !== null
              ? ` Сейчас свободно: overlay ${formatMaybeMegabytes(overlayFreeMb)}, /tmp ${formatMaybeMegabytes(tmpFreeMb)}.`
              : ""}
            {memoryStatus.level !== "unknown"
              ? ` RAM: ${memoryStatus.summary}.`
              : " RAM: нет данных в последнем check-in."}
          </p>
        </div>
      </ActionStrip>

      {deliveryWarnings.length > 0 ? (
        <div className="space-y-2 rounded-2xl border border-amber-400/20 bg-[rgba(110,74,18,0.22)] px-4 py-3">
          {deliveryWarnings.map((warning) => (
            <p key={warning} className="text-sm text-amber-100">
              {warning}
            </p>
          ))}
        </div>
      ) : controlPlaneHealthLoading ? (
        <p className="text-sm text-slate-500">
          Проверяю backend write-probe перед обновлением.
        </p>
      ) : null}

      <ActionStrip justify="start">
        <div className="w-full min-w-0 space-y-1 lg:basis-full">
          <p className="text-sm leading-6 break-words text-slate-300">
            После controller или PassWall-обновлений иногда удобнее сразу
            поставить перезагрузку отсюда, не уходя в другой раздел.
          </p>
          <p className="text-sm leading-6 break-words text-slate-500">
            Панель создаст отдельную terminal-задачу с безопасной задержкой
            перед <code>/sbin/reboot</code>.
          </p>
        </div>
        <button
          type="button"
          disabled={
            !canRunJobs || routerRebootMutation.isPending || routerRebootQueued
          }
          onClick={() => {
            routerRebootMutation.reset();
            setRebootConfirmationOpen(true);
          }}
          className="rounded-md border border-amber-400/25 bg-amber-500/10 px-3 py-2 text-sm font-medium text-amber-100 transition hover:border-amber-300/40 hover:bg-amber-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {routerRebootMutation.isPending
            ? "Ставлю reboot..."
            : routerRebootQueued
              ? "Перезагрузка уже в очереди"
              : "Перезагрузить роутер"}
        </button>
      </ActionStrip>

      <MobileCardList title="Компоненты и версии" hint="Телефонный режим">
        {versionRows.map((row) => (
          <MobileCard
            key={row.key}
            tone={
              row.runtimeCurrent
                ? "good"
                : row.action === "controller"
                  ? "default"
                  : "accent"
            }
          >
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="text-sm font-semibold text-white">{row.name}</p>
                <p className="mt-1 text-xs leading-5 text-slate-400">
                  {row.runtimeCurrent
                    ? "runtime уже актуален"
                    : "доступно действие обновления"}
                </p>
              </div>
              <span
                className={`rounded-full border px-2.5 py-1 text-[11px] ${
                  row.runtimeCurrent
                    ? "border-emerald-400/25 bg-emerald-500/10 text-emerald-100"
                    : "border-white/10 bg-white/5 text-slate-300"
                }`}
              >
                {row.runtimeCurrent ? "актуально" : "доступно"}
              </span>
            </div>

            <div className="mt-3">
              <MobileCardGrid>
                <MobileCardField label="Установлено" value={row.installed} />
                <MobileCardField label="Доступно" value={row.available} />
              </MobileCardGrid>
            </div>

            <div className="mt-3">
              {row.action === "controller" ? (
                <button
                  type="button"
                  disabled={!canRunJobs || controllerUpdateMutation.isPending}
                  onClick={() =>
                    controllerUpdateMutation.mutate({
                      routerId,
                      channel: "stable",
                    })
                  }
                  className="vectra-button-secondary w-full px-3 py-2.5 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {controllerUpdateMutation.isPending
                    ? "Ставлю job..."
                    : controllerNeedsUpdate &&
                        availableControllerVersion !== null
                      ? `Обновить до ${availableControllerVersion}`
                      : availableControllerVersion !== null
                        ? `Переустановить ${availableControllerVersion}`
                        : "Обновить controller"}
                </button>
              ) : (
                <button
                  type="button"
                  disabled={
                    !canRunJobs ||
                    passwallUpdateMutation.isPending ||
                    row.runtimeCurrent
                  }
                  onClick={() => {
                    setPendingPasswallTarget(row.key);
                    passwallUpdateMutation.mutate(
                      row.managedStack
                        ? {
                            routerId,
                            artifactChannel: "stable",
                          }
                        : {
                            routerId,
                            artifactChannel: "stable",
                            packages: row.packages,
                          },
                    );
                  }}
                  className="vectra-button-primary w-full px-3 py-2.5 text-sm font-medium transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {passwallUpdateMutation.isPending &&
                  pendingPasswallTarget === row.key
                    ? "Ставлю job..."
                    : row.managedStack
                      ? `Обновить stack ${row.name}`
                      : row.runtimeCurrent
                        ? `${row.name} актуален по runtime`
                        : `Обновить ${row.name}`}
                </button>
              )}
            </div>
          </MobileCard>
        ))}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "name", label: "Компонент" },
            { key: "installed", label: "Установлено" },
            { key: "available", label: "Доступно" },
            { key: "action", label: "Действие" },
          ]}
        >
          {versionRows.map((row) => (
            <tr
              key={row.key}
              className="border-t border-white/10 text-slate-200"
            >
              <td className="px-3 py-2 font-medium text-white">{row.name}</td>
              <td className="px-3 py-2">{row.installed}</td>
              <td className="px-3 py-2">{row.available}</td>
              <td className="px-3 py-2">
                {row.action === "controller" ? (
                  <button
                    type="button"
                    disabled={!canRunJobs || controllerUpdateMutation.isPending}
                    onClick={() =>
                      controllerUpdateMutation.mutate({
                        routerId,
                        channel: "stable",
                      })
                    }
                    className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {controllerUpdateMutation.isPending
                      ? "Ставлю job..."
                      : controllerNeedsUpdate &&
                          availableControllerVersion !== null
                        ? `Обновить до ${availableControllerVersion}`
                        : availableControllerVersion !== null
                          ? `Переустановить ${availableControllerVersion}`
                          : "Обновить controller"}
                  </button>
                ) : (
                  <button
                    type="button"
                    disabled={
                      !canRunJobs ||
                      passwallUpdateMutation.isPending ||
                      row.runtimeCurrent
                    }
                    onClick={() => {
                      setPendingPasswallTarget(row.key);
                      passwallUpdateMutation.mutate(
                        row.managedStack
                          ? {
                              routerId,
                              artifactChannel: "stable",
                            }
                          : {
                              routerId,
                              artifactChannel: "stable",
                              packages: row.packages,
                            },
                      );
                    }}
                    className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {passwallUpdateMutation.isPending &&
                    pendingPasswallTarget === row.key
                      ? "Ставлю job..."
                      : row.managedStack
                        ? `Обновить stack ${row.name}`
                        : row.runtimeCurrent
                          ? `${row.name} актуален по runtime`
                          : `Обновить ${row.name}`}
                  </button>
                )}
              </td>
            </tr>
          ))}
        </DataTable>
      </div>

      {controllerHint ? (
        <p className="text-sm text-slate-400">{controllerHint}</p>
      ) : null}
      {passwallHint ? (
        <p className="text-sm text-slate-400">{passwallHint}</p>
      ) : null}
      {routerRebootHint ? (
        <p className="text-sm text-slate-400">{routerRebootHint}</p>
      ) : null}
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <p className="vectra-kicker text-slate-500">Задачи от панели</p>
          <span className="text-[11px] text-slate-500">
            последние update-задачи и ответ роутера
          </span>
        </div>
        <RouterManagementTaskLog items={surface.managementTaskLog} />
      </div>
      {!backendDeliveryBlocked && controlPlaneHealth?.checkedAt ? (
        <p className="text-sm text-slate-500">
          Backend write-probe ok: {formatDateTime(controlPlaneHealth.checkedAt)}
          .
        </p>
      ) : null}

      {rebootConfirmationOpen ? (
        <RouterRebootConfirmDialog
          isPending={routerRebootMutation.isPending}
          errorMessage={routerRebootMutation.error?.message ?? null}
          onClose={() => {
            if (routerRebootMutation.isPending) {
              return;
            }
            routerRebootMutation.reset();
            setRebootConfirmationOpen(false);
          }}
          onConfirm={() => {
            routerRebootMutation.mutate({ routerId });
          }}
        />
      ) : null}

      <FieldGrid>
        <TextControl
          label="Путь к Xray"
          value={draft.appUpdate.binaryPaths.xray}
          onChange={(value) =>
            updatePathValue(setDraft, "appUpdate.binaryPaths.xray", value ?? "")
          }
          diff={getDiff(surface, "appUpdate.binaryPaths.xray")}
        />
        <TextControl
          label="Путь к sing-box"
          value={draft.appUpdate.binaryPaths.singBox}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "appUpdate.binaryPaths.singBox",
              value ?? "",
            )
          }
          diff={getDiff(surface, "appUpdate.binaryPaths.singBox")}
        />
        <TextControl
          label="Путь к Hysteria"
          value={draft.appUpdate.binaryPaths.hysteria}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "appUpdate.binaryPaths.hysteria",
              value ?? "",
            )
          }
          diff={getDiff(surface, "appUpdate.binaryPaths.hysteria")}
        />
        <TextControl
          label="Путь к Geoview"
          value={draft.appUpdate.binaryPaths.geoview}
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "appUpdate.binaryPaths.geoview",
              value ?? "",
            )
          }
          diff={getDiff(surface, "appUpdate.binaryPaths.geoview")}
        />
        <SelectControl
          label="Стратегия обновления"
          value={draft.appUpdate.updateStrategy}
          options={updateStrategyOptions}
          onChange={(value) =>
            updatePathValue(setDraft, "appUpdate.updateStrategy", value)
          }
          diff={getDiff(surface, "appUpdate.updateStrategy")}
        />
        <TextControl
          label="Целевая версия PassWall2"
          value={draft.appUpdate.targetVersions.appVersion}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "appUpdate.targetVersions.appVersion",
              value,
            )
          }
          diff={getDiff(surface, "appUpdate.targetVersions.appVersion")}
        />
        <TextControl
          label="Целевая версия Xray"
          value={draft.appUpdate.targetVersions.xray}
          optional
          onChange={(value) =>
            updatePathValue(setDraft, "appUpdate.targetVersions.xray", value)
          }
          diff={getDiff(surface, "appUpdate.targetVersions.xray")}
        />
        <TextControl
          label="Целевая версия sing-box"
          value={draft.appUpdate.targetVersions.singBox}
          optional
          onChange={(value) =>
            updatePathValue(setDraft, "appUpdate.targetVersions.singBox", value)
          }
          diff={getDiff(surface, "appUpdate.targetVersions.singBox")}
        />
        <TextControl
          label="Целевая версия Hysteria"
          value={draft.appUpdate.targetVersions.hysteria}
          optional
          onChange={(value) =>
            updatePathValue(
              setDraft,
              "appUpdate.targetVersions.hysteria",
              value,
            )
          }
          diff={getDiff(surface, "appUpdate.targetVersions.hysteria")}
        />
        <TextControl
          label="Целевая версия Geoview"
          value={draft.appUpdate.targetVersions.geoview}
          optional
          onChange={(value) =>
            updatePathValue(setDraft, "appUpdate.targetVersions.geoview", value)
          }
          diff={getDiff(surface, "appUpdate.targetVersions.geoview")}
        />
      </FieldGrid>
    </div>
  );
}

function RouterRebootConfirmDialog({
  onClose,
  onConfirm,
  isPending,
  errorMessage,
}: {
  onClose: () => void;
  onConfirm: () => void;
  isPending: boolean;
  errorMessage: string | null;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 px-4 py-6">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="router-reboot-confirm-title"
        className="w-full max-w-md rounded-3xl border border-white/10 bg-[var(--vectra-panel)] p-5 shadow-2xl shadow-black/40"
      >
        <div className="space-y-3">
          <div className="space-y-1">
            <p className="vectra-kicker text-amber-200">Router Reboot</p>
            <h2
              id="router-reboot-confirm-title"
              className="text-lg font-semibold text-white"
            >
              Поставить перезагрузку в очередь?
            </h2>
          </div>
          <p className="text-sm leading-6 text-slate-300">
            Панель создаст отдельную задачу на роутере и запланирует{" "}
            <code>/sbin/reboot</code> с короткой задержкой, чтобы controller
            успел принять команду.
          </p>
          <p className="text-sm leading-6 text-slate-500">
            Используй это после обновлений, когда нужно быстро перезапустить
            устройство, не переходя в другой раздел панели.
          </p>
          {errorMessage ? (
            <p className="rounded-2xl border border-rose-400/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-100">
              {errorMessage}
            </p>
          ) : null}
        </div>
        <div className="mt-5 flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
          <button
            type="button"
            onClick={onClose}
            disabled={isPending}
            className="vectra-button-secondary px-4 py-2.5 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            Отмена
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={isPending}
            className="rounded-md border border-amber-400/25 bg-amber-500/10 px-4 py-2.5 text-sm font-medium text-amber-100 transition hover:border-amber-300/40 hover:bg-amber-500/15 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {isPending ? "Ставлю в очередь..." : "Подтвердить перезагрузку"}
          </button>
        </div>
      </div>
    </div>
  );
}

function RuleManageSection({
  routerId,
  draft,
  surface,
  selectedRule,
  selectedRuleId,
  setSelectedRuleId,
  setDraft,
  canRunJobs,
  passwallVersion,
}: {
  routerId: string;
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedRule: DraftConfigInput["basicSettings"]["shuntRules"][number] | null;
  selectedRuleId: string | null;
  setSelectedRuleId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  canRunJobs: boolean;
  passwallVersion: string | null;
}) {
  const router = useRouter();
  const utils = api.useUtils();
  const refreshMutation = api.update.queueRulesRefresh.useMutation({
    onSuccess: async () => {
      await Promise.all([
        utils.draft.editorSurface.invalidate({ routerId }),
        utils.fleet.byId.invalidate({ routerId }),
        utils.fleet.monitoring.invalidate(),
      ]);
      router.refresh();
    },
  });
  const scheduleState = getRuleScheduleState(draft.ruleManage);

  return (
    <div className="space-y-4">
      <FieldGrid>
        <TextControl
          label="URL GeoIP"
          value={draft.ruleManage.geoipUrl}
          onChange={(value) =>
            updatePathValue(setDraft, "ruleManage.geoipUrl", value ?? "")
          }
          diff={getDiff(surface, "ruleManage.geoipUrl")}
        />
        <TextControl
          label="URL GeoSite"
          value={draft.ruleManage.geositeUrl}
          onChange={(value) =>
            updatePathValue(setDraft, "ruleManage.geositeUrl", value ?? "")
          }
          diff={getDiff(surface, "ruleManage.geositeUrl")}
        />
        <TextControl
          label="Каталог ассетов"
          value={draft.ruleManage.assetDirectory}
          onChange={(value) =>
            updatePathValue(setDraft, "ruleManage.assetDirectory", value ?? "")
          }
          diff={getDiff(surface, "ruleManage.assetDirectory")}
        />
        <BooleanControl
          label="Автообновление"
          value={draft.ruleManage.autoUpdate}
          onChange={(value) =>
            updatePathValue(setDraft, "ruleManage.autoUpdate", value)
          }
          diff={getDiff(surface, "ruleManage.autoUpdate")}
        />
        <SelectControl
          label="Режим расписания"
          value={scheduleState.mode}
          options={scheduleModeOptions}
          onChange={(value) =>
            updateRuleSchedule(setDraft, draft.ruleManage, {
              mode: (value ?? "daily") as RuleScheduleState["mode"],
            })
          }
          diff={getDiff(surface, "ruleManage.scheduleMode")}
        />
        {scheduleState.mode === "weekly" ? (
          <SelectControl
            label="День недели"
            value={scheduleState.day}
            options={scheduleDayOptions}
            onChange={(value) =>
              updateRuleSchedule(setDraft, draft.ruleManage, {
                day: value ?? "1",
              })
            }
            diff={getDiff(surface, "ruleManage.scheduleDay")}
          />
        ) : null}
        {scheduleState.mode === "interval" ? (
          <SelectControl
            label="Интервал часов"
            value={scheduleState.interval}
            options={intervalHourOptions}
            onChange={(value) =>
              updateRuleSchedule(setDraft, draft.ruleManage, {
                interval: value ?? "2",
              })
            }
            diff={getDiff(surface, "ruleManage.intervalHours")}
          />
        ) : (
          <SelectControl
            label="Час запуска"
            value={scheduleState.hour}
            options={hourOptions}
            onChange={(value) =>
              updateRuleSchedule(setDraft, draft.ruleManage, {
                hour: value ?? "0",
              })
            }
            diff={getDiff(surface, "ruleManage.scheduleHour")}
          />
        )}
      </FieldGrid>

      <ActionStrip justify="start">
        {ruleAssetOptions.map((asset) => {
          const checked = draft.ruleManage.enabledAssets.includes(asset.value);
          return (
            <label
              key={asset.value}
              className="inline-flex items-center gap-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200"
            >
              <input
                type="checkbox"
                name={`rule-asset-${asset.value}`}
                checked={checked}
                onChange={(event) =>
                  setDraft((previous) =>
                    previous
                      ? updateConfig(previous, (current) => {
                          const values = new Set(
                            current.ruleManage.enabledAssets,
                          );
                          if (event.target.checked) {
                            values.add(asset.value);
                          } else {
                            values.delete(asset.value);
                          }
                          current.ruleManage.enabledAssets = [...values];
                        })
                      : previous,
                  )
                }
              />
              {asset.label}
            </label>
          );
        })}
        <button
          type="button"
          disabled={!canRunJobs || refreshMutation.isPending}
          onClick={() => refreshMutation.mutate({ routerId })}
          className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {refreshMutation.isPending
            ? "Ставлю refresh..."
            : "Обновить GEOIP и GEOSITE"}
        </button>
      </ActionStrip>

      <RuleManageShuntRulesSection
        draft={draft}
        surface={surface}
        selectedRule={selectedRule}
        selectedRuleId={selectedRuleId}
        setSelectedRuleId={setSelectedRuleId}
        setDraft={setDraft}
        passwallVersion={passwallVersion}
      />
    </div>
  );
}

function RuleManageShuntRulesSection({
  draft,
  surface,
  selectedRule,
  selectedRuleId,
  setSelectedRuleId,
  setDraft,
  passwallVersion,
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedRule: DraftConfigInput["basicSettings"]["shuntRules"][number] | null;
  selectedRuleId: string | null;
  setSelectedRuleId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  passwallVersion: string | null;
}) {
  const visibleRules = draft.basicSettings.shuntRules;
  const shuntProtocolOptionsForRouter =
    buildShuntProtocolOptions(passwallVersion);

  return (
    <div className="space-y-4">
      <ActionStrip justify="start">
        <span className="text-sm text-slate-300">
          Таблица совпадает по смыслу с PassWall: список правил сверху, редактор
          выбранной строки ниже.
        </span>
        <button
          type="button"
          onClick={() => {
            const next = addShuntRule(draft);
            setDraft(next);
            setSelectedRuleId(
              next.basicSettings.shuntRules[
                next.basicSettings.shuntRules.length - 1
              ]?.id ?? null,
            );
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white"
        >
          Добавить
        </button>
        <button
          type="button"
          disabled={!selectedRule}
          onClick={() => {
            if (!selectedRule) {
              return;
            }
            const index = draft.basicSettings.shuntRules.findIndex(
              (rule) => rule.id === selectedRule.id,
            );
            const next = deleteShuntRule(draft, index);
            setDraft(next);
            setSelectedRuleId(next.basicSettings.shuntRules[0]?.id ?? null);
          }}
          className="rounded-md border border-rose-400/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-100 transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Удалить
        </button>
        <button
          type="button"
          disabled={!selectedRule}
          onClick={() => {
            if (!selectedRule) {
              return;
            }
            const index = draft.basicSettings.shuntRules.findIndex(
              (rule) => rule.id === selectedRule.id,
            );
            setDraft(moveShuntRuleToTop(draft, index));
            setSelectedRuleId(selectedRule.id);
          }}
          className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-200 transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        >
          В начало
        </button>
      </ActionStrip>

      <MobileCardList title="Rule Manage" hint="Телефонный режим">
        {visibleRules.length > 0 ? (
          visibleRules.map((rule) => {
            const selected = rule.id === selectedRuleId;

            return (
              <MobileCard key={rule.id} tone={selected ? "accent" : "default"}>
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-white">
                      {rule.id}
                    </p>
                    <p className="mt-1 text-xs leading-5 text-slate-400">
                      {rule.label}
                    </p>
                  </div>
                  {selected ? <SelectedPill /> : null}
                </div>

                <div className="mt-3">
                  <MobileCardGrid>
                    <MobileCardField
                      label="Inbound"
                      value={formatExtraSelection(
                        rule.extras,
                        "inbound",
                        shuntInboundOptions,
                      )}
                    />
                    <MobileCardField
                      label="Network"
                      value={getExtraOptionLabel(
                        rule.extras,
                        "network",
                        shuntNetworkOptions,
                        "TCP UDP",
                      )}
                    />
                    <MobileCardField
                      label="Domain"
                      value={`${rule.domainRules.length}`}
                    />
                    <MobileCardField
                      label="IP"
                      value={`${rule.ipRules.length}`}
                    />
                  </MobileCardGrid>
                </div>

                <div className="mt-3">
                  <MobileSelectButton
                    selected={selected}
                    onClick={() => setSelectedRuleId(rule.id)}
                    label="Открыть правило"
                    selectedLabel="Правило открыто"
                  />
                </div>
              </MobileCard>
            );
          })
        ) : (
          <MobileCard>
            <p className="text-sm leading-7 text-slate-300">
              Shunt-правил пока нет.
            </p>
          </MobileCard>
        )}
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "name", label: "Name" },
            { key: "remarks", label: "Remarks" },
            { key: "inbound", label: "Inbound" },
            { key: "network", label: "Network" },
            { key: "domain", label: "Domain" },
            { key: "ip", label: "IP" },
          ]}
        >
          {visibleRules.length > 0 ? (
            visibleRules.map((rule) => (
              <tr
                key={rule.id}
                className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                  rule.id === selectedRuleId
                    ? "bg-[var(--vectra-accent-soft)] ring-1 ring-[var(--vectra-line-strong)] ring-inset"
                    : ""
                }`}
                onClick={() => setSelectedRuleId(rule.id)}
              >
                <td className="px-3 py-2 font-medium text-white">
                  <div className="flex items-center gap-2">
                    <span>{rule.id}</span>
                    {rule.id === selectedRuleId ? <SelectedPill /> : null}
                  </div>
                </td>
                <td className="px-3 py-2">{rule.label}</td>
                <td className="px-3 py-2">
                  {formatExtraSelection(
                    rule.extras,
                    "inbound",
                    shuntInboundOptions,
                  )}
                </td>
                <td className="px-3 py-2">
                  {getExtraOptionLabel(
                    rule.extras,
                    "network",
                    shuntNetworkOptions,
                    "TCP UDP",
                  )}
                </td>
                <td className="px-3 py-2">{rule.domainRules.length}</td>
                <td className="px-3 py-2">{rule.ipRules.length}</td>
              </tr>
            ))
          ) : (
            <DataTableEmpty colSpan={6}>Shunt-правил пока нет.</DataTableEmpty>
          )}
        </DataTable>
      </div>

      {selectedRule ? (
        <SectionBox title="Редактор выбранного правила">
          <FieldGrid>
            <TextControl
              label="Name"
              value={selectedRule.id}
              onChange={(value) => {
                const nextId = value?.trim();
                if (
                  !nextId ||
                  draft.basicSettings.shuntRules.some(
                    (rule) => rule.id === nextId && rule.id !== selectedRule.id,
                  )
                ) {
                  return;
                }
                renameRuleId(setDraft, selectedRule.id, nextId);
                setSelectedRuleId(nextId);
              }}
            />
            <TextControl
              label="Remarks"
              value={selectedRule.label}
              onChange={(value) =>
                updateRuleField(
                  setDraft,
                  selectedRule.id,
                  "label",
                  value ?? selectedRule.label,
                )
              }
              diff={getRuleManageDiff(surface, selectedRule.id, "label")}
            />
            <CheckboxGroupControl
              label="Protocol"
              options={shuntProtocolOptionsForRouter}
              values={getExtraTokens(selectedRule.extras, "protocol")}
              onChange={(values) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "protocol",
                  encodeExtraTokens(values),
                )
              }
              diff={getRuleManageDiff(
                surface,
                selectedRule.id,
                "extras.protocol",
              )}
            />
            <CheckboxGroupControl
              label="Inbound Tag"
              options={shuntInboundOptions}
              values={getExtraTokens(selectedRule.extras, "inbound")}
              onChange={(values) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "inbound",
                  encodeExtraTokens(values),
                )
              }
              diff={getRuleManageDiff(
                surface,
                selectedRule.id,
                "extras.inbound",
              )}
            />
            <SelectControl
              label="Network"
              value={getExtraString(selectedRule.extras, "network", "tcp,udp")}
              options={shuntNetworkOptions}
              onChange={(value) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "network",
                  value ?? "tcp,udp",
                )
              }
              diff={getRuleManageDiff(
                surface,
                selectedRule.id,
                "extras.network",
              )}
            />
            <TextAreaControl
              label="Source"
              rows={4}
              value={getExtraTokens(selectedRule.extras, "source")}
              onChange={(value) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "source",
                  encodeExtraTokens(value),
                )
              }
              diff={getRuleManageDiff(
                surface,
                selectedRule.id,
                "extras.source",
              )}
            />
            <TextControl
              label="Port"
              value={getExtraString(selectedRule.extras, "port")}
              optional
              onChange={(value) =>
                updateRuleExtra(setDraft, selectedRule.id, "port", value)
              }
              diff={getRuleManageDiff(surface, selectedRule.id, "extras.port")}
            />
            <TextAreaControl
              label="Domain"
              rows={8}
              value={selectedRule.domainRules}
              onChange={(value) =>
                updateRuleField(setDraft, selectedRule.id, "domainRules", value)
              }
              diff={getRuleManageDiff(surface, selectedRule.id, "domainRules")}
            />
            <TextAreaControl
              label="IP"
              rows={8}
              value={selectedRule.ipRules}
              onChange={(value) =>
                updateRuleField(setDraft, selectedRule.id, "ipRules", value)
              }
              diff={getRuleManageDiff(surface, selectedRule.id, "ipRules")}
            />
            <BooleanControl
              label="invert (Sing-Box only)"
              value={getExtraBoolean(selectedRule.extras, "invert")}
              onChange={(value) =>
                updateRuleExtra(
                  setDraft,
                  selectedRule.id,
                  "invert",
                  boolExtra(value),
                )
              }
              diff={getRuleManageDiff(
                surface,
                selectedRule.id,
                "extras.invert",
              )}
            />
          </FieldGrid>
        </SectionBox>
      ) : null}
    </div>
  );
}

function GeoViewSection({
  inventory,
}: {
  inventory: RouterWorkspaceInventory;
}) {
  const assets = inventory.rulesAssets;
  const rows = [
    ["Каталог", assets?.assetDirectory ?? null],
    ["GeoIP", assets?.geoipVersion ?? null],
    ["GeoSite", assets?.geositeVersion ?? null],
    ["GeoIP updated", assets?.geoipUpdatedAt ?? null],
    ["GeoSite updated", assets?.geositeUpdatedAt ?? null],
  ].filter((entry): entry is [string, string] => Boolean(entry[1]));

  if (!rows.length) {
    return (
      <EmptyState text="Роутер ещё не прислал данные по rule assets и Geo View пока пуст." />
    );
  }

  return (
    <div className="space-y-3">
      <MobileCardList title="Geo View" hint="Телефонный режим">
        <MobileCard>
          <MobileCardGrid columns={1}>
            {rows.map(([name, value]) => (
              <MobileCardField key={name} label={name} value={value ?? "—"} />
            ))}
          </MobileCardGrid>
        </MobileCard>
      </MobileCardList>

      <div className="max-lg:hidden">
        <DataTable
          columns={[
            { key: "name", label: "Параметр" },
            { key: "value", label: "Значение" },
          ]}
        >
          {rows.map(([name, value]) => (
            <tr key={name} className="border-t border-white/10 text-slate-200">
              <td className="px-3 py-2 font-medium text-white">{name}</td>
              <td className="px-3 py-2">{value}</td>
            </tr>
          ))}
        </DataTable>
      </div>
    </div>
  );
}

function FieldGrid({ children }: { children: ReactNode }) {
  return <div className="grid gap-3 lg:grid-cols-2">{children}</div>;
}

function SectionBox({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="min-w-0 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
      <h3 className="text-base font-semibold text-white">{title}</h3>
      <div className="mt-4 min-w-0">{children}</div>
    </section>
  );
}

function SelectedPill({ label = "выбрано" }: { label?: string }) {
  return (
    <span className="rounded-full border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-2 py-0.5 text-[11px] font-medium text-sky-100">
      {label}
    </span>
  );
}

function MobileSelectButton({
  selected,
  onClick,
  label = "Выбрать",
  selectedLabel = "Выбрано",
}: {
  selected: boolean;
  onClick: () => void;
  label?: string;
  selectedLabel?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-full rounded-xl px-3 py-2.5 text-sm font-medium transition ${
        selected
          ? "border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] text-sky-100"
          : "vectra-button-secondary"
      }`}
    >
      {selected ? selectedLabel : label}
    </button>
  );
}

function SummaryCell({
  label,
  value,
  meta,
}: {
  label: string;
  value: string;
  meta?: string | null;
}) {
  return (
    <div className="min-w-0 rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <p className="vectra-kicker text-slate-500">{label}</p>
      <p className="mt-2 text-sm font-semibold tracking-[-0.01em] break-words text-white sm:text-base">
        {value}
      </p>
      {meta ? (
        <p className="mt-1 text-xs leading-5 break-words text-slate-400 sm:text-sm sm:leading-6">
          {meta}
        </p>
      ) : null}
    </div>
  );
}

function ActionGroup({
  eyebrow,
  title,
  description,
  tone = "default",
  children,
}: {
  eyebrow: string;
  title: string;
  description?: string;
  tone?: "default" | "warning" | "good";
  children: ReactNode;
}) {
  const toneClassName =
    tone === "good"
      ? "border-emerald-400/20 bg-emerald-500/10"
      : tone === "warning"
        ? "border-amber-400/20 bg-amber-500/10"
        : "border-white/10 bg-[var(--vectra-panel-soft)]";

  const eyebrowClassName =
    tone === "good"
      ? "text-emerald-200"
      : tone === "warning"
        ? "text-amber-200"
        : "text-slate-500";

  return (
    <section
      className={`min-w-0 rounded-2xl border px-4 py-4 ${toneClassName}`}
    >
      <p className={`vectra-kicker ${eyebrowClassName}`}>{eyebrow}</p>
      <h3 className="mt-2 text-sm font-semibold tracking-[-0.01em] break-words text-white sm:text-base">
        {title}
      </h3>
      <p className="mt-1 text-sm leading-6 break-words text-slate-400 max-sm:hidden sm:block">
        {description}
      </p>
      <div className="mt-3 space-y-3">{children}</div>
    </section>
  );
}

function UnconfirmedChangesPanel({
  routerChanges,
  panelChanges,
  compact = false,
}: {
  routerChanges: UnconfirmedChangeGroup;
  panelChanges: UnconfirmedChangeGroup;
  compact?: boolean;
}) {
  const content = (
    <div
      className={`grid gap-3 lg:grid-cols-2 [&>*]:min-w-0 ${compact ? "" : "mt-4"}`}
    >
      <UnconfirmedChangeCard
        eyebrow="Изменилось на роутере"
        badge={formatUnconfirmedStatusBadge(routerChanges)}
        group={routerChanges}
        emptyText="Новых неподтверждённых изменений в подробных настройках со стороны роутера сейчас не видно."
      />
      <UnconfirmedChangeCard
        eyebrow="Сохранено в панели"
        badge={formatUnconfirmedStatusBadge(panelChanges)}
        group={panelChanges}
        emptyText="Сохранённый черновик не расходится с текущим подтверждённым состоянием панели."
      />
    </div>
  );

  if (compact) {
    return content;
  }

  return (
    <section className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 py-4">
      <div className="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between">
        <div>
          <p className="vectra-kicker text-[var(--vectra-accent)]">
            Что именно изменилось
          </p>
          <h3 className="mt-2 text-sm font-semibold text-white sm:text-base">
            Неподтверждённые изменения
          </h3>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-300">
            Здесь отдельно показано, что уже пришло с роутера, но вы ещё не
            подтвердили, и что уже сохранено в панели, но роутер это ещё не
            подтвердил как текущее live-состояние.
          </p>
        </div>
      </div>

      {content}
    </section>
  );
}

function UnconfirmedChangeCard({
  eyebrow,
  badge,
  group,
  emptyText,
}: {
  eyebrow: string;
  badge: string;
  group: UnconfirmedChangeGroup;
  emptyText: string;
}) {
  const hasChanges = group.status !== "none";

  return (
    <div className="min-w-0 rounded-2xl border border-white/10 bg-black/10 px-4 py-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="vectra-kicker text-slate-500">{eyebrow}</p>
          <h4 className="mt-2 text-sm font-semibold break-words text-white">
            {group.title}
          </h4>
        </div>
        <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-center text-xs font-medium break-words text-slate-200 sm:max-w-[14rem]">
          {badge}
        </span>
      </div>

      <p className="mt-2 text-sm leading-6 break-words text-slate-300">
        {hasChanges ? group.summary : emptyText}
      </p>

      {hasChanges ? (
        <div className="mt-3 space-y-3">
          <div className="flex flex-wrap gap-2 text-xs text-slate-400">
            <span className="rounded-full border border-white/10 bg-white/5 px-2 py-1 break-words">
              изменений: {group.changeCount}
            </span>
            {group.revisionId ? (
              <span className="rounded-full border border-white/10 bg-white/5 px-2 py-1 break-all">
                ревизия: {group.revisionId}
              </span>
            ) : null}
            {group.changedSections.length ? (
              <span className="rounded-full border border-white/10 bg-white/5 px-2 py-1 break-words">
                секции: {group.changedSections.join(", ")}
              </span>
            ) : null}
          </div>

          {group.items.length ? (
            <div className="space-y-2">
              {group.items.map((item) => (
                <div
                  key={`${group.status}-${item.path}`}
                  className="min-w-0 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3"
                >
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <p className="text-sm font-medium break-words text-white">
                        {item.label}
                      </p>
                      <p className="mt-1 text-xs break-words text-slate-500">
                        {item.section}
                      </p>
                    </div>
                    <code className="text-[11px] break-all text-slate-500">
                      {item.path}
                    </code>
                  </div>
                  <div className="mt-3 grid gap-2 sm:grid-cols-2">
                    <div className="rounded-md border border-white/10 bg-black/10 px-3 py-2">
                      <p className="vectra-kicker text-slate-500">
                        Было подтверждено
                      </p>
                      <p className="mt-1 text-sm break-words text-slate-200">
                        {item.before}
                      </p>
                    </div>
                    <div className="rounded-md border border-white/10 bg-black/10 px-3 py-2">
                      <p className="vectra-kicker text-slate-500">Сейчас</p>
                      <p className="mt-1 text-sm break-words text-slate-200">
                        {item.after}
                      </p>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : group.status === "reimport-needed" ? (
            <div className="rounded-md border border-amber-400/20 bg-amber-500/10 px-3 py-3 text-sm leading-6 text-amber-100">
              Пока видно только расхождение по digest: панель понимает, что
              подробные настройки на роутере изменились, но не знает точные поля
              до нового чтения конфигурации.
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function InlineStateCard({
  eyebrow,
  title,
  description,
  tone = "default",
}: {
  eyebrow: string;
  title: string;
  description: string;
  tone?: "default" | "good" | "warning" | "danger";
}) {
  const toneClassName =
    tone === "good"
      ? "border-emerald-400/20 bg-emerald-500/10"
      : tone === "warning"
        ? "border-amber-400/20 bg-amber-500/10"
        : tone === "danger"
          ? "border-rose-400/20 bg-rose-500/10"
          : "border-white/10 bg-[var(--vectra-panel-soft)]";

  const eyebrowClassName =
    tone === "good"
      ? "text-emerald-200"
      : tone === "warning"
        ? "text-amber-200"
        : tone === "danger"
          ? "text-rose-200"
          : "text-slate-500";

  return (
    <section
      className={`min-w-0 rounded-2xl border px-3 py-3 ${toneClassName}`}
    >
      <p className={`vectra-kicker ${eyebrowClassName}`}>{eyebrow}</p>
      <p className="mt-2 text-sm font-medium break-words text-white sm:text-[15px]">
        {title}
      </p>
      <p className="mt-1 text-sm leading-6 break-words text-slate-300">
        {description}
      </p>
    </section>
  );
}

function TextControl({
  label,
  value,
  onChange,
  optional,
  diff,
  disabled,
  hint,
}: {
  label: string;
  value: string | undefined;
  onChange: (value: string | undefined) => void;
  optional?: boolean;
  diff?: EditorSurface["fieldDiffs"][number];
  disabled?: boolean;
  hint?: string;
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff} hint={hint}>
      <input
        name={controlName}
        disabled={disabled}
        className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)] disabled:cursor-not-allowed disabled:opacity-60"
        value={value ?? ""}
        onChange={(event) =>
          onChange(normalizeTextValue(event.target.value, optional))
        }
      />
    </FieldShell>
  );
}

function NumberControl({
  label,
  value,
  onChange,
  optional,
  diff,
  disabled,
  hint,
}: {
  label: string;
  value: number | undefined;
  onChange: (value: number | undefined | "") => void;
  optional?: boolean;
  diff?: EditorSurface["fieldDiffs"][number];
  disabled?: boolean;
  hint?: string;
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff} hint={hint}>
      <input
        type="number"
        name={controlName}
        disabled={disabled}
        className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)] disabled:cursor-not-allowed disabled:opacity-60"
        value={value === undefined ? "" : String(value)}
        onChange={(event) =>
          onChange(normalizeNumberValue(event.target.value, optional))
        }
      />
    </FieldShell>
  );
}

function SelectControl({
  label,
  value,
  options,
  onChange,
  optional,
  diff,
  disabled,
  hint,
}: {
  label: string;
  value: string | undefined;
  options: ReadonlyArray<Option>;
  onChange: (value: string | undefined) => void;
  optional?: boolean;
  diff?: EditorSurface["fieldDiffs"][number];
  disabled?: boolean;
  hint?: string;
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff} hint={hint}>
      <select
        name={controlName}
        disabled={disabled}
        className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)] disabled:cursor-not-allowed disabled:opacity-60"
        value={value ?? ""}
        onChange={(event) =>
          onChange(normalizeTextValue(event.target.value, optional))
        }
      >
        {optional ? <option value="">Не задано</option> : null}
        {options.map((option) => (
          <option
            key={option.value}
            value={option.value}
            disabled={option.disabled}
            title={option.title}
          >
            {option.label}
          </option>
        ))}
      </select>
    </FieldShell>
  );
}

function BooleanControl({
  label,
  value,
  onChange,
  diff,
}: {
  label: string;
  value: boolean;
  onChange: (value: boolean) => void;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff}>
      <label className="inline-flex items-center gap-2 rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white">
        <input
          type="checkbox"
          name={controlName}
          checked={value}
          onChange={(event) => onChange(event.target.checked)}
        />
        {value ? "включено" : "выключено"}
      </label>
    </FieldShell>
  );
}

function CheckboxGroupControl({
  label,
  values,
  options,
  onChange,
  diff,
  hint,
}: {
  label: string;
  values: string[];
  options: ReadonlyArray<Option>;
  onChange: (value: string[]) => void;
  diff?: EditorSurface["fieldDiffs"][number];
  hint?: string;
}) {
  const controlName = buildControlName(label);
  const selected = new Set(values);

  return (
    <FieldShell label={label} diff={diff} hint={hint}>
      <div className="flex flex-wrap gap-2">
        {options.map((option) => (
          <span
            key={option.value}
            title={option.title}
            className={`inline-flex items-center gap-2 rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white ${
              option.disabled ? "cursor-not-allowed opacity-60" : ""
            }`}
          >
            <input
              type="checkbox"
              name={`${controlName}-${option.value}`}
              checked={selected.has(option.value)}
              disabled={option.disabled}
              onChange={(event) => {
                const next = new Set(values);
                if (event.target.checked) {
                  next.add(option.value);
                } else {
                  next.delete(option.value);
                }
                onChange(
                  options
                    .map((entry) => entry.value)
                    .filter((value) => next.has(value)),
                );
              }}
            />
            {option.label}
          </span>
        ))}
      </div>
    </FieldShell>
  );
}

function TextAreaControl({
  label,
  value,
  rows,
  onChange,
  diff,
}: {
  label: string;
  value: string[] | undefined;
  rows: number;
  onChange: (value: string[]) => void;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff}>
      <textarea
        name={controlName}
        rows={rows}
        className="min-h-24 w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
        value={value?.join("\n") ?? ""}
        onChange={(event) => onChange(splitLines(event.target.value))}
      />
    </FieldShell>
  );
}

function FieldShell({
  label,
  children,
  diff,
  hint,
}: {
  label: string;
  children: ReactNode;
  diff?: EditorSurface["fieldDiffs"][number];
  hint?: string;
}) {
  const sourceLabel =
    diff?.source === "masked"
      ? "скрыто"
      : diff?.source === "live-import"
        ? "считано с роутера"
        : diff?.source === "stale-authoritative"
          ? "эталон панели (stale)"
          : diff?.source === "inventory-only"
            ? "только краткий check-in"
            : diff?.source === "authoritative"
              ? "эталон панели"
              : null;

  return (
    <div className="block rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <div className="flex items-center justify-between gap-3">
        <span className="vectra-kicker text-slate-500">{label}</span>
        {sourceLabel ? (
          <span className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[11px] text-slate-300">
            {sourceLabel}
          </span>
        ) : null}
      </div>
      <div className="mt-2">{children}</div>
      {hint ? (
        <p className="mt-2 text-xs leading-5 text-amber-200">{hint}</p>
      ) : null}
      {diff ? (
        <p className="mt-2 text-xs leading-6 text-slate-400">
          сейчас {diff.currentDisplay} | черновик {diff.draftDisplay}
        </p>
      ) : null}
    </div>
  );
}

function ActualChangesList({
  fieldDiffs,
}: {
  fieldDiffs: PasswallFieldDiff[];
}) {
  const visible = fieldDiffs.slice(0, 8);
  const hiddenCount = Math.max(fieldDiffs.length - visible.length, 0);

  return (
    <div className="mt-4 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-semibold text-white">
            Фактические правки оператора
          </p>
          <p className="mt-1 text-xs leading-5 text-slate-400">
            Здесь считаются поля, которые реально отличаются от базы сравнения.
            Зеркало ShuntRule в Rule Manage скрыто, чтобы одна смена сервера не
            выглядела как две разные правки.
          </p>
        </div>
        <span className="rounded-full border border-white/10 bg-black/10 px-2.5 py-1 text-xs text-slate-300">
          {fieldDiffs.length}
        </span>
      </div>
      {visible.length > 0 ? (
        <ul className="mt-3 space-y-1.5 text-xs leading-5 text-slate-300">
          {visible.map((diff) => (
            <li key={`${diff.path}-${diff.changeType}`} className="break-words">
              <span className="text-slate-500">
                {formatDiffChangeType(diff)}
              </span>{" "}
              {formatOperatorDiffPath(diff.path)}
            </li>
          ))}
          {hiddenCount > 0 ? (
            <li className="text-slate-500">и ещё {hiddenCount}</li>
          ) : null}
        </ul>
      ) : (
        <p className="mt-3 text-xs leading-5 text-slate-400">
          Видимых правок нет. Если ниже есть технические команды — это служебная
          синхронизация, а не новые изменения оператора.
        </p>
      )}
    </div>
  );
}

function OperationRow({ operation }: { operation: PasswallOperationPreview }) {
  const details =
    operation.uciCommands.length > 0
      ? operation.uciCommands.join(" · ")
      : operation.commands.length > 0
        ? operation.commands.join(" · ")
        : "Без подробных команд";
  const detailsCount =
    operation.uciCommands.length || operation.commands.length;
  const longDetails = details.length > 220;

  return (
    <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm text-slate-200">
      <p className="font-semibold text-white">
        {formatOperationTitle(operation)}
      </p>
      <p className="mt-1 text-sm leading-6 text-slate-400">
        {operation.description}
      </p>
      <p className="mt-1 text-xs leading-5 text-slate-500">
        Это технический план применения. Контроллер синхронизирует управляемые
        секции PassWall2 целиком, поэтому команд может быть много даже при одной
        фактической правке.
      </p>
      {longDetails ? (
        <details className="mt-2">
          <summary className="min-h-11 cursor-pointer list-none text-xs font-medium text-slate-300">
            Показать технические UCI-команды ({detailsCount})
          </summary>
          <div className="mt-2 overflow-x-auto rounded-md border border-white/10 bg-black/10 px-2.5 py-2 text-xs leading-5 break-words text-slate-300">
            {details}
          </div>
        </details>
      ) : (
        <p className="mt-2 text-xs leading-5 break-words text-slate-300">
          {details}
        </p>
      )}
    </div>
  );
}

function filterOperatorVisibleFieldDiffs(fieldDiffs: PasswallFieldDiff[]) {
  return fieldDiffs.filter((diff) => {
    if (!diff.path.startsWith("ruleManage.shuntRules")) {
      return true;
    }

    const basicSettingsMirrorPath = diff.path.replace(
      "ruleManage.shuntRules",
      "basicSettings.shuntRules",
    );
    return !fieldDiffs.some(
      (candidate) =>
        candidate.path === basicSettingsMirrorPath &&
        candidate.changeType === diff.changeType,
    );
  });
}

function formatDiffChangeType(diff: PasswallFieldDiff) {
  switch (diff.changeType) {
    case "added":
      return "+";
    case "removed":
      return "−";
    default:
      return "изменено";
  }
}

function formatOperatorDiffPath(path: string) {
  return path
    .replace(/^basicSettings\.main\./, "Основные настройки → ")
    .replace(/^basicSettings\.dns\./, "DNS → ")
    .replace(/^basicSettings\.log\./, "Журнал → ")
    .replace(/^basicSettings\.socks/, "SOCKS")
    .replace(/^basicSettings\.shuntRules/, "ShuntRule")
    .replace(/^ruleManage\.shuntRules/, "Rule Manage → ShuntRule")
    .replace(/^ruleManage\./, "Rule Manage → ")
    .replace(/^subscriptions\.items/, "Подписка")
    .replace(/^subscriptions\./, "Подписки → ")
    .replace(/^nodes/, "Нода")
    .replace(/^appUpdate\./, "App Update → ")
    .replace(/\.outboundNodeId$/, " → сервер")
    .replace(/\.domainRules$/, " → домены")
    .replace(/\.ipRules$/, " → IP")
    .replace(/\.label$/, " → название")
    .replaceAll(".", " → ");
}

function formatOperationTitle(operation: PasswallOperationPreview) {
  switch (operation.kind) {
    case "uci_apply":
      return "Синхронизация базовых настроек";
    case "node_sync":
      return "Синхронизация нод / SOCKS / ShuntRule";
    case "subscription_sync":
      return "Синхронизация подписок";
    case "rule_refresh":
      return "Обновление GeoIP / GeoSite";
    case "package_update":
      return "Обновление пакетов";
    case "service_restart":
      return "Перезапуск PassWall2";
    default:
      return `${operation.section} / ${operation.kind}`;
  }
}

function formatUnconfirmedStatusBadge(group: UnconfirmedChangeGroup) {
  switch (group.status) {
    case "pending-import-review":
      return "нужна проверка базы";
    case "reimport-needed":
      return "нужна сверка";
    case "saved-draft-pending-apply":
      return "ждёт применения";
    default:
      return "чисто";
  }
}

function EmptyState({ text }: { text: string }) {
  return (
    <div className="rounded-md border border-dashed border-white/12 bg-[var(--vectra-panel-soft)] px-3 py-6 text-sm leading-7 text-slate-400">
      {text}
    </div>
  );
}

type ExtrasRecord = DraftConfigInput["nodes"][number]["extras"];
type ExtraValue = ExtrasRecord[string];
type RuleScheduleState = {
  mode: "daily" | "weekly" | "interval";
  day: string;
  hour: string;
  interval: string;
};

function getExtraString(
  extras: ExtrasRecord | undefined,
  key: string,
): string | undefined;
function getExtraString(
  extras: ExtrasRecord | undefined,
  key: string,
  fallback: string,
): string;
function getExtraString(
  extras: ExtrasRecord | undefined,
  key: string,
  fallback?: string,
) {
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
    return value[0] ?? fallback;
  }
  return fallback;
}

function getExtraBoolean(
  extras: ExtrasRecord | undefined,
  key: string,
  fallback = false,
) {
  const value = extras?.[key];
  if (typeof value === "boolean") {
    return value;
  }
  if (typeof value === "number") {
    return value !== 0;
  }
  if (typeof value === "string") {
    if (["1", "true", "yes", "on"].includes(value.toLowerCase())) {
      return true;
    }
    if (["0", "false", "no", "off", ""].includes(value.toLowerCase())) {
      return false;
    }
  }
  return fallback;
}

function getExtraNumber(extras: ExtrasRecord | undefined, key: string) {
  const value = extras?.[key];
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim().length > 0) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  return undefined;
}

function getExtraList(extras: ExtrasRecord | undefined, key: string) {
  const value = extras?.[key];
  if (Array.isArray(value)) {
    return value;
  }
  if (typeof value === "string") {
    return splitLines(value);
  }
  return [];
}

function getExtraTokens(extras: ExtrasRecord | undefined, key: string) {
  const value = extras?.[key];
  if (Array.isArray(value)) {
    return value.flatMap((entry) => tokenizeExtraString(entry));
  }
  if (typeof value === "string") {
    return tokenizeExtraString(value);
  }
  if (typeof value === "number") {
    return [String(value)];
  }
  return [];
}

function tokenizeExtraString(value: string) {
  return value
    .split(/\s+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function encodeExtraTokens(values: string[]): ExtraValue | undefined {
  const next = values.map((value) => value.trim()).filter(Boolean);
  return next.length > 0 ? next.join(" ") : undefined;
}

function formatExtraSelection(
  extras: ExtrasRecord | undefined,
  key: string,
  options: ReadonlyArray<Option>,
) {
  const values = getExtraTokens(extras, key);
  if (values.length === 0) {
    return "не задано";
  }

  return values
    .map(
      (value) =>
        options.find((option) => option.value === value)?.label ?? value,
    )
    .join(", ");
}

function getExtraOptionLabel(
  extras: ExtrasRecord | undefined,
  key: string,
  options: ReadonlyArray<Option>,
  fallback: string,
) {
  const value = getExtraString(extras, key);
  if (!value) {
    return fallback;
  }

  return options.find((option) => option.value === value)?.label ?? value;
}

function boolExtra(value: boolean): ExtraValue {
  return value ? "1" : "0";
}

function getSelectedShuntNode(draft: DraftConfigInput) {
  const selectedId = draft.basicSettings.main.selectedNodeId;
  const selectedNode =
    draft.nodes.find((node) => node.id === selectedId) ?? null;
  if (selectedNode && isLikelyShuntNode(selectedNode)) {
    return selectedNode;
  }

  return draft.nodes.find((node) => isLikelyShuntNode(node)) ?? null;
}

function isLikelyShuntNode(node: DraftConfigInput["nodes"][number]) {
  return (
    node.protocol.toLowerCase() === "shunt" ||
    node.extras.default_node !== undefined ||
    node.extras.default_fakedns !== undefined ||
    node.extras.default_proxy_tag !== undefined ||
    node.extras.fakedns !== undefined ||
    node.label.toLowerCase().includes("shunt") ||
    node.label.includes("分流")
  );
}

function buildShuntTargetOptions(
  draft: DraftConfigInput,
  includeUseDefault: boolean,
) {
  const base = includeUseDefault
    ? shuntTargetBaseOptions
    : shuntDefaultTargetOptions;
  return [
    ...base,
    ...draft.nodes.map((node) => ({
      value: node.id,
      label: node.label,
    })),
  ];
}

function formatShuntTargetLabel(
  draft: DraftConfigInput,
  value: string | undefined,
) {
  switch (value) {
    case undefined:
    case "":
      return "Close (Not use)";
    case "_default":
      return "Use default node";
    case "_direct":
      return "Direct Connection";
    case "_blackhole":
      return "Blackhole (Block)";
    default:
      return resolveNodeLabel(draft, value);
  }
}

function getRuleScheduleState(
  ruleManage: DraftConfigInput["ruleManage"],
): RuleScheduleState {
  const weekUpdate = getExtraString(ruleManage.extras, "week_update");
  const timeUpdate = getExtraString(ruleManage.extras, "time_update");
  const intervalUpdate = getExtraString(ruleManage.extras, "interval_update");

  if (weekUpdate === "8") {
    return {
      mode: "interval",
      day: String(ruleManage.scheduleDay ?? 1),
      hour: String(ruleManage.scheduleHour ?? 0),
      interval: intervalUpdate ?? String(ruleManage.intervalHours ?? 2),
    };
  }

  if (weekUpdate && weekUpdate !== "7") {
    return {
      mode: "weekly",
      day: weekUpdate,
      hour: timeUpdate ?? String(ruleManage.scheduleHour ?? 0),
      interval: intervalUpdate ?? String(ruleManage.intervalHours ?? 2),
    };
  }

  return {
    mode: ruleManage.scheduleMode ?? "daily",
    day: String(ruleManage.scheduleDay ?? 1),
    hour: timeUpdate ?? String(ruleManage.scheduleHour ?? 0),
    interval: intervalUpdate ?? String(ruleManage.intervalHours ?? 2),
  };
}

function updateRuleSchedule(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  ruleManage: DraftConfigInput["ruleManage"],
  patch: Partial<RuleScheduleState>,
) {
  const next = { ...getRuleScheduleState(ruleManage), ...patch };

  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          current.ruleManage.scheduleMode = next.mode;
          current.ruleManage.scheduleHour = Number(next.hour);
          current.ruleManage.intervalHours =
            next.mode === "interval" ? Number(next.interval) : undefined;
          current.ruleManage.scheduleDay =
            next.mode === "weekly" ? Number(next.day) : undefined;

          setExtra(current.ruleManage.extras, "time_update", next.hour);
          if (next.mode === "interval") {
            setExtra(current.ruleManage.extras, "week_update", "8");
            setExtra(
              current.ruleManage.extras,
              "interval_update",
              next.interval,
            );
          } else {
            setExtra(
              current.ruleManage.extras,
              "week_update",
              next.mode === "weekly" ? next.day : "7",
            );
            setExtra(current.ruleManage.extras, "interval_update", undefined);
          }
        })
      : previous,
  );
}

function useSelectionSync(
  ids: string[] | undefined,
  selectedId: string | null,
  setSelectedId: (value: string | null) => void,
) {
  useEffect(() => {
    if (!ids) {
      return;
    }

    if (selectedId && ids.includes(selectedId)) {
      return;
    }

    setSelectedId(ids[0] ?? null);
  }, [ids, selectedId, setSelectedId]);
}

function getDiff(surface: EditorSurface, path: string) {
  return surface.fieldDiffs.find((entry) => entry.path === path);
}

function getRuleManageDiff(
  surface: EditorSurface,
  ruleId: string,
  path: string,
) {
  return getDiff(surface, `Управление правилами[${ruleId}].${path}`);
}

function updatePathValue(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  path: string,
  value: unknown,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          setNestedValue(
            current as unknown as Record<string, unknown>,
            path,
            value,
          );
        })
      : previous,
  );
}

function updateNodeField(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  nodeId: string,
  path: string,
  value: unknown,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          const target = current.nodes.find((node) => node.id === nodeId);
          if (target) {
            setNestedValue(
              target as unknown as Record<string, unknown>,
              path,
              value,
            );
          }
        })
      : previous,
  );
}

function updateSubscriptionField(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  subscriptionId: string,
  path: string,
  value: unknown,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          const target = current.subscriptions.items.find(
            (item) => item.id === subscriptionId,
          );
          if (target) {
            setNestedValue(
              target as unknown as Record<string, unknown>,
              path,
              value,
            );
          }
        })
      : previous,
  );
}

function updateSubscriptionExtra(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  subscriptionId: string,
  key: string,
  value: ExtraValue | undefined,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          const target = current.subscriptions.items.find(
            (item) => item.id === subscriptionId,
          );
          if (target) {
            setExtra(target.extras, key, value);
          }
        })
      : previous,
  );
}

function updateRuleField(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  ruleId: string,
  path: string,
  value: unknown,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          const target = current.basicSettings.shuntRules.find(
            (rule) => rule.id === ruleId,
          );
          if (target) {
            setNestedValue(
              target as unknown as Record<string, unknown>,
              path,
              value,
            );
          }
        })
      : previous,
  );
}

function renameRuleId(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  ruleId: string,
  nextRuleId: string,
) {
  setDraft((previous) =>
    previous ? renameShuntRule(previous, ruleId, nextRuleId) : previous,
  );
}

function updateRuleExtra(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  ruleId: string,
  key: string,
  value: ExtraValue | undefined,
) {
  setDraft((previous) =>
    previous ? updateShuntRuleExtra(previous, ruleId, key, value) : previous,
  );
}

function updateNodeExtra(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  nodeId: string,
  key: string,
  value: ExtraValue | undefined,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          const target = current.nodes.find((node) => node.id === nodeId);
          if (target) {
            setExtra(target.extras, key, value);
          }
        })
      : previous,
  );
}

function updateSocksField(
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>,
  socksId: string,
  path: string,
  value: unknown,
) {
  setDraft((previous) =>
    previous
      ? updateConfig(previous, (current) => {
          const target = current.basicSettings.socks.find(
            (entry) => entry.id === socksId,
          );
          if (target) {
            setNestedValue(
              target as unknown as Record<string, unknown>,
              path,
              value,
            );
          }
        })
      : previous,
  );
}

function resolveNodeLabel(draft: DraftConfigInput, nodeId: string | undefined) {
  if (!nodeId) {
    return "не задана";
  }

  return draft.nodes.find((node) => node.id === nodeId)?.label ?? nodeId;
}

function setExtra(
  extras: ExtrasRecord,
  key: string,
  value: ExtraValue | undefined,
) {
  if (value === undefined || value === null) {
    delete extras[key];
    return;
  }

  if (Array.isArray(value) && value.length === 0) {
    delete extras[key];
    return;
  }

  extras[key] = value;
}

function setNestedValue(
  target: Record<string, unknown>,
  path: string,
  value: unknown,
) {
  const parts = path.split(".");
  let current = target;

  for (const part of parts.slice(0, -1)) {
    const next = current[part];
    if (!next || typeof next !== "object" || Array.isArray(next)) {
      current[part] = {};
    }
    current = current[part] as Record<string, unknown>;
  }

  const last = parts[parts.length - 1];
  if (!last) {
    return;
  }
  if (value === undefined) {
    delete current[last];
    return;
  }
  current[last] = value;
}

function updateConfig(
  previous: DraftConfigInput,
  producer: (draft: DraftConfigInput) => void,
) {
  const next = structuredClone(previous);
  producer(next);
  return normalizeShuntRuleBindings(next);
}

function splitLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function normalizeTextValue(value: string, optional?: boolean) {
  return optional && value.trim().length === 0 ? undefined : value;
}

function normalizeNumberValue(value: string, optional?: boolean) {
  if (value.trim().length === 0) {
    return optional ? undefined : "";
  }
  return Number(value);
}

function buildControlName(label: string) {
  return (
    label
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "field"
  );
}

function supportsControllerFeature(
  currentVersion: string | null | undefined,
  minimumVersion: string,
) {
  if (!normalizeControllerVersion(currentVersion)) {
    return false;
  }

  return (compareControllerVersions(currentVersion, minimumVersion) ?? -1) >= 0;
}

function createSocksDraft(
  nodeId: string,
): DraftConfigInput["basicSettings"]["socks"][number] {
  return {
    id: `socks-${Math.random().toString(36).slice(2, 8)}`,
    enabled: true,
    nodeId,
    port: 2080,
    bindLocal: true,
    autoswitchBackupNodeIds: [],
    extras: {},
  };
}

function formatDateTime(value: Date | string | null | undefined) {
  if (!value) {
    return "никогда";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "неизвестно";
  }

  return date.toLocaleString("ru-RU", { hour12: false });
}

function formatCompactSize(bytes: number | null | undefined) {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes <= 0) {
    return "неизвестно";
  }

  if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  return `${Math.round(bytes / 1024)} KB`;
}

function formatMaybeMegabytes(value: number | null | undefined) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return "неизвестно";
  }

  return `${value} MB`;
}

function formatProxyMode({
  routerReachable,
  directModeActive,
  passwallEnabled,
}: {
  routerReachable: boolean;
  directModeActive: boolean;
  passwallEnabled: boolean;
}) {
  if (!routerReachable) {
    return "нет свежей связи";
  }
  if (directModeActive) {
    return "прямой режим";
  }
  return passwallEnabled ? "прокси-режим" : "PassWall2 выключен";
}

function formatSupportMeta(value: string | null | undefined) {
  switch (value) {
    case "certified":
      return "штатный контур";
    case "pilot":
      return "полный surface, решение на операторе";
    case "blocked":
      return "массовые действия ограничены";
    default:
      return "состояние платформы";
  }
}

function formatServiceState(value: string | null | undefined) {
  switch (value) {
    case "running":
      return "работает";
    case "stopped":
      return "остановлен";
    case "degraded":
      return "с ошибками";
    default:
      return "нет данных";
  }
}
