import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Now } from "./Now";
import { useRuns } from "../store/runs";
import type { EngineClient, Run } from "../api/client";

const base: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-026",
  status: "paused", verdict: "PASSED", pending_kind: "gate",
  pending_since: "2026-07-31T09:00:00Z", started_at: "2026-07-31T08:57:00Z",
  next: ["accept", "reject"],
  budget: { usd: 0.31, tokens: 412000, turns: 4, wallclock_s: 192 },
};

const clientWith = (over: Partial<EngineClient> = {}) =>
  ({
    taskNext: vi.fn(() => Promise.resolve(null)),
    ducklings: vi.fn(() => Promise.resolve([])),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    accept: vi.fn(() => Promise.resolve({ commit_sha: "abc1234" })),
    reject: vi.fn(() => Promise.resolve({})),
    runStart: vi.fn(() => Promise.resolve({ id: "r-9" })),
    ...over,
  }) as unknown as EngineClient;

const seed = (runs: Run[]) => {
  useRuns.setState({
    runs: Object.fromEntries(runs.map((r) => [r.id, r])),
    events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {},
  });
};

// The first screen answers the one question a solo dev arrives with: what
// needs me? (docs/ux-evaluation.md P1)
describe("Now — the inbox", () => {
  beforeEach(() => seed([]));

  it("puts a waiting gate first, with its verdict, age, cost and a decision", async () => {
    seed([base]);
    render(<Now client={clientWith()} projectId="p" />);
    const card = await screen.findByTestId("now-waiting-card");
    expect(card.textContent).toContain("T-026");
    expect(card.textContent).toContain("passed");
    expect(card.textContent).toContain("$0.31");
    expect(screen.getByTestId("now-accept")).toBeTruthy();
    // The evidence is one click away, and the link says so.
    expect(card.textContent).toContain("see the evidence");
  });

  it("accepts without leaving the inbox, never optimistically", async () => {
    seed([base]);
    const client = clientWith();
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-accept"));
    await waitFor(() => expect(client.accept).toHaveBeenCalledWith("r-1"));
  });

  it("routes a question to the answer, not to Accept/Reject", async () => {
    seed([{ ...base, pending_kind: "question", verdict: "", next: ["answer", "abort"] }]);
    render(<Now client={clientWith()} projectId="p" />);
    const card = await screen.findByTestId("now-waiting-card");
    expect(screen.queryByTestId("now-accept")).toBeNull();
    expect(card.textContent).toContain("answer it");
  });

  // Only the LATEST run of a task, still failed, for work never subsequently
  // accepted. An old failure whose task a later run completed is history, and
  // offering it here would offer redoing finished work.
  it("shows a failure once, and not after a later run superseded it", async () => {
    seed([
      { ...base, id: "r-old", status: "failed", verdict: "FAILED", pending_kind: undefined,
        started_at: "2026-07-31T08:00:00Z", ended_at: "2026-07-31T08:10:00Z",
        failure: "budget exceeded: 436339 >= 400000" },
      { ...base, id: "r-new", status: "done", accepted: true, pending_kind: undefined,
        started_at: "2026-07-31T09:00:00Z" },
    ]);
    render(<Now client={clientWith()} projectId="p" />);
    await screen.findByTestId("now-view");
    expect(screen.queryByTestId("now-failure")).toBeNull();
  });

  it("shows an unsuperseded failure with its reason's first line", async () => {
    seed([{ ...base, id: "r-f", status: "failed", verdict: "FAILED", pending_kind: undefined,
      ended_at: "2026-07-31T09:10:00Z",
      failure: "panic: runtime error: slice bounds out of range [92:78]\nstack…" }]);
    render(<Now client={clientWith()} projectId="p" />);
    const f = await screen.findByTestId("now-failure");
    expect(f.textContent).toContain("slice bounds out of range");
    expect(f.textContent).not.toContain("stack…");
  });

  it("shows live spend on a running run", async () => {
    seed([{ ...base, id: "r-live", status: "running", verdict: "", pending_kind: undefined }]);
    useRuns.setState({
      spend: {
        "r-live": {
          usd: 0.42, tokens: 214000, turns: 2, wallclock_s: 120,
          limit: { usd: 5, tokens: 1500000, turns: 24, wallclock_s: 1800 },
          ducklings: {},
        },
      },
    });
    render(<Now client={clientWith()} projectId="p" />);
    const row = await screen.findByTestId("now-running-row");
    expect(row.textContent).toContain("214.0k / 1.5M");
    expect(row.textContent).toContain("$0.4200");
  });

  // "Nothing needs me" and "what should I do next" are the same moment.
  it("offers the next ready task when the queue is empty", async () => {
    const client = clientWith({
      taskNext: vi.fn(() =>
        Promise.resolve({ id: "T-028", title: "Angle input validation", milestone: "M-07", status: "todo" }),
      ),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const next = await screen.findByTestId("now-next");
    expect(next.textContent).toContain("T-028");
    fireEvent.click(screen.getByTestId("run-start"));
    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-028", expect.anything()),
    );
  });

  it("says when nothing is ready either, which is itself the answer", async () => {
    seed([{ ...base, id: "r-d", status: "done", accepted: true, pending_kind: undefined }]);
    render(<Now client={clientWith()} projectId="p" />);
    await screen.findByTestId("now-quiet");
    expect(screen.getByTestId("now-all-done").textContent).toContain("done, running, or waiting");
  });
});
