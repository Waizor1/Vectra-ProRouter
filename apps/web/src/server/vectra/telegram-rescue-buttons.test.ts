import { describe, expect, it, vi } from "vitest";

vi.mock("~/env", () => ({
  env: {
    VECTRA_TELEGRAM_CALLBACK_SECRET:
      "telegram-callback-secret-for-tests-000000",
    VECTRA_DEFAULT_CONTROL_DOMAIN: "https://router.vectra-pro.net",
  },
}));

const { buildTelegramRescueButtons } = await import("./telegram-rescue");

const CASE_ID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";

function labels(rows: ReturnType<typeof buildTelegramRescueButtons>) {
  return rows.flat().map((button) => button.text);
}

describe("buildTelegramRescueButtons", () => {
  it("offers log collection by default", () => {
    expect(labels(buildTelegramRescueButtons(CASE_ID))).toContain(
      "Collect logs",
    );
  });

  // A vctl router never runs collect_router_logs.
  it("hides log collection when asked to", () => {
    const shown = labels(
      buildTelegramRescueButtons(CASE_ID, new Date(), { collectLogs: false }),
    );

    expect(shown).not.toContain("Collect logs");
    expect(shown).toContain("Silence 1h");
  });
});
