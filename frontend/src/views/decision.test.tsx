import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import type { EngineClient, Run } from "../api/client";
import type { DucklabEvent } from "../api/events";

// B-261, observed on r-20260826-164440-3olh: a build with a green gate and a
// reviewer who requested changes stacked FOUR decision surfaces. Blueprint
// rule: one control surface per state. These tests hand the view exactly that
// state and assert it draws ONE decision, with every action's consequence.
const run: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-216",
  status: "paused", verdict: "PASSED", started_at: "2026-08-26T16:44:40Z",
  pending_kind: "gate", pending_since: "2026-08-26T16:50:00Z",
  roster: { implementer: "luna", reviewer: "glm52" },
  budget: { usd: 0.31, tokens: 412000, turns: 4, wallclock_s: 192 },
  next: ["accept", "reject"],
};

const ev = (type: string, seq: number, data: Record<string, unknown>) =>
  ({ type, seq, run_id: "r-1", data }) as unknown as DucklabEvent;

const dissentEvents = [
  ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "luna" }),
  ev("turn_end", 2, { round: 1, turn: 0, role: "implementer" }),
  ev("turn_start", 3, { round: 1, turn: 1, role: "reviewer", duckling: "glm52" }),
  ev("message", 4, { round: 1, turn: 1, content: "…", verdict: "request-changes", findings: [
    { severity: "major", file: "app.py", line: 12, issue: "wrong week boundary", fix: "use ISO weeks" },
    { severity: "minor", issue: "missing null check" },
  ] }),
  ev("turn_end", 5, { round: 1, turn: 1, role: "reviewer" }),
  ev("gate", 6, { gate: "tests", cmd: "go test ./...", exit: 0, ms: 8210 }),
  ev("verdict", 7, { verdict: "PASSED" }),
];

const clientWith = (over: Partial<EngineClient> = {}) =>
  ({
    run: vi.fn(() => Promise.resolve({ run, events: dissentEvents })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([{ id: "luna", provider: "x", model: "l" }, { id: "glm52", provider: "x", model: "g" }])),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    tasks: vi.fn(() => Promise.resolve([{ id: "T-216", title: "Publish on accept", milestone: "M-07", status: "in_progress" }])),
    accept: vi.fn(() => Promise.resolve({ commit_sha: "abc1234" })),
    runStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
    runFileFindings: vi.fn(() => Promise.resolve({ items: [{ id: "B-77" }, { id: "B-78" }] })),
    nextFor: vi.fn(() => Promise.resolve({ ref: "T-216", kind: "task", rungs: [], steps: [] })),
    ...over,
  }) as unknown as EngineClient;

const seed = (runs: Run[], events: DucklabEvent[] = dissentEvents) =>
  useRuns.setState({
    runs: Object.fromEntries(runs.map((r) => [r.id, r])),
    events: { "r-1": events }, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, connection: "open",
  });

describe("RunView — one decision surface at a green gate with an unconvinced reviewer", () => {
  beforeEach(() => seed([run]));

  it("draws exactly one decision card and no sibling surfaces", async () => {
    render(<RunView runId="r-1" client={clientWith()} />);
    const card = await screen.findByTestId("decision-card");
    expect(screen.getAllByTestId("decision-card")).toHaveLength(1);
    expect(screen.queryByTestId("reviewer-dissent")).toBeNull();
    expect(screen.queryByTestId("pending-human")).toBeNull();
    // The dissent, the competing accept and the filing are inside the card.
    expect(within(card).getByTestId("decision-dissent").textContent).toContain("2 findings");
    expect(within(card).getByTestId("accept-and-fix")).toBeTruthy();
    expect(within(card).getByTestId("accept-and-fix-launches").textContent).toContain("launches a new run");
    expect(within(card).getByTestId("accept-and-fix-consequence").textContent).toMatch(/\$0\.31\d* already spent stays on the record/);
    expect(within(card).getByTestId("file-findings-button").textContent).toContain("File 2 findings as bugs");
    expect(screen.getAllByTestId("file-findings")).toHaveLength(1);
    expect(within(card).getByTestId("file-findings")).toBeTruthy();
  });

  // B-258: the follow-up run goes through the one relaunch path — the
  // mode's saved line-up — and never carries seat overrides copied from
  // this run.
  it("accept-then-fix accepts, then relaunches through the single launch path without seat overrides", async () => {
    const client = clientWith();
    render(<RunView runId="r-1" client={client} />);
    fireEvent.click(await screen.findByTestId("accept-and-fix"));
    await waitFor(() => expect(client.accept).toHaveBeenCalledWith("r-1"));
    await waitFor(() => expect(client.runStart).toHaveBeenCalled());
    const [projectId, taskId, opts] = (client.runStart as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]! as [string, string, Record<string, unknown>];
    expect(projectId).toBe("p");
    expect(taskId).toBe("T-216");
    expect(opts.redo).toBe(true);
    expect(opts.mode).toBe("pair");
    expect(String(opts.note)).toContain("wrong week boundary");
    expect(opts.seats).toBeUndefined();
  });

  it("files the findings from the card and reports the bug ids", async () => {
    const client = clientWith();
    render(<RunView runId="r-1" client={client} />);
    fireEvent.click(await screen.findByTestId("file-findings-button"));
    await waitFor(() => expect(client.runFileFindings).toHaveBeenCalledWith("r-1"));
    expect((await screen.findByTestId("file-findings-done")).textContent).toContain("B-77, B-78");
  });

  it("keeps the dissent as history once the decision is made", async () => {
    const done: Run = { ...run, status: "done", accepted: true, commit_sha: "abc1234", next: [] };
    seed([done]);
    render(<RunView runId="r-1" client={clientWith({ run: vi.fn(() => Promise.resolve({ run: done, events: dissentEvents })) } as unknown as Partial<EngineClient>)} />);
    await screen.findByTestId("run-view");
    expect(screen.queryByTestId("decision-card")).toBeNull();
    expect(screen.getByTestId("reviewer-dissent")).toBeTruthy();
    expect(screen.queryByTestId("accept-and-fix")).toBeNull();
  });

  // B-247: a re-run of a task whose work already landed says so where the
  // accept is offered; a first run says nothing.
  it("warns when the task already landed under another accepted run", async () => {
    seed([run, { ...run, id: "r-0", status: "done", accepted: true, commit_sha: "b0c33af9deadbeef", started_at: "2026-08-26T04:00:00Z", next: [] }]);
    render(<RunView runId="r-1" client={clientWith()} />);
    const card = await screen.findByTestId("decision-card");
    expect(within(card).getByTestId("landed-notice").textContent).toContain("already landed as b0c33af");
  });

  it("says nothing about landing on a task's first run", async () => {
    render(<RunView runId="r-1" client={clientWith()} />);
    await screen.findByTestId("decision-card");
    expect(screen.queryByTestId("landed-notice")).toBeNull();
  });
});
