import { describe, it, expect } from "vitest";
import { parseRoute, routeHref } from "./routes";

describe("routing", () => {
  it("parses each view", () => {
    expect(parseRoute("#/overview")).toEqual({ name: "overview" });
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

  it("falls back to overview for anything unknown or empty", () => {
    expect(parseRoute("")).toEqual({ name: "overview" });
    expect(parseRoute("#/nonsense")).toEqual({ name: "overview" });
  });

  it("round-trips through href", () => {
    for (const r of [
      { name: "overview" as const }, { name: "runs" as const },
      { name: "run" as const, id: "r-1" }, { name: "ducklings" as const },
      { name: "settings" as const },
    ]) {
      expect(parseRoute(routeHref(r))).toEqual(r);
    }
  });
});
