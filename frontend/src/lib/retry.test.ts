import { describe, it, expect, vi } from "vitest";
import { retryRoute, reviseRun, revisesDocument, splitRevisionNote } from "./retry";
import type { Run } from "../api/client";

// B-517: one rule for which door a run's retry goes through.
describe("retryRoute", () => {
  it("routes document stages to a revision and code runs to their task", () => {
    expect(retryRoute({ stage: "intake", task_id: "" })).toBe("stage");
    expect(retryRoute({ stage: "spec", task_id: "" })).toBe("stage");
    expect(retryRoute({ stage: "plan", task_id: "" })).toBe("stage");
    expect(retryRoute({ stage: "release", task_id: "" })).toBe("release");
    expect(retryRoute({ stage: "build", task_id: "T-1" })).toBe("build");
    expect(retryRoute({ stage: "test", task_id: "T-1" })).toBe("test");
  });

  it("has no retry for a code run without a task or a stage without a relaunch", () => {
    expect(retryRoute({ stage: "build", task_id: "" })).toBeNull();
    expect(retryRoute({ stage: "test", task_id: "" })).toBeNull();
    for (const stage of ["chat", "triage", "review"]) expect(retryRoute({ stage, task_id: "T-1" })).toBeNull();
    expect(revisesDocument({ stage: "intake", task_id: "" })).toBe(true);
    expect(revisesDocument({ stage: "build", task_id: "T-1" })).toBe(false);
  });
});


describe("reviseRun", () => {
  const client = () => ({
    stageStart: vi.fn(() => Promise.resolve({ id: "r-rev" } as Run)),
    releasePlan: vi.fn(() => Promise.resolve({ id: "r-rel" } as Run)),
  });

  // B-521: the revision names the run it revises, so the engine continues
  // that run's references, mode, rounds and intent instead of guessing.
  it("sends a stage's note as a revision of that run", async () => {
    const c = client();
    await reviseRun(c, { id: "r-tpfn", project_id: "p", stage: "intake", task_id: "" }, "fix REQ-008");
    expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { revise: "fix REQ-008", revisesRun: "r-tpfn" });
    expect(c.releasePlan).not.toHaveBeenCalled();
  });

  it("sends the person's note and the carried-over findings apart", async () => {
    const c = client();
    await reviseRun(c, { id: "r-tpfn", project_id: "p", stage: "intake", task_id: "" }, "Keep it short\n- [major] REQ-008 invents", {
      note: "Keep it short", findings: "- [major] REQ-008 invents",
    });
    expect(c.stageStart).toHaveBeenCalledWith("p", "intake", {
      revise: "Keep it short", reviseFindings: "- [major] REQ-008 invents", revisesRun: "r-tpfn",
    });
  });

  it("sends a release's note through the release door, whole", async () => {
    const c = client();
    await reviseRun(c, { id: "r-r", project_id: "p", stage: "release", task_id: "" }, "drop T-3\n- [minor] x", { note: "drop T-3", findings: "- [minor] x" });
    expect(c.releasePlan).toHaveBeenCalledWith("p", "", "drop T-3\n- [minor] x");
  });

  it("replays a plan amendment's request", async () => {
    const c = client();
    await reviseRun(c, { id: "r-amend", project_id: "p", stage: "plan", task_id: "", stage_request: { extend: " add CSV ", mode: "solo", rounds: 1 } }, "one task");
    expect(c.stageStart).toHaveBeenCalledWith("p", "plan", { revise: "one task", revisesRun: "r-amend", extend: "add CSV", mode: "solo", rounds: 1 });
  });

  it("refuses to revise a code run", async () => {
    const c = client();
    await expect(reviseRun(c, { id: "r-b", project_id: "p", stage: "build", task_id: "T-1" }, "x")).rejects.toThrow(/not a document/);
    expect(c.stageStart).not.toHaveBeenCalled();
  });
});

// B-521: a line still as Ducklab seeded it is carried over; a line the person
// wrote or edited is theirs.
describe("splitRevisionNote", () => {
  const seed = "- [major] REQ-008 invents behavior. Fix: remove it.\n- [minor] REQ-009 adds a confirmation.";

  it("sends seeded findings untouched as findings", () => {
    expect(splitRevisionNote(seed, seed)).toEqual({ note: "", findings: seed });
  });

  it("keeps the person's words and edits as their note", () => {
    const text = "Use the guidebook.\n\n- [major] REQ-008 invents behavior. Fix: remove it.\n- [minor] REQ-009 adds a confirmation, but keep it.";
    expect(splitRevisionNote(text, seed)).toEqual({
      note: "Use the guidebook.\n\n- [minor] REQ-009 adds a confirmation, but keep it.",
      findings: "- [major] REQ-008 invents behavior. Fix: remove it.",
    });
  });

  it("is all note when nothing was seeded", () => {
    expect(splitRevisionNote("Restore the Ans sentence.", "")).toEqual({ note: "Restore the Ans sentence.", findings: "" });
  });
});
