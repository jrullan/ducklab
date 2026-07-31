import { describe, it, expect } from "vitest";
import { interruptions } from "./attention";
import type { Run } from "../api/client";

const run = (over: Partial<Run>): Run =>
  ({
    id: "r-1", project_id: "p", stage: "build", mode: "solo", task_id: "T-025",
    status: "running", verdict: "", started_at: "2026-07-31T10:00:00Z",
    ...over,
  }) as Run;

// The client rendered "waiting for a human" as three passive indicators on
// three screens, so the one person the product serves polled views to find out
// whether anything had happened.
describe("what merits interrupting the person", () => {
  it("a run newly paused at its gate", () => {
    const before = { "r-1": run({ status: "running" }) };
    const after = { "r-1": run({ status: "paused", pending_kind: "gate", verdict: "PASSED" }) };
    const got = interruptions(before, after);
    expect(got).toHaveLength(1);
    expect(got[0]!.title).toContain("T-025");
    expect(got[0]!.body).toContain("PASSED");
  });

  it("a run newly failed, with the reason's first line", () => {
    const before = { "r-1": run({ status: "running" }) };
    const after = {
      "r-1": run({ status: "failed", verdict: "FAILED", failure: "budget exceeded: 436339 >= 400000\nlong detail" }),
    };
    const got = interruptions(before, after);
    expect(got).toHaveLength(1);
    expect(got[0]!.body).toBe("budget exceeded: 436339 >= 400000");
  });

  it("a question names itself, not the gate", () => {
    const before = { "r-1": run({}) };
    const after = { "r-1": run({ status: "paused", pending_kind: "question" }) };
    expect(interruptions(before, after)[0]!.body).toContain("question");
  });

  // Silence is information; spending it on non-decisions teaches the person to
  // ignore the sound.
  it("says nothing when a run completes without needing anyone", () => {
    const before = { "r-1": run({ status: "running" }) };
    const after = { "r-1": run({ status: "done", verdict: "PASSED", accepted: true }) };
    expect(interruptions(before, after)).toHaveLength(0);
  });

  // The app just opened; the person is already looking at it.
  it("says nothing on the first snapshot", () => {
    const after = { "r-1": run({ status: "paused", pending_kind: "gate" }) };
    expect(interruptions(null, after)).toHaveLength(0);
  });

  // An interruption repeated is an alarm.
  it("does not repeat itself for a state it already announced", () => {
    const waiting = { "r-1": run({ status: "paused", pending_kind: "gate" }) };
    expect(interruptions(waiting, { ...waiting })).toHaveLength(0);
    const failed = { "r-1": run({ status: "failed" }) };
    expect(interruptions(failed, { ...failed })).toHaveLength(0);
  });

  it("names a run with no task by its stage", () => {
    const before = { "r-9": run({ id: "r-9", task_id: "", stage: "triage" }) };
    const after = {
      "r-9": run({ id: "r-9", task_id: "", stage: "triage", status: "paused", pending_kind: "gate" }),
    };
    expect(interruptions(before, after)[0]!.title).toContain("triage");
  });
});
