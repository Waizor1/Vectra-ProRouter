import { routers } from "@vectra/db";
import { eq, isNull, or, type SQL } from "drizzle-orm";
import { DEFAULT_PARTNER_ID } from "./partner-registry";

/** The partner that owns a router; a router from before there were several is Vectra Connect's. */
export function routerPartnerId(row: { partnerId?: string | null }) {
  return row.partnerId ?? DEFAULT_PARTNER_ID;
}

/** SQL twin of routerPartnerId(row) === partnerId. */
export function routerOwnedByPartner(partnerId: string): SQL {
  return partnerId === DEFAULT_PARTNER_ID
    ? (or(isNull(routers.partnerId), eq(routers.partnerId, DEFAULT_PARTNER_ID)) as SQL)
    : eq(routers.partnerId, partnerId);
}
