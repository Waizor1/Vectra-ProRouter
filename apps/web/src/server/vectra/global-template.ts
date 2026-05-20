import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import {
  eventLog,
  operatorGlobalTemplates,
  passwallDesiredRevisions,
  passwallSecretBlobs,
  routers,
} from "@vectra/db";
import type { routerInventorySnapshots } from "@vectra/db";
import { and, desc, eq, inArray, isNull, like } from "drizzle-orm";

import { db as defaultDb } from "~/server/db";
import { isRouterReachable } from "~/server/vectra/router-presence";
import {
  createOperatorDraftRevisionWithDb,
  queueDesiredRevisionApplyJobWithDb,
} from "~/server/vectra/router-control";
import { hydratePasswallConfig } from "~/server/vectra/secrets";
import {
  canRunDestructiveAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";
import { loadLatestSnapshots } from "~/server/vectra/fleet-monitoring-data";

export const AX3000T_GLOBAL_TEMPLATE_KEY = "ax3000t-global-baseline";

type DatabaseClient = typeof defaultDb;

type GlobalTemplateSeed = {
  installBaselineUci: string;
  rolloutConfig: PasswallDesiredConfig;
};

export type GlobalTemplateRolloutMode = "draft_only" | "queue_apply";

export type GlobalTemplateWorkspace = Awaited<
  ReturnType<typeof loadGlobalTemplateWorkspace>
>;

export type GlobalTemplateRolloutResult = Awaited<
  ReturnType<typeof executeGlobalTemplateRollout>
>;

function resolveAppPath(...segments: string[]) {
  const candidates = [
    path.resolve(process.cwd(), ...segments),
    path.resolve(process.cwd(), "apps", "web", ...segments),
  ];

  const resolved = candidates.find((candidate) => existsSync(candidate));
  if (!resolved) {
    throw new Error(`Unable to resolve app path for ${segments.join("/")}.`);
  }

  return resolved;
}

let cachedSeed: GlobalTemplateSeed | null = null;

function readGlobalTemplateSeed(): GlobalTemplateSeed {
  if (cachedSeed) {
    return cachedSeed;
  }

  const installBaselinePath = resolveAppPath(
    "public",
    "install",
    "ax3000t-passwall2-baseline.uci",
  );
  const rolloutTemplatePath = resolveAppPath(
    "src",
    "app",
    "enrollment",
    "__fixtures__",
    "ax3000t-global-rollout-template.seed.json",
  );

  cachedSeed = {
    installBaselineUci: readFileSync(installBaselinePath, "utf8"),
    rolloutConfig: passwallDesiredConfigSchema.parse(
      JSON.parse(readFileSync(rolloutTemplatePath, "utf8")) as unknown,
    ),
  };

  return cachedSeed;
}

export function validateInstallBaselineUci(text: string) {
  const issues: string[] = [];
  const normalized = text.trim();

  if (normalized.length === 0) {
    issues.push("Install baseline не может быть пустым.");
  }

  if (text.includes("\r")) {
    issues.push("Install baseline должен храниться в LF-формате без CRLF.");
  }

  if (!text.includes("config nodes 'myshunt'")) {
    issues.push("В install baseline должен оставаться shunt-узел myshunt.");
  }

  if (/^config subscribe_list\b/m.test(text)) {
    issues.push(
      "Install baseline не должен содержать subscription-секции с чужими URL.",
    );
  }

  if (/^config nodes '(?!myshunt')/m.test(text)) {
    issues.push(
      "Install baseline не должен публиковать реальные proxy-node секции; разрешён только myshunt.",
    );
  }

  if (!text.includes("option default_fakedns '0'")) {
    issues.push("Для Default в install baseline должен оставаться FakeDNS = 0.");
  }

  if (!text.includes("option direct_fakedns '0'")) {
    issues.push("Для Direct в install baseline должен оставаться FakeDNS = 0.");
  }

  if (!text.includes("option remote_dns_protocol 'udp'")) {
    issues.push("Install baseline должен использовать UDP remote DNS.");
  }

  if (!text.includes("option remote_dns '8.8.8.8'")) {
    issues.push("Install baseline должен использовать 8.8.8.8 как remote DNS.");
  }

  if (/^\s*option\s+remote_dns_doh\b/m.test(text)) {
    issues.push("Install baseline не должен хранить remote_dns_doh при UDP DNS.");
  }

  if (text.includes("common.dot.dns.yandex.net")) {
    issues.push("Install baseline не должен пиновать legacy Yandex DNS host.");
  }

  return issues;
}

export function validateRolloutTemplateConfig(config: PasswallDesiredConfig) {
  const issues: string[] = [];

  if (config.subscriptions.items.length > 0) {
    issues.push(
      "Fleet-template не должен хранить subscription items: ссылки остаются локальными для каждого роутера.",
    );
  }

  if (config.nodes.some((node) => node.protocol !== "shunt")) {
    issues.push(
      "Fleet-template не должен хранить реальные proxy-node секции: разрешены только template-managed shunt nodes.",
    );
  }

  const dns = config.basicSettings.dns;

  if (dns.remoteDnsProtocol !== "udp") {
    issues.push("Fleet-template должен использовать UDP remote DNS.");
  }

  if (dns.remoteDns !== "8.8.8.8") {
    issues.push("Fleet-template должен использовать 8.8.8.8 как remote DNS.");
  }

  if (dns.remoteDnsDoh.trim().length > 0) {
    issues.push("Fleet-template не должен хранить remoteDnsDoh при UDP DNS.");
  }

  if (dns.remoteDnsDetour !== "direct") {
    issues.push("Fleet-template должен отправлять remote DNS напрямую.");
  }

  if (
    dns.dnsHosts.some((entry) =>
      entry.toLowerCase().includes("common.dot.dns.yandex.net"),
    )
  ) {
    issues.push("Fleet-template не должен пиновать legacy Yandex DNS host.");
  }

  return issues;
}

function formatBlockedReason(args: {
  importState: string;
  supportAllowed: boolean;
  supportReason: string;
}) {
  if (args.importState !== "approved") {
    return "Сначала завершите import review и переведите роутер в approved.";
  }

  if (!args.supportAllowed) {
    return args.supportReason;
  }

  return null;
}

function firstNonEmptyText(...values: Array<string | null | undefined>) {
  for (const value of values) {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (trimmed.length > 0) {
        return trimmed;
      }
    }
  }

  return null;
}

function buildRouterDisplayName(
  router: typeof routers.$inferSelect,
  snapshot: typeof routerInventorySnapshots.$inferSelect | null,
) {
  return (
    firstNonEmptyText(
      router.displayName,
      router.hostname,
      snapshot?.payload.hostname,
    ) ?? router.deviceIdentifier
  );
}

export function mergeGlobalTemplateIntoRouterConfig(args: {
  template: PasswallDesiredConfig;
  routerConfig: PasswallDesiredConfig;
}) {
  const templateNodeIds = new Set(args.template.nodes.map((node) => node.id));
  const preservedRouterNodes = args.routerConfig.nodes.filter(
    (node) => !templateNodeIds.has(node.id),
  );
  const selectedNodeId =
    args.routerConfig.basicSettings.main.selectedNodeId ??
    args.template.basicSettings.main.selectedNodeId;

  return passwallDesiredConfigSchema.parse({
    schemaVersion: 1,
    basicSettings: {
      ...args.template.basicSettings,
      main: {
        ...args.template.basicSettings.main,
        selectedNodeId,
      },
      socks: args.routerConfig.basicSettings.socks,
    },
    nodes: [...args.template.nodes, ...preservedRouterNodes],
    subscriptions: {
      ...args.template.subscriptions,
      items: args.routerConfig.subscriptions.items,
    },
    appUpdate: args.template.appUpdate,
    ruleManage: args.template.ruleManage,
  });
}

export async function getOrCreateGlobalTemplate(client: DatabaseClient = defaultDb) {
  const [existing] = await client
    .select()
    .from(operatorGlobalTemplates)
    .where(eq(operatorGlobalTemplates.templateKey, AX3000T_GLOBAL_TEMPLATE_KEY))
    .limit(1);

  if (existing) {
    return existing;
  }

  const seed = readGlobalTemplateSeed();
  const [created] = await client
    .insert(operatorGlobalTemplates)
    .values({
      templateKey: AX3000T_GLOBAL_TEMPLATE_KEY,
      title: "AX3000T baseline и fleet-template",
      installBaselineUci: seed.installBaselineUci,
      rolloutConfig: seed.rolloutConfig,
      rolloutMode: "settings_only",
      note: "Создано из санитизированного AX3000T baseline.",
    })
    .returning();

  if (!created) {
    throw new Error("Failed to create operator global template.");
  }

  return created;
}

export async function saveGlobalTemplate(
  input: {
    installBaselineUci: string;
    rolloutConfig: PasswallDesiredConfig;
    note?: string;
  },
  client: DatabaseClient = defaultDb,
) {
  const parsedRolloutConfig = passwallDesiredConfigSchema.parse(input.rolloutConfig);
  const installIssues = validateInstallBaselineUci(input.installBaselineUci);
  const rolloutIssues = validateRolloutTemplateConfig(parsedRolloutConfig);

  if (installIssues.length > 0 || rolloutIssues.length > 0) {
    return {
      ok: false as const,
      issues: [...installIssues, ...rolloutIssues],
      template: null,
    };
  }

  const existing = await getOrCreateGlobalTemplate(client);
  const [updated] = await client
    .update(operatorGlobalTemplates)
    .set({
      title: existing.title,
      installBaselineUci: input.installBaselineUci,
      rolloutConfig: parsedRolloutConfig,
      note: input.note?.trim() ? input.note.trim() : null,
    })
    .where(eq(operatorGlobalTemplates.id, existing.id))
    .returning();

  return {
    ok: true as const,
    issues: [],
    template: updated ?? existing,
  };
}

async function getHydratedRouterConfig(
  client: DatabaseClient,
  routerId: string,
  preferredRevisionId: string | null | undefined,
) {
  const revision =
    preferredRevisionId
      ? (
          await client
            .select()
            .from(passwallDesiredRevisions)
            .where(eq(passwallDesiredRevisions.id, preferredRevisionId))
            .limit(1)
        )[0] ?? null
      : null;

  const fallbackRevision =
    revision ??
    (
      await client
        .select()
        .from(passwallDesiredRevisions)
        .where(eq(passwallDesiredRevisions.routerId, routerId))
        .orderBy(desc(passwallDesiredRevisions.revisionNumber))
        .limit(1)
    )[0] ??
    null;

  if (!fallbackRevision) {
    return null;
  }

  const [secret] = await client
    .select()
    .from(passwallSecretBlobs)
    .where(eq(passwallSecretBlobs.desiredRevisionId, fallbackRevision.id))
    .orderBy(desc(passwallSecretBlobs.createdAt))
    .limit(1);

  return hydratePasswallConfig(
    fallbackRevision.config,
    secret?.ciphertext ?? null,
  );
}

export async function loadGlobalTemplateWorkspace(
  client: DatabaseClient = defaultDb,
) {
  const template = await getOrCreateGlobalTemplate(client);
  const installBaselineIssues = validateInstallBaselineUci(
    template.installBaselineUci,
  );
  const rolloutTemplateIssues = validateRolloutTemplateConfig(
    passwallDesiredConfigSchema.parse(template.rolloutConfig),
  );

  const routerRows = await client
    .select()
    .from(routers)
    .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));

  const routerIds = routerRows.map((router) => router.id);
  const [latestSnapshots, historyRows] = await Promise.all([
    loadLatestSnapshots(client, routerIds),
    client
      .select()
      .from(eventLog)
      .where(
        and(
          isNull(eventLog.routerId),
          like(eventLog.type, "fleet.rollout.%"),
        ),
      )
      .orderBy(desc(eventLog.createdAt))
      .limit(20),
  ]);

  const rolloutTargets = routerRows.map((router) => {
    const snapshot = latestSnapshots.get(router.id) ?? null;
    const support = describeEffectiveRouterSupport({
      router: {
        boardName: router.boardName,
        target: router.target,
        architecture: router.architecture,
        openwrtRelease: router.openwrtRelease,
      },
      inventory: snapshot?.payload ?? null,
    });
    const supportAllowed = canRunDestructiveAction(support.state);

    return {
      id: router.id,
      displayName:
        firstNonEmptyText(router.displayName, router.hostname) ??
        router.deviceIdentifier,
      hostname: router.hostname,
      deviceIdentifier: router.deviceIdentifier,
      status: router.status,
      importState: router.importState,
      lastSeenAt: router.lastSeenAt,
      reachable: isRouterReachable(router.lastSeenAt),
      selectedNodeLabel: snapshot?.payload.selectedNodeLabel ?? null,
      supportState: support.state,
      supportTitle: support.title,
      supportReason: support.reason,
      rolloutEligible: router.importState === "approved" && supportAllowed,
      blockedReason: formatBlockedReason({
        importState: router.importState,
        supportAllowed,
        supportReason: support.reason,
      }),
    };
  });

  return {
    template,
    installBaselineIssues,
    rolloutTemplateIssues,
    summary: {
      eligibleRouterCount: rolloutTargets.filter((router) => router.rolloutEligible)
        .length,
      blockedRouterCount: rolloutTargets.filter((router) => !router.rolloutEligible)
        .length,
      managedNodeCount: template.rolloutConfig.nodes.length,
      shuntRuleCount: template.rolloutConfig.basicSettings.shuntRules.length,
    },
    rolloutTargets,
    history: historyRows,
  };
}

export async function executeGlobalTemplateRollout(
  input: {
    routerIds: string[];
    mode: GlobalTemplateRolloutMode;
    note?: string;
  },
  client: DatabaseClient = defaultDb,
) {
  const template = await getOrCreateGlobalTemplate(client);
  const templateConfig = passwallDesiredConfigSchema.parse(template.rolloutConfig);
  const rolloutIssues = validateRolloutTemplateConfig(templateConfig);

  if (rolloutIssues.length > 0) {
    return {
      ok: false as const,
      issues: rolloutIssues,
      event: null,
      results: [],
      summary: {
        requestedRouterCount: input.routerIds.length,
        preparedCount: 0,
        queuedCount: 0,
        blockedCount: 0,
        failedCount: 0,
      },
    };
  }

  const requestedRouterIds = [...new Set(input.routerIds)];
  const routerRows =
    requestedRouterIds.length > 0
      ? await client
          .select()
          .from(routers)
          .where(inArray(routers.id, requestedRouterIds))
      : [];
  const routerById = new Map(routerRows.map((router) => [router.id, router]));
  const latestSnapshots = await loadLatestSnapshots(
    client,
    routerRows.map((router) => router.id),
  );
  const note = firstNonEmptyText(input.note);

  const results: Array<{
    routerId: string;
    displayName: string;
    status: "prepared" | "queued" | "blocked" | "failed";
    reason: string | null;
    revisionId: string | null;
    jobId: string | null;
  }> = [];

  for (const routerId of requestedRouterIds) {
    const router = routerById.get(routerId);

    if (!router) {
      results.push({
        routerId,
        displayName: routerId,
        status: "failed",
        reason: "Роутер не найден в системе.",
        revisionId: null,
        jobId: null,
      });
      continue;
    }

    const snapshot = latestSnapshots.get(router.id) ?? null;
    const support = describeEffectiveRouterSupport({
      router: {
        boardName: router.boardName,
        target: router.target,
        architecture: router.architecture,
        openwrtRelease: router.openwrtRelease,
      },
      inventory: snapshot?.payload ?? null,
    });
    const supportAllowed = canRunDestructiveAction(support.state);
    const blockedReason = formatBlockedReason({
      importState: router.importState,
      supportAllowed,
      supportReason: support.reason,
    });
    const displayName = buildRouterDisplayName(router, snapshot);

    if (blockedReason) {
      results.push({
        routerId: router.id,
        displayName,
        status: "blocked",
        reason: blockedReason,
        revisionId: null,
        jobId: null,
      });
      continue;
    }

    try {
      const rolloutConfig = await buildTemplateRolloutDraft(client, {
        routerId: router.id,
        preferredRevisionId: router.activeRevisionId ?? router.lastAppliedRevisionId,
        templateConfig,
      });
      const revision = await createOperatorDraftRevisionWithDb(client, {
        routerId: router.id,
        config: rolloutConfig,
        note: [
          `Fleet rollout from global baseline "${template.title}".`,
          note,
        ]
          .filter(Boolean)
          .join(" "),
      });
      const applyJob =
        input.mode === "queue_apply"
          ? await queueDesiredRevisionApplyJobWithDb(client, {
              routerId: router.id,
              desiredRevisionId: revision.id,
            })
          : null;

      results.push({
        routerId: router.id,
        displayName,
        status: input.mode === "queue_apply" ? "queued" : "prepared",
        reason: null,
        revisionId: revision.id,
        jobId: applyJob?.id ?? null,
      });
    } catch (error) {
      results.push({
        routerId: router.id,
        displayName,
        status: "failed",
        reason:
          error instanceof Error
            ? error.message
            : "Не удалось подготовить rollout для этого роутера.",
        revisionId: null,
        jobId: null,
      });
    }
  }

  const summary = {
    requestedRouterCount: requestedRouterIds.length,
    preparedCount: results.filter((result) => result.status === "prepared").length,
    queuedCount: results.filter((result) => result.status === "queued").length,
    blockedCount: results.filter((result) => result.status === "blocked").length,
    failedCount: results.filter((result) => result.status === "failed").length,
  };
  const severity =
    summary.blockedCount > 0 || summary.failedCount > 0 ? "warning" : "info";
  const [event] = await client
    .insert(eventLog)
    .values({
      routerId: null,
      type:
        input.mode === "queue_apply"
          ? "fleet.rollout.queued"
          : "fleet.rollout.prepared",
      severity,
      message:
        input.mode === "queue_apply"
          ? `Оператор подготовил и поставил в очередь глобальный baseline для ${summary.queuedCount} из ${summary.requestedRouterCount} роутеров.`
          : `Оператор подготовил черновики глобального baseline для ${summary.preparedCount} из ${summary.requestedRouterCount} роутеров.`,
      metadata: {
        templateId: template.id,
        templateKey: template.templateKey,
        mode: input.mode,
        note,
        summary,
        results,
      },
    })
    .returning();

  return {
    ok: true as const,
    issues: [],
    event: event ?? null,
    results,
    summary,
  };
}

export async function getCurrentAx3000tInstallBaseline(
  client: DatabaseClient = defaultDb,
) {
  return (await getOrCreateGlobalTemplate(client)).installBaselineUci;
}

export async function buildTemplateRolloutDraft(
  client: DatabaseClient,
  args: {
    routerId: string;
    preferredRevisionId: string | null | undefined;
    templateConfig: PasswallDesiredConfig;
  },
) {
  const routerConfig = await getHydratedRouterConfig(
    client,
    args.routerId,
    args.preferredRevisionId,
  );

  if (!routerConfig) {
    throw new Error(`Router ${args.routerId} has no authoritative config.`);
  }

  return mergeGlobalTemplateIntoRouterConfig({
    template: args.templateConfig,
    routerConfig,
  });
}
