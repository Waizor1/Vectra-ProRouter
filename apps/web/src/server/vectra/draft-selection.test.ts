import { describe, expect, it } from "vitest";

import {
  isEditableDraftRevision,
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
});
