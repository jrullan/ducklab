import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";

function makeClient(trace: Record<string, unknown>, sections = [{ id: "REQ-1", title: "Requirement", body: "People can see why a run exists. More detail follows." }]) {
  return {
    run: vi.fn().mockResolvedValue({ run: {
      id: "run-1", project_id: "project-1", stage: "build", mode: "solo", task_id: "task-1",
      status: "done", verdict: "PASSED", started_at: "2026-01-01T00:00:00Z",
    }, events: [] }),
    ducklings: vi.fn().mockResolvedValue([]),
    modeDefaults: vi.fn().mockResolvedValue({ ducklings: {} }),
    report: vi.fn().mockResolvedValue({ rows: [] }),
    tasks: vi.fn().mockResolvedValue([]),
    traceShow: vi.fn((_: string, id: string) => Promise.resolve(trace[id] ?? { id, up: [] })),
    artifact: vi.fn().mockResolvedValue({ sections }),
    runDiff: vi.fn().mockResolvedValue({ diff: "", tests: "" }),
    runVerify: vi.fn().mockResolvedValue(""),
    runCandidates: vi.fn().mockResolvedValue([]),
    runLLM: vi.fn().mockResolvedValue([]),
  };
}

function seedRun(id = "run-1") {
  useRuns.setState({
    runs: { [id]: { id, project_id: "project-1", stage: "build", mode: "solo", task_id: "task-1", status: "done", verdict: "PASSED", started_at: "2026-01-01T00:00:00Z" } },
    events: { [id]: [] }, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open",
  });
}

describe("RunView origin panel", () => {
  it("quotes the requirement and links every document in the breadcrumb", async () => {
    seedRun();
    const client = makeClient({
      "task-1": { id: "task-1", kind: "task", title: "Implement origin panel", up: ["spec-1"] },
      "spec-1": { id: "spec-1", kind: "spec", title: "Origin visibility", up: ["plan-1"] },
      "plan-1": { id: "plan-1", kind: "plan", title: "Build origin panel", up: ["REQ-1"] },
      "REQ-1": { id: "REQ-1", kind: "requirement", title: "Origin is visible", up: [] },
    });

    render(<RunView runId="run-1" client={client as never} />);

    expect(await screen.findByText("“People can see why a run exists.”")).toBeInTheDocument();
    const breadcrumb = await screen.findByTestId("run-origin-breadcrumb");
    expect(breadcrumb.querySelector('a[href="#/cycle/intake?section=REQ-1"]')).toHaveTextContent("Origin is visible");
    expect(breadcrumb.querySelector('a[href="#/cycle/plan?section=plan-1"]')).toHaveTextContent("Build origin panel");
    expect(breadcrumb.querySelector('a[href="#/cycle/spec?section=spec-1"]')).toHaveTextContent("Origin visibility");
    expect(breadcrumb.querySelector('a[href="#/cycle/plan?section=task-1"]')).toHaveTextContent("Implement origin panel");
  });

  it("says plainly when the run has no document spine", async () => {
    seedRun();
    const client = makeClient({ "task-1": { id: "task-1", kind: "task", title: "Unlinked task", up: [] } });

    render(<RunView runId="run-1" client={client as never} />);

    await waitFor(() => expect(screen.getByTestId("run-origin-none")).toHaveTextContent("this run has no document behind it — worth knowing"));
    expect(screen.queryByTestId("run-origin-requirement")).not.toBeInTheDocument();
  });
});
