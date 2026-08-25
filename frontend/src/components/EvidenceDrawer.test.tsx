import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Run } from "../api/client";
import { EvidenceDrawer } from "./EvidenceDrawer";

const run: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "solo", task_id: "T-167",
  status: "paused", verdict: "PASSED", started_at: "2026-01-01T00:00:00Z",
  next: ["accept", "reject"],
  pending_data: {
    tests: "passed", test_note: "12 tests passed", tests_on_final_revision: true,
    reviewer_verdict: "Ship it: the change is focused.",
    files: [{ path: "src/app.ts", summary: "adds the evidence drawer" }],
    additions: 18, deletions: 3, logs: "raw output",
  },
  spend: { builder: { calls: 2, tokens: 100, cost_usd: 0.12 } },
  budget: { usd: 0.12, tokens: 100, turns: 2, wallclock_s: 10, limit: { usd: 1, tokens: 1000, turns: 10, wallclock_s: 100 } },
};

describe("EvidenceDrawer", () => {
  it("puts conclusion tiles first and keeps technical details closed", () => {
    render(<EvidenceDrawer run={run} onClose={() => {}} />);
    expect(screen.getByTestId("evidence-tiles")).toHaveTextContent("passed");
    expect(screen.getByTestId("evidence-tiles")).toHaveTextContent("Ship it: the change is focused.");
    expect(screen.getByTestId("evidence-freshness")).toHaveTextContent("final revision");
    expect(screen.getByText("src/app.ts")).toBeInTheDocument();
    expect(screen.getByText("Cost breakdown per seat").closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("Raw logs").closest("details")).not.toHaveAttribute("open");
  });

  it("closes from its close button", () => {
    const onClose = vi.fn();
    render(<EvidenceDrawer run={run} onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: "Close evidence" }));
    expect(onClose).toHaveBeenCalledOnce();
  });
});
