import { routers } from "@vectra/db";
import { inArray } from "drizzle-orm";

import { DEFAULT_PARTNER_ID } from "./partner-registry";
import { routerPartnerId } from "./partner-scope";
import {
  enqueuePartnerWebhookWithDb,
  schedulePartnerWebhookDelivery,
} from "./partner-webhooks";

/**
 * What Vectra's engineer did to the router, as coarse as the partner needs:
 * the first group are interactive or rescue actions, the rest are the
 * operator's routine work grouped by what it does to the customer's router.
 */
export type VendorAccessKind =
  | "terminal"
  | "direct_mode"
  | "reconnect"
  | "safe_repair"
  | "collect_logs"
  | "reboot"
  | "rename"
  // A config (PassWall / xray / route policy / rollout) queued to be applied.
  | "config_apply"
  // Controller, PassWall or package software replaced.
  | "software_update"
  // Refresh of subscriptions or rules, runtime repair, ipset clear, firmware check.
  | "maintenance"
  // Read-only: logs, baselines, subscription inspection.
  | "diagnostics";

type Client = Parameters<typeof enqueuePartnerWebhookWithDb>[0];
type VendorAccess = { kind: VendorAccessKind; by: "operator" | "telegram" };

// A driver error carries the failed statement with its parameters, and the
// webhook payload (the partner's ownerRef) is one of them: the log line keeps
// only the error's kind and, if there is one, its underlying cause.
function describeFailure(error: unknown) {
  if (!(error instanceof Error)) return "unknown error";
  return error.cause instanceof Error
    ? `${error.name}: ${error.cause.message}`
    : error.name;
}

/**
 * A partner's customer router was reached by Vectra (the router software's
 * vendor): the partner hears of it. Vectra Connect is Vectra itself and an
 * unclaimed router has nobody to tell — both stay silent. Never fails the
 * operator's action: a lost notice is logged, the job still runs.
 */
export async function notifyVendorAccessWithDb(
  client: Client,
  router: { id: string; ownerRef: string | null; partnerId: string | null },
  access: VendorAccess,
) {
  const partnerId = routerPartnerId(router);
  if (!router.ownerRef || partnerId === DEFAULT_PARTNER_ID) return null;
  try {
    const id = await enqueuePartnerWebhookWithDb(client, {
      event: "router.vendor_access",
      routerId: router.id,
      ownerRef: router.ownerRef,
      partnerId,
      detail: { kind: access.kind, by: access.by },
    });
    if (id) schedulePartnerWebhookDelivery();
    return id;
  } catch (error) {
    console.error(
      `[vendor-access] notice not queued for router ${router.id} (${access.kind}): ${describeFailure(error)}`,
    );
    return null;
  }
}

/**
 * The same notice for a batch the operator just queued (a rollout): one read
 * of the routers, one notice per partner router among them. Returns how many
 * notices were queued. Never fails the operator's action.
 */
export async function notifyVendorAccessForRoutersWithDb(
  client: Client,
  routerIds: readonly string[],
  access: VendorAccess,
) {
  if (routerIds.length === 0) return 0;
  try {
    const rows = await client
      .select({
        id: routers.id,
        ownerRef: routers.ownerRef,
        partnerId: routers.partnerId,
      })
      .from(routers)
      .where(inArray(routers.id, [...routerIds]));
    const wanted = new Set(routerIds);
    let announced = 0;
    for (const row of rows) {
      if (!wanted.has(row.id)) continue;
      if (await notifyVendorAccessWithDb(client, row, access)) announced += 1;
    }
    return announced;
  } catch (error) {
    console.error(
      `[vendor-access] batch notice not queued (${access.kind}): ${describeFailure(error)}`,
    );
    return 0;
  }
}
