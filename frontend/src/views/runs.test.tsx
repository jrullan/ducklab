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
