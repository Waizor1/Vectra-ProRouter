// Panel-side rollout guard for controller self-updates: a router whose
// control plane is already shaky is the one an update is most likely to lose
// (leonid-avito, 2026-10-06). Such a router is refused unless the operator
// passes `force: true`.

export const controllerUpdateRolloutCheckInMaxAgeMs = 10 * 60 * 1000;
export const controllerUpdateRolloutIncidentLookbackMs = 24 * 60 * 60 * 1000;

export type ControllerUpdateRolloutIncident = {
  type: string;
  state: string;
  openedAt: Date | string | null;
};

export type ControllerUpdateRolloutHealth =
  | { ok: true }
  | { ok: false; reasons: string[] };

function toDate(value: Date | string | null | undefined) {
  if (!value) {
    return null;
  }
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

export function evaluateControllerUpdateRolloutHealth(args: {
  router: {
    status?: string | null;
    lastCheckInAt?: Date | string | null;
  };
  incidents: ReadonlyArray<ControllerUpdateRolloutIncident>;
  now?: Date;
}): ControllerUpdateRolloutHealth {
  const now = args.now ?? new Date();
  const reasons: string[] = [];

  if (args.router.status === "direct" || args.router.status === "rescue") {
    reasons.push(`роутер в режиме ${args.router.status}`);
  }

  const lastCheckIn = toDate(args.router.lastCheckInAt);
  if (!lastCheckIn) {
    reasons.push("роутер ещё ни разу не выходил на связь");
  } else {
    const ageMs = now.getTime() - lastCheckIn.getTime();
    if (ageMs > controllerUpdateRolloutCheckInMaxAgeMs) {
      reasons.push(
        `последний check-in ${Math.round(ageMs / 60000)} мин назад (> ${
          controllerUpdateRolloutCheckInMaxAgeMs / 60000
        } мин)`,
      );
    }
  }

  const unreachable = args.incidents.filter(
    (incident) => incident.type === "server_unreachable",
  );
  if (unreachable.some((incident) => incident.state === "open")) {
    reasons.push("открыт инцидент server_unreachable");
  } else {
    const since = now.getTime() - controllerUpdateRolloutIncidentLookbackMs;
    const recent = unreachable.find((incident) => {
      const openedAt = toDate(incident.openedAt);
      return openedAt !== null && openedAt.getTime() >= since;
    });
    if (recent) {
      reasons.push("инцидент server_unreachable за последние 24 ч");
    }
  }

  return reasons.length === 0 ? { ok: true } : { ok: false, reasons };
}

const refusalPrefix =
  "Обновление контроллера отклонено: связь роутера с панелью ненадёжна (";
const refusalSuffix =
  "). Если уверены, подтвердите «Всё равно обновить» на вкладке «Обновления» роутера или запустите VectraPanelCli.sh update controller <роутер> --force.";

export class ControllerUpdateRolloutRefusal extends Error {
  readonly reasons: string[];

  constructor(reasons: string[]) {
    super(`${refusalPrefix}${reasons.join("; ")}${refusalSuffix}`);
    this.name = "ControllerUpdateRolloutRefusal";
    this.reasons = reasons;
  }
}

// The reasons of a rollout refusal from an error that crossed tRPC (only its
// message survives), or null when the error is something else.
export function readControllerUpdateRolloutRefusal(error: unknown) {
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : null;
  if (!message?.startsWith(refusalPrefix) || !message.endsWith(refusalSuffix)) {
    return null;
  }
  return message.slice(refusalPrefix.length, message.length - refusalSuffix.length);
}

// The router guard's refusal (exit 75) of a version this router rolled back
// from in the last 24 h, as the job's stderr carries it.
export const controllerSelfUpdateRecentRollbackMarker =
  "controller self-update refused (75):";

// The newest controller update in the router's task log, when it was refused
// because the router rolled back from that version less than 24 h ago: the
// one case where a healthy router needs an operator force.
export function findRecentControllerRollbackRefusal(
  taskLog: ReadonlyArray<{
    kind: string;
    stderr: string | null;
    error?: string | null;
    artifactVersion: string | null;
  }>,
) {
  const latest = taskLog.find(
    (item) =>
      item.kind === "controller-self-update" ||
      item.kind === "controller-update",
  );
  if (!latest) {
    return null;
  }
  const text = [latest.stderr, latest.error].filter(Boolean).join("\n");
  const index = text.indexOf(controllerSelfUpdateRecentRollbackMarker);
  if (index < 0) {
    return null;
  }
  return {
    artifactVersion: latest.artifactVersion,
    detail: text.slice(index).split("\n")[0] ?? "",
  };
}
