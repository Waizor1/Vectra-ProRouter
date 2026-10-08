import { DEFAULT_PARTNER_ID } from "./partner-registry";
import { routerPartnerId } from "./partner-scope";
import {
  enqueuePartnerWebhookWithDb,
  schedulePartnerWebhookDelivery,
} from "./partner-webhooks";

export type VendorAccessKind =
  | "terminal"
  | "direct_mode"
  | "reconnect"
  | "safe_repair"
  | "collect_logs"
  | "reboot"
  | "rename";

/**
 * A partner's customer router was reached by Vectra (the router software's
 * vendor): the partner hears of it. Vectra Connect is Vectra itself and an
 * unclaimed router has nobody to tell — both stay silent. Never fails the
 * operator's action: a lost notice is logged, the job still runs.
 */
export async function notifyVendorAccessWithDb(
  client: Parameters<typeof enqueuePartnerWebhookWithDb>[0],
  router: { id: string; ownerRef: string | null; partnerId: string | null },
  access: { kind: VendorAccessKind; by: "operator" | "telegram" },
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
    console.error("[vendor-access] notice not queued", error);
    return null;
  }
}
