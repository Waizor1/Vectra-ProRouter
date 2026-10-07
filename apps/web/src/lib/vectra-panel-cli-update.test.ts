import { describe, expect, it } from "vitest";

import { controllerUpdateMutationInput } from "../../scripts/vectra-panel-cli-update.mjs";

describe("VectraPanelCli update controller", () => {
  it("is not forced by default", () => {
    expect(controllerUpdateMutationInput("r1", [])).toEqual({
      routerId: "r1",
      channel: "stable",
      force: false,
    });
  });

  it("passes --force and --channel through", () => {
    expect(
      controllerUpdateMutationInput("r1", ["--channel", "beta", "--force"]),
    ).toEqual({ routerId: "r1", channel: "beta", force: true });
  });

  it("rejects an unknown channel", () => {
    expect(() =>
      controllerUpdateMutationInput("r1", ["--channel", "nightly"]),
    ).toThrow(/stable or beta/);
  });
});
