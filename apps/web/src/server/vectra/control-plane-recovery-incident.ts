// Incidents opened by the on-router control-plane recovery state machine carry
// this origin. It lives in its own module because both the writer
// (router-control) and the auto-rescue monitor have to agree on it, and
// router-control already imports auto-rescue — a constant duplicated across the
// two would be free to drift with nothing to catch it.
export const controlPlaneRecoveryIncidentOrigin = "control-plane-recovery";

export function hasControlPlaneRecoveryIncidentOrigin(
  metadata: Record<string, unknown> | null | undefined,
) {
  return metadata?.origin === controlPlaneRecoveryIncidentOrigin;
}

// `type` is intentionally a plain string rather than the healthIncidents enum:
// the function narrows it itself, and callers hold the incident in several
// shapes (a DB row, a decoded job transition, a test fixture).
export function isControlPlaneRecoveryIncident(
  incident: { type: string; metadata?: unknown } | null | undefined,
) {
  if (!incident) {
    return false;
  }

  if (
    incident.type !== "server_unreachable" &&
    incident.type !== "proxy_outage"
  ) {
    return false;
  }

  const metadata = incident.metadata;
  if (typeof metadata !== "object" || metadata === null) {
    return false;
  }

  return hasControlPlaneRecoveryIncidentOrigin(
    metadata as Record<string, unknown>,
  );
}
