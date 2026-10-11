import { describe, expect, it } from "vitest";
import { stageRefs, suggestScope } from "./refroles";

describe("reference roles (B-520)", () => {
  it("sends background as plain paths and scope with its role; images never carry scope", () => {
    expect(stageRefs(["/a.md", "/b.md", "/c.png"], new Set(["/b.md", "/c.png"]))).toEqual([
      "/a.md",
      { path: "/b.md", role: "scope" },
      "/c.png",
    ]);
  });

  it("hints only when the brief targets a reference and nothing is marked", () => {
    const docs = ["/m/guide.md"];
    expect(suggestScope("Use this guide to fill in the gap", docs, new Set())).toBe(true);
    expect(suggestScope("Implement what is described in the manual", docs, new Set())).toBe(true);
    expect(suggestScope("Close the gap to the intended functionality", docs, new Set())).toBe(true);
    expect(suggestScope("Use this guide to fill in the gap", docs, new Set(["/m/guide.md"]))).toBe(false);
    expect(suggestScope("Use this guide to fill in the gap", [], new Set())).toBe(false);
    expect(suggestScope("Add a dark mode toggle.", docs, new Set())).toBe(false);
  });
});
