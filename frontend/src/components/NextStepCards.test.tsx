import { describe, expect, it } from "vitest";
import { nextStepHref } from "./NextStepCards";

describe("nextStepHref", () => {
  it("takes the visual-check setup step to project configuration", () => {
    expect(nextStepHref({
      id: "visual-check",
      action: "Set up a visual check",
      reason: "a reference and run URL exist",
      kind: "project",
      ref: "visual-check",
    })).toBe("#/projects");
  });
});
