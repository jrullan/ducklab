import { describe, it, expect } from "vitest";
import { seatsFromRoster, seatLabel, fixedSeats, rolesForMode } from "./seats";

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

  it("seats pair and solo by role, in seat order, when the advisor is named", () => {
    const withAdvisor = { ...roster, advisor: "k3" };
    expect(seatsFromRoster("pair", withAdvisor)).toEqual(["deepseekv4pro", "k3", "luna"]);
    expect(seatsFromRoster("solo", withAdvisor)).toEqual(["deepseekv4pro", "k3"]);
  });

  // B-394: a run record names one duckling per role; the contestants and
  // workers beyond the implementer are not in it. Projecting every role made
  // the architect, judge, scribe and triager participants of the relaunch.
  it("seeds a tournament or split with the implementer only, never every role", () => {
    expect(seatsFromRoster("tournament", roster)).toEqual(["deepseekv4pro"]);
    expect(seatsFromRoster("split", roster)).toEqual(["deepseekv4pro"]);
    expect(seatsFromRoster("tournament", { advisor: "k3" })).toEqual([]);
  });

  it("falls back to the deduplicated roster for a mode it does not know", () => {
    expect(seatsFromRoster("something-new", roster)).toEqual([
      "pato-sonnet", "deepseekv4pro", "dsv4flash", "luna", "k3",
    ]);
  });

  it("survives an absent roster", () => {
    expect(seatsFromRoster("pair", undefined)).toEqual([]);
  });

  it("keeps position as role for the fixed modes", () => {
    expect(fixedSeats("pair")).toBe(3);
    expect([0, 1, 2].map((i) => seatLabel("pair", i))).toEqual(["implementer", "advisor", "reviewer"]);
    expect([0, 1].map((i) => seatLabel("solo", i))).toEqual(["implementer", "advisor"]);
  });
});

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
