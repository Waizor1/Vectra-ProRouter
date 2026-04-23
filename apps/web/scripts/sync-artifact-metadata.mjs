#!/usr/bin/env node

import { createHash, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import postgres from "postgres";
import { z } from "zod";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const workspaceRoot = path.resolve(__dirname, "..", "..", "..");

const artifactTypeSchema = z.enum([
  "controller",
  "passwall_package",
  "passwall_bundle",
  "firmware",
]);
const channelSchema = z.enum(["stable", "beta"]);
const passwallRecoveryDependencySchema = z.enum([
  "dnsmasq-full",
  "kmod-nft-socket",
  "kmod-nft-tproxy",
  "kmod-nft-nat",
]);

const feedIndexSchema = z.object({
  feedName: z.string().min(1),
  channel: channelSchema,
  targetArch: z.string().min(1),
  version: z.string().min(1),
  release: z.string().min(1),
  packages: z.array(z.string().min(1)).min(1),
  publicKey: z.string().min(1),
  feedConfig: z.string().min(1),
  packagesIndex: z.string().min(1),
  packagesSignature: z.string().min(1),
});

const artifactSeedSchema = z
  .object({
    type: artifactTypeSchema,
    channel: channelSchema.default("stable"),
    name: z.string().min(1),
    version: z.string().min(1),
    architecture: z.string().nullable().optional(),
    boardName: z.string().nullable().optional(),
    layoutFamily: z.string().nullable().optional(),
    downloadUrl: z.string().url().optional(),
    downloadPath: z.string().min(1).optional(),
    checksumSha256: z.string().min(1).optional(),
    signatureUrl: z.string().url().nullable().optional(),
    signaturePath: z.string().min(1).optional(),
    file: z.string().min(1).optional(),
    metadata: z.record(z.string(), z.unknown()).default({}),
    publishedAt: z.string().datetime().optional(),
  })
  .superRefine((artifact, ctx) => {
    if (!artifact.downloadUrl && !artifact.downloadPath) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Each artifact requires downloadUrl or downloadPath.",
      });
    }

    if (!artifact.checksumSha256 && !artifact.file) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Each artifact requires checksumSha256 or file.",
      });
    }
  });

const firmwareManifestSeedSchema = z.object({
  boardName: z.string().min(1),
  target: z.string().min(1),
  architecture: z.string().min(1),
  layoutFamily: z.string().min(1),
  channel: channelSchema.default("stable"),
  version: z.string().min(1),
  artifactName: z.string().min(1),
  artifactVersion: z.string().min(1).optional(),
  artifactType: artifactTypeSchema.default("firmware"),
  validationCommand: z
    .string()
    .min(1)
    .default("sysupgrade -T /tmp/firmware.bin"),
  rolloutPolicy: z.record(z.string(), z.unknown()).default({}),
});

const seedFileSchema = z.object({
  artifacts: z.array(artifactSeedSchema).default([]),
  firmwareManifests: z.array(firmwareManifestSeedSchema).default([]),
});

const passwallMirrorPackageEntrySchema = z.object({
  name: z.string().min(1),
  filename: z.string().min(1),
  version: z.string().min(1),
  downloadSizeBytes: z.number().int().nonnegative(),
  installedSizeBytes: z.number().int().nonnegative(),
});

const passwallMirrorRuntimeTargetSchema = z.object({
  componentName: z.string().min(1),
  remoteVersion: z.string().min(1),
  releaseUrl: z.string().url().nullable().optional(),
  assetName: z.string().min(1).nullable().optional(),
  assetUrl: z.string().url().nullable().optional(),
  assetSizeBytes: z.number().int().nonnegative().nullable().optional(),
});

const passwallMirrorManifestSchema = z.object({
  tag: z.string().min(1),
  arch: z.string().min(1),
  runtimeTargets: z.record(passwallMirrorRuntimeTargetSchema).default({}),
  requiredPackages: z.array(passwallMirrorPackageEntrySchema).min(1),
  optionalPackages: z.array(passwallMirrorPackageEntrySchema).default([]),
  sourceUrls: z.object({
    release: z.string().url().nullable().optional(),
    luciAppPackage: z.string().url().nullable().optional(),
    packageBundle: z.string().url().nullable().optional(),
  }),
});

const passwallRecoveryDependencies = [
  "dnsmasq-full",
  "kmod-nft-socket",
  "kmod-nft-tproxy",
  "kmod-nft-nat",
];
const passwallManagedInstallOrder = [
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
];

function printUsage() {
  console.log(`Usage:
  node apps/web/scripts/sync-artifact-metadata.mjs [options]

Options:
  --feed-dir PATH              Parse OpenWrt feed metadata from index.json in this directory
  --feed-subpath NAME          Public subpath prefix for feed URLs (default: openwrt)
  --passwall-mirror-dir PATH   Parse mirrored PassWall manifest + packages from this directory
  --passwall-mirror-subpath    Public subpath prefix for PassWall mirror URLs
                               (default: bootstrap/passwall2)
  --spec PATH                  JSON file with extra artifact and firmware manifest definitions
  --artifact-base-url URL      Base URL for hosted artifacts
                               (default: VECTRA_ARTIFACT_BASE_URL or https://api.vectra-pro.net/artifacts)
  --apply                      Persist changes to PostgreSQL using DATABASE_URL
  --dry-run                    Print the upsert plan without touching the database (default)
  --help                       Show this help

Examples:
  node apps/web/scripts/sync-artifact-metadata.mjs \\
    --feed-dir deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \\
    --spec deploy/runtime/artifacts/seed/pilot-artifacts.json \\
    --apply

  node apps/web/scripts/sync-artifact-metadata.mjs \\
    --feed-dir dist/openwrt-feed/stable/aarch64_cortex-a53 \\
    --dry-run`);
}

function parseArgs(argv) {
  const options = {
    feedDir: null,
    feedSubpath: "openwrt",
    passwallMirrorDir: null,
    passwallMirrorSubpath: "bootstrap/passwall2",
    spec: null,
    artifactBaseUrl:
      process.env.VECTRA_ARTIFACT_BASE_URL ??
      "https://api.vectra-pro.net/artifacts",
    apply: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    switch (arg) {
      case "--feed-dir":
        options.feedDir = argv[++index] ?? null;
        break;
      case "--feed-subpath":
        options.feedSubpath = argv[++index] ?? options.feedSubpath;
        break;
      case "--passwall-mirror-dir":
        options.passwallMirrorDir = argv[++index] ?? null;
        break;
      case "--passwall-mirror-subpath":
        options.passwallMirrorSubpath =
          argv[++index] ?? options.passwallMirrorSubpath;
        break;
      case "--spec":
        options.spec = argv[++index] ?? null;
        break;
      case "--artifact-base-url":
        options.artifactBaseUrl = argv[++index] ?? options.artifactBaseUrl;
        break;
      case "--apply":
        options.apply = true;
        break;
      case "--dry-run":
        options.apply = false;
        break;
      case "--help":
      case "-h":
        printUsage();
        process.exit(0);
      default:
        throw new Error(`Unknown argument: ${arg}`);
    }
  }

  if (!options.feedDir && !options.passwallMirrorDir && !options.spec) {
    throw new Error(
      "Provide at least one of --feed-dir, --passwall-mirror-dir, or --spec.",
    );
  }

  return options;
}

function trimTrailingSlash(value) {
  return value.replace(/\/+$/, "");
}

function resolveWithinWorkspace(inputPath) {
  if (path.isAbsolute(inputPath)) {
    return inputPath;
  }

  return path.resolve(workspaceRoot, inputPath);
}

function joinPublicUrl(baseUrl, maybeRelativePath) {
  if (!maybeRelativePath) {
    return null;
  }
  if (/^https?:\/\//i.test(maybeRelativePath)) {
    return maybeRelativePath;
  }
  return `${trimTrailingSlash(baseUrl)}/${maybeRelativePath.replace(/^\/+/, "")}`;
}

async function hashFileSha256(filePath) {
  const contents = await readFile(filePath);
  return createHash("sha256").update(contents).digest("hex");
}

async function readJson(filePath, schema) {
  const raw = await readFile(filePath, "utf8");
  return schema.parse(JSON.parse(raw));
}

function toNaturalKey(artifact) {
  return [
    artifact.type,
    artifact.channel,
    artifact.name,
    artifact.version,
    artifact.architecture ?? "",
    artifact.boardName ?? "",
    artifact.layoutFamily ?? "",
  ].join("|");
}

function parseOpenWrtPackageFileName(packageFile, targetArch) {
  const suffixes = [`_${targetArch}.ipk`, "_all.ipk"];
  const matchedSuffix = suffixes.find((suffix) => packageFile.endsWith(suffix));

  if (!matchedSuffix) {
    throw new Error(
      `Unable to match package architecture suffix for ${packageFile} against target ${targetArch}.`,
    );
  }

  const architecture = matchedSuffix === "_all.ipk" ? null : targetArch;
  const stem = packageFile.slice(0, -matchedSuffix.length);
  const separatorIndex = stem.lastIndexOf("_");

  if (separatorIndex <= 0 || separatorIndex === stem.length - 1) {
    throw new Error(`Unable to parse OpenWrt package filename: ${packageFile}`);
  }

  return {
    name: stem.slice(0, separatorIndex),
    version: stem.slice(separatorIndex + 1),
    architecture,
  };
}

async function buildFeedArtifacts(options) {
  if (!options.feedDir) {
    return [];
  }

  const feedDir = resolveWithinWorkspace(options.feedDir);
  const indexPath = path.join(feedDir, "index.json");
  const indexJson = await readJson(indexPath, feedIndexSchema);
  const publicBase = trimTrailingSlash(options.artifactBaseUrl);
  const feedBaseUrl = `${publicBase}/${options.feedSubpath}/${indexJson.channel}/${indexJson.targetArch}`;

  const artifacts = [];

  for (const packageFile of indexJson.packages) {
    const parsedFile = parseOpenWrtPackageFileName(
      packageFile,
      indexJson.targetArch,
    );
    const filePath = path.join(feedDir, packageFile);

    artifacts.push(
      artifactSeedSchema.parse({
        type: "controller",
        channel: indexJson.channel,
        name: parsedFile.name,
        version: parsedFile.version,
        architecture: parsedFile.architecture,
        boardName: null,
        layoutFamily: null,
        downloadUrl: `${feedBaseUrl}/${packageFile}`,
        checksumSha256: await hashFileSha256(filePath),
        signatureUrl: null,
        metadata: {
          source: "openwrt-feed-index",
          fileName: packageFile,
          feedName: indexJson.feedName,
          feedBaseUrl,
          feedConfigUrl: `${feedBaseUrl}/${indexJson.feedConfig}`,
          packagesIndexUrl: `${feedBaseUrl}/${indexJson.packagesIndex}`,
          packagesSignatureUrl: `${feedBaseUrl}/${indexJson.packagesSignature}`,
          publicKeyUrl: `${feedBaseUrl}/${indexJson.publicKey}`,
          targetArch: indexJson.targetArch,
        },
        publishedAt: new Date().toISOString(),
      }),
    );
  }

  return artifacts;
}

function parsePasswallPackageArchitecture(fileName, fallbackArchitecture) {
  try {
    return parseOpenWrtPackageFileName(fileName, fallbackArchitecture)
      .architecture;
  } catch {
    return fallbackArchitecture;
  }
}

function buildPasswallPackageNaturalMetadata(args) {
  return {
    source: "vectra-passwall-mirror",
    releaseTag: args.manifest.tag,
    manifestUrl: args.manifestUrl,
    releaseUrl: args.manifest.sourceUrls.release ?? null,
    packageBundleUrl: args.manifest.sourceUrls.packageBundle ?? null,
    required: args.required,
    fileName: args.entry.filename,
    mirrorBaseUrl: args.mirrorBaseUrl,
    downloadSizeBytes: args.entry.downloadSizeBytes,
    installedSizeBytes: args.entry.installedSizeBytes,
  };
}

async function buildPasswallMirrorArtifacts(options) {
  if (!options.passwallMirrorDir) {
    return [];
  }

  const mirrorDir = resolveWithinWorkspace(options.passwallMirrorDir);
  const manifestPath = path.join(mirrorDir, "manifest.json");
  const manifest = await readJson(manifestPath, passwallMirrorManifestSchema);
  const publicBase = trimTrailingSlash(options.artifactBaseUrl);
  const mirrorBaseUrl = `${publicBase}/${options.passwallMirrorSubpath}/${manifest.tag}/${manifest.arch}`;
  const manifestUrl = `${mirrorBaseUrl}/manifest.json`;

  const packageArtifacts = await Promise.all(
    [...manifest.requiredPackages, ...manifest.optionalPackages].map(
      async (entry) => {
        const filePath = path.join(mirrorDir, entry.filename);
        const required = manifest.requiredPackages.some(
          (candidate) => candidate.name === entry.name,
        );

        return artifactSeedSchema.parse({
          type: "passwall_package",
          channel: "stable",
          name: entry.name,
          version: entry.version,
          architecture: parsePasswallPackageArchitecture(
            entry.filename,
            manifest.arch,
          ),
          boardName: null,
          layoutFamily: null,
          downloadUrl: `${mirrorBaseUrl}/${entry.filename}`,
          checksumSha256: await hashFileSha256(filePath),
          signatureUrl: null,
          metadata: buildPasswallPackageNaturalMetadata({
            manifest,
            manifestUrl,
            mirrorBaseUrl,
            entry,
            required,
          }),
          publishedAt: new Date().toISOString(),
        });
      },
    ),
  );

  const bundlePackageArtifacts = packageArtifacts.map((artifact) => ({
    name: artifact.name,
    artifactUrl: artifact.downloadUrl,
    artifactVersion: artifact.version,
    sha256: artifact.checksumSha256,
    signatureUrl: artifact.signatureUrl ?? null,
    source: "vectra",
    required: artifact.metadata.required !== false,
    downloadSizeBytes:
      typeof artifact.metadata.downloadSizeBytes === "number"
        ? artifact.metadata.downloadSizeBytes
        : null,
    installedSizeBytes:
      typeof artifact.metadata.installedSizeBytes === "number"
        ? artifact.metadata.installedSizeBytes
        : null,
  }));
  const managedPackageList = Array.from(
    new Set([
      ...manifest.requiredPackages.map((entry) => entry.name),
      ...passwallRecoveryDependencies,
    ]),
  ).sort((left, right) => {
    const leftIndex = passwallManagedInstallOrder.indexOf(left);
    const rightIndex = passwallManagedInstallOrder.indexOf(right);
    if (leftIndex === -1 && rightIndex === -1) {
      return left.localeCompare(right);
    }
    if (leftIndex === -1) {
      return 1;
    }
    if (rightIndex === -1) {
      return -1;
    }
    return leftIndex - rightIndex;
  });

  const bundleArtifact = artifactSeedSchema.parse({
    type: "passwall_bundle",
    channel: "stable",
    name: "passwall2-managed-stack",
    version: manifest.tag,
    architecture: manifest.arch,
    boardName: null,
    layoutFamily: null,
    downloadUrl: manifestUrl,
    checksumSha256: await hashFileSha256(manifestPath),
    signatureUrl: null,
    metadata: {
      source: "vectra",
      releaseTag: manifest.tag,
      manifestUrl,
      releaseUrl: manifest.sourceUrls.release ?? null,
      packageBundleUrl: manifest.sourceUrls.packageBundle ?? null,
      runtimeTargets: manifest.runtimeTargets,
      requiredPackages: manifest.requiredPackages,
      optionalPackages: manifest.optionalPackages,
      packageArtifacts: bundlePackageArtifacts,
      managedPackageList,
      recoveryDependencies: passwallRecoveryDependencies,
      installOrder: passwallManagedInstallOrder,
    },
    publishedAt: new Date().toISOString(),
  });

  return [...packageArtifacts, bundleArtifact];
}

async function buildSpecArtifacts(options) {
  if (!options.spec) {
    return { artifacts: [], firmwareManifests: [] };
  }

  const specPath = resolveWithinWorkspace(options.spec);
  const seed = await readJson(specPath, seedFileSchema);

  const artifacts = await Promise.all(
    seed.artifacts.map(async (artifact) => {
      const resolvedFile = artifact.file
        ? resolveWithinWorkspace(artifact.file)
        : null;

      return {
        type: artifact.type,
        channel: artifact.channel,
        name: artifact.name,
        version: artifact.version,
        architecture: artifact.architecture ?? null,
        boardName: artifact.boardName ?? null,
        layoutFamily: artifact.layoutFamily ?? null,
        downloadUrl:
          artifact.downloadUrl ??
          joinPublicUrl(options.artifactBaseUrl, artifact.downloadPath),
        checksumSha256:
          artifact.checksumSha256 ??
          (resolvedFile ? await hashFileSha256(resolvedFile) : null),
        signatureUrl:
          artifact.signatureUrl ??
          joinPublicUrl(options.artifactBaseUrl, artifact.signaturePath),
        metadata: artifact.metadata,
        publishedAt: artifact.publishedAt ?? new Date().toISOString(),
      };
    }),
  );

  return {
    artifacts: artifacts.map((artifact) =>
      artifactSeedSchema.parse({
        ...artifact,
        file: undefined,
        downloadPath: undefined,
        signaturePath: undefined,
      }),
    ),
    firmwareManifests: seed.firmwareManifests,
  };
}

async function selectExistingArtifact(sql, artifact) {
  const rows = await sql`
    select id
    from vectra_artifact
    where type = ${artifact.type}
      and channel = ${artifact.channel}
      and name = ${artifact.name}
      and version = ${artifact.version}
      and architecture is not distinct from ${artifact.architecture}
      and board_name is not distinct from ${artifact.boardName}
      and layout_family is not distinct from ${artifact.layoutFamily}
    limit 1
  `;

  return rows[0] ?? null;
}

async function upsertArtifact(sql, artifact) {
  const existing = await selectExistingArtifact(sql, artifact);
  const metadataJson = JSON.stringify(artifact.metadata ?? {});
  const publishedAt = new Date(
    artifact.publishedAt ?? new Date().toISOString(),
  );

  if (existing) {
    await sql`
      update vectra_artifact
      set download_url = ${artifact.downloadUrl},
          checksum_sha256 = ${artifact.checksumSha256},
          signature_url = ${artifact.signatureUrl},
          metadata = ${metadataJson}::jsonb,
          published_at = ${publishedAt}
      where id = ${existing.id}
    `;

    return { id: existing.id, action: "updated" };
  }

  const id = randomUUID();
  await sql`
    insert into vectra_artifact (
      id,
      type,
      channel,
      name,
      version,
      architecture,
      board_name,
      layout_family,
      download_url,
      checksum_sha256,
      signature_url,
      metadata,
      published_at
    ) values (
      ${id},
      ${artifact.type},
      ${artifact.channel},
      ${artifact.name},
      ${artifact.version},
      ${artifact.architecture},
      ${artifact.boardName},
      ${artifact.layoutFamily},
      ${artifact.downloadUrl},
      ${artifact.checksumSha256},
      ${artifact.signatureUrl},
      ${metadataJson}::jsonb,
      ${publishedAt}
    )
  `;

  return { id, action: "inserted" };
}

async function upsertFirmwareManifest(sql, manifest, artifactId) {
  const existingRows = await sql`
    select id
    from vectra_firmware_manifest
    where board_name = ${manifest.boardName}
      and target = ${manifest.target}
      and architecture = ${manifest.architecture}
      and layout_family = ${manifest.layoutFamily}
      and channel = ${manifest.channel}
    limit 1
  `;

  const rolloutPolicyJson = JSON.stringify(manifest.rolloutPolicy ?? {});

  if (existingRows[0]) {
    await sql`
      update vectra_firmware_manifest
      set version = ${manifest.version},
          validation_command = ${manifest.validationCommand},
          artifact_id = ${artifactId},
          rollout_policy = ${rolloutPolicyJson}::jsonb
      where id = ${existingRows[0].id}
    `;

    return { id: existingRows[0].id, action: "updated" };
  }

  const id = randomUUID();
  await sql`
    insert into vectra_firmware_manifest (
      id,
      board_name,
      target,
      architecture,
      layout_family,
      channel,
      version,
      validation_command,
      artifact_id,
      rollout_policy
    ) values (
      ${id},
      ${manifest.boardName},
      ${manifest.target},
      ${manifest.architecture},
      ${manifest.layoutFamily},
      ${manifest.channel},
      ${manifest.version},
      ${manifest.validationCommand},
      ${artifactId},
      ${rolloutPolicyJson}::jsonb
    )
  `;

  return { id, action: "inserted" };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const [feedArtifacts, passwallMirrorArtifacts, specPayload] =
    await Promise.all([
      buildFeedArtifacts(options),
      buildPasswallMirrorArtifacts(options),
      buildSpecArtifacts(options),
    ]);

  const allArtifacts = [
    ...feedArtifacts,
    ...passwallMirrorArtifacts,
    ...specPayload.artifacts,
  ];
  const dedupedArtifacts = Array.from(
    new Map(
      allArtifacts.map((artifact) => [toNaturalKey(artifact), artifact]),
    ).values(),
  );

  const plan = {
    mode: options.apply ? "apply" : "dry-run",
    artifactBaseUrl: trimTrailingSlash(options.artifactBaseUrl),
    feedDir: options.feedDir,
    passwallMirrorDir: options.passwallMirrorDir,
    spec: options.spec,
    artifacts: dedupedArtifacts.map((artifact) => ({
      type: artifact.type,
      channel: artifact.channel,
      name: artifact.name,
      version: artifact.version,
      architecture: artifact.architecture ?? null,
      boardName: artifact.boardName ?? null,
      layoutFamily: artifact.layoutFamily ?? null,
      downloadUrl: artifact.downloadUrl,
    })),
    firmwareManifests: specPayload.firmwareManifests,
  };

  if (!options.apply) {
    console.log(JSON.stringify(plan, null, 2));
    return;
  }

  if (!process.env.DATABASE_URL) {
    throw new Error("DATABASE_URL is required when using --apply.");
  }

  const sql = postgres(process.env.DATABASE_URL, {
    max: 1,
  });

  try {
    const artifactIds = new Map();
    const result = await sql.begin(async (tx) => {
      const appliedArtifacts = [];
      for (const artifact of dedupedArtifacts) {
        const upserted = await upsertArtifact(tx, artifact);
        const key = toNaturalKey(artifact);
        artifactIds.set(key, upserted.id);
        appliedArtifacts.push({
          action: upserted.action,
          id: upserted.id,
          name: artifact.name,
          version: artifact.version,
          channel: artifact.channel,
        });
      }

      const appliedManifests = [];
      for (const manifest of specPayload.firmwareManifests) {
        const artifactKey = toNaturalKey({
          type: manifest.artifactType,
          channel: manifest.channel,
          name: manifest.artifactName,
          version: manifest.artifactVersion ?? manifest.version,
          architecture: manifest.architecture,
          boardName: manifest.boardName,
          layoutFamily: manifest.layoutFamily,
        });

        let artifactId = artifactIds.get(artifactKey);
        if (!artifactId) {
          const existingArtifact = await selectExistingArtifact(tx, {
            type: manifest.artifactType,
            channel: manifest.channel,
            name: manifest.artifactName,
            version: manifest.artifactVersion ?? manifest.version,
            architecture: manifest.architecture,
            boardName: manifest.boardName,
            layoutFamily: manifest.layoutFamily,
          });
          artifactId = existingArtifact?.id ?? null;
        }

        if (!artifactId) {
          throw new Error(
            `Firmware manifest ${manifest.boardName}/${manifest.layoutFamily} references missing artifact ${manifest.artifactName}@${manifest.artifactVersion ?? manifest.version}.`,
          );
        }

        const upserted = await upsertFirmwareManifest(tx, manifest, artifactId);
        appliedManifests.push({
          action: upserted.action,
          id: upserted.id,
          boardName: manifest.boardName,
          layoutFamily: manifest.layoutFamily,
          version: manifest.version,
        });
      }

      return {
        artifacts: appliedArtifacts,
        firmwareManifests: appliedManifests,
      };
    });

    console.log(JSON.stringify({ ...plan, result }, null, 2));
  } finally {
    await sql.end({ timeout: 5 });
  }
}

main().catch((error) => {
  console.error(
    `[sync-artifact-metadata] ${error instanceof Error ? error.message : String(error)}`,
  );
  process.exit(1);
});
