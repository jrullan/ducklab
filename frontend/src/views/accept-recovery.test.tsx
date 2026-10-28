import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { Now } from "./Now";
import { Board } from "./Board";
import { useRuns } from "../store/runs";
import type { EngineClient, Run } from "../api/client";

// Review of #181 (comment 4394805772): an accept started from Now or Board
// that the engine answered in_progress settles only through the live
// accept_settled event. When the stream drops, App.onReconnect refreshes the
// run LIST (setRuns) — a payload without last_accept — and neither view
// reads run detail, so the button said "Accepting… verifying from a clean
// checkout" forever. A bulk replacement must settle it: the verifying accept
// refetches its own run, whose read carries the outcome.

const paused: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "solo", task_id: "T-001",
  status: "paused", verdict: "PASSED", pending_kind: "gate",
  pending_since: "2026-10-27T19:44:56Z", started_at: "2026-10-27T19:40:00Z",
  next: ["accept", "reject"],
};

type Outcome = {
  name: string;
  // The run read the refetch returns: what the engine knows after settling.
  detail: Run;
  // The run as the list refresh delivers it — no last_accept.
  listed: Run;
  expectState: Record<string, unknown>;
};

const outcomes: Outcome[] = [
  {
    name: "accepted",
    detail: { ...paused, status: "done", accepted: true, commit_sha: "abc1234", next: [], last_accept: { attempt: "acc-1", outcome: "accepted", commit_sha: "abc1234" } },
    listed: { ...paused, status: "done", accepted: true, commit_sha: "abc1234", next: [] },
    expectState: { kind: "committed", sha: "abc1234" },
  },
  {
    name: "refused",
    detail: { ...paused, last_accept: { attempt: "acc-1", outcome: "refused", error: "rebased commit failed its gate" } },
    listed: paused,
    expectState: { kind: "error", message: "rebased commit failed its gate" },
  },
  {
    name: "interrupted",
    detail: { ...paused, last_accept: { attempt: "acc-1", outcome: "interrupted", error: "the run was aborted" } },
    listed: paused,
    expectState: { kind: "error", message: "the run was aborted", interrupted: true },
  },
];

const client = (detail: Run) =>
  ({
    // Now
    taskNext: vi.fn(() => Promise.resolve(null)),
    projectNext: vi.fn(() => Promise.resolve([])),
    appStatus: vi.fn(() => Promise.resolve({ configured: false, running: false })),
    bugs: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([])),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    // Board
    tasks: vi.fn(() => Promise.resolve([{ id: "T-001", title: "Waiting one", milestone: "M-01", status: "review", next: [] }])),
    projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "pytest -q" })),
    // The accept outlives its request; the outcome is only on the run read.
    accept: vi.fn(() => Promise.resolve({ commit_sha: "", in_progress: true, attempt: "acc-1", info: "accept in progress" })),
    run: vi.fn(() => Promise.resolve({ run: detail, events: [] })),
  }) as unknown as EngineClient;

beforeEach(() => {
  useRuns.setState({ runs: { "r-1": paused }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {} });
});

const verifyingThenReconnect = async (c: EngineClient, listed: Run) => {
  fireEvent.click(await screen.findByTestId("now-accept"));
  await waitFor(() => expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "pending", verifying: true, attempt: "acc-1" }));
  expect(screen.getByTestId("now-accept").textContent).toBe("Accepting… verifying from a clean checkout");
  // The stream dropped accept_settled; the reconnect refreshes the list.
  act(() => useRuns.getState().setRuns([listed]));
  // The list carries no last_accept: the replacement must prompt the run
  // read that does.
  await waitFor(() => expect(c.run).toHaveBeenCalledWith("r-1"));
};

describe("an in-progress accept from Now settles after a reconnect", () => {
  for (const o of outcomes) {
    it(o.name, async () => {
      const c = client(o.detail);
      render(<Now client={c} projectId="p" />);
      await verifyingThenReconnect(c, o.listed);
      await waitFor(() => expect(useRuns.getState().acceptState["r-1"]).toEqual(o.expectState));
      if (o.name === "interrupted") {
        expect(screen.getByTestId("now-accept-error").textContent).toContain("accept interrupted — nothing was decided");
      } else if (o.name === "refused") {
        expect(screen.getByTestId("now-accept-error").textContent).toContain("accept failed: rebased commit failed its gate");
      }
    });
  }
});

describe("an in-progress accept from Board settles after a reconnect", () => {
  for (const o of outcomes) {
    it(o.name, async () => {
      const c = client(o.detail);
      render(<Board client={c} projectId="p" />);
      fireEvent.click(await screen.findByText("Waiting one"));
      await screen.findByTestId("now-waiting-card");
      await verifyingThenReconnect(c, o.listed);
      await waitFor(() => expect(useRuns.getState().acceptState["r-1"]).toEqual(o.expectState));
    });
  }
});
