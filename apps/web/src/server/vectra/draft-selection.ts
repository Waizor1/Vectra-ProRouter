type RevisionLike = {
  id: string;
  origin: string | null | undefined;
  status: string | null | undefined;
  configDigest: string | null | undefined;
};

const editableDraftStatuses = new Set(["draft", "queued", "failed"]);

function normalizeDigest(value: string | null | undefined) {
  const trimmed = value?.trim();
  return trimmed && trimmed.length > 0 ? trimmed : null;
}

export function isLiveImportRevision(revision: RevisionLike | null | undefined) {
  return (
    revision?.origin === "router_import" || revision?.origin === "operator_reimport"
  );
}

export function isOperatorDraftRevision(revision: RevisionLike | null | undefined) {
  return revision?.origin === "operator_draft";
}

export function isEditableDraftRevision(revision: RevisionLike | null | undefined) {
  return (
    isOperatorDraftRevision(revision) &&
    editableDraftStatuses.has((revision?.status ?? "").trim().toLowerCase())
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

export function pickLatestOperatorDraft<T extends RevisionLike>(revisions: T[]) {
  return revisions.find((revision) => isOperatorDraftRevision(revision)) ?? null;
}

export function pickLatestEditableDraft<T extends RevisionLike>(revisions: T[]) {
  return revisions.find((revision) => isEditableDraftRevision(revision)) ?? null;
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
    args.currentLiveRevision ??
    args.importedRevision ??
    args.activeRevision ??
    args.revisions[0] ??
    null
  );
}
