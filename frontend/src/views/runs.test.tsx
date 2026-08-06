import { describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Runs } from "./Runs";
import type { Run } from "../api/client";

const mk = (over: Partial<Run>): Run => ({
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-001",
  status: "done", verdict: "PASSED", started_at: "2026-07-20T00:00:00Z", ...over,
});

const RUNS: Run[] = [
  mk({ id: "r-old", started_at: "2026-07-01T00:00:00Z" }),
  mk({ id: "r-stage", stage: "intake", task_id: "", mode: "council", status: "paused", verdict: "", started_at: "2026-07-26T00:00:00Z" }),
  mk({ id: "r-new", status: "failed", started_at: "2026-07-27T00:00:00Z" }),
];

describe("Runs", () => {
  it("is its own view, newest first", () => {
    render(<Runs runs={RUNS} />);
    const order = screen.getAllByTestId("runs-row").map((r) => r.dataset.run);
    expect(order).toEqual(["r-new", "r-stage", "r-old"]);
  });

  // Rows were labelled with task_id alone, so the artifact stages — the only
  // runs that pause at a human gate — rendered an anchor with no text.
  it("gives a stage run a clickable label", () => {
    render(<Runs runs={RUNS} />);
    const row = screen.getAllByTestId("runs-row").find((r) => r.dataset.run === "r-stage")!;
    const link = row.querySelector("a")!;
    expect(link.textContent).toBe("intake");
    expect(link.getAttribute("href")).toBe("#/runs/r-stage");
  });

  it("filters to the runs waiting for a person", () => {
    render(<Runs runs={RUNS} />);
    fireEvent.click(screen.getByTestId("runs-filter-waiting"));
    const rows = screen.getAllByTestId("runs-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]!.dataset.run).toBe("r-stage");
  });

  it("says there are none rather than showing an empty table", () => {
    render(<Runs runs={[]} />);
    expect(screen.queryByTestId("runs-view")).toBeNull();
    expect(screen.getByText(/No runs yet/)).toBeTruthy();
  });
});


// What each run spent, in the list where runs are compared — live for a
// running one, ~ when built on estimated counts, and silence over a zero.
describe("the cost column", () => {
  const base = { project_id: "p", stage: "build", mode: "solo", task_id: "T-001",
    status: "done" as const, verdict: "PASSED", started_at: "2026-08-06T10:00:00Z" };
  it("shows the spend, marks estimates, and stays quiet at zero", () => {
    render(
      <Runs
        runs={[
          mk({ ...base, id: "r-1", budget: { usd: 0.4419, tokens: 1, turns: 1, wallclock_s: 1 } }),
          mk({ ...base, id: "r-2", tokens_estimated: true, budget: { usd: 0.1, tokens: 1, turns: 1, wallclock_s: 1 } }),
          mk({ ...base, id: "r-3", budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0 } }),
        ]}
      />,
    );
    const cells = screen.getAllByTestId("run-cost").map((c) => c.textContent);
    expect(cells).toContain("$0.4419");
    expect(cells).toContain("~$0.1000");
    expect(cells).toContain("—");
  });
});
