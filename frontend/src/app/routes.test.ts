import { describe, it, expect } from "vitest";
import { parseRoute, routeHref } from "./routes";

describe("routing", () => {
  it("parses each view", () => {
    expect(parseRoute("#/now")).toEqual({ name: "now" });
    // Overview was absorbed by the inbox; old links land where its job went.
    expect(parseRoute("#/overview")).toEqual({ name: "now" });
    expect(parseRoute("#/bench")).toEqual({ name: "bench" });
    expect(parseRoute("#/runs")).toEqual({ name: "runs" });
    expect(parseRoute("#/ducklings")).toEqual({ name: "ducklings" });
    expect(parseRoute("#/settings")).toEqual({ name: "settings" });
  });

  // A pop-out window opens a run route directly (08 §1.3).
  it("parses a run id so a pop-out can open it directly", () => {
    expect(parseRoute("#/runs/r-20260726-120000-fake")).toEqual({
      name: "run", id: "r-20260726-120000-fake",
    });
  });

  // The inbox is the default screen: for one person, "what needs me" is the
  // question the app opens on (docs/ux-evaluation.md P1).
  it("falls back to the inbox for anything unknown or empty", () => {
    expect(parseRoute("")).toEqual({ name: "now" });
    expect(parseRoute("#/nonsense")).toEqual({ name: "now" });
  });

  it("round-trips through href", () => {
    for (const r of [
      { name: "now" as const }, { name: "runs" as const }, { name: "bench" as const },
      { name: "run" as const, id: "r-1" }, { name: "ducklings" as const },
      { name: "settings" as const },
    ]) {
      expect(parseRoute(routeHref(r))).toEqual(r);
    }
  });
});

// Both new views must be addressable, since a pop-out opens a route directly
// and the desktop can be launched onto one.
describe("the cycle and board routes round-trip", () => {
  it("parses and renders back", () => {
    for (const name of ["cycle", "board"] as const) {
      expect(parseRoute(`#/${name}`)).toEqual({ name });
      expect(routeHref({ name })).toBe(`#/${name}`);
    }
  });
});
