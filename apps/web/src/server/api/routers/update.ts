import {
  artifacts,
  firmwareManifests,
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
import {
  executeGlobalTemplateRollout,
  loadGlobalTemplateWorkspace,
  saveGlobalTemplate,
} from "~/server/vectra/global-template";
import type { db as DatabaseClientValue } from "~/server/db";
import {
  canRunDestructiveAction,
  canRunUpdateAction,
  describeEffectiveRouterSupport,
} from "~/server/vectra/support";

const DEFAULT_PASSWALL_PACKAGE_LIST = [
  "luci-app-passwall2",
  "xray-core",
  "sing-box",
  "hysteria",
  "geoview",
  "v2ray-geoip",
  "v2ray-geosite",
  "dnsmasq-full",
  "chinadns-ng",
  "kmod-nft-socket",
  "kmod-nft-tproxy",
  "kmod-nft-nat",
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

  if (!canRunUpdateAction(support.state)) {
    throw new TRPCError({
      code: "FORBIDDEN",
      message:
        "Эта операция разрешена только для pilot/certified board/layout пар на поддерживаемой платформе.",
    });
  }

  return { router, snapshot, support };
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

  queueControllerUpdate: protectedProcedure
    .input(
      z.object({
        routerId: z.string().uuid(),
        channel: z.enum(["stable", "beta"]).default("stable"),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      await assertUpdateCapableRouter(ctx, input.routerId);

      const controllerArtifacts = await ctx.db
        .select()
        .from(artifacts)
        .where(
          and(
            eq(artifacts.type, "controller"),
            eq(artifacts.channel, input.channel),
            inArray(artifacts.name, CONTROLLER_PACKAGE_LIST),
          ),
        )
        .orderBy(desc(artifacts.publishedAt), desc(artifacts.version))
        .limit(12);

      const latestByName = new Map<
        string,
        (typeof controllerArtifacts)[number]
      >();
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

      const primaryArtifact =
        latestByName.get("vectra-controller-agent") ?? null;
      const primaryPackageArtifact = packageArtifacts[0] ?? null;

      const payload = updateControllerJobPayloadSchema.parse({
        channel: input.channel,
        packageList: CONTROLLER_PACKAGE_LIST,
        packageArtifacts,
        artifactUrl:
          primaryArtifact?.downloadUrl ??
          primaryPackageArtifact?.artifactUrl ??
          null,
        sha256:
          primaryArtifact?.checksumSha256 ??
          primaryPackageArtifact?.sha256 ??
          null,
        signatureUrl:
          primaryArtifact?.signatureUrl ??
          primaryPackageArtifact?.signatureUrl ??
          null,
        artifactVersion:
          primaryArtifact?.version ??
          primaryPackageArtifact?.artifactVersion ??
          null,
      });

      const dedupeKey = `update_controller:${input.routerId}:${input.channel}:${
        payload.artifactVersion ?? "latest"
      }`;

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
          type: "update_controller",
          state: "queued",
          dedupeKey,
          payload,
        })
        .returning();

      return job;
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
      await assertUpdateCapableRouter(ctx, input.routerId);

      const isScopedPackageUpdate = input.packages !== undefined;
      const packageList = input.packages
        ? [...input.packages]
        : DEFAULT_PASSWALL_PACKAGE_LIST;

      const [latestPasswall] = isScopedPackageUpdate
        ? [undefined]
        : await ctx.db
            .select()
            .from(artifacts)
            .where(
              and(
                inArray(artifacts.type, ["passwall_package", "passwall_bundle"]),
                eq(artifacts.channel, input.artifactChannel),
              ),
            )
            .orderBy(desc(artifacts.publishedAt), desc(artifacts.version))
            .limit(1);

      const payload = updatePasswallPackagesJobPayloadSchema.parse({
        channel: input.artifactChannel,
        packageList:
          isScopedPackageUpdate
            ? packageList
            : (latestPasswall?.metadata.packageList as string[] | undefined) ??
              packageList,
        packageArtifacts: [],
        artifactUrl: isScopedPackageUpdate ? null : latestPasswall?.downloadUrl ?? null,
        sha256: isScopedPackageUpdate ? null : latestPasswall?.checksumSha256 ?? null,
        signatureUrl: isScopedPackageUpdate ? null : latestPasswall?.signatureUrl ?? null,
        artifactVersion: isScopedPackageUpdate ? null : latestPasswall?.version ?? null,
      });

      const dedupeKey = `update_passwall_packages:${input.routerId}:${
        input.artifactChannel
      }:${payload.packageList.join(",")}:${payload.artifactVersion ?? "opkg"}`;
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
          type: "update_passwall_packages",
          state: "queued",
          dedupeKey,
          payload,
        })
        .returning();

      return job;
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
