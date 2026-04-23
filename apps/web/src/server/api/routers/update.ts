import {
  artifacts,
  firmwareManifests,
  jobResults,
  jobs,
  routerInventorySnapshots,
  routers,
} from "@vectra/db";
import {
  passwallDesiredConfigSchema,
  updateControllerJobPayloadSchema,
  updatePasswallPackagesJobPayloadSchema,
  validateFirmwareJobPayloadSchema,
} from "@vectra/contracts";
import { TRPCError } from "@trpc/server";
import { and, desc, eq, inArray } from "drizzle-orm";
import { z } from "zod";

import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import { resolveInstalledControllerVersion } from "~/lib/controller-version";
import {
  buildTerminalControllerSelfUpdatePayload,
  shouldUseTerminalControllerSelfUpdate,
} from "~/lib/controller-update-jobs";
import { buildTerminalRouterRebootPayload } from "~/lib/router-reboot-jobs";
import {
  PASSWALL_MANAGED_STACK_REQUIRED_PACKAGES,
  buildLatestPasswallArtifactMap,
  buildFallbackPasswallBundleMetadata,
  buildPasswallBundleMetadataFromArtifact,
  findPasswallRuntimeTarget,
  resolveInstalledOptionalPasswallPackages,
  resolvePasswallPackageArtifactsFromRows,
  sortPasswallPackageList,
  type PasswallArtifactOrigin,
  type PasswallBundleMetadata,
  type PasswallPackageArtifactDescriptor,
  type PasswallUpdateScope,
} from "~/lib/passwall-artifacts";
import {
  executeGlobalTemplateRollout,
  getOrCreateGlobalTemplate,
  loadGlobalTemplateWorkspace,
  saveGlobalTemplate,
} from "~/server/vectra/global-template";
import { buildRouterManagementTaskLog } from "~/server/vectra/editor-surface";
import {
  assignRoutersToGroup,
  deleteRolloutProfile,
  deleteRouterGroup,
  loadProfilesAndGroupsWorkspace,
  loadVersionDriftWorkspace,
  queueGroupProfileRollout,
  saveRolloutProfile,
  saveRouterGroup,
} from "~/server/vectra/rollout-control";
import type { db as DatabaseClientValue } from "~/server/db";
import {
  canRunDestructiveAction,
  canRunUpdateAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

const DEFAULT_PASSWALL_PACKAGE_LIST = [
  ...PASSWALL_MANAGED_STACK_REQUIRED_PACKAGES,
];
const SELECTABLE_PASSWALL_PACKAGE_NAMES = [
  "luci-app-passwall2",
  "xray-core",
  "sing-box",
  "hysteria",
  "geoview",
] as const;
const CONTROLLER_PACKAGE_LIST = [
  "vectra-controller-agent",
  "luci-app-vectra-controller",
];
const passwallUpdatePackageSchema = z.enum(SELECTABLE_PASSWALL_PACKAGE_NAMES);
type DatabaseClient = typeof DatabaseClientValue;
type ArtifactRow = typeof artifacts.$inferSelect;

type RouterMutationContext = { db: DatabaseClient };

async function getRouterForMutation(
  ctx: { db: DatabaseClient },
  routerId: string,
) {
  const [router] = await ctx.db
    .select()
    .from(routers)
    .where(eq(routers.id, routerId))
    .limit(1);

  if (!router) {
    throw new TRPCError({
      code: "NOT_FOUND",
      message: `Router ${routerId} was not found.`,
    });
  }

  const [snapshot] = await ctx.db
    .select()
    .from(routerInventorySnapshots)
    .where(eq(routerInventorySnapshots.routerId, routerId))
    .orderBy(desc(routerInventorySnapshots.createdAt))
    .limit(1);

  return { router, snapshot: snapshot ?? null };
}

async function assertCertifiedRouter(
  ctx: { db: DatabaseClient },
  routerId: string,
) {
  const { router, snapshot } = await getRouterForMutation(ctx, routerId);
  const support = describeEffectiveRouterSupport({
    router: {
      boardName: router.boardName,
      target: router.target,
      architecture: router.architecture,
      openwrtRelease: router.openwrtRelease,
    },
    inventory: snapshot?.payload ?? null,
  });

  if (!canRunDestructiveAction(support.state)) {
    throw new TRPCError({
      code: "FORBIDDEN",
      message:
        "Эта операция разрешена только для поддерживаемых pilot/certified board/layout пар.",
    });
  }

  return { router, snapshot, support };
}

async function assertUpdateCapableRouter(
  ctx: RouterMutationContext,
  routerId: string,
) {
  const { router, snapshot } = await getRouterForMutation(ctx, routerId);
  const support = describeEffectiveRouterSupport({
    router: {
      boardName: router.boardName,
      target: router.target,
      architecture: router.architecture,
      openwrtRelease: router.openwrtRelease,
    },
    inventory: snapshot?.payload ?? null,
  });

  if (!canRunUpdateAction(support.state)) {
    throw new TRPCError({
      code: "FORBIDDEN",
      message:
        "Эта операция разрешена только для pilot/certified board/layout пар на поддерживаемой платформе.",
    });
  }

  return { router, snapshot, support };
}

function buildUpdateMonitorRouterDisplayName(args: {
  router:
    | (typeof routers.$inferSelect)
    | null
    | undefined;
  snapshot:
    | (typeof routerInventorySnapshots.$inferSelect)
    | null
    | undefined;
}) {
  const displayName = args.router?.displayName?.trim();
  if (displayName) {
    return displayName;
  }

  const hostname = args.router?.hostname?.trim();
  if (hostname) {
    return hostname;
  }

  const snapshotHostname = args.snapshot?.payload?.hostname?.trim();
  if (snapshotHostname) {
    return snapshotHostname;
  }

  return args.router?.deviceIdentifier ?? args.router?.id ?? "unknown-router";
}

async function enqueuePasswallPackageUpdate(args: {
  ctx: RouterMutationContext;
  routerId: string;
  artifactChannel: "stable" | "beta";
  packages?: readonly z.infer<typeof passwallUpdatePackageSchema>[];
}) {
  const { router, snapshot } = await assertUpdateCapableRouter(
    args.ctx,
    args.routerId,
  );

  const passwallArtifacts = await args.ctx.db
    .select()
    .from(artifacts)
    .where(
      and(
        inArray(artifacts.type, ["passwall_package", "passwall_bundle"]),
        eq(artifacts.channel, args.artifactChannel),
      ),
    )
    .orderBy(desc(artifacts.publishedAt), desc(artifacts.version))
    .limit(96);

  const resolved = resolvePasswallTargetMetadata({
    routerArchitecture: router.architecture ?? snapshot?.payload.architecture,
    snapshotPayload: snapshot?.payload ?? null,
    channel: args.artifactChannel,
    scopedPackages: args.packages ? [...args.packages] : undefined,
    passwallArtifacts,
  });

  const payload = updatePasswallPackagesJobPayloadSchema.parse({
    channel: args.artifactChannel,
    packageList: resolved.packageList,
    packageArtifacts: resolved.packageArtifacts.map((artifact) => ({
      name: artifact.name,
      artifactUrl: artifact.artifactUrl,
      sha256: artifact.sha256,
      signatureUrl: artifact.signatureUrl,
      artifactVersion: artifact.artifactVersion,
      source: artifact.source,
      required: artifact.required,
      downloadSizeBytes: artifact.downloadSizeBytes,
      installedSizeBytes: artifact.installedSizeBytes,
    })),
    targetVersion: resolved.targetVersion,
    strategy: resolved.strategy,
    packageTargetVersion: resolved.packageTargetVersion,
    runtimeTargetVersion: resolved.runtimeTargetVersion,
    targetReleaseTag: resolved.targetReleaseTag,
    originSource: resolved.originSource,
    fallbackPolicy: resolved.fallbackPolicy,
    updateScope: resolved.updateScope,
    artifactUrl: resolved.artifactUrl,
    sha256: resolved.sha256,
    signatureUrl: resolved.signatureUrl,
    artifactVersion: resolved.artifactVersion,
  });

  const dedupeKey = `update_passwall_packages:${args.routerId}:${args.artifactChannel}:${payload.updateScope}:${payload.strategy}:${payload.packageList.join(",")}:${payload.targetVersion}:${payload.packageTargetVersion ?? "none"}:${payload.runtimeTargetVersion ?? "none"}:${payload.originSource}`;
  const [existingJob] = await args.ctx.db
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, args.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, ["queued", "delivered", "running"]),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await args.ctx.db
    .insert(jobs)
    .values({
      routerId: args.routerId,
      type: "update_passwall_packages",
      state: "queued",
      dedupeKey,
      payload,
    })
    .returning();

  return job;
}

async function enqueueControllerUpdateJob(args: {
  ctx: RouterMutationContext;
  routerId: string;
  channel: "stable" | "beta";
}) {
  const { snapshot } = await assertUpdateCapableRouter(args.ctx, args.routerId);

  const controllerArtifacts = await args.ctx.db
    .select()
    .from(artifacts)
    .where(
      and(
        eq(artifacts.type, "controller"),
        eq(artifacts.channel, args.channel),
        inArray(artifacts.name, CONTROLLER_PACKAGE_LIST),
      ),
    )
    .orderBy(desc(artifacts.publishedAt), desc(artifacts.version))
    .limit(12);

  const latestByName = new Map<string, (typeof controllerArtifacts)[number]>();
  for (const artifact of controllerArtifacts) {
    if (!latestByName.has(artifact.name)) {
      latestByName.set(artifact.name, artifact);
    }
  }

  const packageArtifacts = CONTROLLER_PACKAGE_LIST.flatMap((name) => {
    const artifact = latestByName.get(name);
    return artifact
      ? [
          {
            name: artifact.name,
            artifactUrl: artifact.downloadUrl,
            sha256: artifact.checksumSha256,
            signatureUrl: artifact.signatureUrl,
            artifactVersion: artifact.version,
          },
        ]
      : [];
  });

  const primaryArtifact = latestByName.get("vectra-controller-agent") ?? null;
  const primaryPackageArtifact = packageArtifacts[0] ?? null;
  const payload = updateControllerJobPayloadSchema.parse({
    channel: args.channel,
    packageList: CONTROLLER_PACKAGE_LIST,
    packageArtifacts,
    artifactUrl:
      primaryArtifact?.downloadUrl ??
      primaryPackageArtifact?.artifactUrl ??
      null,
    sha256:
      primaryArtifact?.checksumSha256 ?? primaryPackageArtifact?.sha256 ?? null,
    signatureUrl:
      primaryArtifact?.signatureUrl ??
      primaryPackageArtifact?.signatureUrl ??
      null,
    artifactVersion:
      primaryArtifact?.version ??
      primaryPackageArtifact?.artifactVersion ??
      null,
  });

  const installedControllerVersion = resolveInstalledControllerVersion({
    controllerVersion: snapshot?.controllerVersion ?? null,
    payload: snapshot?.payload ?? null,
  });
  const useTerminalSelfUpdate = shouldUseTerminalControllerSelfUpdate(
    installedControllerVersion,
  );
  const terminalPayload = useTerminalSelfUpdate
    ? buildTerminalControllerSelfUpdatePayload({
        artifactVersion: payload.artifactVersion,
        packageArtifacts,
      })
    : null;
  const dedupeKey = `update_controller:${args.routerId}:${args.channel}:${
    payload.artifactVersion ?? "latest"
  }`;
  const [existingJob] = await args.ctx.db
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, args.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, ["queued", "delivered", "running"]),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await args.ctx.db
    .insert(jobs)
    .values({
      routerId: args.routerId,
      type: terminalPayload ? "run_terminal_command" : "update_controller",
      state: "queued",
      dedupeKey,
      payload: terminalPayload ?? payload,
    })
    .returning();

  return job;
}

async function enqueueRouterRebootJob(args: {
  ctx: RouterMutationContext;
  routerId: string;
}) {
  await assertCertifiedRouter(args.ctx, args.routerId);

  const dedupeKey = `router_reboot:${args.routerId}`;
  const [existingJob] = await args.ctx.db
    .select()
    .from(jobs)
    .where(
      and(
        eq(jobs.routerId, args.routerId),
        eq(jobs.dedupeKey, dedupeKey),
        inArray(jobs.state, ["queued", "delivered", "running"]),
      ),
    )
    .limit(1);

  if (existingJob) {
    return existingJob;
  }

  const [job] = await args.ctx.db
    .insert(jobs)
    .values({
      routerId: args.routerId,
      type: "run_terminal_command",
      state: "queued",
      dedupeKey,
      payload: buildTerminalRouterRebootPayload(),
    })
    .returning();

  return job;
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

function resolveManagedPasswallPackageList(snapshotPayload: unknown) {
  return sortPasswallPackageList([
    ...DEFAULT_PASSWALL_PACKAGE_LIST,
    ...resolveInstalledOptionalPasswallPackages(snapshotPayload),
  ]);
}

function snapshotHasInstalledPackage(
  snapshotPayload: unknown,
  packageName: string,
) {
  if (!snapshotPayload || typeof snapshotPayload !== "object") {
    return false;
  }

  const packageVersions =
    "packageVersions" in snapshotPayload &&
    snapshotPayload.packageVersions &&
    typeof snapshotPayload.packageVersions === "object"
      ? (snapshotPayload.packageVersions as Record<string, unknown>)
      : null;

  const version = packageVersions?.[packageName];
  return typeof version === "string" && version.trim().length > 0;
}

function buildPasswallArtifactDescriptorMap(
  passwallArtifacts: ArtifactRow[],
  bundleMetadata: PasswallBundleMetadata,
) {
  const latestMatchingReleaseRows = buildLatestPasswallArtifactMap(
    passwallArtifacts.filter(
      (artifact) =>
        artifact.type === "passwall_package" &&
        readPasswallArtifactReleaseTag(artifact) === bundleMetadata.releaseTag,
    ),
  );

  return new Map<string, PasswallPackageArtifactDescriptor>(
    [
      ...bundleMetadata.packageArtifacts,
      ...resolvePasswallPackageArtifactsFromRows([
        ...latestMatchingReleaseRows.values(),
      ]),
    ].map((artifact) => [artifact.name, artifact]),
  );
}

function readPasswallArtifactReleaseTag(artifact: ArtifactRow) {
  const metadata = artifact.metadata;
  if (!metadata || typeof metadata !== "object") {
    return null;
  }

  const value = "releaseTag" in metadata ? metadata.releaseTag : null;
  return typeof value === "string" && value.trim().length > 0 ? value : null;
}

function latestPasswallBundleArtifactForRouter(args: {
  artifacts: ArtifactRow[];
  channel: "stable" | "beta";
  architecture: string | null | undefined;
}) {
  return (
    args.artifacts.find(
      (artifact) =>
        artifact.type === "passwall_bundle" &&
        artifact.channel === args.channel &&
        artifactMatchesRouterArchitecture(artifact, args.architecture),
    ) ?? null
  );
}

function resolvePasswallTargetMetadata(args: {
  routerArchitecture: string | null | undefined;
  snapshotPayload: unknown;
  channel: "stable" | "beta";
  scopedPackages: string[] | undefined;
  passwallArtifacts: ArtifactRow[];
}) {
  const updateScope: PasswallUpdateScope =
    args.scopedPackages && args.scopedPackages.length > 0
      ? "scoped-package"
      : "managed-stack";
  const latestBundleArtifact = latestPasswallBundleArtifactForRouter({
    artifacts: args.passwallArtifacts,
    channel: args.channel,
    architecture: args.routerArchitecture,
  });
  const latestPackageArtifacts = args.passwallArtifacts.filter(
    (artifact) =>
      artifact.type === "passwall_package" &&
      artifact.channel === args.channel &&
      artifactMatchesRouterArchitecture(artifact, args.routerArchitecture),
  );
  const bundleMetadata =
    buildPasswallBundleMetadataFromArtifact(latestBundleArtifact) ??
    buildFallbackPasswallBundleMetadata();
  const packageArtifactMap = buildPasswallArtifactDescriptorMap(
    latestPackageArtifacts,
    bundleMetadata,
  );
  const recoveryDependencySet = new Set<string>(
    bundleMetadata.recoveryDependencies,
  );
  const packageList =
    updateScope === "managed-stack"
      ? resolveManagedPasswallPackageList(args.snapshotPayload).filter(
          (packageName) =>
            !recoveryDependencySet.has(packageName) ||
            packageArtifactMap.has(packageName) ||
            !snapshotHasInstalledPackage(args.snapshotPayload, packageName),
        )
      : sortPasswallPackageList(args.scopedPackages ?? []);

  const packageArtifacts = packageList
    .map((packageName) => packageArtifactMap.get(packageName) ?? null)
    .filter(
      (artifact): artifact is PasswallPackageArtifactDescriptor =>
        artifact !== null,
    );

  if (packageArtifacts.length === 0) {
    throw new TRPCError({
      code: "PRECONDITION_FAILED",
      message:
        "Для выбранного PassWall update не удалось собрать ни одного pinned package artifact. Сначала синхронизируйте mirrored metadata.",
    });
  }

  const targetVersion =
    updateScope === "managed-stack"
      ? bundleMetadata.releaseTag
      : (packageArtifacts[0]?.artifactVersion ?? bundleMetadata.releaseTag);
  const originSource: PasswallArtifactOrigin = packageArtifacts.every(
    (artifact) => artifact.source === "upstream",
  )
    ? "upstream"
    : "vectra";
  const primaryArtifact = packageArtifacts[0] ?? null;
  const passwallAppArtifact =
    packageArtifacts.find(
      (artifact) => artifact.name === "luci-app-passwall2",
    ) ?? primaryArtifact;
  const scopedPackageName =
    updateScope === "scoped-package" && packageList.length === 1
      ? packageList[0]
      : null;
  const strategy =
    scopedPackageName === "xray-core"
      ? ("xray-built-in-first" as const)
      : ("managed-stack-package-first" as const);
  const runtimeTarget =
    typeof scopedPackageName === "string"
      ? findPasswallRuntimeTarget(bundleMetadata, scopedPackageName)
      : null;
  const packageTargetVersion =
    updateScope === "managed-stack"
      ? (passwallAppArtifact?.artifactVersion ??
        primaryArtifact?.artifactVersion ??
        bundleMetadata.releaseTag)
      : (primaryArtifact?.artifactVersion ?? bundleMetadata.releaseTag);
  const runtimeTargetVersion =
    typeof scopedPackageName === "string"
      ? (runtimeTarget?.remoteVersion ??
        primaryArtifact?.artifactVersion ??
        null)
      : null;

  return {
    packageList,
    packageArtifacts,
    strategy,
    updateScope,
    targetVersion,
    packageTargetVersion,
    runtimeTargetVersion,
    targetReleaseTag: bundleMetadata.releaseTag,
    originSource,
    fallbackPolicy: "adaptive-component-fallback" as const,
    artifactUrl:
      latestBundleArtifact?.downloadUrl ?? primaryArtifact?.artifactUrl ?? null,
    sha256:
      latestBundleArtifact?.checksumSha256 ?? primaryArtifact?.sha256 ?? null,
    signatureUrl:
      latestBundleArtifact?.signatureUrl ??
      primaryArtifact?.signatureUrl ??
      null,
    artifactVersion: latestBundleArtifact?.version ?? targetVersion,
  };
}

export const updateRouter = createTRPCRouter({
  artifacts: protectedProcedure.query(async ({ ctx }) => {
    return ctx.db
      .select()
      .from(artifacts)
      .orderBy(desc(artifacts.publishedAt), desc(artifacts.version));
  }),

  firmwareMatrix: protectedProcedure.query(async ({ ctx }) => {
    return ctx.db
      .select()
      .from(firmwareManifests)
      .orderBy(desc(firmwareManifests.createdAt));
  }),

  globalTemplateWorkspace: protectedProcedure.query(async ({ ctx }) => {
    return loadGlobalTemplateWorkspace(ctx.db);
  }),

  profilesAndGroupsWorkspace: protectedProcedure.query(async ({ ctx }) => {
    const globalTemplate = await getOrCreateGlobalTemplate(ctx.db);
    return loadProfilesAndGroupsWorkspace({
      templateConfig: passwallDesiredConfigSchema.parse(
        globalTemplate.rolloutConfig,
      ),
      client: ctx.db,
    });
  }),

  versionDriftWorkspace: protectedProcedure.query(async ({ ctx }) => {
    return loadVersionDriftWorkspace(ctx.db);
  }),

  launchProgress: protectedProcedure
    .input(
      z.object({
        jobIds: z.array(z.string().uuid()).min(1).max(64),
      }),
    )
    .query(async ({ ctx, input }) => {
      const requestedJobIds = [...new Set(input.jobIds)];
      const monitoredJobs = await ctx.db
        .select()
        .from(jobs)
        .where(inArray(jobs.id, requestedJobIds))
        .orderBy(desc(jobs.createdAt));

      if (monitoredJobs.length === 0) {
        return {
          items: [],
        };
      }

      const routerIds = [...new Set(monitoredJobs.map((job) => job.routerId))];
      const routerRows = await ctx.db
        .select()
        .from(routers)
        .where(inArray(routers.id, routerIds));
      const routerById = new Map(routerRows.map((router) => [router.id, router]));

      const snapshotRows = await ctx.db
        .select()
        .from(routerInventorySnapshots)
        .where(inArray(routerInventorySnapshots.routerId, routerIds))
        .orderBy(desc(routerInventorySnapshots.createdAt));
      const latestSnapshotByRouterId = new Map<
        string,
        typeof routerInventorySnapshots.$inferSelect
      >();
      for (const snapshot of snapshotRows) {
        if (!latestSnapshotByRouterId.has(snapshot.routerId)) {
          latestSnapshotByRouterId.set(snapshot.routerId, snapshot);
        }
      }

      const monitoredResults = await ctx.db
        .select()
        .from(jobResults)
        .where(inArray(jobResults.jobId, requestedJobIds))
        .orderBy(desc(jobResults.reportedAt));
      const resultsByRouterId = new Map<
        string,
        Array<typeof jobResults.$inferSelect>
      >();
      for (const result of monitoredResults) {
        const bucket = resultsByRouterId.get(result.routerId);
        if (bucket) {
          bucket.push(result);
        } else {
          resultsByRouterId.set(result.routerId, [result]);
        }
      }

      const jobById = new Map(monitoredJobs.map((job) => [job.id, job]));
      const itemByJobId = new Map<
        string,
        ReturnType<typeof buildRouterManagementTaskLog>[number] & {
          routerId: string;
          displayName: string;
        }
      >();

      for (const routerId of routerIds) {
        const routerJobs = monitoredJobs.filter((job) => job.routerId === routerId);
        if (routerJobs.length === 0) {
          continue;
        }

        const latestSnapshot = latestSnapshotByRouterId.get(routerId) ?? null;
        const displayName = buildUpdateMonitorRouterDisplayName({
          router: routerById.get(routerId),
          snapshot: latestSnapshot,
        });
        const items = buildRouterManagementTaskLog({
          jobs: routerJobs,
          results: resultsByRouterId.get(routerId) ?? [],
          installedControllerVersion: latestSnapshot?.controllerVersion ?? null,
        });

        for (const item of items) {
          itemByJobId.set(item.jobId, {
            ...item,
            routerId,
            displayName,
          });
        }
      }

      return {
        items: requestedJobIds.flatMap((jobId) => {
          const item = itemByJobId.get(jobId);
          const job = jobById.get(jobId);
          if (item) {
            return [item];
          }
          if (!job) {
            return [];
          }

          const latestSnapshot = latestSnapshotByRouterId.get(job.routerId) ?? null;
          return [
            {
              jobId,
              routerId: job.routerId,
              displayName: buildUpdateMonitorRouterDisplayName({
                router: routerById.get(job.routerId),
                snapshot: latestSnapshot,
              }),
              kind: "passwall-update" as const,
              label: "Задача обновления",
              jobType: job.type,
              updateScope:
                typeof job.payload?.updateScope === "string"
                  ? job.payload.updateScope
                  : null,
              jobState: job.state,
              resultStatus: null,
              createdAt: job.createdAt,
              reportedAt: null,
              summary: "Ожидаю первый ответ от роутера.",
              error: null,
              stdout: null,
              stderr: null,
              command:
                typeof job.payload?.command === "string"
                  ? job.payload.command
                  : null,
              artifactVersion:
                typeof job.payload?.artifactVersion === "string"
                  ? job.payload.artifactVersion
                  : null,
              targetVersion:
                typeof job.payload?.targetVersion === "string"
                  ? job.payload.targetVersion
                  : null,
              packageTargetVersion:
                typeof job.payload?.packageTargetVersion === "string"
                  ? job.payload.packageTargetVersion
                  : null,
              runtimeTargetVersion:
                typeof job.payload?.runtimeTargetVersion === "string"
                  ? job.payload.runtimeTargetVersion
                  : null,
              deliveryBlocked: false,
              deliveryBlockedReason: null,
              packageResults: [],
            },
          ];
        }),
      };
    }),

  saveGlobalTemplate: protectedProcedure
    .input(
      z.object({
        installBaselineUci: z.string(),
        rolloutConfig: passwallDesiredConfigSchema,
        note: z.string().trim().max(500).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return saveGlobalTemplate(input, ctx.db);
    }),

  saveRolloutProfile: protectedProcedure
    .input(
      z.object({
        profileId: z.string().uuid().optional(),
        name: z.string().trim().min(1).max(120),
        description: z.string().trim().max(400).optional(),
        note: z.string().trim().max(500).optional(),
        rolloutConfig: passwallDesiredConfigSchema,
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return saveRolloutProfile({
        ...input,
        client: ctx.db,
      });
    }),

  deleteRolloutProfile: protectedProcedure
    .input(
      z.object({
        profileId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return deleteRolloutProfile(input.profileId, ctx.db);
    }),

  saveRouterGroup: protectedProcedure
    .input(
      z.object({
        groupId: z.string().uuid().optional(),
        name: z.string().trim().min(1).max(120),
        description: z.string().trim().max(400).optional(),
        rolloutProfileId: z.string().uuid().nullable().optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return saveRouterGroup({
        ...input,
        client: ctx.db,
      });
    }),

  deleteRouterGroup: protectedProcedure
    .input(
      z.object({
        groupId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return deleteRouterGroup(input.groupId, ctx.db);
    }),

  assignRoutersToGroup: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1),
        groupId: z.string().uuid().nullable(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return assignRoutersToGroup({
        routerIds: input.routerIds,
        groupId: input.groupId,
        client: ctx.db,
      });
    }),

  queueGlobalTemplateRollout: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1),
        mode: z.enum(["draft_only", "queue_apply"]).default("draft_only"),
        note: z.string().trim().max(500).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return executeGlobalTemplateRollout(input, ctx.db);
    }),

  queueGroupProfileRollout: protectedProcedure
    .input(
      z.object({
        groupId: z.string().uuid(),
        mode: z.enum(["draft_only", "queue_apply"]).default("draft_only"),
        note: z.string().trim().max(500).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return queueGroupProfileRollout({
        ...input,
        client: ctx.db,
      });
    }),

  queueBulkPasswallPackageUpdate: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1),
        artifactChannel: z.enum(["stable", "beta"]).default("stable"),
        packages: z.array(passwallUpdatePackageSchema).min(1).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const results = [] as Array<{
        routerId: string;
        status: "queued" | "failed";
        reason: string | null;
        jobId: string | null;
      }>;

      for (const routerId of [...new Set(input.routerIds)]) {
        try {
          const job = await enqueuePasswallPackageUpdate({
            ctx,
            routerId,
            artifactChannel: input.artifactChannel,
            packages: input.packages,
          });

          results.push({
            routerId,
            status: "queued",
            reason: null,
            jobId: job?.id ?? null,
          });
        } catch (error) {
          results.push({
            routerId,
            status: "failed",
            reason:
              error instanceof Error
                ? error.message
                : "Не удалось поставить update job.",
            jobId: null,
          });
        }
      }

      return {
        ok: true as const,
        results,
      };
    }),

  queueBulkXrayUpdate: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1),
        artifactChannel: z.enum(["stable", "beta"]).default("stable"),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const uniqueRouterIds = [...new Set(input.routerIds)];
      return {
        ok: true as const,
        results: await Promise.all(
          uniqueRouterIds.map(async (routerId) => {
            try {
              const job = await enqueuePasswallPackageUpdate({
                ctx,
                routerId,
                artifactChannel: input.artifactChannel,
                packages: ["xray-core"],
              });

              return {
                routerId,
                status: "queued" as const,
                reason: null,
                jobId: job?.id ?? null,
              };
            } catch (error) {
              return {
                routerId,
                status: "failed" as const,
                reason:
                  error instanceof Error
                    ? error.message
                    : "Не удалось поставить update job.",
                jobId: null,
              };
            }
          }),
        ),
      };
    }),

  queueBulkControllerUpdate: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1),
        channel: z.enum(["stable", "beta"]).default("stable"),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const results = [] as Array<{
        routerId: string;
        status: "queued" | "failed";
        reason: string | null;
        jobId: string | null;
      }>;

      for (const routerId of [...new Set(input.routerIds)]) {
        try {
          const job = await enqueueControllerUpdateJob({
            ctx,
            routerId,
            channel: input.channel,
          });

          results.push({
            routerId,
            status: "queued",
            reason: null,
            jobId: job?.id ?? null,
          });
        } catch (error) {
          results.push({
            routerId,
            status: "failed",
            reason:
              error instanceof Error
                ? error.message
                : "Не удалось поставить update job.",
            jobId: null,
          });
        }
      }

      return {
        ok: true as const,
        results,
      };
    }),

  queueBulkRouterReboot: protectedProcedure
    .input(
      z.object({
        routerIds: z.array(z.string().uuid()).min(1),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const results = [] as Array<{
        routerId: string;
        status: "queued" | "failed";
        reason: string | null;
        jobId: string | null;
      }>;

      for (const routerId of [...new Set(input.routerIds)]) {
        try {
          const job = await enqueueRouterRebootJob({
            ctx,
            routerId,
          });

          results.push({
            routerId,
            status: "queued",
            reason: null,
            jobId: job?.id ?? null,
          });
        } catch (error) {
          results.push({
            routerId,
            status: "failed",
            reason:
              error instanceof Error
                ? error.message
                : "Не удалось поставить reboot job.",
            jobId: null,
          });
        }
      }

      return {
        ok: true as const,
        results,
      };
    }),

  queueRouterReboot: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return enqueueRouterRebootJob({
        ctx,
        routerId: input.routerId,
      });
    }),

  queueControllerUpdate: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        channel: z.enum(["stable", "beta"]).default("stable"),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return enqueueControllerUpdateJob({
        ctx,
        routerId: input.routerId,
        channel: input.channel,
      });
    }),

  queueSubscriptionsRefresh: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertCertifiedRouter(ctx, input.routerId);

      const dedupeKey = `refresh_subscriptions:${input.routerId}`;
      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.dedupeKey, dedupeKey),
            inArray(jobs.state, ["queued", "delivered", "running"]),
          ),
        )
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "refresh_subscriptions",
          state: "queued",
          dedupeKey,
          payload: {},
        })
        .returning();

      return job;
    }),

  queueSubscriptionsInspect: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await getRouterForMutation(ctx, input.routerId);

      const dedupeKey = `inspect_subscriptions:${input.routerId}`;
      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.dedupeKey, dedupeKey),
            inArray(jobs.state, ["queued", "delivered", "running"]),
          ),
        )
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "inspect_subscriptions",
          state: "queued",
          dedupeKey,
          payload: {},
        })
        .returning();

      return job;
    }),

  queueRulesRefresh: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertCertifiedRouter(ctx, input.routerId);

      const dedupeKey = `refresh_rules:${input.routerId}`;
      const [existingJob] = await ctx.db
        .select()
        .from(jobs)
        .where(
          and(
            eq(jobs.routerId, input.routerId),
            eq(jobs.dedupeKey, dedupeKey),
            inArray(jobs.state, ["queued", "delivered", "running"]),
          ),
        )
        .limit(1);

      if (existingJob) {
        return existingJob;
      }

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "refresh_rules",
          state: "queued",
          dedupeKey,
          payload: {},
        })
        .returning();

      return job;
    }),

  queuePasswallPackageUpdate: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        artifactChannel: z.enum(["stable", "beta"]).default("stable"),
        packages: z.array(passwallUpdatePackageSchema).min(1).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      return enqueuePasswallPackageUpdate({
        ctx,
        routerId: input.routerId,
        artifactChannel: input.artifactChannel,
        packages: input.packages,
      });
    }),

  queueFirmwareValidation: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        manifestId: z.string().uuid(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertCertifiedRouter(ctx, input.routerId);

      const [manifest] = await ctx.db
        .select()
        .from(firmwareManifests)
        .where(eq(firmwareManifests.id, input.manifestId))
        .limit(1);

      const [artifact] = manifest
        ? await ctx.db
            .select()
            .from(artifacts)
            .where(
              and(
                eq(artifacts.id, manifest.artifactId),
                eq(artifacts.type, "firmware"),
              ),
            )
            .limit(1)
        : [undefined];

      const payload = validateFirmwareJobPayloadSchema.parse({
        manifestId: input.manifestId,
        channel: manifest?.channel ?? "stable",
        boardName: manifest?.boardName ?? null,
        target: manifest?.target ?? null,
        architecture: manifest?.architecture ?? null,
        layoutFamily: manifest?.layoutFamily ?? null,
        artifactUrl: artifact?.downloadUrl,
        sha256: artifact?.checksumSha256,
        signatureUrl: artifact?.signatureUrl ?? null,
        artifactVersion: artifact?.version ?? manifest?.version ?? null,
        validationCommand:
          manifest?.validationCommand ?? "sysupgrade -T /tmp/firmware.bin",
      });

      const [job] = await ctx.db
        .insert(jobs)
        .values({
          routerId: input.routerId,
          type: "validate_firmware",
          state: "queued",
          payload,
        })
        .returning();

      return job;
    }),
});
