import { describe, expect, it, vi } from "vitest";
import type { Run } from "../api/client";
import { makeUnknownRunAdopter } from "./unknownRuns";

const run = (id: string, project_id: string) => ({ id, project_id, stage: "triage", mode: "solo", status: "running", verdict: "", started_at: "" }) as unknown as Run;

function setup(project = "p", runs: Record<string, Run> = {}) {
  const fetchRun = vi.fn(async (id: string) => ({ run: run(id, project), events: [{ type: "turn_start", run_id: id, seq: 2 }] }));
  const adopt = vi.fn((r: Run, _events: unknown[]) => {
    runs[r.id] = r;
  });
  const onEvent = makeUnknownRunAdopter({ projectId: () => project, known: (id) => id in runs, fetchRun, adopt });
  return { onEvent, fetchRun, adopt, runs };
}

describe("adopting a run whose run_start never arrived (B-538)", () => {
  it("fetches an unknown run of the current project once, with its events", async () => {
    const { onEvent, fetchRun, adopt } = setup();
    onEvent({ type: "turn_start", run_id: "r-triage", project_id: "p", seq: 2 });
    onEvent({ type: "tool_call", run_id: "r-triage", project_id: "p", seq: 3 });
    await vi.waitFor(() => expect(adopt).toHaveBeenCalledTimes(1));
    expect(fetchRun).toHaveBeenCalledTimes(1);
    const [adopted, events] = adopt.mock.calls[0] ?? [];
    expect(adopted?.id).toBe("r-triage");
    expect(events).toEqual([{ type: "turn_start", run_id: "r-triage", seq: 2 }]);
  });

  it("leaves known runs, run_start, and events without the project's id alone", async () => {
    const { onEvent, fetchRun } = setup("p", { "r-known": run("r-known", "p") });
    onEvent({ type: "turn_start", run_id: "r-known", project_id: "p", seq: 2 });
    onEvent({ type: "run_start", run_id: "r-new", project_id: "p", seq: 1 });
    // token_delta carries a run_id but no project_id: it may be another project's.
    onEvent({ type: "token_delta", run_id: "r-elsewhere" });
    onEvent({ type: "turn_start", run_id: "r-other", project_id: "q", seq: 2 });
    await Promise.resolve();
    expect(fetchRun).not.toHaveBeenCalled();
  });

  it("does not adopt a fetched run that belongs to another project", async () => {
    const runs: Record<string, Run> = {};
    const adopt = vi.fn();
    const onEvent = makeUnknownRunAdopter({
      projectId: () => "p",
      known: (id) => id in runs,
      fetchRun: async (id) => ({ run: run(id, "q"), events: [] }),
      adopt,
    });
    onEvent({ type: "turn_start", run_id: "r-x", project_id: "p", seq: 2 });
    await new Promise((r) => setTimeout(r, 0));
    expect(adopt).not.toHaveBeenCalled();
  });
});
