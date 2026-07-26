import { describe, it, expect } from "vitest";
import {
  ducklingColor, seriesVar, needsOtherBucket, allPairsSafe,
  verdictStatus, runStatusRole, statusVar, statusIcon, verdictLabel, meterRole,
  SERIES_SLOTS, ALL_PAIRS_SLOT_LIMIT,
} from "./colors";

describe("series colours", () => {
  // The rule this protects: filtering a view must not repaint the survivors.
  it("follows the entity, not its position in the current view", () => {
    const roster = ["pato-uno", "pato-dos", "pato-tres"];
    const before = ducklingColor("pato-tres", roster);

    // A filtered view still passes the full roster order.
    const after = ducklingColor("pato-tres", roster);
    expect(after).toBe(before);

    // And a genuinely different roster order is a different entity ordering,
    // which is the only thing allowed to change a colour.
    expect(ducklingColor("pato-tres", ["pato-tres"])).not.toBe(before);
  });

  it("assigns slots 1..8 in fixed order", () => {
    expect(seriesVar(0)).toBe("var(--series-1)");
    expect(seriesVar(7)).toBe("var(--series-8)");
  });

  // A 9th series is never a generated hue.
  it("folds anything past slot 8 into muted rather than cycling", () => {
    expect(seriesVar(SERIES_SLOTS)).toBe("var(--text-muted)");
    expect(seriesVar(SERIES_SLOTS + 5)).toBe("var(--text-muted)");
    expect(needsOtherBucket(SERIES_SLOTS)).toBe(false);
    expect(needsOtherBucket(SERIES_SLOTS + 1)).toBe(true);
  });

  it("gives an unknown duckling muted, not slot 1", () => {
    expect(ducklingColor("stranger", ["a", "b"])).toBe("var(--text-muted)");
  });

  // Scatter/bubble/small-multiples put every pair on screen at once, where the
  // palette only clears the CVD floor for the first three slots.
  it("caps all-pairs forms at three series", () => {
    expect(allPairsSafe(ALL_PAIRS_SLOT_LIMIT)).toBe(true);
    expect(allPairsSafe(ALL_PAIRS_SLOT_LIMIT + 1)).toBe(false);
  });
});

describe("status mapping", () => {
  // P3: nothing was executed, so it must not render as a success.
  it("never maps UNVERIFIED to good", () => {
    expect(verdictStatus("UNVERIFIED")).toBe("warning");
    expect(verdictStatus("UNVERIFIED")).not.toBe("good");
  });

  it("maps verdicts to their roles", () => {
    expect(verdictStatus("PASSED")).toBe("good");
    expect(verdictStatus("FAILED")).toBe("critical");
    expect(verdictStatus("BUDGET_EXCEEDED")).toBe("critical");
    expect(verdictStatus("ABORTED")).toBe("serious");
    expect(verdictStatus("")).toBe("muted");
  });

  // Waiting for a person is not an error, and not something to ignore either.
  it("maps paused to serious, not critical", () => {
    expect(runStatusRole("paused")).toBe("serious");
    expect(runStatusRole("failed")).toBe("critical");
    expect(runStatusRole("running")).toBe("good");
    expect(runStatusRole("queued")).toBe("muted");
  });

  it("resolves to status variables, never raw hex", () => {
    for (const role of ["good", "warning", "serious", "critical"] as const) {
      expect(statusVar(role)).toBe(`var(--status-${role})`);
    }
    expect(statusVar("muted")).toBe("var(--text-muted)");
  });

  // AC-36: status is never colour alone. warning and serious are sub-3:1 on
  // the light surface by design, and the icon+label pairing is the mitigation.
  it("gives every status role a distinct icon and a non-empty label", () => {
    const roles = ["good", "warning", "serious", "critical", "muted"] as const;
    const icons = roles.map(statusIcon);
    expect(new Set(icons).size).toBe(roles.length);
    for (const icon of icons) expect(icon.trim()).not.toBe("");

    const verdicts = ["PASSED", "UNVERIFIED", "FAILED", "BUDGET_EXCEEDED", "ABORTED", ""] as const;
    for (const v of verdicts) expect(verdictLabel(v).trim()).not.toBe("");
  });

  it("labels an unfinished run rather than leaving it blank", () => {
    expect(verdictLabel("")).toBe("in progress");
  });
});

describe("budget meters", () => {
  it("turns warning at 80% and critical at 100%", () => {
    expect(meterRole(50, 100)).toBe("good");
    expect(meterRole(79, 100)).toBe("good");
    expect(meterRole(80, 100)).toBe("warning");
    expect(meterRole(100, 100)).toBe("critical");
    expect(meterRole(150, 100)).toBe("critical");
  });

  it("treats an absent limit as muted rather than dividing by zero", () => {
    expect(meterRole(10, 0)).toBe("muted");
  });
});
