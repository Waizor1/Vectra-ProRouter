import type {
  PasswallDesiredConfig,
  RouterInventory,
  RescueRepairResultPayload,
} from "@vectra/contracts";
import {
  artifactTypeSchema,
  controllerChannelSchema,
  credentialTypeSchema,
  incidentStateSchema,
  incidentTypeSchema,
  jobResultStatusSchema,
  jobStateSchema,
  jobTypeSchema,
  routerImportStateSchema,
  routerStatusSchema,
  secretBlobScopeSchema,
  severitySchema,
} from "@vectra/contracts";
import { sql } from "drizzle-orm";
import {
  boolean,
  index,
  integer,
  jsonb,
  pgEnum,
  pgTableCreator,
  text,
  timestamp,
  uniqueIndex,
} from "drizzle-orm/pg-core";

const routerStatusEnum = pgEnum(
  "vectra_router_status",
  routerStatusSchema.options,
);
const credentialTypeEnum = pgEnum(
  "vectra_credential_type",
  credentialTypeSchema.options,
);
const jobTypeEnum = pgEnum("vectra_job_type", jobTypeSchema.options);
const jobStateEnum = pgEnum("vectra_job_state", jobStateSchema.options);
const jobResultStatusEnum = pgEnum(
  "vectra_job_result_status",
  jobResultStatusSchema.options,
);
const artifactTypeEnum = pgEnum(
  "vectra_artifact_type",
  artifactTypeSchema.options,
);
const channelEnum = pgEnum(
  "vectra_controller_channel",
  controllerChannelSchema.options,
);
const importStateEnum = pgEnum(
  "vectra_router_import_state",
  routerImportStateSchema.options,
);
const incidentTypeEnum = pgEnum(
  "vectra_incident_type",
  incidentTypeSchema.options,
);
const incidentStateEnum = pgEnum(
  "vectra_incident_state",
  incidentStateSchema.options,
);
const severityEnum = pgEnum("vectra_severity", severitySchema.options);
const secretBlobScopeEnum = pgEnum(
  "vectra_secret_blob_scope",
  secretBlobScopeSchema.options,
);
const pushAlertKindEnum = pgEnum("vectra_push_alert_kind", [
  "offline",
  "direct_mode",
  "incident",
]);

export const createTable = pgTableCreator((name) => `vectra_${name}`);

const timestamps = {
  createdAt: timestamp("created_at", { withTimezone: true })
    .notNull()
    .defaultNow(),
  updatedAt: timestamp("updated_at", { withTimezone: true })
    .notNull()
    .defaultNow()
    .$onUpdate(() => new Date()),
};

export const routers = createTable(
  "router",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    deviceIdentifier: text("device_identifier").notNull(),
    displayName: text("display_name"),
    hostname: text("hostname"),
    panelDomain: text("panel_domain"),
    model: text("model"),
    boardName: text("board_name"),
    target: text("target"),
    architecture: text("architecture"),
    openwrtRelease: text("openwrt_release"),
    status: routerStatusEnum("status").notNull().default("pending"),
    importState: importStateEnum("import_state")
      .notNull()
      .default("awaiting_import"),
    controllerChannel: channelEnum("controller_channel")
      .notNull()
      .default("stable"),
    rolloutGroupId: text("rollout_group_id"),
    pendingImportRevisionId: text("pending_import_revision_id"),
    activeRevisionId: text("active_revision_id"),
    lastAppliedRevisionId: text("last_applied_revision_id"),
    lastConfigDigest: text("last_config_digest"),
    approvedAt: timestamp("approved_at", { withTimezone: true }),
    lastSeenAt: timestamp("last_seen_at", { withTimezone: true }),
    lastCheckInAt: timestamp("last_check_in_at", { withTimezone: true }),
    lastDirectModeAt: timestamp("last_direct_mode_at", { withTimezone: true }),
    lastRescueReason: text("last_rescue_reason"),
    ...timestamps,
  },
  (table) => [
    uniqueIndex("vectra_router_device_identifier_idx").on(
      table.deviceIdentifier,
    ),
    index("vectra_router_status_idx").on(table.status),
    index("vectra_router_last_seen_idx").on(table.lastSeenAt),
    index("vectra_router_rollout_group_idx").on(table.rolloutGroupId),
  ],
);

export const routerCredentials = createTable(
  "router_credential",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    type: credentialTypeEnum("type").notNull().default("agent_token"),
    tokenHash: text("token_hash").notNull(),
    tokenPreview: text("token_preview").notNull(),
    devicePublicKey: text("device_public_key").notNull(),
    issuedAt: timestamp("issued_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
    lastUsedAt: timestamp("last_used_at", { withTimezone: true }),
    revokedAt: timestamp("revoked_at", { withTimezone: true }),
  },
  (table) => [
    index("vectra_router_credential_router_idx").on(table.routerId),
    uniqueIndex("vectra_router_credential_token_idx").on(table.tokenHash),
  ],
);

export const routerInventorySnapshots = createTable(
  "router_inventory_snapshot",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    source: text("source").notNull().default("check_in"),
    payload: jsonb("payload").$type<RouterInventory>().notNull(),
    passwallEnabled: boolean("passwall_enabled").notNull().default(false),
    selectedNodeId: text("selected_node_id"),
    nodeCount: integer("node_count").notNull().default(0),
    subscriptionCount: integer("subscription_count").notNull().default(0),
    controllerVersion: text("controller_version"),
    passwallAppVersion: text("passwall_app_version"),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    index("vectra_router_inventory_router_idx").on(table.routerId),
    index("vectra_router_inventory_created_idx").on(table.createdAt),
    index("vectra_router_inventory_router_created_idx").on(
      table.routerId,
      table.createdAt.desc(),
    ),
  ],
);

export const passwallDesiredRevisions = createTable(
  "passwall_desired_revision",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    revisionNumber: integer("revision_number").notNull(),
    status: text("status").notNull().default("draft"),
    origin: text("origin").notNull().default("operator_draft"),
    configDigest: text("config_digest"),
    config: jsonb("config").$type<PasswallDesiredConfig>().notNull(),
    rawImportedSnapshot: jsonb("raw_imported_snapshot")
      .$type<Record<string, unknown> | null>()
      .default(null),
    createdBy: text("created_by").notNull().default("operator"),
    note: text("note"),
    approvedAt: timestamp("approved_at", { withTimezone: true }),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    uniqueIndex("vectra_passwall_revision_router_revision_idx").on(
      table.routerId,
      table.revisionNumber,
    ),
    index("vectra_passwall_revision_router_idx").on(table.routerId),
  ],
);

export const passwallAppliedRevisions = createTable(
  "passwall_applied_revision",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    desiredRevisionId: text("desired_revision_id").references(
      () => passwallDesiredRevisions.id,
      { onDelete: "set null" },
    ),
    jobId: text("job_id"),
    result: text("result").notNull().default("applied"),
    uciDigest: text("uci_digest"),
    stdout: text("stdout"),
    stderr: text("stderr"),
    config: jsonb("config").$type<PasswallDesiredConfig | null>().default(null),
    rawSnapshot: jsonb("raw_snapshot")
      .$type<Record<string, unknown> | null>()
      .default(null),
    reportedAt: timestamp("reported_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [index("vectra_passwall_applied_router_idx").on(table.routerId)],
);

export const passwallSecretBlobs = createTable(
  "passwall_secret_blob",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    desiredRevisionId: text("desired_revision_id").references(
      () => passwallDesiredRevisions.id,
      { onDelete: "cascade" },
    ),
    scope: secretBlobScopeEnum("scope").notNull(),
    ciphertext: text("ciphertext").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    index("vectra_passwall_secret_router_idx").on(table.routerId),
    index("vectra_passwall_secret_revision_idx").on(table.desiredRevisionId),
  ],
);

export type RescueCaseState =
  | "open"
  | "repairing"
  | "escalated"
  | "silenced"
  | "resolved";

export type RescueCaseTrigger =
  | "direct_mode"
  | "proxy_outage"
  | "server_unreachable"
  | "stale_check_in"
  | "foreign_reachability_blocked"
  | "telegram_blocked";

export type RescueCaseEvidence = Record<string, unknown>;
export type RescueCaseRepairAttempt =
  | RescueRepairResultPayload
  | Record<string, unknown>;

export const jobs = createTable(
  "job",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    type: jobTypeEnum("type").notNull(),
    state: jobStateEnum("state").notNull().default("queued"),
    payload: jsonb("payload")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    desiredRevisionId: text("desired_revision_id").references(
      () => passwallDesiredRevisions.id,
      { onDelete: "set null" },
    ),
    dedupeKey: text("dedupe_key"),
    deliverAfter: timestamp("deliver_after", { withTimezone: true }),
    deliveredAt: timestamp("delivered_at", { withTimezone: true }),
    completedAt: timestamp("completed_at", { withTimezone: true }),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    index("vectra_job_router_state_idx").on(table.routerId, table.state),
    uniqueIndex("vectra_job_dedupe_idx").on(table.dedupeKey),
  ],
);

export const rescueCases = createTable(
  "rescue_case",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    trigger: text("trigger").$type<RescueCaseTrigger>().notNull(),
    state: text("state").$type<RescueCaseState>().notNull().default("open"),
    triggerDetails: jsonb("trigger_details")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    evidence: jsonb("evidence")
      .$type<RescueCaseEvidence>()
      .notNull()
      .default({}),
    repairAttempts: jsonb("repair_attempts")
      .$type<RescueCaseRepairAttempt[]>()
      .notNull()
      .default([]),
    diagnosis: jsonb("diagnosis")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    startedAt: timestamp("started_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
    lastAttemptAt: timestamp("last_attempt_at", { withTimezone: true }),
    escalatedAt: timestamp("escalated_at", { withTimezone: true }),
    resolvedAt: timestamp("resolved_at", { withTimezone: true }),
    silencedUntil: timestamp("silenced_until", { withTimezone: true }),
    ...timestamps,
  },
  (table) => [
    index("vectra_rescue_case_router_state_idx").on(
      table.routerId,
      table.state,
    ),
    index("vectra_rescue_case_started_idx").on(table.startedAt),
    index("vectra_rescue_case_resolved_idx").on(table.resolvedAt),
    uniqueIndex("vectra_rescue_case_one_active_router_idx")
      .on(table.routerId)
      .where(
        sql`${table.state} IN ('open', 'repairing', 'escalated', 'silenced')`,
      ),
  ],
);

export const jobResults = createTable(
  "job_result",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    jobId: text("job_id")
      .notNull()
      .references(() => jobs.id, { onDelete: "cascade" }),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    status: jobResultStatusEnum("status").notNull(),
    payload: jsonb("payload")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    reportedAt: timestamp("reported_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    index("vectra_job_result_router_idx").on(table.routerId),
    index("vectra_job_result_job_idx").on(table.jobId),
  ],
);

export type RouterOnboardingProfileBaseline =
  | "standard-non-hh"
  | "hh-exempt"
  | "subscription-only";

export type RouterOnboardingRuntimePolicy =
  | "auto-minimal-passwall-xray"
  | "controller-only";

export type RouterOnboardingVerifyPolicy = "route-smoke" | "services-only";

export type RouterOnboardingState =
  | "created"
  | "preflight"
  | "request_initial_import"
  | "approve_initial_import"
  | "rename_router"
  | "ensure_runtime"
  | "apply_subscription"
  | "refresh_subscription"
  | "resolve_route_baseline"
  | "apply_route_baseline"
  | "verify_runtime"
  | "repair_runtime"
  | "final_reimport"
  | "done";

export type RouterOnboardingRunStatus =
  | "running"
  | "waiting"
  | "blocked"
  | "failed"
  | "done"
  | "paused";

export const routerOnboardingProfiles = createTable(
  "router_onboarding_profile",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    enabled: boolean("enabled").notNull().default(true),
    targetHostname: text("target_hostname"),
    displayName: text("display_name"),
    subscriptionSecretCiphertext: text("subscription_secret_ciphertext"),
    subscriptionUrlHash: text("subscription_url_hash"),
    subscriptionRemark: text("subscription_remark"),
    baseline: text("baseline")
      .$type<RouterOnboardingProfileBaseline>()
      .notNull()
      .default("standard-non-hh"),
    runtimePolicy: text("runtime_policy")
      .$type<RouterOnboardingRuntimePolicy>()
      .notNull()
      .default("auto-minimal-passwall-xray"),
    verifyPolicy: text("verify_policy")
      .$type<RouterOnboardingVerifyPolicy>()
      .notNull()
      .default("route-smoke"),
    notes: text("notes"),
    ...timestamps,
  },
  (table) => [
    uniqueIndex("vectra_router_onboarding_profile_router_idx").on(
      table.routerId,
    ),
    index("vectra_router_onboarding_profile_enabled_idx").on(table.enabled),
  ],
);

export const routerOnboardingRuns = createTable(
  "router_onboarding_run",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    profileId: text("profile_id")
      .notNull()
      .references(() => routerOnboardingProfiles.id, { onDelete: "cascade" }),
    state: text("state")
      .$type<RouterOnboardingState>()
      .notNull()
      .default("created"),
    status: text("status")
      .$type<RouterOnboardingRunStatus>()
      .notNull()
      .default("running"),
    attempt: integer("attempt").notNull().default(0),
    lastJobId: text("last_job_id").references(() => jobs.id, {
      onDelete: "set null",
    }),
    activeRevisionId: text("active_revision_id").references(
      () => passwallDesiredRevisions.id,
      { onDelete: "set null" },
    ),
    lastError: text("last_error"),
    nextRunAfter: timestamp("next_run_after", { withTimezone: true }),
    completedAt: timestamp("completed_at", { withTimezone: true }),
    ...timestamps,
  },
  (table) => [
    index("vectra_router_onboarding_run_router_status_idx").on(
      table.routerId,
      table.status,
    ),
    index("vectra_router_onboarding_run_profile_idx").on(table.profileId),
    uniqueIndex("vectra_router_onboarding_run_one_active_router_idx")
      .on(table.routerId)
      .where(
        sql`${table.status} IN ('running', 'waiting', 'blocked', 'failed', 'paused')`,
      ),
  ],
);

export const artifacts = createTable(
  "artifact",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    type: artifactTypeEnum("type").notNull(),
    channel: channelEnum("channel").notNull().default("stable"),
    name: text("name").notNull(),
    version: text("version").notNull(),
    architecture: text("architecture"),
    boardName: text("board_name"),
    layoutFamily: text("layout_family"),
    downloadUrl: text("download_url").notNull(),
    checksumSha256: text("checksum_sha256").notNull(),
    signatureUrl: text("signature_url"),
    metadata: jsonb("metadata")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    publishedAt: timestamp("published_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    index("vectra_artifact_lookup_idx").on(
      table.type,
      table.channel,
      table.name,
    ),
  ],
);

export const firmwareManifests = createTable(
  "firmware_manifest",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    boardName: text("board_name").notNull(),
    target: text("target").notNull(),
    architecture: text("architecture").notNull(),
    layoutFamily: text("layout_family").notNull(),
    channel: channelEnum("channel").notNull().default("stable"),
    version: text("version").notNull(),
    validationCommand: text("validation_command")
      .notNull()
      .default("sysupgrade -T /tmp/firmware.bin"),
    artifactId: text("artifact_id")
      .notNull()
      .references(() => artifacts.id, { onDelete: "cascade" }),
    rolloutPolicy: jsonb("rollout_policy")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [
    uniqueIndex("vectra_firmware_manifest_unique_idx").on(
      table.boardName,
      table.target,
      table.architecture,
      table.layoutFamily,
      table.channel,
    ),
  ],
);

export const eventLog = createTable(
  "event_log",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id").references(() => routers.id, {
      onDelete: "set null",
    }),
    type: text("type").notNull(),
    severity: severityEnum("severity").notNull().default("info"),
    message: text("message").notNull(),
    metadata: jsonb("metadata")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (table) => [index("vectra_event_log_router_idx").on(table.routerId)],
);

export const operatorPushSubscriptions = createTable(
  "operator_push_subscription",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    operatorUser: text("operator_user").notNull(),
    endpoint: text("endpoint").notNull(),
    p256dh: text("p256dh").notNull(),
    auth: text("auth").notNull(),
    userAgent: text("user_agent"),
    lastSuccessfulPushAt: timestamp("last_successful_push_at", {
      withTimezone: true,
    }),
    lastFailureAt: timestamp("last_failure_at", { withTimezone: true }),
    lastFailureReason: text("last_failure_reason"),
    disabledAt: timestamp("disabled_at", { withTimezone: true }),
    ...timestamps,
  },
  (table) => [
    uniqueIndex("vectra_operator_push_subscription_endpoint_idx").on(
      table.endpoint,
    ),
    index("vectra_operator_push_subscription_user_idx").on(table.operatorUser),
    index("vectra_operator_push_subscription_disabled_idx").on(
      table.disabledAt,
    ),
  ],
);

export const operatorGlobalTemplates = createTable(
  "operator_global_template",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    templateKey: text("template_key").notNull(),
    title: text("title").notNull(),
    installBaselineUci: text("install_baseline_uci").notNull(),
    rolloutConfig: jsonb("rollout_config")
      .$type<PasswallDesiredConfig>()
      .notNull(),
    rolloutMode: text("rollout_mode").notNull().default("settings_only"),
    note: text("note"),
    ...timestamps,
  },
  (table) => [
    uniqueIndex("vectra_operator_global_template_key_idx").on(
      table.templateKey,
    ),
  ],
);

export const operatorRolloutProfiles = createTable(
  "operator_rollout_profile",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    profileKey: text("profile_key").notNull(),
    name: text("name").notNull(),
    description: text("description"),
    rolloutConfig: jsonb("rollout_config")
      .$type<PasswallDesiredConfig>()
      .notNull(),
    note: text("note"),
    ...timestamps,
  },
  (table) => [
    uniqueIndex("vectra_operator_rollout_profile_key_idx").on(table.profileKey),
  ],
);

export const operatorRouterGroups = createTable(
  "operator_router_group",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    groupKey: text("group_key").notNull(),
    name: text("name").notNull(),
    description: text("description"),
    rolloutProfileId: text("rollout_profile_id").references(
      () => operatorRolloutProfiles.id,
      { onDelete: "set null" },
    ),
    ...timestamps,
  },
  (table) => [
    uniqueIndex("vectra_operator_router_group_key_idx").on(table.groupKey),
    index("vectra_operator_router_group_profile_idx").on(
      table.rolloutProfileId,
    ),
  ],
);

export const operatorPushAlerts = createTable(
  "operator_push_alert",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    kind: pushAlertKindEnum("kind").notNull(),
    severity: severityEnum("severity").notNull().default("warning"),
    dedupeKey: text("dedupe_key").notNull(),
    title: text("title").notNull(),
    body: text("body").notNull(),
    href: text("href").notNull(),
    payload: jsonb("payload")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
    resolvedAt: timestamp("resolved_at", { withTimezone: true }),
  },
  (table) => [
    uniqueIndex("vectra_operator_push_alert_dedupe_idx").on(table.dedupeKey),
    index("vectra_operator_push_alert_router_idx").on(table.routerId),
    index("vectra_operator_push_alert_resolved_idx").on(table.resolvedAt),
  ],
);

export const healthIncidents = createTable(
  "health_incident",
  {
    id: text("id")
      .primaryKey()
      .$defaultFn(() => crypto.randomUUID()),
    routerId: text("router_id")
      .notNull()
      .references(() => routers.id, { onDelete: "cascade" }),
    type: incidentTypeEnum("type").notNull(),
    state: incidentStateEnum("state").notNull().default("open"),
    reason: text("reason").notNull(),
    metadata: jsonb("metadata")
      .$type<Record<string, unknown>>()
      .notNull()
      .default({}),
    openedAt: timestamp("opened_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
    resolvedAt: timestamp("resolved_at", { withTimezone: true }),
  },
  (table) => [index("vectra_health_incident_router_idx").on(table.routerId)],
);
