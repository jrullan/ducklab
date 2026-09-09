import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Run } from "../api/client";
import { WaitingCard } from "./WaitingCard";
import { useRuns } from "../store/runs";

const run: Run = {
  id: "run-167",
  project_id: "project-1",
  stage: "build",
  mode: "solo",
  task_id: "T-167",
  status: "paused",
  verdict: "PASSED",
  started_at: "2026-01-01T00:00:00Z",
  pending_since: "2026-01-01T00:01:00Z",
  next: ["accept", "reject"],
  pending_data: {
    tests: "passed",
    reviewer_verdict: "Ship it.",
  },
};

describe("WaitingCard evidence", () => {
  it("opens the evidence drawer from the card and keeps decisions visible", () => {
    render(
      <WaitingCard
        run={run}
        accepting={false}
        onAccept={vi.fn()}
        onReject={vi.fn()}
        onAbort={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId("review-evidence"));

    expect(screen.getByRole("dialog", { name: "Evidence" })).toBeInTheDocument();
    expect(screen.getByTestId("now-accept")).toBeInTheDocument();
    expect(screen.getByTestId("now-reject")).toBeInTheDocument();
  });

  it("does not offer evidence review for question-only cards", () => {
    render(
      <WaitingCard
        run={{ ...run, next: ["answer"] }}
        accepting={false}
        onAccept={vi.fn()}
        onReject={vi.fn()}
        onAbort={vi.fn()}
      />,
    );

    expect(screen.queryByTestId("review-evidence")).not.toBeInTheDocument();
  });
});

// B-247: the inbox card for a task whose work already landed must say so.
// The T-181 phantom re-run was accepted from a card that looked like any
// other, twelve hours after the task's commit.
describe("WaitingCard and a task that already landed", () => {
  const seed = (runs: Run[]) =>
    useRuns.setState({ runs: Object.fromEntries(runs.map((r) => [r.id, r])), events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {} });

  it("shows the landed commit when another accepted run of the task exists", () => {
    seed([
      run,
      { ...run, id: "run-100", status: "done", accepted: true, commit_sha: "b0c33af9deadbeef", started_at: "2025-12-31T00:00:00Z" },
    ]);
    render(<WaitingCard run={run} accepting={false} onAccept={() => {}} onReject={() => {}} onAbort={() => {}} />);
    expect(screen.getByTestId("landed-notice").textContent).toContain("already landed as b0c33af");
    seed([]);
  });

  it("shows nothing on a task's first run", () => {
    seed([run]);
    render(<WaitingCard run={run} accepting={false} onAccept={() => {}} onReject={() => {}} onAbort={() => {}} />);
    expect(screen.queryByTestId("landed-notice")).toBeNull();
    seed([]);
  });
});
