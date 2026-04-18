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
  MASKED_SECRET_PLACEHOLDER,
  passwallDesiredConfigSchema,
  passwallNodeProtocolSchema,
  passwallTransportSchema,
  summarizePasswallRevisionDiff,
  type PasswallDesiredConfig,
  type PasswallOperationPreview,
} from "@vectra/contracts";

import { ActionStrip } from "~/components/action-strip";
import { DataTable, DataTableEmpty } from "~/components/data-table";
import { DisabledFeatureNotice } from "~/components/disabled-feature-notice";
import { ImportReviewActions } from "~/components/import-review-actions";
import { Panel } from "~/components/panel";
import { RescueActions } from "~/components/rescue-actions";
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
  PASSWALL_PACKAGE_TARGET_ROWS,
  buildFallbackPasswallBundleMetadata,
  buildPasswallBundleMetadataFromArtifact,
  packageNameToRuntimeKey,
} from "~/lib/passwall-artifacts";
import {
  formatPasswallAvailableVersion,
  formatPasswallManagedStackAvailableVersion,
  summarizePasswallAttempt,
} from "~/lib/passwall-update-summary";
import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
  isRouterOnboardingPending,
} from "~/lib/router-onboarding";
import {
  formatTelegramReachabilityLabel,
  getTelegramReachabilityChecks,
} from "~/lib/telegram-reachability";
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

type Option = {
  value: string;
  label: string;
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
  { value: "2", label: "Полный re-import" },
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
  { value: "bittorrent", label: "bittorrent" },
] as const satisfies ReadonlyArray<Option>;

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
const minimumTerminalControllerVersion = "0.1.12-r9";

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
    { initialData: initialSurface },
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
    if (!surface.data?.draftConfig) {
      return;
    }

    const revisionId =
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
    setDraft(nextDraft);
    setLoadedRevisionId(revisionId);
    setSavedRevisionId(surface.data.latestDraftId ?? null);
    setSelectedNodeId(nextDraft.nodes[0]?.id ?? null);
    setSelectedSubscriptionId(nextDraft.subscriptions.items[0]?.id ?? null);
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
  const [consoleSelection, setConsoleSelection] = useState<RouterConsoleSelection>(
    normalizedConsoleSelection,
  );
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
    draft?.nodes.map((node) => node.id),
    selectedNodeId,
    setSelectedNodeId,
  );
  useSelectionSync(
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

  const validation = deferredDraft
    ? passwallDesiredConfigSchema.safeParse(deferredDraft)
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
          surface.data.authoritativeConfig ?? surface.data.currentLiveConfig,
          validDraft,
        )
      : null;

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

  if (surface.isLoading || !surface.data || !draft) {
    return (
      <div className="rounded-md border border-white/10 bg-[var(--vectra-panel)] px-4 py-4 text-sm text-slate-300">
        Загружаю рабочую поверхность роутера...
      </div>
    );
  }

  const editor = surface.data;
  const currentDraftFingerprint = JSON.stringify(draft);
  const loadedDraftFingerprint = JSON.stringify(editor.draftConfig);
  const hasUnsavedChanges = currentDraftFingerprint !== loadedDraftFingerprint;
  const selectedNode =
    draft.nodes.find((node) => node.id === selectedNodeId) ?? null;
  const selectedSubscription =
    draft.subscriptions.items.find(
      (item) => item.id === selectedSubscriptionId,
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
      ? "Сначала завершите импорт и подтвердите эталон."
      : !editor.routerRuntimeSummary.destructiveActionsAllowed
        ? "Для этого роутера применение отключено."
        : "Сохранит текущие поля и поставит применение в очередь.";

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
        selectedNode={selectedNode}
        selectedNodeId={selectedNodeId}
        setSelectedNodeId={setSelectedNodeId}
        setDraft={setDraft}
      />
    );
  } else if (effectivePrimaryTab === "node-subscribe") {
    tabContent = (
      <SubscriptionSection
        routerId={routerId}
        draft={draft}
        surface={editor}
        selectedSubscription={selectedSubscription}
        selectedSubscriptionId={selectedSubscriptionId}
        setSelectedSubscriptionId={setSelectedSubscriptionId}
        setDraft={setDraft}
        canRunJobs={editor.routerRuntimeSummary.destructiveActionsAllowed}
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
  const onboarding = describeRouterOnboarding(
    editor.routerRuntimeSummary.importState,
  );
  const onboardingPending = isRouterOnboardingPending(
    editor.routerRuntimeSummary.importState,
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

  return (
    <div className="space-y-4 xl:space-y-5">
      <div className="vectra-main-grid gap-4 xl:gap-5">
        <section className="vectra-hero-panel min-w-0 rounded-[1.6rem] px-4 py-4 sm:px-5 sm:py-5">
          <div className="space-y-3">
            <div>
              <p className="vectra-kicker text-[var(--vectra-accent)]">Router Console</p>
              <h2 className="mt-1 text-xl font-semibold tracking-[-0.03em] text-white sm:text-2xl">
                {editor.routerRuntimeSummary.name}
              </h2>
              <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-400">
                Сначала сверьте текущее состояние и контекст импорта, затем выберите безопасное действие справа: сохранить, применить, открыть диагностику или перейти в опасную зону.
              </p>
            </div>
            <div className="vectra-summary-grid min-w-0">
            <SummaryCell
              label="Платформа"
              value={editor.routerRuntimeSummary.name}
              meta={`${editor.routerRuntimeSummary.boardName ?? "board n/a"} · ${
                editor.routerRuntimeSummary.layoutFamily ?? "layout n/a"
              }`}
            />
            <SummaryCell
              label="Связь с контроллером"
              value={formatDateTime(editor.routerRuntimeSummary.lastSeenAt)}
              meta={
                routerReachable ? "контроллер на связи" : "свежей связи нет"
              }
            />
            <SummaryCell
              label="Этап подключения"
              value={formatRouterImportStateLabel(
                editor.routerRuntimeSummary.importState,
              )}
              meta={
                onboardingPending
                  ? onboarding.cardHint
                  : "Эталон подтверждён. Локальные изменения делаются уже здесь."
              }
            />
            <SummaryCell
              label="Поддержка платформы"
              value={editor.routerRuntimeSummary.supportTitle}
              meta={editor.routerRuntimeSummary.supportReason}
            />
            <SummaryCell
              label="Выбранная нода"
              value={
                editor.routerRuntimeSummary.selectedNodeLabel ??
                draft.basicSettings.main.selectedNodeId ??
                "не выбрана"
              }
              meta={`${currentModeLabel} · задач в очереди: ${editor.routerRuntimeSummary.pendingChanges}`}
            />
            {telegramChecks.length > 0 ? (
              <details className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 xl:col-span-3">
                <summary className="cursor-pointer list-none">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="vectra-kicker text-slate-500">Проверки Telegram</p>
                      <p className="mt-2 text-sm font-medium text-white">
                        {formatTelegramReachabilityLabel(inventory.telegramReachability)}
                      </p>
                    </div>
                    <span className="text-xs text-slate-400">{telegramChecks.length} цели</span>
                  </div>
                </summary>
                <div className="mt-3 grid gap-2 md:grid-cols-2">
                  {telegramChecks.map((check) => (
                    <div
                      key={`${check.label}-${check.checkedAt ?? "na"}`}
                      className="rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <p className="text-sm font-medium text-white">{check.label}</p>
                        <span
                          className={`text-xs ${
                            check.reachable ? "text-emerald-100" : "text-rose-200"
                          }`}
                        >
                          {check.reachable ? "доступно" : "недоступно"}
                        </span>
                      </div>
                      <p className="mt-2 text-xs leading-5 text-slate-400">{check.detail}</p>
                      <p className="mt-1 text-[11px] leading-5 text-slate-500">
                        Проверка {formatDateTime(check.checkedAt)}
                      </p>
                    </div>
                  ))}
                </div>
              </details>
            ) : null}
            </div>

            <div className="vectra-stat-grid">
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

              <div className="min-w-0 rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-3 py-3 sm:px-4 sm:py-4">
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
                    description={
                      validationMessage ??
                      (hasUnsavedChanges
                          ? "Панель и apply используют только сохранённую ревизию. Пока вы не сохраните форму, эти изменения видны только в текущем окне."
                          : savedDraftExists
                            ? "Можно безопасно перейти к apply или оставить текущую сохранённую ревизию как есть."
                            : "Текущая форма валидна, но ещё не записана в панель как отдельная ревизия.")
                    }
                  />
                  <InlineStateCard
                    eyebrow="Следующий шаг"
                    title={
                      editor.approvalRequired
                        ? "Сначала подтвердите import"
                        : !editor.routerRuntimeSummary.destructiveActionsAllowed
                          ? "Apply сейчас заблокирован"
                          : hasUnsavedChanges
                            ? "Сохраните и примените на роутере"
                            : "Можно применять сохранённый черновик"
                    }
                    tone={
                      editor.approvalRequired ||
                      !editor.routerRuntimeSummary.destructiveActionsAllowed
                        ? "warning"
                        : "good"
                    }
                    description={
                      editor.approvalRequired
                        ? "Пока import не принят как эталон, Vectra не отправляет apply на роутер."
                        : !editor.routerRuntimeSummary.destructiveActionsAllowed
                          ? "Для этого роутера destructive/apply-действия сейчас отключены политикой поддержки."
                          : hasUnsavedChanges
                            ? "Основной безопасный путь для новых правок — сначала сохранить текущие поля в ревизию, затем сразу поставить apply в очередь."
                            : "Если правки уже сохранены, apply использует последнюю ревизию из панели без скрытых изменений из формы."
                    }
                  />
                </div>
                <div className="mt-3 grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(320px,0.9fr)]">
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
                  <div className="min-w-0 rounded-md border border-white/10 bg-black/10 px-3 py-2 text-sm leading-6 text-slate-300">
                    {validationMessage ? (
                      <span className="text-rose-200">{validationMessage}</span>
                    ) : (
                      <>
                        <strong className="text-white">Проверка черновика.</strong>{" "}
                        <code>{MASKED_SECRET_PLACEHOLDER}</code> = сохранённый секрет. Сохранение пишет ревизию только в панель, apply всегда идёт из уже сохранённого черновика. Заблокированные роутеры всё равно позволяют сохранить ревизию без apply.
                      </>
                    )}
                  </div>
                </div>
              </div>
          </div>
        </section>

        <div className="min-w-0 xl:sticky xl:top-4 xl:self-start">
          <RouterActionRail
            routerId={routerId}
            importedRevisionId={editor.importedRevisionId}
            importState={editor.routerRuntimeSummary.importState}
            validDraft={Boolean(validDraft)}
            savePending={saveMutation.isPending}
            queuePending={queueMutation.isPending}
            deletePending={deleteRouterMutation.isPending}
            canApplyCurrentDraft={canApplyCurrentDraft}
            canQueueApply={canQueueApply}
            hasUnsavedChanges={hasUnsavedChanges}
            saveDisabledReason={saveDisabledReason}
            applyDisabledReason={applyDisabledReason}
            handleSaveDraft={handleSaveDraft}
            handleSaveAndApply={handleSaveAndApply}
            watchLogsSupported={watchLogsSupported}
            watchLogsHref={watchLogsHref}
            minimumWatchLogsControllerVersion={minimumWatchLogsControllerVersion}
            needsRecoveryAction={needsRecoveryAction}
            directModeActive={directModeActive}
            routerReachable={routerReachable}
            handleDeleteRouter={handleDeleteRouter}
          />
        </div>
      </div>

      <div className="vectra-main-grid gap-4 xl:gap-5">
        <div className="min-w-0">
          <Panel
            eyebrow="PassWall workspace"
            title="Вкладки и редакторы"
            tone="muted"
          >
            <TabBar items={primaryItems} ariaLabel="Основные вкладки PassWall" />
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

        <div className="min-w-0">
          <Panel eyebrow="Предпросмотр применения" title="Что уйдёт на роутер" tone="muted">
            <div className="vectra-stat-grid">
              <StatusTile
                label="Перезапуск"
                value={preview?.requiresRestart ? "нужен" : "нет"}
                compact
              />
              <StatusTile
                label="Подписки"
                value={preview?.refreshSubscriptions ? "обновить" : "без изменений"}
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
        </div>
      </div>
    </div>
  );
}

function RouterActionRail({
  routerId,
  importedRevisionId,
  importState,
  validDraft,
  savePending,
  queuePending,
  deletePending,
  canApplyCurrentDraft,
  canQueueApply,
  hasUnsavedChanges,
  saveDisabledReason,
  applyDisabledReason,
  handleSaveDraft,
  handleSaveAndApply,
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
  validDraft: boolean;
  savePending: boolean;
  queuePending: boolean;
  deletePending: boolean;
  canApplyCurrentDraft: boolean;
  canQueueApply: boolean;
  hasUnsavedChanges: boolean;
  saveDisabledReason: string;
  applyDisabledReason: string;
  handleSaveDraft: () => Promise<void>;
  handleSaveAndApply: () => Promise<void>;
  watchLogsSupported: boolean;
  watchLogsHref: string;
  minimumWatchLogsControllerVersion: string;
  needsRecoveryAction: boolean;
  directModeActive: boolean;
  routerReachable: boolean;
  handleDeleteRouter: () => void;
}) {
  return (
    <div className="space-y-3">
      <ImportReviewActions
        routerId={routerId}
        revisionId={importedRevisionId}
        importState={importState}
      />

      <section className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] px-4 py-4">
        <p className="vectra-kicker text-[var(--vectra-accent)]">Операторский поток</p>
        <p className="mt-3 text-sm leading-6 text-slate-300">
          Сначала сверьте состояние и import, затем выполните ближайшее безопасное действие. Apply никогда не берёт скрытые правки из формы — он работает только с уже сохранённой ревизией.
        </p>
      </section>

      <ActionGroup
        eyebrow="Следующее безопасное действие"
        title="Сохранение и применение"
        tone={canApplyCurrentDraft || hasUnsavedChanges ? "good" : "default"}
        description={
          hasUnsavedChanges
            ? "Сначала зафиксируйте текущие поля как ревизию, затем при необходимости сразу отправьте apply на роутер."
            : canQueueApply
              ? "Форма уже совпадает с сохранённой ревизией. Можно сразу ставить apply в очередь."
              : "Проверьте причину блокировки ниже: она показывает, почему apply сейчас недоступен."
        }
      >
        <div className="grid gap-2">
          <InlineStateCard
            eyebrow="Сохранение в панели"
            title={
              hasUnsavedChanges
                ? "Новые правки есть только в форме"
                : validDraft
                  ? "Сохранённая ревизия актуальна"
                  : "Сначала исправьте форму"
            }
            tone={
              !validDraft ? "danger" : hasUnsavedChanges ? "warning" : "good"
            }
            description={saveDisabledReason}
          />
          <InlineStateCard
            eyebrow="Применение на роутере"
            title={
              !canApplyCurrentDraft
                ? "Apply сейчас недоступен"
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
                ? "Если ничего не менялось, на роутер уйдёт уже сохранённый черновик из панели."
                : applyDisabledReason
            }
          />
        </div>
        <ActionStrip justify="start" dense>
          <button
            type="button"
            disabled={!validDraft || savePending || (!hasUnsavedChanges && canQueueApply)}
            onClick={() => {
              void handleSaveDraft();
            }}
            className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            {savePending ? "Сохраняю..." : "Сохранить только в панели"}
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
                : "Сохранить и применить на роутере"}
          </button>
          <Link
            href={`/drafts?routerId=${routerId}`}
            className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
          >
            Экспертный JSON
          </Link>
        </ActionStrip>
      </ActionGroup>

      <ActionGroup
        eyebrow="Диагностика"
        title="Recovery и журналы"
        description="Сначала безопасные read-only или recovery-действия, без удаления данных из панели."
      >
        <ActionStrip justify="start" dense>
          <RescueActions
            routerId={routerId}
            needsRecoveryAction={needsRecoveryAction}
            directModeActive={directModeActive}
            routerReachable={routerReachable}
          />
          {watchLogsSupported ? (
            <Link
              href={watchLogsHref}
              className="vectra-button-secondary px-3 py-2 text-sm font-medium transition hover:border-white/20 hover:text-white"
            >
              Открыть Watch Logs
            </Link>
          ) : (
            <span className="rounded-xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-2 text-sm text-slate-400">
              Watch Logs включится после controller {minimumWatchLogsControllerVersion}
            </span>
          )}
        </ActionStrip>
      </ActionGroup>

      <ActionGroup
        eyebrow="Опасная зона"
        title="Удаление роутера"
        tone="warning"
      >
        <ActionStrip justify="start" dense>
          <button
            type="button"
            disabled={deletePending}
            onClick={handleDeleteRouter}
            className="vectra-button-danger px-3 py-2 text-sm font-medium transition hover:border-rose-300/40 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {deletePending ? "Удаляю роутер..." : "Удалить роутер из системы"}
          </button>
        </ActionStrip>
        <InlineStateCard
          eyebrow="Что именно удалится"
          title="Панель забудет этот роутер, но не удалит пакеты на устройстве"
          tone="danger"
          description="Удаляются черновики, задачи, снапшоты и связанные записи панели. На самом роутере PassWall2 и controller не трогаются. Если контроллер снова зарегистрируется, устройство появится как новый или повторно импортированный роутер."
        />
      </ActionGroup>
    </div>
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
                  ? "bg-[var(--vectra-accent-soft)] ring-1 ring-inset ring-[var(--vectra-line-strong)]"
                  : ""
              }`}
              onClick={() => setSelectedSocksId(item.id)}
            >
              <td className="px-3 py-2 font-medium text-white">
                <div className="flex items-center gap-2">
                  <span>{item.id}</span>
                  {item.id === selectedSocksId ? (
                    <span className="rounded-full border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-2 py-0.5 text-[11px] font-medium text-sky-100">
                      выбрано
                    </span>
                  ) : null}
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
          updatePathValue(setDraft, "basicSettings.dns.remoteDns", value ?? "")
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
          updatePathValue(setDraft, "basicSettings.dns.remoteDnsDetour", value)
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
  selectedNode,
  selectedNodeId,
  setSelectedNodeId,
  setDraft,
}: {
  draft: DraftConfigInput;
  selectedNode: DraftConfigInput["nodes"][number] | null;
  selectedNodeId: string | null;
  setSelectedNodeId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
}) {
  return (
    <div className="space-y-4">
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

      <DataTable
        columns={[
          { key: "label", label: "Нода" },
          { key: "protocol", label: "Протокол" },
          { key: "endpoint", label: "Endpoint" },
          { key: "state", label: "Состояние" },
        ]}
      >
        {draft.nodes.length > 0 ? (
          draft.nodes.map((node) => (
            <tr
              key={node.id}
              className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                node.id === selectedNodeId
                  ? "bg-[var(--vectra-accent-soft)] ring-1 ring-inset ring-[var(--vectra-line-strong)]"
                  : ""
              }`}
              onClick={() => setSelectedNodeId(node.id)}
            >
              <td className="px-3 py-2">
                <div className="flex items-center gap-2 font-medium text-white">
                  <span>{node.label}</span>
                  {node.id === selectedNodeId ? (
                    <span className="rounded-full border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-2 py-0.5 text-[11px] font-medium text-sky-100">
                      выбрано
                    </span>
                  ) : null}
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
          <DataTableEmpty colSpan={4}>Ноды пока не добавлены.</DataTableEmpty>
        )}
      </DataTable>

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
          </FieldGrid>
        </SectionBox>
      ) : null}
    </div>
  );
}

function SubscriptionSection({
  routerId,
  draft,
  surface,
  selectedSubscription,
  selectedSubscriptionId,
  setSelectedSubscriptionId,
  setDraft,
  canRunJobs,
}: {
  routerId: string;
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedSubscription:
    | DraftConfigInput["subscriptions"]["items"][number]
    | null;
  selectedSubscriptionId: string | null;
  setSelectedSubscriptionId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  canRunJobs: boolean;
}) {
  const router = useRouter();
  const utils = api.useUtils();
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

  return (
    <div className="space-y-4">
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
          disabled={!canRunJobs || refreshMutation.isPending}
          onClick={() => refreshMutation.mutate({ routerId })}
          className="rounded-md bg-[var(--vectra-accent-soft)] px-3 py-2 text-sm font-medium text-white transition hover:bg-[rgba(99,185,255,0.22)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {refreshMutation.isPending
            ? "Ставлю refresh..."
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

      <DataTable
        columns={[
          { key: "remark", label: "Подписка" },
          { key: "url", label: "URL" },
          { key: "mode", label: "Режим" },
          { key: "state", label: "Состояние" },
        ]}
      >
        {draft.subscriptions.items.length > 0 ? (
          draft.subscriptions.items.map((item) => (
            <tr
              key={item.id}
              className={`cursor-pointer border-t border-white/10 text-slate-200 transition hover:bg-white/[0.04] ${
                item.id === selectedSubscriptionId
                  ? "bg-[var(--vectra-accent-soft)] ring-1 ring-inset ring-[var(--vectra-line-strong)]"
                  : ""
              }`}
              onClick={() => setSelectedSubscriptionId(item.id)}
            >
              <td className="px-3 py-2">
                <div className="flex items-center gap-2 font-medium text-white">
                  <span>{item.remark}</span>
                  {item.id === selectedSubscriptionId ? (
                    <span className="rounded-full border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-2 py-0.5 text-[11px] font-medium text-sky-100">
                      выбрано
                    </span>
                  ) : null}
                </div>
                <div className="text-xs text-slate-500">{item.id}</div>
              </td>
              <td className="px-3 py-2">{item.url}</td>
              <td className="px-3 py-2">
                {item.addMode === "1" ? "merge" : "re-import"}
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
              options={subscriptionItemDomainStrategyOptions}
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
  title,
  description,
}: {
  draft: DraftConfigInput;
  selectedRule: DraftConfigInput["basicSettings"]["shuntRules"][number] | null;
  selectedRuleId: string | null;
  setSelectedRuleId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  title: string;
  description?: string;
}) {
  const selectedShuntNode = getSelectedShuntNode(draft);
  const ruleTargetOptions = buildShuntTargetOptions(draft, true);
  const defaultTargetOptions = buildShuntTargetOptions(draft, false);
  const visibleRules = draft.basicSettings.shuntRules;

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

      <DataTable
        columns={[
          { key: "label", label: title },
          { key: "node", label: "Target" },
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
                  ? "bg-[var(--vectra-accent-soft)] ring-1 ring-inset ring-[var(--vectra-line-strong)]"
                  : ""
              }`}
              onClick={() => setSelectedRuleId(rule.id)}
            >
              <td className="px-3 py-2">
                <div className="flex items-center gap-2 font-medium text-white">
                  <span>{rule.label}</span>
                  {rule.id === selectedRuleId ? (
                    <span className="rounded-full border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-2 py-0.5 text-[11px] font-medium text-sky-100">
                      выбрано
                    </span>
                  ) : null}
                </div>
                <div className="text-xs text-slate-500">{rule.id}</div>
              </td>
              <td className="px-3 py-2">
                {formatShuntTargetLabel(draft, rule.outboundNodeId)}
              </td>
              <td className="px-3 py-2">
                {selectedShuntNode &&
                getExtraBoolean(selectedShuntNode.extras, `${rule.id}_fakedns`)
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
          <DataTableEmpty colSpan={6}>Shunt-правил пока нет.</DataTableEmpty>
        )}
      </DataTable>

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
          </FieldGrid>
        </SectionBox>
      ) : null}
    </div>
  );
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
  const artifactsQuery = api.update.artifacts.useQuery();
  const latestControllerArtifact =
    artifactsQuery.data?.find(
      (artifact) =>
        artifact.type === "controller" &&
        artifact.name === "vectra-controller-agent",
    ) ?? null;
  const latestPasswallBundleArtifact =
    artifactsQuery.data?.find((artifact) => artifact.type === "passwall_bundle") ??
    null;
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
  const passwallAttempt = surface.lastPasswallUpdateAttempt ?? null;
  const passwallHint = summarizePasswallAttempt(passwallAttempt);
  const overlayFreeMb = inventory.resources?.overlayFreeMb ?? null;
  const tmpFreeMb = inventory.resources?.tmpFreeMb ?? null;
  const backendDeliveryBlocked =
    controlPlaneHealth !== null &&
    (!controlPlaneHealth.ok || controlPlaneHealth.checks?.dbWriteProbe === false);
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
  const versionRows = [
    {
      key: "controller",
      name: "Controller",
      installed: formatControllerVersion(installedControllerVersion),
      available: controllerAvailableLabel,
      action: "controller" as const,
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
    })),
  ];

  return (
    <div className="space-y-4">
      <ActionStrip justify="start">
        <span className="text-sm text-slate-300">
          Кнопка `PassWall2` обновляет не только LuCI-приложение, а весь managed
          stack: bundle `{passwallBundleMetadata.releaseTag}`, app-package
          `{passwallAppArtifact?.artifactVersion ?? "unknown"}`, recovery deps и
          post-update repair.
        </span>
        <span className="text-sm text-slate-400">
          Сам `luci-app-passwall2`
          {passwallAppArtifact
            ? ` небольшой: ${formatCompactSize(passwallAppArtifact.downloadSizeBytes)} download / ${formatCompactSize(passwallAppArtifact.installedSizeBytes)} installed.`
            : " небольшой по сравнению со stack-компонентами."}{" "}
          Тяжёлое место занимают `xray-core`
          {xrayArtifact
            ? ` (${formatCompactSize(xrayArtifact.downloadSizeBytes)} download / ${formatCompactSize(xrayArtifact.installedSizeBytes)} installed)`
            : ""}{" "}
          и recovery-пакеты.
          {overlayFreeMb !== null || tmpFreeMb !== null
            ? ` Сейчас свободно: overlay ${formatMaybeMegabytes(overlayFreeMb)}, /tmp ${formatMaybeMegabytes(tmpFreeMb)}.`
            : ""}
        </span>
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

      <DataTable
        columns={[
          { key: "name", label: "Компонент" },
          { key: "installed", label: "Установлено" },
          { key: "available", label: "Доступно" },
          { key: "action", label: "Действие" },
        ]}
      >
        {versionRows.map((row) => (
          <tr key={row.key} className="border-t border-white/10 text-slate-200">
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
                  disabled={!canRunJobs || passwallUpdateMutation.isPending}
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
                      : `Обновить ${row.name}`}
                </button>
              )}
            </td>
          </tr>
        ))}
      </DataTable>

      {controllerHint ? (
        <p className="text-sm text-slate-400">{controllerHint}</p>
      ) : null}
      {passwallHint ? (
        <p className="text-sm text-slate-400">{passwallHint}</p>
      ) : null}
      {!backendDeliveryBlocked && controlPlaneHealth?.checkedAt ? (
        <p className="text-sm text-slate-500">
          Backend write-probe ok: {formatDateTime(controlPlaneHealth.checkedAt)}.
        </p>
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

function RuleManageSection({
  routerId,
  draft,
  surface,
  selectedRule,
  selectedRuleId,
  setSelectedRuleId,
  setDraft,
  canRunJobs,
}: {
  routerId: string;
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedRule: DraftConfigInput["basicSettings"]["shuntRules"][number] | null;
  selectedRuleId: string | null;
  setSelectedRuleId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
  canRunJobs: boolean;
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
}: {
  draft: DraftConfigInput;
  surface: EditorSurface;
  selectedRule: DraftConfigInput["basicSettings"]["shuntRules"][number] | null;
  selectedRuleId: string | null;
  setSelectedRuleId: (value: string | null) => void;
  setDraft: Dispatch<SetStateAction<DraftConfigInput | null>>;
}) {
  const visibleRules = draft.basicSettings.shuntRules;

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
                  ? "bg-[var(--vectra-accent-soft)] ring-1 ring-inset ring-[var(--vectra-line-strong)]"
                  : ""
              }`}
              onClick={() => setSelectedRuleId(rule.id)}
            >
              <td className="px-3 py-2 font-medium text-white">
                <div className="flex items-center gap-2">
                  <span>{rule.id}</span>
                  {rule.id === selectedRuleId ? (
                    <span className="rounded-full border border-[var(--vectra-line-strong)] bg-[var(--vectra-accent-soft)] px-2 py-0.5 text-[11px] font-medium text-sky-100">
                      выбрано
                    </span>
                  ) : null}
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
              options={shuntProtocolOptions}
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
  ].filter(([, value]) => Boolean(value));

  if (!rows.length) {
    return (
      <EmptyState text="Роутер ещё не прислал данные по rule assets и Geo View пока пуст." />
    );
  }

  return (
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
    <section className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-4 py-4">
      <h3 className="text-base font-semibold text-white">{title}</h3>
      <div className="mt-4">{children}</div>
    </section>
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
    <div className="rounded-2xl border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <p className="vectra-kicker text-slate-500">{label}</p>
      <p className="mt-2 text-sm font-semibold tracking-[-0.01em] text-white sm:text-base">
        {value}
      </p>
      {meta ? (
        <p className="mt-1 text-xs leading-5 text-slate-400 sm:text-sm sm:leading-6">
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
    <section className={`rounded-2xl border px-4 py-4 ${toneClassName}`}>
      <p className={`vectra-kicker ${eyebrowClassName}`}>{eyebrow}</p>
      <h3 className="mt-2 text-sm font-semibold tracking-[-0.01em] text-white sm:text-base">
        {title}
      </h3>
      <p className="mt-1 hidden text-sm leading-6 text-slate-400 sm:block">
        {description}
      </p>
      <div className="mt-3 space-y-3">{children}</div>
    </section>
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
    <section className={`rounded-2xl border px-3 py-3 ${toneClassName}`}>
      <p className={`vectra-kicker ${eyebrowClassName}`}>{eyebrow}</p>
      <p className="mt-2 text-sm font-medium text-white sm:text-[15px]">{title}</p>
      <p className="mt-1 text-sm leading-6 text-slate-300">{description}</p>
    </section>
  );
}

function TextControl({
  label,
  value,
  onChange,
  optional,
  diff,
}: {
  label: string;
  value: string | undefined;
  onChange: (value: string | undefined) => void;
  optional?: boolean;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff}>
      <input
        name={controlName}
        className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
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
}: {
  label: string;
  value: number | undefined;
  onChange: (value: number | undefined | "") => void;
  optional?: boolean;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff}>
      <input
        type="number"
        name={controlName}
        className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
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
}: {
  label: string;
  value: string | undefined;
  options: ReadonlyArray<Option>;
  onChange: (value: string | undefined) => void;
  optional?: boolean;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  const controlName = buildControlName(label);
  return (
    <FieldShell label={label} diff={diff}>
      <select
        name={controlName}
        className="w-full rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white transition outline-none focus:border-[var(--vectra-line-strong)]"
        value={value ?? ""}
        onChange={(event) =>
          onChange(normalizeTextValue(event.target.value, optional))
        }
      >
        {optional ? <option value="">Не задано</option> : null}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
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
}: {
  label: string;
  values: string[];
  options: ReadonlyArray<Option>;
  onChange: (value: string[]) => void;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  const controlName = buildControlName(label);
  const selected = new Set(values);

  return (
    <FieldShell label={label} diff={diff}>
      <div className="flex flex-wrap gap-2">
        {options.map((option) => (
          <span
            key={option.value}
            className="inline-flex items-center gap-2 rounded-md border border-white/10 bg-[rgba(11,14,20,0.86)] px-3 py-2 text-sm text-white"
          >
            <input
              type="checkbox"
              name={`${controlName}-${option.value}`}
              checked={selected.has(option.value)}
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
}: {
  label: string;
  children: ReactNode;
  diff?: EditorSurface["fieldDiffs"][number];
}) {
  return (
    <div className="block rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3">
      <span className="vectra-kicker text-slate-500">{label}</span>
      <div className="mt-2">{children}</div>
      {diff ? (
        <p className="mt-2 text-xs leading-6 text-slate-400">
          сейчас {diff.currentDisplay} | черновик {diff.draftDisplay}
        </p>
      ) : null}
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
  const detailsCount = operation.uciCommands.length || operation.commands.length;
  const longDetails = details.length > 220;

  return (
    <div className="rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 text-sm text-slate-200">
      <p className="font-semibold text-white">
        {operation.section} / {operation.kind}
      </p>
      <p className="mt-1 text-sm leading-6 text-slate-400">
        {operation.description}
      </p>
      {longDetails ? (
        <details className="mt-2">
          <summary className="cursor-pointer list-none text-xs font-medium text-slate-300">
            Показать команды ({detailsCount})
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
  next.ruleManage.shuntRules = structuredClone(next.basicSettings.shuntRules);
  return next;
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
