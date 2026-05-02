import { describe, expect, it } from "vitest";

import {
  isEditableDraftRevision,
  isSupersededEditableDraft,
  pickActiveRevision,
  pickCurrentLiveRevision,
  pickImportedRevision,
  pickLatestEditableDraft,
  pickLatestOperatorDraft,
  pickWorkspaceRevision,
} from "./draft-selection";

const baseRevisions = [
  {
    id: "draft-applied",
    origin: "operator_draft",
    status: "applied",
    configDigest: "digest-draft",
  },
  {
    id: "import-live",
    origin: "router_import",
    status: "approved",
    configDigest: "digest-live",
  },
  {
    id: "active-rev",
    origin: "router_import",
    status: "approved",
    configDigest: "digest-live",
  },
];

describe("draft selection", () => {
  it("treats only draft, queued and failed operator drafts as editable", () => {
    expect(
      isEditableDraftRevision({
        id: "editable-draft",
        origin: "operator_draft",
        status: "draft",
        configDigest: null,
      }),
    ).toBe(true);
    expect(
      isEditableDraftRevision({
        id: "queued-draft",
        origin: "operator_draft",
        status: "queued",
        configDigest: null,
      }),
    ).toBe(true);
    expect(
      isEditableDraftRevision({
        id: "applied-draft",
        origin: "operator_draft",
        status: "applied",
        configDigest: null,
      }),
    ).toBe(false);
  });

  it("keeps the latest operator draft available for history but not as editable workspace base", () => {
    expect(pickLatestOperatorDraft(baseRevisions)?.id).toBe("draft-applied");
    expect(pickLatestEditableDraft(baseRevisions)).toBeNull();
  });

  it("does not treat discarded operator drafts as latest panel context", () => {
    const revisions = [
      {
        id: "discarded-draft",
        origin: "operator_draft",
        status: "discarded",
        configDigest: "digest-discarded",
        revisionNumber: 32,
      },
      {
        id: "applied-draft",
        origin: "operator_draft",
        status: "applied",
        configDigest: "digest-applied",
        revisionNumber: 31,
      },
    ];

    expect(pickLatestOperatorDraft(revisions)?.id).toBe("applied-draft");
  });

  it("prefers the matching live import over an older applied operator draft", () => {
    const importedRevision = pickImportedRevision({
      pendingImportRevisionId: null,
      revisions: baseRevisions,
    });
    const activeRevision = pickActiveRevision({
      activeRevisionId: "active-rev",
      revisions: baseRevisions,
    });
    const currentLiveRevision = pickCurrentLiveRevision({
      snapshotDigest: "digest-live",
      revisions: baseRevisions,
    });
    const latestEditableDraft = pickLatestEditableDraft(baseRevisions);

    const workspaceRevision = pickWorkspaceRevision({
      latestEditableDraft,
      currentLiveRevision,
      importedRevision,
      activeRevision,
      revisions: baseRevisions,
    });

    expect(currentLiveRevision?.id).toBe("import-live");
    expect(workspaceRevision?.id).toBe("import-live");
  });

  it("still prefers a real editable draft when it exists", () => {
    const revisions = [
      {
        id: "draft-queued",
        origin: "operator_draft",
        status: "queued",
        configDigest: "digest-draft",
      },
      ...baseRevisions,
    ];

    const workspaceRevision = pickWorkspaceRevision({
      latestEditableDraft: pickLatestEditableDraft(revisions),
      currentLiveRevision: pickCurrentLiveRevision({
        snapshotDigest: "digest-live",
        revisions,
      }),
      importedRevision: pickImportedRevision({
        pendingImportRevisionId: null,
        revisions,
      }),
      activeRevision: pickActiveRevision({
        activeRevisionId: "active-rev",
        revisions,
      }),
      revisions,
    });

    expect(workspaceRevision?.id).toBe("draft-queued");
  });

  it("ignores editable operator drafts that are older than the confirmed live baseline", () => {
    const revisions = [
      {
        id: "active-live",
        origin: "router_import",
        status: "approved",
        configDigest: "digest-live",
        revisionNumber: 19,
        createdAt: "2026-04-28T21:00:00.000Z",
      },
      {
        id: "stale-draft",
        origin: "operator_draft",
        status: "draft",
        configDigest: "digest-stale",
        revisionNumber: 13,
        createdAt: "2026-04-23T12:00:00.000Z",
      },
    ];

    const activeRevision = pickActiveRevision({
      activeRevisionId: "active-live",
      revisions,
    });

    expect(
      isSupersededEditableDraft({
        draftRevision: revisions[1],
        activeRevision,
      }),
    ).toBe(true);
    expect(
      pickLatestEditableDraft({
        revisions,
        activeRevision,
      }),
    ).toBeNull();
  });

  it("keeps a newer editable draft visible until the operator applies or discards it", () => {
    const revisions = [
      {
        id: "new-draft",
        origin: "operator_draft",
        status: "draft",
        configDigest: "digest-new-draft",
        revisionNumber: 31,
        createdAt: "2026-04-29T23:08:00.000Z",
      },
      {
        id: "active-live",
        origin: "router_import",
        status: "approved",
        configDigest: "digest-live",
        revisionNumber: 30,
        createdAt: "2026-04-28T21:00:00.000Z",
      },
    ];

    const activeRevision = pickActiveRevision({
      activeRevisionId: "active-live",
      revisions,
    });

    expect(
      pickLatestEditableDraft({
        revisions,
        activeRevision,
      })?.id,
    ).toBe("new-draft");
  });
});
