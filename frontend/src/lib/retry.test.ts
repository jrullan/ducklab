import { describe, it, expect, vi } from "vitest";
import { retryRoute, reviseRun, revisesDocument } from "./retry";
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

  it("sends a stage's note as a revision of that stage", async () => {
    const c = client();
    await reviseRun(c, { project_id: "p", stage: "intake", task_id: "" }, "fix REQ-008");
    expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { revise: "fix REQ-008" });
    expect(c.releasePlan).not.toHaveBeenCalled();
  });

  it("sends a release's note through the release door", async () => {
    const c = client();
    await reviseRun(c, { project_id: "p", stage: "release", task_id: "" }, "drop T-3");
    expect(c.releasePlan).toHaveBeenCalledWith("p", "", "drop T-3");
  });

  it("replays a plan amendment's request", async () => {
    const c = client();
    await reviseRun(c, { project_id: "p", stage: "plan", task_id: "", stage_request: { extend: " add CSV ", mode: "solo", rounds: 1 } }, "one task");
    expect(c.stageStart).toHaveBeenCalledWith("p", "plan", { revise: "one task", extend: "add CSV", mode: "solo", rounds: 1 });
  });

  it("refuses to revise a code run", async () => {
    const c = client();
    await expect(reviseRun(c, { project_id: "p", stage: "build", task_id: "T-1" }, "x")).rejects.toThrow(/not a document/);
    expect(c.stageStart).not.toHaveBeenCalled();
  });
});
