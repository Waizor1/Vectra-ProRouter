import { describe, expect, it } from "vitest";

import {
  resolveImportedConfigDigest,
  resolvePersistedConfigDigest,
  shouldRequestImportOnCheckIn,
} from "./config-sync";

describe("resolvePersistedConfigDigest", () => {
  it("keeps the authoritative digest during inventory-only check-ins", () => {
    expect(
      resolvePersistedConfigDigest({
        previousDigest: "digest-authoritative",
        reportedDigest: "digest-live",
        hasPasswallImport: false,
      })
    ).toBe("digest-authoritative");
  });

  it("accepts the reported digest once a full import is attached", () => {
    expect(
      resolvePersistedConfigDigest({
        previousDigest: "digest-authoritative",
        reportedDigest: "digest-live",
        hasPasswallImport: true,
      })
    ).toBe("digest-live");
  });
});

describe("shouldRequestImportOnCheckIn", () => {
  it("requests a re-import when an approved router reports a new digest", () => {
    expect(
      shouldRequestImportOnCheckIn({
        importState: "approved",
        hasPasswallImport: false,
        reportedDigest: "digest-live",
        authoritativeDigest: "digest-authoritative",
      })
    ).toBe(true);
  });

  it("does not request import when the digest is unchanged", () => {
    expect(
      shouldRequestImportOnCheckIn({
        importState: "approved",
        hasPasswallImport: false,
        reportedDigest: "digest-authoritative",
        authoritativeDigest: "digest-authoritative",
      })
    ).toBe(false);
  });

  it("does not request import when the router already attached an import payload", () => {
    expect(
      shouldRequestImportOnCheckIn({
        importState: "approved",
        hasPasswallImport: true,
        reportedDigest: "digest-live",
        authoritativeDigest: "digest-authoritative",
      })
    ).toBe(false);
  });

  it("does not request import before the router reaches approved state", () => {
    expect(
      shouldRequestImportOnCheckIn({
        importState: "import_review",
        hasPasswallImport: false,
        reportedDigest: "digest-live",
        authoritativeDigest: "digest-authoritative",
      })
    ).toBe(false);
  });

  it("requests import when the operator explicitly moved the router into awaiting_import", () => {
    expect(
      shouldRequestImportOnCheckIn({
        importState: "awaiting_import",
        hasPasswallImport: false,
        reportedDigest: "digest-authoritative",
        authoritativeDigest: "digest-authoritative",
      })
    ).toBe(true);
  });

  it("stops requesting import in awaiting_import once the router already attached the import payload", () => {
    expect(
      shouldRequestImportOnCheckIn({
        importState: "awaiting_import",
        hasPasswallImport: true,
        reportedDigest: "digest-live",
        authoritativeDigest: "digest-authoritative",
      })
    ).toBe(false);
  });
});

describe("resolveImportedConfigDigest", () => {
  it("prefers the digest reported by the agent import payload", () => {
    expect(
      resolveImportedConfigDigest({
        importedDigest: "digest-from-agent",
        fallbackDigest: "digest-from-server",
      })
    ).toBe("digest-from-agent");
  });

  it("falls back to the local digest only when the import payload omitted it", () => {
    expect(
      resolveImportedConfigDigest({
        importedDigest: "",
        fallbackDigest: "digest-from-server",
      })
    ).toBe("digest-from-server");
  });
});
