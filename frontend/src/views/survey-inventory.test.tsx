import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EngineClient, type Run } from "../api/client";
import { useRuns } from "../store/runs";
import { Cycle } from "./Cycle";
import { RunView } from "./RunView";

const inventory = [
  { name: "Billing API", kind: "service", "evidence-path": "internal/billing/api.go" },
  { name: "Admin UI", kind: "client", "evidence-path": "web/admin.tsx" },
];

const surveyRun: Run = {
  id: "r-survey", project_id: "p", stage: "intake", mode: "council", task_id: "",
  status: "paused", verdict: "", started_at: "2026-08-23T00:00:00Z",
  pending_kind: "gate", next: ["accept", "request_changes", "reject"],
  pending_data: { unaccounted: inventory },
};

function runClient(run: Run, events: unknown[] = []) {
  return {
    run: vi.fn(() => Promise.resolve({ run, events })),
    artifact: vi.fn(() => Promise.resolve({ kind: "requirements", markdown: "", sections: [] })),
    traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
    projects: vi.fn(() => Promise.resolve([])),
    roster: vi.fn(() => Promise.resolve({ entries: [] })),
    runBrief: vi.fn(() => Promise.resolve("")),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([])),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    tasks: vi.fn(() => Promise.resolve([])),
  } as unknown as EngineClient;
}

describe("adoption survey coverage on decision surfaces", () => {
  beforeEach(() => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("names the recorded unaccounted surfaces on both proposal cards", async () => {
    const cycleClient = runClient(surveyRun);
    (cycleClient as unknown as { artifact: ReturnType<typeof vi.fn> }).artifact.mockResolvedValue({
      kind: "requirements", markdown: "", sections: [], proposal: { run_id: "r-survey", diff: "" },
    });
    render(<Cycle client={cycleClient} projectId="p" />);
    expect((await screen.findByTestId("cycle-proposal")).textContent).toContain(
      "2 surface areas unaccounted: Billing API, Admin UI",
    );

    const runClientForView = runClient(surveyRun);
    render(<RunView runId="r-survey" client={runClientForView} />);
    await waitFor(() => expect(screen.getByTestId("run-view").textContent).toContain(
      "2 surface areas unaccounted: Billing API, Admin UI",
    ));
  });

  it("does not add an unaccounted line when the recorded list is empty", async () => {
    const emptySurvey = { ...surveyRun, pending_data: { unaccounted: [] } };
    const cycleClient = runClient(emptySurvey);
    (cycleClient as unknown as { artifact: ReturnType<typeof vi.fn> }).artifact.mockResolvedValue({
      kind: "requirements", markdown: "", sections: [], proposal: { run_id: "r-survey", diff: "" },
    });
    render(<Cycle client={cycleClient} projectId="p" />);
    await screen.findByTestId("cycle-proposal");
    expect(screen.queryByText(/surface areas unaccounted/)).toBeNull();

    render(<RunView runId="r-survey" client={runClient(emptySurvey)} />);
    await screen.findByTestId("run-view");
    expect(screen.queryByText(/surface areas unaccounted/)).toBeNull();
  });

  it("keeps the recorded inventory folded until opened, then shows every kind and evidence path", async () => {
    const client = runClient(surveyRun, [{
      type: "survey_inventory",
      data: { items: inventory },
    }]);
    render(<RunView runId="r-survey" client={client} />);

    const inventoryBlock = await screen.findByTestId("survey-inventory");
    expect(inventoryBlock.tagName).toBe("DETAILS");
    expect((inventoryBlock as HTMLDetailsElement).open).toBe(false);
    fireEvent.click(inventoryBlock.querySelector("summary")!);
    expect(inventoryBlock.textContent).toContain("Billing API");
    expect(inventoryBlock.textContent).toContain("service");
    expect(inventoryBlock.textContent).toContain("internal/billing/api.go");
    expect(inventoryBlock.textContent).toContain("Admin UI");
    expect(inventoryBlock.textContent).toContain("client");
    expect(inventoryBlock.textContent).toContain("web/admin.tsx");
  });
});
