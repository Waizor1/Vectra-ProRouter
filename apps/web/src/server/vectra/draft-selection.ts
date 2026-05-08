type RevisionLike = {
  id: string;
  origin: string | null | undefined;
  status: string | null | undefined;
  configDigest: string | null | undefined;
  revisionNumber?: number | null | undefined;
  createdAt?: Date | string | null | undefined;
};

const editableDraftStatuses = new Set(["draft", "queued", "failed"]);

function normalizeDigest(value: string | null | undefined) {
  const trimmed = value?.trim();
  return trimmed && trimmed.length > 0 ? trimmed : null;
}

export function isLiveImportRevision(
  revision: RevisionLike | null | undefined,
) {
  return (
    revision?.origin === "router_import" ||
    revision?.origin === "operator_reimport"
  );
}

export function isOperatorDraftRevision(
  revision: RevisionLike | null | undefined,
) {
  return revision?.origin === "operator_draft";
}

export function isEditableDraftRevision(
  revision: RevisionLike | null | undefined,
) {
  return (
    isOperatorDraftRevision(revision) &&
    editableDraftStatuses.has((revision?.status ?? "").trim().toLowerCase())
  );
}

function normalizeRevisionNumber(revision: RevisionLike | null | undefined) {
  return typeof revision?.revisionNumber === "number" &&
    Number.isFinite(revision.revisionNumber)
    ? revision.revisionNumber
    : null;
}

function normalizeRevisionTime(revision: RevisionLike | null | undefined) {
  if (!revision?.createdAt) {
    return null;
  }

  const createdAt =
    revision.createdAt instanceof Date
      ? revision.createdAt
      : new Date(revision.createdAt);
  const timestamp = createdAt.getTime();
  return Number.isFinite(timestamp) ? timestamp : null;
}

function revisionSupersedesDraft(
  supersedingRevision: RevisionLike | null | undefined,
  draftRevision: RevisionLike | null | undefined,
) {
  if (!supersedingRevision || !draftRevision) {
    return false;
  }

  if (supersedingRevision.id === draftRevision.id) {
    return false;
  }

  const supersedingNumber = normalizeRevisionNumber(supersedingRevision);
  const draftNumber = normalizeRevisionNumber(draftRevision);
  if (supersedingNumber !== null && draftNumber !== null) {
    return supersedingNumber >= draftNumber;
  }

  const supersedingTime = normalizeRevisionTime(supersedingRevision);
  const draftTime = normalizeRevisionTime(draftRevision);
  if (supersedingTime !== null && draftTime !== null) {
    return supersedingTime >= draftTime;
  }

  return false;
}

function pickNewestRevision<T extends RevisionLike>(
  revisions: Array<T | null | undefined>,
) {
  return revisions.filter((revision): revision is T => Boolean(revision)).sort(
    (left, right) => {
      const rightNumber = normalizeRevisionNumber(right);
      const leftNumber = normalizeRevisionNumber(left);
      if (rightNumber !== null && leftNumber !== null) {
        return rightNumber - leftNumber;
      }

      const rightTime = normalizeRevisionTime(right);
      const leftTime = normalizeRevisionTime(left);
      if (rightTime !== null && leftTime !== null) {
        return rightTime - leftTime;
      }

      return 0;
    },
  )[0] ?? null;
}

export function isSupersededEditableDraft(args: {
  draftRevision: RevisionLike | null | undefined;
  activeRevision?: RevisionLike | null | undefined;
  currentLiveRevision?: RevisionLike | null | undefined;
}) {
  if (!isEditableDraftRevision(args.draftRevision)) {
    return false;
  }

  return [args.currentLiveRevision, args.activeRevision].some((revision) =>
    revisionSupersedesDraft(revision, args.draftRevision),
  );
}

export function pickImportedRevision<T extends RevisionLike>(args: {
  pendingImportRevisionId: string | null | undefined;
  revisions: T[];
}) {
  return (
    (args.pendingImportRevisionId
      ? args.revisions.find(
          (revision) => revision.id === args.pendingImportRevisionId,
        )
      : undefined) ??
    args.revisions.find((revision) => isLiveImportRevision(revision)) ??
    null
  );
}

export function pickActiveRevision<T extends RevisionLike>(args: {
  activeRevisionId: string | null | undefined;
  revisions: T[];
}) {
  return (
    (args.activeRevisionId
      ? args.revisions.find((revision) => revision.id === args.activeRevisionId)
      : undefined) ?? null
  );
}

export function pickLatestOperatorDraft<T extends RevisionLike>(
  revisions: T[],
) {
  return (
    revisions.find(
      (revision) =>
        isOperatorDraftRevision(revision) &&
        (revision.status ?? "").trim().toLowerCase() !== "discarded",
    ) ?? null
  );
}

export function pickLatestEditableDraft<T extends RevisionLike>(
  input:
    | T[]
    | {
        revisions: T[];
        activeRevision?: T | null;
        currentLiveRevision?: T | null;
      },
) {
  const revisions = Array.isArray(input) ? input : input.revisions;
  const activeRevision = Array.isArray(input)
    ? null
    : (input.activeRevision ?? null);
  const currentLiveRevision = Array.isArray(input)
    ? null
    : (input.currentLiveRevision ?? null);

  return (
    revisions.find(
      (revision) =>
        isEditableDraftRevision(revision) &&
        !isSupersededEditableDraft({
          draftRevision: revision,
          activeRevision,
          currentLiveRevision,
        }),
    ) ?? null
  );
}

export function pickCurrentLiveRevision<T extends RevisionLike>(args: {
  snapshotDigest: string | null | undefined;
  revisions: T[];
}) {
  const snapshotDigest = normalizeDigest(args.snapshotDigest);
  if (!snapshotDigest) {
    return null;
  }

  return (
    args.revisions.find(
      (revision) =>
        isLiveImportRevision(revision) &&
        normalizeDigest(revision.configDigest) === snapshotDigest,
    ) ?? null
  );
}

export function pickWorkspaceRevision<T extends RevisionLike>(args: {
  latestEditableDraft: T | null;
  currentLiveRevision: T | null;
  importedRevision: T | null;
  activeRevision: T | null;
  revisions: T[];
}) {
  return (
    args.latestEditableDraft ??
    pickNewestRevision([
      args.currentLiveRevision,
      args.importedRevision,
      args.activeRevision,
    ]) ??
    args.revisions[0] ??
    null
  );
}
