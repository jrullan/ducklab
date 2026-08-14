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

// A test run and a build run of the same task both read "T-083", so the
// person who launched a TDD chain could not tell which phase finished — nor
// notice when a relaunch quietly became build-only. The stage always shows.
import { runLabel } from "../lib/runview";
describe("runLabel names the kind", () => {
  it("puts the stage beside the task, and stands alone without one", () => {
    expect(runLabel({ id: "r1", stage: "test", task_id: "T-083" })).toBe("test · T-083");
    expect(runLabel({ id: "r1", stage: "build", task_id: "T-083" })).toBe("build · T-083");
    expect(runLabel({ id: "r1", stage: "spec" })).toBe("spec");
    expect(runLabel({ id: "r1" })).toBe("r1");
  });

  it("names a taskless run's subject — the bug a triage read", () => {
    expect(runLabel({ id: "r1", stage: "triage", subject: "B-059" })).toBe("triage · B-059");
    expect(runLabel({ id: "r1", stage: "triage", subject: "3 open bugs" })).toBe("triage · 3 open bugs");
    // A task outranks a subject: builds keep naming their task.
    expect(runLabel({ id: "r1", stage: "build", task_id: "T-083", subject: "B-1" })).toBe("build · T-083");
  });
});


// The runs table says what each run cost in TIME and rounds, not only money:
// the tracker's wallclock when recorded, the started→ended span otherwise.
describe("took and turns in the runs table", () => {
  it("renders the tracker's wallclock and the turn count", () => {
    const run = {
      id: "r-a", project_id: "p", stage: "build", mode: "pair", task_id: "T-1",
      status: "done", verdict: "PASSED", started_at: "2026-08-11T22:00:00Z",
      ended_at: "2026-08-11T22:06:00Z",
      budget: { usd: 0.01, tokens: 1000, turns: 3, wallclock_s: 357.5 },
    } as unknown as Run;
    render(<Runs runs={[run]} />);
    expect(screen.getByTestId("run-took").textContent).toBe("5m58s");
    expect(screen.getByTestId("run-turns").textContent).toBe("3");
  });
});

// "No encuentro ninguna tarea test fallida de la T-110" — and the record had
// two. A test-first that concludes cleanly with verdict FAILED (its test
// never landed red) wears status "done"; the failed filter went by status
// alone and the verdict was grey prose. The person hunting the runs they
// were told to relaunch found only the crashed build.
describe("failed runs are found by outcome", () => {
  const runs: Run[] = [
    mk({ id: "r-test-flop", stage: "test", task_id: "T-110", status: "done", verdict: "FAILED" }),
    mk({ id: "r-build-crash", stage: "build", task_id: "T-110", status: "failed", verdict: "FAILED", started_at: "2026-07-21T00:00:00Z" }),
    mk({ id: "r-good", status: "done", verdict: "PASSED", started_at: "2026-07-22T00:00:00Z" }),
  ];

  it("the failed filter includes done runs whose verdict is FAILED", () => {
    render(<Runs runs={runs} />);
    fireEvent.click(screen.getByTestId("runs-filter-failed"));
    const shown = screen.getAllByTestId("runs-row").map((r) => r.dataset.run);
    expect(shown).toContain("r-test-flop");
    expect(shown).toContain("r-build-crash");
    expect(shown).not.toContain("r-good");
  });
});
