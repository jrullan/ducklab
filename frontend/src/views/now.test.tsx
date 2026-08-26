import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
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
    projectNext: vi.fn(() => Promise.resolve([])),
    appStatus: vi.fn(() => Promise.resolve({ configured: false, running: false })),
    bugs: vi.fn(() => Promise.resolve([])),
    moveBug: vi.fn(() => Promise.resolve({})),
    ducklings: vi.fn(() => Promise.resolve([])),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    accept: vi.fn(() => Promise.resolve({ commit_sha: "abc1234" })),
    reject: vi.fn(() => Promise.resolve({})),
    abort: vi.fn(() => Promise.resolve({})),
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

  it("explains each pending card variant in plain language", async () => {
    const variants = [
      { pending_kind: "gate", verdict: "PASSED", text: "finished and passed its tests" },
      { pending_kind: "question", verdict: "", text: "paused to ask you a question" },
      { pending_kind: "dissent", verdict: "PASSED", text: "a reviewer disagreed" },
      { pending_kind: "gate", verdict: "UNVERIFIED", text: "finished without verified tests" },
    ];
    for (const [index, variant] of variants.entries()) {
      seed([{ ...base, id: `r-${index}`, pending_kind: variant.pending_kind, verdict: variant.verdict }]);
      const { unmount } = render(<Now client={clientWith()} projectId="p" />);
      const card = await screen.findByTestId("now-waiting-card");
      expect(card.textContent).toContain(variant.text);
      unmount();
    }
  });

  it("explains a warning as a passed-with-caveat result", async () => {
    seed([{ ...base, warning: "tests ran in a fallback environment" }]);
    render(<Now client={clientWith()} projectId="p" />);
    const card = await screen.findByTestId("now-waiting-card");
    expect(card.textContent).toContain("passed with caveat");
    expect(screen.getByLabelText("passed with caveat: tests ran in a fallback environment")).toBeTruthy();
  });

  it("accepts without leaving the inbox, never optimistically", async () => {
    seed([base]);
    const client = clientWith();
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-accept"));
    await waitFor(() => expect(client.accept).toHaveBeenCalledWith("r-1"));
  });

  it("exposes and invokes every legal stop decision on a paused run card", async () => {
    seed([{ ...base, next: ["reject", "abort"] }]);
    const client = clientWith();
    render(<Now client={client} projectId="p" />);
    await screen.findByTestId("now-waiting-card");

    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    fireEvent.click(screen.getByRole("button", { name: "Abort" }));

    await waitFor(() => {
      expect(client.reject).toHaveBeenCalledWith("r-1");
      expect(client.abort).toHaveBeenCalledWith("r-1");
    });
  });

  it("routes a question to the answer, not to Accept/Reject", async () => {
    seed([{ ...base, pending_kind: "question", verdict: "", next: ["answer", "abort"] }]);
    render(<Now client={clientWith()} projectId="p" />);
    const card = await screen.findByTestId("now-waiting-card");
    expect(screen.queryByTestId("now-accept")).toBeNull();
    expect(card.textContent).toContain("answer it");
  });

  it("keeps ended and aborted chats out of the decision inbox", async () => {
    seed([
      { ...base, id: "chat-ended", stage: "chat", status: "done", verdict: "ABORTED", pending_kind: "chat", next: [] },
      { ...base, id: "chat-aborted", stage: "chat", status: "failed", verdict: "ABORTED", pending_kind: undefined, next: [] },
    ]);
    render(<Now client={clientWith()} projectId="p" />);
    await screen.findByTestId("now-view");
    expect(screen.queryByTestId("now-waiting")).toBeNull();
    expect(screen.queryByTestId("now-failures")).toBeNull();
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

  it("puts running work before every other inbox section", async () => {
    seed([
      base,
      { ...base, id: "r-live", task_id: "T-live", status: "running", verdict: "", pending_kind: undefined },
      { ...base, id: "r-failed", task_id: "T-failed", status: "failed", verdict: "FAILED", pending_kind: undefined,
        ended_at: "2026-07-31T09:10:00Z", failure: "gate failed" },
    ]);
    const client = clientWith({
      bugs: vi.fn(() => Promise.resolve([
        { id: "B-fixed", title: "A fixed report", severity: "high", status: "fixed", task_id: "T-fixed",
          source: "desktop", created_at: "2026-07-30T23:00:00Z", updated_at: "2026-07-31T01:35:00Z",
          next: ["verified", "in_progress"] },
        { id: "B-reopened", title: "A reopened report", severity: "high", status: "in_progress", task_id: "T-reopened",
          source: "desktop", created_at: "2026-07-30T23:00:00Z", updated_at: "2026-07-31T01:45:00Z",
          next: ["fixed"] },
      ])),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);

    const running = await screen.findByTestId("now-running");
    expect(screen.getAllByTestId("now-running")).toHaveLength(1);
    expect(within(running).getAllByRole("list")).toHaveLength(1);
    expect(screen.getAllByTestId("now-running-row")).toHaveLength(1);
    expect(screen.queryByTestId("utility-drawer")).toBeNull();
    expect(screen.queryByTestId("guide-panel")).toBeNull();
    for (const section of [
      await screen.findByTestId("now-waiting"),
      await screen.findByTestId("now-verify"),
      await screen.findByTestId("now-reopened"),
      await screen.findByTestId("now-failures"),
    ]) {
      expect(running.compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0);
    }
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

  it("shows a queued run's engine reason verbatim without requiring it on older records", async () => {
    seed([
      { ...base, id: "r-queued", status: "queued", verdict: "", pending_kind: undefined,
        queued_reason: "another run holds this project working tree" } as unknown as Run,
      { ...base, id: "r-legacy-queued", status: "queued", verdict: "", pending_kind: undefined },
    ]);
    render(<Now client={clientWith()} projectId="p" />);
    const rows = await screen.findAllByTestId("now-running-row");
    expect(rows.find((row) => row.textContent?.includes("T-026"))?.textContent).toContain(
      "another run holds this project working tree",
    );
    expect(rows.map((row) => row.textContent).join(" ")).not.toContain("undefined");
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
    fireEvent.click(screen.getByTestId("launch-modal-trigger"));
    fireEvent.click(screen.getByTestId("run-start"));
    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-028", expect.anything()),
    );
  });

  it("renders next steps as a native Now section", async () => {
    const client = clientWith({
      projectNext: vi.fn(() => Promise.resolve([
        { kind: "task", id: "T-029", action: "start T-029", reason: "it is ready" },
      ])),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const nextSteps = await screen.findByTestId("now-next-steps");
    expect(nextSteps.tagName).toBe("SECTION");
    expect(screen.getAllByTestId("now-next-steps")).toHaveLength(1);
  });

  it("shows pending plan evidence through the view join and explains approval", async () => {
    const artifact = {
      kind: "plan", version: 1, approved: false, markdown: "",
      sections: [],
      proposal: { diff: "", sections: [
        { id: "T-201", title: "first", body: "", fields: { lane: "build", owner: "alice", files: "src/shared.ts" } },
        { id: "T-202", title: "second", body: "", fields: { lane: "test", owner: "bob", files: "src/shared.ts" } },
      ] },
    };
    const client = clientWith({
      artifact: vi.fn(() => Promise.resolve(artifact)),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [{ kind: "missing", id: "T-202", detail: "missing criterion" }], proposed: ["plan"] })),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const card = await screen.findByTestId("now-plan-card");
    expect(card.textContent).toContain("criteria covered: 1");
    expect(card.textContent).toContain("tasks proposed: 2");
    expect(card.textContent).toContain("can run in parallel: 2");
    expect(card.textContent).toContain("files with two owners: 1");
    fireEvent.click(screen.getByTestId("plan-examine"));
    expect(screen.getByTestId("plan-drawer-meaning").textContent).toBe(
      "you approve these tasks being born and their lanes — you are not approving code yet",
    );
  });

  it("does not show a plan decision when no proposal is waiting", async () => {
    const client = clientWith({
      artifact: vi.fn(() => Promise.resolve({ kind: "plan", version: 1, approved: true, markdown: "", sections: [] })),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    await screen.findByTestId("now-view");
    await waitFor(() => expect(client.artifact).toHaveBeenCalledWith("p", "plan"));
    expect(screen.queryByTestId("now-plan-card")).toBeNull();
  });

  it("says when nothing is ready either, which is itself the answer", async () => {
    seed([{ ...base, id: "r-d", status: "done", accepted: true, pending_kind: undefined }]);
    render(<Now client={clientWith()} projectId="p" />);
    await screen.findByTestId("now-quiet");
    expect(screen.getByTestId("now-all-done").textContent).toContain("done, running, or waiting");
  });
});

// Overview's job, absorbed when it retired. Spend used to be a prop there, and
// the one caller passed `spentToday={0}` — the screen whose job was to say what
// the work cost reported zero while runs spent real money.
describe("the inbox's footer", () => {
  beforeEach(() => seed([]));

  it("adds up what the runs actually cost, today apart from all time", async () => {
    const today = new Date().toISOString().slice(0, 10);
    seed([
      { ...base, id: "r-a", status: "done", verdict: "PASSED", accepted: true, pending_kind: undefined,
        started_at: `${today}T09:00:00Z`,
        budget: { usd: 1.5, tokens: 0, turns: 0, wallclock_s: 0 } },
      { ...base, id: "r-old", status: "done", verdict: "FAILED", pending_kind: undefined,
        started_at: "2026-01-01T09:00:00Z",
        budget: { usd: 9, tokens: 0, turns: 0, wallclock_s: 0 } },
    ]);
    render(<Now client={clientWith()} projectId="p" />);
    const footer = await screen.findByTestId("now-footer");
    expect(footer.textContent).toContain("today $1.50");
    expect(footer.textContent).toContain("all time $10.50");
    expect(footer.textContent).toContain("1/2 passed");
  });

  it("formats zero spend as two decimal places", async () => {
    seed([{ ...base, budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0 } }]);
    render(<Now client={clientWith()} projectId="p" />);
    expect((await screen.findByTestId("now-footer")).textContent).toContain("$0.00");
    expect(screen.getByTestId("now-waiting-card").textContent).not.toContain("$0.0000");
  });

  it("shows nothing before any run exists", async () => {
    render(<Now client={clientWith()} projectId="p" />);
    await screen.findByTestId("now-view");
    expect(screen.queryByTestId("now-footer")).toBeNull();
  });
});

// "Verified" is the one judgement a run must not make for a person — but the
// system never ASKED for it either. A bug reached fixed and sat there unless
// the person remembered the bugs board existed; the question belongs in the
// queue of questions.
describe("verification in the inbox", () => {
  beforeEach(() => seed([]));

  const fixedBug = {
    id: "B-003", title: "Angle in red vertex does not allow changing", severity: "high",
    status: "fixed", task_id: "T-026", source: "desktop",
    created_at: "2026-07-30T23:00:00Z", updated_at: "2026-07-31T01:35:00Z",
    next: ["verified", "in_progress"],
  };

  it("asks whether the fix actually answered the report", async () => {
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([fixedBug])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const card = await screen.findByTestId("now-verify-card");
    expect(card.textContent).toContain("B-003");
    expect(card.textContent).toContain("fixed by T-026");
    // The honest caveat, from the project that taught it: 21 accepted tasks
    // against a syntax gate and the feature never worked.
    expect(card.textContent).toContain("may prove much less");
  });

  it("moves it with the person's verdict, either way", async () => {
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([fixedBug])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-yes"));
    await waitFor(() => expect(client.moveBug).toHaveBeenCalledWith("p", "B-003", "verified"));
  });

  it("offers only what the engine states", async () => {
    const stuck = { ...fixedBug, next: [] as string[] };
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([stuck])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    await screen.findByTestId("now-verify-card");
    expect(screen.queryByTestId("now-verify-yes")).toBeNull();
  });
});

// The person said "still broken", and then the system said nothing at all:
// the verify card only exists at fixed, in_progress reads as "being worked
// on", and nobody was working on it. Measured on B-003, which vanished from
// every queue the moment its reporter sent it back.
describe("reopened reports in the inbox", () => {
  beforeEach(() => seed([]));

  const reopened = {
    id: "B-003", title: "Angle in red vertex does not allow changing", severity: "high",
    status: "in_progress", task_id: "T-026", source: "desktop",
    created_at: "2026-07-30T23:00:00Z", updated_at: "2026-07-31T01:45:21Z",
    next: ["fixed", "triaged"],
  };

  it("surfaces one, offering new work in the task's own last mode", async () => {
    seed([
      { ...base, id: "r-old", task_id: "T-026", mode: "pair", status: "done",
        verdict: "PASSED", accepted: true, pending_kind: undefined,
        started_at: "2026-07-31T01:20:20Z" },
    ]);
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([reopened])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const card = await screen.findByTestId("now-reopened-card");
    expect(card.textContent).toContain("B-003");
    expect(card.textContent).toContain("sent the report back");
    fireEvent.click(screen.getByTestId("now-reopened-run"));
    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-026", { mode: "pair" }),
    );
  });

  // While new work IS running, the report is genuinely in progress and the
  // card would be a nag to start a second run against the same tree.
  it("stays quiet while a run for its task is in flight", async () => {
    seed([
      { ...base, id: "r-live", task_id: "T-026", status: "running", verdict: "",
        pending_kind: undefined },
    ]);
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([reopened])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    await screen.findByTestId("now-view");
    expect(screen.queryByTestId("now-reopened-card")).toBeNull();
  });
});

// T-101: an accepted build was followed a minute later by a redundant run
// someone aborted, and the corpse sat in the inbox for eight hours "awaiting
// your call" — over work already committed. Any accepted run retires its
// task's failures from the inbox; history lives in Records.
describe("failures of settled tasks", () => {
  it("drops a failure whose task a run already accepted", async () => {
    useRuns.setState({
      runs: {
        "r-ok": { id: "r-ok", project_id: "p", stage: "build", mode: "solo", task_id: "T-101", status: "done", verdict: "PASSED", accepted: true, started_at: "2026-08-12T01:56:07Z" } as never,
        "r-dead": { id: "r-dead", project_id: "p", stage: "build", mode: "solo", task_id: "T-101", status: "failed", verdict: "ABORTED", started_at: "2026-08-12T01:57:30Z", ended_at: "2026-08-12T01:58:00Z" } as never,
      },
      events: {}, deltas: {}, reasoning: {}, spend: {},
    });
    render(<Now client={clientWith()} projectId="p" />);
    await waitFor(() => screen.getByTestId("now-view"));
    expect(screen.queryByTestId("now-failure")).toBeNull();
  });
});
