import {
  artifacts,
  eventLog,
  jobs,
  operatorRolloutProfiles,
  operatorRouterGroups,
  routers,
} from "@vectra/db";
import type { routerInventorySnapshots } from "@vectra/db";
import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import { desc, eq, inArray } from "drizzle-orm";

import {
  buildFallbackPasswallBundleMetadata,
  buildPasswallBundleMetadataFromArtifact,
  findPasswallRuntimeTarget,
  formatPasswallArtifactSourceLabel,
  packageNameToRuntimeKey,
} from "~/lib/passwall-artifacts";
import {
  compareControllerVersions,
  formatControllerVersion,
} from "~/lib/controller-version";
import { isControllerUpdateJob } from "~/lib/controller-update-jobs";
import { compareLooseSemverVersions } from "~/lib/passwall-update-summary";
import { db as defaultDb } from "~/server/db";
import {
  createOperatorDraftRevisionWithDb,
  queueDesiredRevisionApplyJobWithDb,
} from "~/server/vectra/router-control";
import {
  canRunDestructiveAction,
  canRunUpdateAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";
import { buildTemplateRolloutDraft } from "~/server/vectra/global-template";
import { loadLatestSnapshots } from "~/server/vectra/fleet-monitoring-data";

type DatabaseClient = typeof defaultDb;
type RouterRow = typeof routers.$inferSelect;
type SnapshotRow = typeof routerInventorySnapshots.$inferSelect;
type ArtifactRow = typeof artifacts.$inferSelect;

const DEFAULT_GROUP_KEYS = [
  {
    groupKey: "all",
    name: "Общая группа",
    description: "Основная группа для штатной массовой рассылки по парку.",
  },
  {
    groupKey: "test",
    name: "Тестовая группа",
    description: "Небольшая группа для прогонов перед широкой рассылкой.",
  },
  {
    groupKey: "special",
    name: "Особая группа",
    description:
      "Отдельная конфигурация для нестандартного подмножества роутеров.",
  },
] as const;

const DEFAULT_PROFILE_KEY = "default-global-rollout";

function firstNonEmptyText(...values: Array<string | null | undefined>) {
  for (const value of values) {
    if (typeof value !== "string") {
      continue;
    }

    const trimmed = value.trim();
    if (trimmed.length > 0) {
      return trimmed;
    }
  }

  return null;
}

function normalizeOptionalText(value: string | null | undefined) {
  return value?.trim() ?? null;
}

function buildRouterDisplayName(
  router: RouterRow,
  snapshot: SnapshotRow | null,
) {
  return (
    firstNonEmptyText(
      router.displayName,
      router.hostname,
      snapshot?.payload.hostname,
    ) ?? router.deviceIdentifier
  );
}

export async function getOrCreateDefaultRolloutProfile(
  templateConfig: PasswallDesiredConfig,
  client: DatabaseClient = defaultDb,
) {
  const [existing] = await client
    .select()
    .from(operatorRolloutProfiles)
    .where(eq(operatorRolloutProfiles.profileKey, DEFAULT_PROFILE_KEY))
    .limit(1);

  if (existing) {
    return existing;
  }

  const [created] = await client
    .insert(operatorRolloutProfiles)
    .values({
      profileKey: DEFAULT_PROFILE_KEY,
      name: "Стандартный rollout профиль",
      description: "Базовый reusable профиль для уже подключённого парка.",
      rolloutConfig: passwallDesiredConfigSchema.parse(templateConfig),
      note: "Создан из текущего fleet-template как стартовая reusable конфигурация.",
    })
    .returning();

  if (!created) {
    throw new Error("Failed to create default rollout profile.");
  }

  return created;
}

export async function ensureDefaultRouterGroups(
  defaultProfileId: string,
  client: DatabaseClient = defaultDb,
) {
  const existing = await client.select().from(operatorRouterGroups);
  const groupsByKey = new Map(existing.map((group) => [group.groupKey, group]));

  for (const seed of DEFAULT_GROUP_KEYS) {
    if (groupsByKey.has(seed.groupKey)) {
      continue;
    }

    const [created] = await client
      .insert(operatorRouterGroups)
      .values({
        groupKey: seed.groupKey,
        name: seed.name,
        description: seed.description,
        rolloutProfileId: defaultProfileId,
      })
      .returning();

    if (created) {
      groupsByKey.set(created.groupKey, created);
    }
  }

  return Array.from(groupsByKey.values()).sort((left, right) =>
    left.name.localeCompare(right.name, "ru"),
  );
}

export async function loadProfilesAndGroupsWorkspace(args: {
  templateConfig: PasswallDesiredConfig;
  client?: DatabaseClient;
}) {
  const client = args.client ?? defaultDb;
  const defaultProfile = await getOrCreateDefaultRolloutProfile(
    args.templateConfig,
    client,
  );
  const groups = await ensureDefaultRouterGroups(defaultProfile.id, client);
  const profiles = await client
    .select()
    .from(operatorRolloutProfiles)
    .orderBy(operatorRolloutProfiles.name);

  const routerRows = await client
    .select()
    .from(routers)
    .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));
  const routerIds = routerRows.map((router) => router.id);
  const latestSnapshots = await loadLatestSnapshots(client, routerIds);
  const groupsById = new Map(groups.map((group) => [group.id, group]));

  const groupRouterMap = new Map<string, typeof routerRows>();
  for (const router of routerRows) {
    if (!router.rolloutGroupId) {
      continue;
    }

    const current = groupRouterMap.get(router.rolloutGroupId) ?? [];
    current.push(router);
    groupRouterMap.set(router.rolloutGroupId, current);
  }

  const routerItems = routerRows.map((router) => {
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
    const group = router.rolloutGroupId
      ? (groupsById.get(router.rolloutGroupId) ?? null)
      : null;

    return {
      id: router.id,
      displayName: buildRouterDisplayName(router, snapshot),
      hostname: router.hostname,
      deviceIdentifier: router.deviceIdentifier,
      importState: router.importState,
      status: router.status,
      rolloutGroupId: router.rolloutGroupId,
      rolloutGroupName: group?.name ?? null,
      supportState: support.state,
      supportTitle: support.title,
      supportReason: support.reason,
      lastSeenAt: router.lastSeenAt,
    };
  });

  return {
    profiles: profiles.map((profile) => ({
      id: profile.id,
      profileKey: profile.profileKey,
      name: profile.name,
      description: profile.description,
      note: profile.note,
      updatedAt: profile.updatedAt,
      rolloutConfig: profile.rolloutConfig,
      shuntRuleCount: profile.rolloutConfig.basicSettings.shuntRules.length,
      managedNodeCount: profile.rolloutConfig.nodes.length,
      groupCount: groups.filter(
        (group) => group.rolloutProfileId === profile.id,
      ).length,
    })),
    groups: groups.map((group) => ({
      id: group.id,
      groupKey: group.groupKey,
      name: group.name,
      description: group.description,
      rolloutProfileId: group.rolloutProfileId,
      rolloutProfileName:
        profiles.find((profile) => profile.id === group.rolloutProfileId)
          ?.name ?? null,
      routerCount: (groupRouterMap.get(group.id) ?? []).length,
      updatedAt: group.updatedAt,
    })),
    routers: routerItems,
    unassignedRouters: routerItems.filter((router) => !router.rolloutGroupId),
  };
}

function artifactMatchesRouterArchitecture(
  artifact: ArtifactRow,
  architecture: string | null | undefined,
) {
  if (!architecture) {
    return true;
  }

  return (
    artifact.architecture === null || artifact.architecture === architecture
  );
}

function latestPasswallArtifactsForRouter(args: {
  artifactsRows: ArtifactRow[];
  channel: "stable" | "beta";
  architecture: string | null | undefined;
}) {
  const bundle =
    args.artifactsRows.find(
      (artifact) =>
        artifact.type === "passwall_bundle" &&
        artifact.channel === args.channel &&
        artifactMatchesRouterArchitecture(artifact, args.architecture),
    ) ?? null;

  const packages = args.artifactsRows.filter(
    (artifact) =>
      artifact.type === "passwall_package" &&
      artifact.channel === args.channel &&
      artifactMatchesRouterArchitecture(artifact, args.architecture),
  );

  return { bundle, packages };
}

function componentInstalledVersion(
  snapshot: SnapshotRow | null,
  packageName: string,
) {
  const payload = snapshot?.payload;
  if (!payload) {
    return null;
  }

  const runtimeKey = packageNameToRuntimeKey(packageName);
  if (packageName === "luci-app-passwall2") {
    return (
      snapshot?.passwallAppVersion ??
      payload.packageVersions[packageName] ??
      null
    );
  }

  return (
    payload.binaryVersions[runtimeKey] ??
    payload.packageVersions[packageName] ??
    payload.packageVersions[runtimeKey] ??
    null
  );
}

export async function loadVersionDriftWorkspace(
  client: DatabaseClient = defaultDb,
) {
  const routerRows = await client
    .select()
    .from(routers)
    .orderBy(desc(routers.lastSeenAt), desc(routers.createdAt));
  const groups = await client.select().from(operatorRouterGroups);
  const groupsById = new Map(groups.map((group) => [group.id, group]));
  const snapshots = await loadLatestSnapshots(
    client,
    routerRows.map((router) => router.id),
  );
  const queuedJobs = await client
    .select()
    .from(jobs)
    .where(
      inArray(
        jobs.routerId,
        routerRows.map((router) => router.id),
      ),
    )
    .orderBy(desc(jobs.createdAt));
  const artifactRows = await client
    .select()
    .from(artifacts)
    .where(
      inArray(artifacts.type, [
        "controller",
        "passwall_bundle",
        "passwall_package",
      ]),
    )
    .orderBy(desc(artifacts.publishedAt), desc(artifacts.version));

  const latestControllerArtifact =
    artifactRows.find(
      (artifact) =>
        artifact.type === "controller" && artifact.channel === "stable",
    ) ?? null;

  const rows = routerRows.map((router) => {
    const snapshot = snapshots.get(router.id) ?? null;
    const payload = snapshot?.payload ?? null;
    const support = describeEffectiveRouterSupport({
      router: {
        boardName: router.boardName,
        target: router.target,
        architecture: router.architecture,
        openwrtRelease: router.openwrtRelease,
      },
      inventory: payload,
    });
    const controllerInstalled = snapshot?.controllerVersion ?? null;
    const controllerAvailable = latestControllerArtifact?.version ?? null;
    const controllerComparison = compareControllerVersions(
      controllerInstalled,
      controllerAvailable,
    );
    const passwallArtifacts = latestPasswallArtifactsForRouter({
      artifactsRows: artifactRows,
      channel: "stable",
      architecture: router.architecture ?? payload?.architecture,
    });
    const passwallBundleMetadata =
      buildPasswallBundleMetadataFromArtifact(passwallArtifacts.bundle) ??
      buildFallbackPasswallBundleMetadata();
    const xrayInstalled = componentInstalledVersion(snapshot, "xray-core");
    const xrayAvailable =
      findPasswallRuntimeTarget(passwallBundleMetadata, "xray-core")
        ?.remoteVersion ??
      passwallBundleMetadata.packageArtifacts.find(
        (artifact) => artifact.name === "xray-core",
      )?.artifactVersion ??
      null;
    const passwallInstalled =
      snapshot?.passwallAppVersion ??
      payload?.packageVersions["luci-app-passwall2"] ??
      null;
    const passwallAvailable = passwallBundleMetadata.releaseTag;
    const hasQueuedUpdate = queuedJobs.some(
      (job) =>
        job.routerId === router.id &&
        ["queued", "delivered", "running"].includes(job.state) &&
        (isControllerUpdateJob(job) || job.type === "update_passwall_packages"),
    );

    const xrayOutdated =
      compareLooseSemverVersions(xrayInstalled, xrayAvailable) === -1;
    const passwallOutdated =
      compareLooseSemverVersions(passwallInstalled, passwallAvailable) === -1;
    const blocked =
      !canRunUpdateAction(support.state) || router.importState !== "approved";

    return {
      id: router.id,
      displayName: buildRouterDisplayName(router, snapshot),
      rolloutGroupId: router.rolloutGroupId,
      rolloutGroupName: router.rolloutGroupId
        ? (groupsById.get(router.rolloutGroupId)?.name ?? null)
        : null,
      importState: router.importState,
      supportState: support.state,
      supportReason: support.reason,
      lastSeenAt: router.lastSeenAt,
      controllerInstalled: formatControllerVersion(controllerInstalled),
      controllerAvailable,
      controllerNeedsUpdate:
        controllerComparison !== null && controllerComparison < 0,
      passwallInstalled: passwallInstalled ?? "неизвестно",
      passwallAvailable,
      passwallAvailableLabel: `${passwallAvailable} · ${formatPasswallArtifactSourceLabel(
        passwallBundleMetadata.source,
      )}`,
      passwallNeedsUpdate: passwallOutdated,
      xrayInstalled: xrayInstalled ?? "неизвестно",
      xrayAvailable: xrayAvailable ?? "не опубликовано",
      xrayNeedsUpdate: xrayOutdated,
      hasQueuedUpdate,
      blocked,
      blockedReason: blocked
        ? router.importState !== "approved"
          ? "Сначала переведите роутер в approved."
          : support.reason
        : null,
    };
  });

  return {
    summary: {
      outdatedPasswallCount: rows.filter((row) => row.passwallNeedsUpdate)
        .length,
      outdatedXrayCount: rows.filter((row) => row.xrayNeedsUpdate).length,
      blockedCount: rows.filter((row) => row.blocked).length,
      queuedCount: rows.filter((row) => row.hasQueuedUpdate).length,
    },
    rows,
  };
}

export async function saveRolloutProfile(args: {
  profileId?: string;
  name: string;
  description?: string;
  note?: string;
  rolloutConfig: PasswallDesiredConfig;
  client?: DatabaseClient;
}) {
  const client = args.client ?? defaultDb;
  const rolloutConfig = passwallDesiredConfigSchema.parse(args.rolloutConfig);
  const normalizedName = args.name.trim();

  if (!normalizedName) {
    throw new Error("Название профиля не может быть пустым.");
  }

  if (args.profileId) {
    const [updated] = await client
      .update(operatorRolloutProfiles)
      .set({
        name: normalizedName,
        description: normalizeOptionalText(args.description),
        note: normalizeOptionalText(args.note),
        rolloutConfig,
      })
      .where(eq(operatorRolloutProfiles.id, args.profileId))
      .returning();

    if (!updated) {
      throw new Error("Профиль не найден.");
    }

    return updated;
  }

  const profileKey = `profile-${crypto.randomUUID()}`;
  const [created] = await client
    .insert(operatorRolloutProfiles)
    .values({
      profileKey,
      name: normalizedName,
      description: normalizeOptionalText(args.description),
      note: normalizeOptionalText(args.note),
      rolloutConfig,
    })
    .returning();

  if (!created) {
    throw new Error("Не удалось создать профиль rollout.");
  }

  return created;
}

export async function saveRouterGroup(args: {
  groupId?: string;
  name: string;
  description?: string;
  rolloutProfileId?: string | null;
  client?: DatabaseClient;
}) {
  const client = args.client ?? defaultDb;
  const normalizedName = args.name.trim();
  if (!normalizedName) {
    throw new Error("Название группы не может быть пустым.");
  }

  if (args.groupId) {
    const [updated] = await client
      .update(operatorRouterGroups)
      .set({
        name: normalizedName,
        description: normalizeOptionalText(args.description),
        rolloutProfileId: args.rolloutProfileId ?? null,
      })
      .where(eq(operatorRouterGroups.id, args.groupId))
      .returning();

    if (!updated) {
      throw new Error("Группа не найдена.");
    }

    return updated;
  }

  const [created] = await client
    .insert(operatorRouterGroups)
    .values({
      groupKey: `group-${crypto.randomUUID()}`,
      name: normalizedName,
      description: normalizeOptionalText(args.description),
      rolloutProfileId: args.rolloutProfileId ?? null,
    })
    .returning();

  if (!created) {
    throw new Error("Не удалось создать группу роутеров.");
  }

  return created;
}

export async function assignRoutersToGroup(args: {
  routerIds: string[];
  groupId: string | null;
  client?: DatabaseClient;
}) {
  const client = args.client ?? defaultDb;
  const routerIds = [...new Set(args.routerIds)];
  if (routerIds.length === 0) {
    return [];
  }

  return client
    .update(routers)
    .set({ rolloutGroupId: args.groupId })
    .where(inArray(routers.id, routerIds))
    .returning();
}

export async function deleteRolloutProfile(
  profileId: string,
  client: DatabaseClient = defaultDb,
) {
  await client
    .update(operatorRouterGroups)
    .set({ rolloutProfileId: null })
    .where(eq(operatorRouterGroups.rolloutProfileId, profileId));

  const [deleted] = await client
    .delete(operatorRolloutProfiles)
    .where(eq(operatorRolloutProfiles.id, profileId))
    .returning();

  return deleted ?? null;
}

export async function deleteRouterGroup(
  groupId: string,
  client: DatabaseClient = defaultDb,
) {
  await client
    .update(routers)
    .set({ rolloutGroupId: null })
    .where(eq(routers.rolloutGroupId, groupId));

  const [deleted] = await client
    .delete(operatorRouterGroups)
    .where(eq(operatorRouterGroups.id, groupId))
    .returning();

  return deleted ?? null;
}

export async function queueGroupProfileRollout(args: {
  groupId: string;
  mode: "draft_only" | "queue_apply";
  note?: string;
  client?: DatabaseClient;
}) {
  const client = args.client ?? defaultDb;
  const [group] = await client
    .select()
    .from(operatorRouterGroups)
    .where(eq(operatorRouterGroups.id, args.groupId))
    .limit(1);

  if (!group) {
    throw new Error("Группа роутеров не найдена.");
  }

  if (!group.rolloutProfileId) {
    throw new Error("Для группы ещё не выбран rollout профиль.");
  }

  const [profile] = await client
    .select()
    .from(operatorRolloutProfiles)
    .where(eq(operatorRolloutProfiles.id, group.rolloutProfileId))
    .limit(1);

  if (!profile) {
    throw new Error("Профиль группы не найден.");
  }

  const routerRows = await client
    .select()
    .from(routers)
    .where(eq(routers.rolloutGroupId, group.id));
  const latestSnapshots = await loadLatestSnapshots(
    client,
    routerRows.map((router) => router.id),
  );

  const results: Array<{
    routerId: string;
    displayName: string;
    status: "prepared" | "queued" | "blocked" | "failed";
    reason: string | null;
    revisionId: string | null;
    jobId: string | null;
  }> = [];

  for (const router of routerRows) {
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

    if (router.importState !== "approved") {
      results.push({
        routerId: router.id,
        displayName: buildRouterDisplayName(router, snapshot),
        status: "blocked",
        reason: "Сначала переведите роутер в approved.",
        revisionId: null,
        jobId: null,
      });
      continue;
    }

    if (!canRunDestructiveAction(support.state)) {
      results.push({
        routerId: router.id,
        displayName: buildRouterDisplayName(router, snapshot),
        status: "blocked",
        reason: support.reason,
        revisionId: null,
        jobId: null,
      });
      continue;
    }

    try {
      const rolloutConfig = await buildTemplateRolloutDraft(client, {
        routerId: router.id,
        preferredRevisionId:
          router.activeRevisionId ?? router.lastAppliedRevisionId,
        templateConfig: passwallDesiredConfigSchema.parse(
          profile.rolloutConfig,
        ),
      });
      const revision = await createOperatorDraftRevisionWithDb(client, {
        routerId: router.id,
        config: rolloutConfig,
        note: [
          `Group rollout from profile \"${profile.name}\" / group \"${group.name}\".`,
          normalizeOptionalText(args.note),
        ]
          .filter(Boolean)
          .join(" "),
      });
      const applyJob =
        args.mode === "queue_apply"
          ? await queueDesiredRevisionApplyJobWithDb(client, {
              routerId: router.id,
              desiredRevisionId: revision.id,
            })
          : null;

      results.push({
        routerId: router.id,
        displayName: buildRouterDisplayName(router, snapshot),
        status: args.mode === "queue_apply" ? "queued" : "prepared",
        reason: null,
        revisionId: revision.id,
        jobId: applyJob?.id ?? null,
      });
    } catch (error) {
      results.push({
        routerId: router.id,
        displayName: buildRouterDisplayName(router, snapshot),
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
    requestedRouterCount: routerRows.length,
    preparedCount: results.filter((result) => result.status === "prepared")
      .length,
    queuedCount: results.filter((result) => result.status === "queued").length,
    blockedCount: results.filter((result) => result.status === "blocked")
      .length,
    failedCount: results.filter((result) => result.status === "failed").length,
  };

  const [event] = await client
    .insert(eventLog)
    .values({
      routerId: null,
      type:
        args.mode === "queue_apply"
          ? "fleet.rollout.group.queued"
          : "fleet.rollout.group.prepared",
      severity:
        summary.blockedCount > 0 || summary.failedCount > 0
          ? "warning"
          : "info",
      message:
        args.mode === "queue_apply"
          ? `Группа \"${group.name}\" поставлена в очередь для ${summary.queuedCount} из ${summary.requestedRouterCount} роутеров.`
          : `Для группы \"${group.name}\" подготовлены черновики для ${summary.preparedCount} из ${summary.requestedRouterCount} роутеров.`,
      metadata: {
        groupId: group.id,
        groupName: group.name,
        profileId: profile.id,
        profileName: profile.name,
        mode: args.mode,
        note: normalizeOptionalText(args.note),
        summary,
        results,
      },
    })
    .returning();

  return {
    ok: true as const,
    event: event ?? null,
    results,
    summary,
  };
}
