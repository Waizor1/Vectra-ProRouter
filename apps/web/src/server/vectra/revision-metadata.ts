import { passwallDesiredRevisions } from "@vectra/db";
import { and, desc, inArray, sql } from "drizzle-orm";

import type { db as appDb } from "~/server/db";

type DatabaseClient = Pick<typeof appDb, "select">;

export type PasswallRevisionMetadataRow = {
  id: string;
  routerId: string;
  revisionNumber: number;
  status: string;
  origin: string;
  configDigest: string | null;
  createdBy: string;
  note: string | null;
  approvedAt: Date | null;
  createdAt: Date;
  hasRawImportedSnapshot: boolean;
};

export async function loadRevisionMetadata(
  database: DatabaseClient,
  routerIds: string[],
  options: {
    origins?: string[];
  } = {},
) {
  if (routerIds.length === 0) {
    return [] as PasswallRevisionMetadataRow[];
  }

  const whereClause =
    options.origins && options.origins.length > 0
      ? and(
          inArray(passwallDesiredRevisions.routerId, routerIds),
          inArray(passwallDesiredRevisions.origin, options.origins),
        )
      : inArray(passwallDesiredRevisions.routerId, routerIds);

  return database
    .select({
      id: passwallDesiredRevisions.id,
      routerId: passwallDesiredRevisions.routerId,
      revisionNumber: passwallDesiredRevisions.revisionNumber,
      status: passwallDesiredRevisions.status,
      origin: passwallDesiredRevisions.origin,
      configDigest: passwallDesiredRevisions.configDigest,
      createdBy: passwallDesiredRevisions.createdBy,
      note: passwallDesiredRevisions.note,
      approvedAt: passwallDesiredRevisions.approvedAt,
      createdAt: passwallDesiredRevisions.createdAt,
      hasRawImportedSnapshot: sql<boolean>`case
        when ${passwallDesiredRevisions.rawImportedSnapshot} is null then false
        else true
      end`.as("hasRawImportedSnapshot"),
    })
    .from(passwallDesiredRevisions)
    .where(whereClause)
    .orderBy(desc(passwallDesiredRevisions.createdAt));
}
