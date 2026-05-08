import { describe, expect, it } from "vitest";

import {
  describeRouterOnboarding,
  formatRouterImportStateLabel,
  isRouterOnboardingPending,
} from "../src/lib/router-onboarding";

describe("router onboarding copy", () => {
  it("explains the first settings read stage in plain language", () => {
    const onboarding = describeRouterOnboarding("awaiting_import");

    expect(onboarding.badge).toBe("Первое чтение");
    expect(onboarding.title).toBe("Считать текущие настройки");
    expect(onboarding.reimportLabel).toBe("Считать настройки с роутера");
    expect(onboarding.approveUnavailableLabel).toBe(
      "Сначала считать настройки",
    );
  });

  it("marks settings review as the confirmation stage", () => {
    const onboarding = describeRouterOnboarding("import_review");

    expect(onboarding.badge).toBe("Проверить базу");
    expect(onboarding.approveLabel).toBe("Принять как базу");
    expect(onboarding.cardActionLabel).toBe("Проверить базу");
  });

  it("treats out_of_sync as a review problem and not a normal happy path", () => {
    const onboarding = describeRouterOnboarding("out_of_sync");

    expect(onboarding.title).toContain("различаются");
    expect(onboarding.cardHint).toContain("правильной базой");
    expect(onboarding.tone).toBe("warning");
  });

  it("treats approved routers as ready for normal work", () => {
    const onboarding = describeRouterOnboarding("approved");

    expect(onboarding.badge).toBe("Готов");
    expect(onboarding.cardActionLabel).toBe("Открыть роутер");
    expect(onboarding.tone).toBe("good");
  });

  it("formats import labels consistently", () => {
    expect(formatRouterImportStateLabel("approved")).toBe("в работе");
    expect(formatRouterImportStateLabel("import_review")).toBe(
      "проверить базу",
    );
    expect(formatRouterImportStateLabel("awaiting_import")).toBe(
      "ждёт первое чтение",
    );
  });

  it("detects whether onboarding is still incomplete", () => {
    expect(isRouterOnboardingPending("awaiting_import")).toBe(true);
    expect(isRouterOnboardingPending("import_review")).toBe(true);
    expect(isRouterOnboardingPending("out_of_sync")).toBe(true);
    expect(isRouterOnboardingPending("approved")).toBe(false);
  });
});
