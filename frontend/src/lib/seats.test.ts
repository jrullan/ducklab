import { describe, it, expect } from "vitest";
import { seatsFromRoster } from "./seats";

// A roster names EVERY role, architect first — and Object.values() seeded the
// relaunch panel in that order, so the failed run's ARCHITECT sat in the
// implementer seat: the panel offered a pair the run never was, while the
// board (seeded from Settings) showed another. Position is meaning; the
// extraction must be positional.
describe("seatsFromRoster", () => {
  const roster = {
    architect: "pato-sonnet", implementer: "deepseekv4pro", judge: "dsv4flash",
    reviewer: "luna", scribe: "dsv4flash", triager: "k3",
  };

  it("seats a pair as implementer then reviewer", () => {
    expect(seatsFromRoster("pair", roster)).toEqual(["deepseekv4pro", "luna"]);
  });

  it("seats a solo with its implementer only", () => {
    expect(seatsFromRoster("solo", roster)).toEqual(["deepseekv4pro"]);
  });

  it("falls back to the deduplicated roster where seats have no named role", () => {
    expect(seatsFromRoster("tournament", roster)).toEqual([
      "pato-sonnet", "deepseekv4pro", "dsv4flash", "luna", "k3",
    ]);
  });

  it("survives an absent roster", () => {
    expect(seatsFromRoster("pair", undefined)).toEqual([]);
  });
});

import { rolesForMode } from "./seats";
describe("rolesForMode", () => {
  it("seats only the roles a mode uses, duck included", () => {
    expect(rolesForMode("pair")).toEqual(["implementer", "advisor", "reviewer"]);
    expect(rolesForMode("solo")).toEqual(["implementer", "advisor"]);
    expect(rolesForMode("triage")).toEqual(["triager"]);
  });
  it("shows everything for a mode it does not know", () => {
    expect(rolesForMode("something-new")).toBeNull();
  });
});
