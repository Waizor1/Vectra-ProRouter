import { z } from "zod";

export const CONNECT_ACTION_NAMES = [
  "select_entry",
  "set_rules",
  "set_service",
  "set_wifi",
  "restart_vpn",
  "reboot",
  "update_now",
  "set_auto_update",
  "refresh_subscription",
] as const;
export type ConnectActionName = (typeof CONNECT_ACTION_NAMES)[number];
const id = z.string().regex(/^[A-Za-z0-9._:-]{1,64}$/);
const entryId = id.nullable();
// Match the established Connect validator: IDN, underscore, wildcard/suffix
// and trailing dot are supported; URLs, whitespace and empty labels are not.
const domain = z
  .string()
  .trim()
  .toLowerCase()
  .min(1)
  .max(253)
  .regex(/^(?:\*\.|\.)?[\p{L}\p{N}_-]+(?:\.[\p{L}\p{N}_-]+)*\.?$/u);
const domains = z.array(domain).transform((values) => [...new Set(values)]);
const empty = z.object({}).strict();
export const CONNECT_ACTION_PARAMS = {
  select_entry: z.object({ entryId }).strict(),
  set_rules: z
    .object({ direct: domains, vpn: domains })
    .strict()
    .refine(
      (value) => value.direct.length + value.vpn.length <= 300,
      "at most 300 domains",
    ),
  set_service: z.object({ service: id, entryId }).strict(),
  set_wifi: z
    .object({
      ssid: z
        .string()
        .refine(
          (value) =>
            Buffer.byteLength(value, "utf8") >= 1 &&
            Buffer.byteLength(value, "utf8") <= 32 &&
            !/[\x00-\x1f\x7f]/.test(value),
          "ssid must be 1-32 UTF-8 bytes without control characters",
        ),
      password: z
        .string()
        .regex(
          /^[\x20-\x7e]{8,63}$/,
          "password must be 8-63 printable ASCII characters",
        ),
    })
    .strict(),
  restart_vpn: empty,
  reboot: empty,
  update_now: empty,
  set_auto_update: z.object({ enabled: z.boolean() }).strict(),
  refresh_subscription: empty,
};

/** Parameters are parsed independently of capability: invalid input is never
 * silently accepted for a router that happens to advertise the operation. */
export function parseConnectActionParams(
  action: ConnectActionName,
  params: unknown,
) {
  return CONNECT_ACTION_PARAMS[action].safeParse(params);
}
