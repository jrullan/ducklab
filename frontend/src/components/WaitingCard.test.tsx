import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Run } from "../api/client";
import { WaitingCard } from "./WaitingCard";

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
