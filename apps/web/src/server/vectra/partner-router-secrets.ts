import type { RouterInventory } from "@vectra/contracts";
import type { jobs, routerInventorySnapshots, routers } from "@vectra/db";
import {
  decryptJson,
  encryptJson,
  sanitizePasswallRawSnapshot,
} from "./secrets";
import {
  parseConnectActionParams,
  type ConnectActionName,
} from "./partner-action-params";

type WifiSecretEnvelope = {
  routerId: string;
  ownerRef: string;
  wifi: Array<{ band: string; ssid: string; password: string }>;
};
/** Wire-only credentials never enter ordinary inventory. Metadata and owner
 * binding are authenticated by the same AES-GCM envelope as the password. */
export function protectConnectInventory(
  routerId: string,
  ownerRef: string | null,
  inventory: RouterInventory,
) {
  const connect = inventory.connect;
  const wifi = connect?.wifi?.map(
    ({ password: _password, ...publicFields }) => publicFields,
  );
  const sanitized: RouterInventory = {
    ...inventory,
    ...(inventory.rawSnapshot
      ? { rawSnapshot: sanitizePasswallRawSnapshot(inventory.rawSnapshot) }
      : {}),
    ...(connect ? { connect: { ...connect, ...(wifi ? { wifi } : {}) } } : {}),
  };
  const secrets =
    ownerRef && connect?.ownerRef === ownerRef
      ? (connect.wifi?.flatMap((item) =>
          item.password ? [{ ...item, password: item.password }] : [],
        ) ?? [])
      : [];
  return {
    inventory: sanitized,
    ciphertext: secrets.length
      ? encryptJson({
          routerId,
          ownerRef: ownerRef!,
          wifi: secrets,
        } satisfies WifiSecretEnvelope)
      : null,
  };
}

export function hydrateConnectWifi(
  router: typeof routers.$inferSelect,
  inventory: typeof routerInventorySnapshots.$inferSelect | null,
) {
  const wifi = inventory?.payload.connect?.wifi?.map(
    ({ password: _password, ...publicFields }) => publicFields,
  );
  if (
    !inventory?.connectSecretCiphertext ||
    !router.ownerRef ||
    router.releasedAt ||
    !router.claimedAt ||
    inventory.createdAt < router.claimedAt ||
    inventory.routerId !== router.id ||
    inventory.payload.connect?.ownerRef !== router.ownerRef
  )
    return wifi;
  try {
    const envelope = decryptJson<WifiSecretEnvelope>(
      inventory.connectSecretCiphertext,
    );
    if (
      envelope.routerId !== router.id ||
      envelope.ownerRef !== router.ownerRef
    )
      return wifi;
    return wifi?.map((item) => {
      const secret = envelope.wifi.find(
        (value) => value.band === item.band && value.ssid === item.ssid,
      );
      return secret ? { ...item, password: secret.password } : item;
    });
  } catch {
    return wifi;
  }
}

export function protectPartnerParams(
  action: ConnectActionName,
  params: Record<string, unknown>,
  context: { routerId: string; ownerRef: string; actionId: string },
) {
  return action === "set_wifi"
    ? {
        paramsCiphertext: encryptJson({
          purpose: "connect-partner-job-v1",
          ...context,
          action,
          params,
        }),
      }
    : { params };
}

/** Only used inside the authenticated current-owner check-in serializer. */
export function hydratePartnerJobPayload(
  job: typeof jobs.$inferSelect,
  ownerRef: string | null,
) {
  if (job.payload.origin !== "partner_action") return job.payload;
  const { actionId, action, ownerRef: boundOwner } = job.payload;
  if (!ownerRef || boundOwner !== ownerRef || actionId !== job.id)
    throw new Error("Partner action owner mismatch");
  let params: unknown = job.payload.params ?? {};
  if (action === "set_wifi") {
    if (typeof job.payload.paramsCiphertext !== "string")
      throw new Error("Missing bound partner action ciphertext");
    const envelope = decryptJson<{
      purpose?: string;
      routerId?: string;
      ownerRef?: string;
      actionId?: string;
      action?: string;
      params?: unknown;
    }>(job.payload.paramsCiphertext);
    if (
      envelope?.purpose !== "connect-partner-job-v1" ||
      envelope.routerId !== job.routerId ||
      envelope.ownerRef !== ownerRef ||
      envelope.actionId !== job.id ||
      envelope.action !== action
    )
      throw new Error("Partner action ciphertext context mismatch");
    params = envelope.params;
  } else if (job.payload.paramsCiphertext !== undefined) {
    throw new Error("Unexpected partner action ciphertext");
  }
  const parsed = parseConnectActionParams(action as ConnectActionName, params);
  if (!parsed.success)
    throw new Error("Invalid stored partner action parameters");
  return {
    origin: "partner_action",
    actionId,
    ownerRef,
    action,
    params: parsed.data,
  };
}
