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
  "set_port_forwards",
] as const;
export type ConnectActionName = (typeof CONNECT_ACTION_NAMES)[number];
/** Flags a router advertises next to its actions, refining one of them. */
export const CONNECT_CAPABILITY_FLAGS = [
  "set_service_auto",
  "set_wifi_band",
] as const;
/** set_service's entryId taking a service back to its default; only a router
 * advertising set_service_auto understands it. */
export const CONNECT_SERVICE_AUTO = ":auto";
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
// One port forward exactly as vctl's portForwardList reads it: these keys
// and no other (deviceName is telemetry, never a parameter), the router's
// syntax for each. Whether destIp is in the LAN and what conflicts is the
// router's to judge.
const ipv4Octet = "(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])";
const ipv4 = new RegExp(`^${ipv4Octet}(?:\\.${ipv4Octet}){3}$`);
const portForward = z
  .object({
    id: z
      .string()
      .regex(/^[0-9a-f]{8}$/)
      .optional(),
    preset: z
      .string()
      .regex(/^[a-z0-9-]{1,24}$/)
      .nullable()
      .optional(),
    destIp: z.string().regex(ipv4, "destIp must be an IPv4 address"),
    port: z.string().refine((value) => {
      const match = /^([0-9]{1,5})(?:-([0-9]{1,5}))?$/.exec(value);
      if (!match) return false;
      const lo = Number(match[1]);
      const hi = match[2] === undefined ? lo : Number(match[2]);
      return lo >= 1 && hi <= 65535 && lo <= hi;
    }, "port must be a port or a range a-b within 1-65535"),
    proto: z.enum(["tcp", "udp", "both"]),
    direct: z.boolean(),
    enabled: z.boolean(),
  })
  .strict();
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
      // Only that band's networks; without it, every one. Only a router
      // advertising set_wifi_band understands it.
      band: z.enum(["2g", "5g", "6g"]).optional(),
    })
    .strict(),
  restart_vpn: empty,
  reboot: empty,
  update_now: empty,
  set_auto_update: z.object({ enabled: z.boolean() }).strict(),
  refresh_subscription: empty,
  // The whole list: it replaces every Vectra rule, [] removes them all.
  set_port_forwards: z
    .object({
      rules: z
        .array(portForward)
        .max(32, "at most 32 rules")
        .refine((rules) => {
          const ids = rules.flatMap((rule) => (rule.id ? [rule.id] : []));
          return new Set(ids).size === ids.length;
        }, "rule ids must be unique"),
    })
    .strict(),
};

/** Parameters are parsed independently of capability: invalid input is never
 * silently accepted for a router that happens to advertise the operation. */
export function parseConnectActionParams(
  action: ConnectActionName,
  params: unknown,
) {
  return CONNECT_ACTION_PARAMS[action].safeParse(params);
}
