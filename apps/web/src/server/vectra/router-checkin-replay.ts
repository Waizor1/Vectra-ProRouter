// A2 check-in nonce single-use. A signed check-in's nonce is consumed the first
// time its signature verifies, so a captured (nonce, signature, body) tuple
// cannot replay within the 24h nonce window. Reached only once a signature has
// cryptographically verified (see gateCheckInSignature), so with both signature
// switches off nothing here runs.
//
// Like duplicate-credential detection it must never cost a router its check-in:
// any error (the table not migrated yet, a database hiccup) is swallowed with a
// throttled warning and the check-in proceeds as if the ledger did not exist
// (fail-open). Legitimate sequential check-ins each carry a distinct fresh
// panel-minted nonce, so they never collide; only a reused tuple does.
// Contract: ai_docs/develop/features/router-auth-integrity-and-native-runtime.md.

import { routerCheckInNonces } from "@vectra/db";
import { and, eq, lt } from "drizzle-orm";

import { db } from "~/server/db";
import { MemoryWindowRateLimiter } from "~/server/vectra/public-install-rate-limit";
import {
  CHECKIN_NONCE_MAX_AGE_SECONDS,
  ROUTER_AUTH_CLOCK_SKEW_SECONDS,
  sha256Hex,
} from "~/server/vectra/router-auth-integrity";

type ReplayDatabase = Pick<typeof db, "insert" | "delete">;

const WARNING_INTERVAL_MS = 10 * 60 * 1000;
let warningThrottle = new MemoryWindowRateLimiter(1, WARNING_INTERVAL_MS);

export function resetCheckInReplayStateForTest() {
  warningThrottle = new MemoryWindowRateLimiter(1, WARNING_INTERVAL_MS);
}

function warnReplayCheckFailed(error: unknown) {
  if (warningThrottle.consume("consume").allowed) {
    console.warn(
      "[checkin-replay] nonce single-use check skipped; the check-in proceeds",
      { error: error instanceof Error ? error.name : typeof error },
    );
  }
}

/**
 * Records this router's nonce as consumed and says whether it had already been
 * consumed (a replay). The insert's uniqueness on (routerId, nonceHash) makes
 * single-use atomic and race-free, so two requests carrying the same tuple
 * cannot both be told `replay: false`. Fails open (`replay: false`) on any
 * error so a database hiccup can never reject a legitimate check-in.
 */
export async function consumeCheckInNonce(args: {
  routerId: string;
  nonce: string;
  nonceIatUnix: number;
  now: Date;
  database?: ReplayDatabase;
}): Promise<{ replay: boolean }> {
  const database = args.database ?? db;
  const nonceHash = sha256Hex(args.nonce);
  // Keep each row strictly LONGER than the verifier's acceptance window
  // (it accepts while now - iat <= MAX_AGE) plus clock skew, so the
  // opportunistic prune below can never drop a nonce the verifier would still
  // accept -- which would otherwise reopen a sub-second / skew replay gap at
  // the TTL edge.
  const expiresAt = new Date(
    (args.nonceIatUnix +
      CHECKIN_NONCE_MAX_AGE_SECONDS +
      ROUTER_AUTH_CLOCK_SKEW_SECONDS +
      1) *
      1000,
  );
  try {
    // Opportunistic prune so the ledger stays bounded by the nonce window.
    await database
      .delete(routerCheckInNonces)
      .where(
        and(
          eq(routerCheckInNonces.routerId, args.routerId),
          lt(routerCheckInNonces.expiresAt, args.now),
        ),
      );
    const inserted = await database
      .insert(routerCheckInNonces)
      .values({ routerId: args.routerId, nonceHash, expiresAt })
      .onConflictDoNothing()
      .returning({ nonceHash: routerCheckInNonces.nonceHash });
    return { replay: inserted.length === 0 };
  } catch (error) {
    warnReplayCheckFailed(error);
    return { replay: false };
  }
}
