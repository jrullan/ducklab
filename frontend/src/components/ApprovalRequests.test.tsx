// B-530: an operator refused a lane widening had the person's "Ok" in chat
// and no door to carry it, so it accepted as "human" itself. The engine now
// derives the actor from the credential; these are the person's half of the
// legitimate door — the request is shown with who asks, what and why, and
// Approve/Decline go to the engine with the person's own client.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ApprovalRequest, EngineClient, Run } from "../api/client";
import { ApprovalRequests, PendingApprovals } from "./ApprovalRequests";
import { pendingApprovals, useRuns } from "../store/runs";
import { Now } from "../views/Now";
import { actorPhrase } from "../lib/runview";

describe("actor phrases", () => {
  it("never turns the engine's agent actor back into the person", () => {
    expect(actorPhrase("agent")).toBe("an agent");
    expect(actorPhrase("agent:ci")).toBe("the agent ci");
    expect(actorPhrase("human")).toBe("the person");
  });
});

const request: ApprovalRequest = {
  id: "ar-1",
  action: "accept",
  reason: "Jose said Ok in chat to widening T-004's lane",
  pending_kind: "gate",
  pending_since: "2026-10-27T18:54:16Z",
  lane_widening: ["tests/parser.test.mjs"],
  requested_by: "mcp:opencode",
  requested_at: "2026-10-27T19:00:00Z",
  status: "pending",
};

const run: Run = {
  id: "r-20261027-185416-7mhr",
  project_id: "p",
  stage: "test",
  mode: "solo",
  task_id: "T-004",
  status: "paused",
  verdict: "PASSED",
  pending_kind: "gate",
  pending_since: "2026-10-27T18:54:16Z",
  started_at: "2026-10-27T18:50:00Z",
  next: ["accept", "reject"],
  approval_requests: [request],
};

const seed = (runs: Run[]) =>
  useRuns.setState({
    runs: Object.fromEntries(runs.map((r) => [r.id, r])),
    events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {},
  });

const clientWith = (over: Partial<EngineClient> = {}) =>
  ({
    approveRequest: vi.fn(() =>
      Promise.resolve({ request: { ...request, status: "landed", decided_by: "human", outcome: "accepted as abc1234" } }),
    ),
    declineRequest: vi.fn((_r: string, _q: string, reason: string) =>
      Promise.resolve({ ...request, status: "declined", decided_by: "human", decline_reason: reason }),
    ),
    ...over,
  }) as unknown as EngineClient;

describe("approval requests on a run", () => {
  beforeEach(() => seed([run]));

  it("says who asks, what, and why, with Approve and Decline", () => {
    render(<ApprovalRequests client={clientWith()} run={run} />);
    expect(screen.getByTestId("approval-requested-by").textContent).toBe("mcp:opencode");
    expect(screen.getByTestId("approval-request").textContent).toContain("widen the task's lane");
    expect(screen.getByTestId("approval-request").textContent).toContain("tests/parser.test.mjs");
    expect(screen.getByTestId("approval-reason").textContent).toContain("Jose said Ok");
    expect(screen.getByTestId("approval-approve")).toBeTruthy();
    expect(screen.getByTestId("approval-decline")).toBeTruthy();
  });

  // B-541: lifting a budget cap is the person's decision too; the card names
  // the cap in words the person reads, not the engine's kind token.
  it("names the cap a budget-lift request would remove", () => {
    const lift: ApprovalRequest = {
      ...request, action: "budget_lift", budget_kind: "usd", lane_widening: undefined,
      pending_kind: "budget", reason: "the person agreed to spend more on T-004",
    };
    const paused: Run = { ...run, pending_kind: "budget", approval_requests: [lift] };
    seed([paused]);
    render(<ApprovalRequests client={clientWith()} run={paused} />);
    expect(screen.getByTestId("approval-request").textContent).toContain("lift the run's dollar cap");
    expect(screen.getByTestId("approval-approve")).toBeTruthy();
  });

  it("approves through the person's client and shows the outcome", async () => {
    const client = clientWith();
    render(<ApprovalRequests client={client} run={run} />);
    fireEvent.click(screen.getByTestId("approval-approve"));
    await waitFor(() => expect(client.approveRequest).toHaveBeenCalledWith(run.id, "ar-1"));
    await waitFor(() =>
      expect(useRuns.getState().runs[run.id]!.approval_requests![0]!.status).toBe("landed"),
    );
  });

  it("declines with the person's words and decides nothing", async () => {
    const client = clientWith();
    render(<ApprovalRequests client={client} run={run} />);
    fireEvent.click(screen.getByTestId("approval-decline"));
    fireEvent.change(screen.getByTestId("approval-decline-reason"), { target: { value: "not this lane" } });
    fireEvent.click(screen.getByTestId("approval-decline-confirm"));
    await waitFor(() => expect(client.declineRequest).toHaveBeenCalledWith(run.id, "ar-1", "not this lane"));
    expect(client.approveRequest).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(useRuns.getState().runs[run.id]!.approval_requests![0]!.status).toBe("declined"),
    );
  });

  it("shows the engine's refusal instead of pretending it landed", async () => {
    const client = clientWith({
      approveRequest: vi.fn(() => Promise.reject(new Error("approved, but the accept was refused: reviewer requested changes"))),
    });
    render(<ApprovalRequests client={client} run={run} />);
    fireEvent.click(screen.getByTestId("approval-approve"));
    expect((await screen.findByTestId("approval-error")).textContent).toContain("reviewer requested changes");
  });

  it("shows a decided request with how it ended and no buttons", () => {
    const decided = { ...run, approval_requests: [{ ...request, status: "declined", decided_by: "human", decline_reason: "no" }] };
    render(<ApprovalRequests client={clientWith()} run={decided} />);
    expect(screen.queryByTestId("approval-approve")).toBeNull();
    expect(screen.getByTestId("approval-outcome").textContent).toContain("declined by human");
  });
});

describe("approval requests in Now", () => {
  it("lists every pending request across runs and counts it as needing the person", async () => {
    const other: Run = { ...run, id: "r-2", task_id: "T-005", approval_requests: [{ ...request, id: "ar-1", status: "declined" }] };
    seed([run, other]);
    expect(pendingApprovals(useRuns.getState().runs).map((x) => x.run.id)).toEqual([run.id]);
    const client = {
      ...clientWith(),
      taskNext: vi.fn(() => Promise.resolve(null)),
      projectNext: vi.fn(() => Promise.resolve([])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      report: vi.fn(() => Promise.resolve({ rows: [] })),
    } as unknown as EngineClient;
    render(<Now client={client} projectId="p" />);
    const section = await screen.findByTestId("now-approvals");
    expect(section.textContent).toContain("mcp:opencode");
    expect(section.querySelectorAll("[data-testid=approval-request]").length).toBe(1);
    expect(screen.getByTestId("now-approvals-count").textContent).toBe("1");
  });

  it("renders nothing when no agent is waiting", () => {
    const { container } = render(<PendingApprovals client={clientWith()} items={[]} />);
    expect(container.innerHTML).toBe("");
  });
});

// Review of #181 (R4): an approved accept that outlives its request answers
// "approved — accept in progress"; the request settles later on the stream.
// The display must follow it to its final status and never move back —
// not when the HTTP answer lands after approval_resolved, not after a
// reconnect's list refresh.
describe("an approved accept in progress, displayed", () => {
  function Live({ client }: { client: EngineClient }) {
    const live = useRuns((s) => s.runs[run.id]);
    return live ? <ApprovalRequests client={client} run={live} /> : null;
  }

  it("shows in progress, then the settlement, and never regresses", async () => {
    seed([run]);
    let answer: (v: unknown) => void = () => {};
    const client = clientWith({
      approveRequest: vi.fn(() => new Promise((resolve) => { answer = resolve; })),
    } as never);
    render(<Live client={client} />);
    fireEvent.click(screen.getByTestId("approval-approve"));
    const inProgress = { ...request, status: "approved", decided_by: "human", outcome: "accept in progress" };
    // The engine claimed it (approval_granted), then settled it before the
    // approve's HTTP answer reached the desktop.
    useRuns.getState().applyEvent({ type: "approval_granted", run_id: run.id, seq: 1, data: { request: inProgress } });
    expect((await screen.findByTestId("approval-outcome")).textContent).toContain("approved by human — accept in progress");
    const landed = { ...request, status: "landed", decided_by: "human", outcome: "accepted as abc1234" };
    useRuns.getState().applyEvent({ type: "approval_resolved", run_id: run.id, seq: 2, data: { request: landed } });
    await waitFor(() => expect(screen.getByTestId("approval-outcome").textContent).toContain("landed by human — accepted as abc1234"));
    // The late HTTP answer, flushed through the component's own update.
    await act(async () => {
      answer({ request: inProgress, accept: { commit_sha: "", in_progress: true } });
    });
    expect(screen.getByTestId("approval-outcome").textContent).toContain("landed by human — accepted as abc1234");
    // A reconnect replaces the run list with a snapshot taken mid-flight.
    act(() => useRuns.getState().setRuns([{ ...run, approval_requests: [inProgress] }]));
    expect(screen.getByTestId("approval-outcome").textContent).toContain("landed by human — accepted as abc1234");
  });

  it("follows the settlement after a reconnect when the stream lost it", async () => {
    seed([{ ...run, approval_requests: [{ ...request, status: "approved", decided_by: "human", outcome: "accept in progress" }] }]);
    render(<Live client={clientWith()} />);
    expect(screen.getByTestId("approval-outcome").textContent).toContain("accept in progress");
    useRuns.getState().setRuns([{ ...run, approval_requests: [{ ...request, status: "failed", decided_by: "human", outcome: "refused: rebased commit failed its gate" }] }]);
    await waitFor(() => expect(screen.getByTestId("approval-outcome").textContent).toContain("failed by human — refused: rebased commit failed its gate"));
  });
});

describe("approval events keep the store current", () => {
  it("adds a filed request and replaces it when the person answers", () => {
    seed([{ ...run, approval_requests: [] }]);
    useRuns.getState().applyEvent({ type: "approval_requested", run_id: run.id, seq: 1, data: { request } });
    expect(useRuns.getState().runs[run.id]!.approval_requests).toEqual([request]);
    useRuns.getState().applyEvent({
      type: "approval_declined", run_id: run.id, seq: 2,
      data: { request: { ...request, status: "declined" } },
    });
    const list = useRuns.getState().runs[run.id]!.approval_requests!;
    expect(list.length).toBe(1);
    expect(list[0]!.status).toBe("declined");
  });
});
