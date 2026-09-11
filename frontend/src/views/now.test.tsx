import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { Now } from "./Now";
import { useRuns } from "../store/runs";
import type { Artifact, EngineClient, Run } from "../api/client";

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

  it("keeps every terminal chat out of every Now bucket", async () => {
    seed([
      { ...base, id: "chat-done", stage: "chat", status: "done", verdict: "PASSED", pending_kind: "chat", next: [] },
      { ...base, id: "chat-failed", stage: "chat", status: "failed", verdict: "FAILED", pending_kind: undefined, next: [], failure: "stream read: context canceled" },
      { ...base, id: "chat-aborted", stage: "chat", status: "failed", verdict: "ABORTED", pending_kind: undefined, next: [] },
    ]);
    render(<Now client={clientWith()} projectId="p" />);
    await screen.findByTestId("now-quiet");
    expect(screen.queryByTestId("now-running")).toBeNull();
    expect(screen.queryByTestId("now-waiting")).toBeNull();
    expect(screen.queryByTestId("now-failures")).toBeNull();
    expect(screen.queryByTestId("now-reopened")).toBeNull();
    expect(screen.queryByTestId("now-footer")).toBeNull();
    expect(screen.queryByText(/chat-(done|failed|aborted)/)).toBeNull();
  });

  // B-251: the terminal variant alone would have passed before the terminal-chat
  // filter existed, because a dead chat never counted as in flight. The pair
  // pins the interaction: a live conversation about the task keeps the
  // reopened card quiet exactly as any in-flight run does; a dead one does not.
  it("lets a live chat about the task keep a reopened report quiet, and a dead one not", async () => {
    const reopened = { id: "B-reopened", title: "Still broken", severity: "high", status: "in_progress", task_id: "T-026", source: "desktop", created_at: "2026-07-30T23:00:00Z", updated_at: "2026-07-31T01:45:00Z", next: ["fixed"] };
    seed([{ ...base, id: "chat-live", stage: "chat", task_id: "T-026", status: "paused", verdict: "", pending_kind: "chat", next: ["reply", "end"] }]);
    const live = render(<Now client={clientWith({ bugs: vi.fn(() => Promise.resolve([reopened])) } as Partial<EngineClient>)} projectId="p" />);
    await screen.findByTestId("now-view");
    expect(screen.queryByTestId("now-reopened-card")).toBeNull();
    live.unmount();

    seed([{ ...base, id: "chat-dead", stage: "chat", task_id: "T-026", status: "failed", verdict: "FAILED", next: [] }]);
    render(<Now client={clientWith({ bugs: vi.fn(() => Promise.resolve([reopened])) } as Partial<EngineClient>)} projectId="p" />);
    expect(await screen.findByTestId("now-reopened-card")).toBeTruthy();
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

  it("puts decisions before ambient running work", async () => {
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
      expect(section.compareDocumentPosition(running) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0);
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

  it("renders one launcher, not a second guide card, for the same ready task", async () => {
    const task = { id: "T-048", title: "Clipboard inspection", milestone: "M-07", status: "todo", next: ["test_first", "run"] };
    const client = clientWith({
      taskNext: vi.fn(() => Promise.resolve(task)),
      projectNext: vi.fn(() => Promise.resolve([
        { kind: "task", id: "test-first", ref: "T-048", action: "Start T-048", reason: "ready" },
        { kind: "stage", id: "spec-debt", ref: "spec", action: "Teach the spec", reason: "one task wears spec-debt" },
      ])),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    expect(await screen.findByTestId("now-next")).toHaveTextContent("T-048 — Clipboard inspection");
    expect(screen.queryByTestId("now-next-group-task")).toBeNull();
    expect(screen.getAllByText(/T-048/)).toHaveLength(1);
    expect(screen.getByTestId("now-next-group-stage")).toHaveTextContent("Teach the spec");
  });

  it("shows one chain estimate derived from per-run history", async () => {
    const client = clientWith({
      taskNext: vi.fn(() => Promise.resolve({ id: "T-048", title: "Clipboard inspection", milestone: "M-07", status: "todo", next: ["test_first", "run"] })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {}, test_mode: "solo", build_mode: "pair" })),
      report: vi.fn(() => Promise.resolve({ rows: [
        { key: "solo", cost_usd: 0.30, runs: 3 },
        { key: "pair", cost_usd: 0.80, runs: 2 },
      ] })),
    } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const summary = await screen.findByTestId("tdd-summary");
    await waitFor(() => expect(summary).toHaveTextContent("estimated chain ~$0.50 (test ~$0.10 + build ~$0.40)"));
    expect(screen.getAllByText(/\$0\.50/)).toHaveLength(1);
  });

  it("uses calm status language and a context-neutral note on a ready task", async () => {
    const client = clientWith({
      taskNext: vi.fn(() => Promise.resolve({ id: "T-048", title: "Clipboard inspection", milestone: "M-07", status: "todo", next: ["test_first", "run"] })),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const clear = await screen.findByTestId("now-clear");
    expect(clear).toHaveTextContent("No decisions waiting");
    expect(clear.parentElement).toHaveAttribute("aria-live", "polite");
    expect(clear.parentElement).not.toHaveTextContent("0 failed");
    expect(clear.parentElement).not.toHaveTextContent("0 to verify");
    expect(screen.queryByText("Nothing needs you.")).toBeNull();
    expect(screen.getByPlaceholderText("Anything this run should know?")).toBeInTheDocument();
  });

  it("explains a document run and the consequence of starting current-plan work beside it", async () => {
    seed([{
      ...base, id: "r-plan", stage: "plan", task_id: "", mode: "solo", status: "running",
      verdict: "", pending_kind: undefined, roster: { architect: "beelink-local" },
    }]);
    useRuns.setState({ spend: {
      "r-plan": { usd: 0.12, tokens: 64000, turns: 3, wallclock_s: 125,
        limit: { usd: 5, tokens: 5000000, turns: 40, wallclock_s: 1800 }, ducklings: {} },
    } });
    const client = clientWith({
      taskNext: vi.fn(() => Promise.resolve({ id: "T-048", title: "Clipboard inspection", milestone: "M-07", status: "todo", next: ["test_first", "run"] })),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const row = await screen.findByTestId("now-running-row");
    expect(row).toHaveTextContent("Drafting the plan");
    expect(row).toHaveTextContent("beelink-local");
    expect(row).toHaveTextContent("2m05s");
    expect(row).toHaveTextContent("64.0k / 5.0M tokens · 3 model turns · $0.1200");
    expect(within(row).getByRole("link", { name: "Open run" })).toHaveAttribute("href", "#/runs/r-plan");
    const ready = screen.getByTestId("now-next");
    expect(screen.getByTestId("now-parallel-note")).toHaveTextContent("review it first if it may change this task");
    expect(row.compareDocumentPosition(ready) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("renders next steps as a native Now section", async () => {
    const client = clientWith({
      projectNext: vi.fn(() => Promise.resolve([
        { kind: "task", id: "test-first", ref: "T-029", action: "Start T-029 (test first, then build)", reason: "it is ready" },
      ])),
    } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const nextSteps = await screen.findByTestId("now-next-steps");
    expect(nextSteps.tagName).toBe("SECTION");
    expect(screen.getAllByTestId("now-next-steps")).toHaveLength(1);
  });

  // B-282: the guide's typed steps are rendered as what they are — action
  // cards grouped by kind, each carrying its consequence and its cost — and
  // the install step is not rendered at all: the sidebar footer owns
  // landed-vs-serving (T-228), and one truth gets one surface.
  it("groups next steps by kind as action cards with reason and cost, and leaves install to the footer", async () => {
    const client = clientWith({
      projectNext: vi.fn(() => Promise.resolve([
        { kind: "project", id: "install", action: "Reinstall ducklab — the repo is 3 commit(s) ahead of the running engine", reason: "run `make install`" },
        { kind: "task", id: "test-first", ref: "T-029", action: "Start T-029 (test first, then build)", reason: "it is the next task whose dependencies are all accepted" },
        { kind: "bug", id: "verify-bug", ref: "B-1", refs: ["B-1", "B-2"], action: "Verify 2 fixed bugs — confirm each fix answers its report", reason: "2 fixes are waiting for human verification" },
      ])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {}, build_mode: "pair" })),
      report: vi.fn(() => Promise.resolve({ rows: [{ key: "pair", cost_usd: 0.94, runs: 3 }] })),
    } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const section = await screen.findByTestId("now-next-steps");
    expect(section.textContent).not.toContain("Reinstall");
    const bugs = await screen.findByTestId("now-next-group-bug");
    const tasks = await screen.findByTestId("now-next-group-task");
    expect(bugs.compareDocumentPosition(tasks) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByTestId("now-next-group-project")).toBeNull();
    const cards = screen.getAllByTestId("now-next-step");
    expect(cards).toHaveLength(2);
    const task = cards.find((c) => c.getAttribute("data-kind") === "task")!;
    expect(within(task).getByRole("link").textContent).toBe("Start T-029");
    expect(task.textContent).toContain("next task whose dependencies are all accepted");
    await waitFor(() => expect(within(task).getByTestId("now-next-step-cost").textContent).toContain("opens pair · ~$0.3133 per run (3 samples)"));
    const bug = cards.find((c) => c.getAttribute("data-kind") === "bug")!;
    expect(bug.textContent).toContain("2 fixes are waiting");
    expect(within(bug).queryByTestId("now-next-step-cost")).toBeNull();
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
    expect(card.textContent).toContain("tasks covered: 1/2");
    expect(card.textContent).toContain("ownership lanes declared: 2/2");
    expect(card.textContent).toContain("ownership collisions in proposed plan: 1");
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

  it("counts an amendment against approved tasks without treating milestones as tasks", async () => {
    const tasks = Array.from({ length: 38 }, (_, index) => ({
      id: `T-${String(index + 1).padStart(3, "0")}`,
      title: `task ${index + 1}`,
      body: "",
      fields: { owns: `src/${index + 1}.rs` },
    }));
    const milestones = Array.from({ length: 6 }, (_, index) => ({
      id: `M-${String(index + 1).padStart(2, "0")}`,
      title: `milestone ${index + 1}`,
      body: "",
      fields: {},
    }));
    const artifact: Artifact = {
      kind: "plan", version: 1, approved: true, markdown: "", sections: [...milestones, ...tasks],
      proposal: {
        diff: "+ one task",
        sections: [...milestones, ...tasks, { id: "T-039", title: "new task", body: "", fields: { owns: "src/new.rs" } }],
      },
    };
    const client = clientWith({
      artifact: vi.fn(() => Promise.resolve(artifact)),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: ["plan"] })),
    } as Partial<EngineClient>);

    render(<Now client={client} projectId="p" />);
    const card = await screen.findByTestId("now-plan-card");
    expect(card).toHaveTextContent("adds 1, changes 0, and removes 0 task from the approved 38-task plan");
    expect(card).not.toHaveTextContent("44-task");
    expect(card).toHaveTextContent("changed tasks covered: 1/1");
  });

  it("shows one decision for one paused plan and keeps two aborted runs in history", async () => {
    const planRun: Run = {
      ...base,
      id: "r-plan",
      stage: "plan",
      task_id: "",
      verdict: "PASSED",
      next: ["accept", "request_changes", "reject"],
    };
    seed([
      planRun,
      { ...base, id: "r-aborted-1", task_id: "T-001", status: "failed", verdict: "ABORTED", pending_kind: undefined, next: [], ended_at: "2026-07-31T08:00:00Z" },
      { ...base, id: "r-aborted-2", task_id: "T-002", status: "failed", verdict: "ABORTED", pending_kind: undefined, next: [], ended_at: "2026-07-31T08:30:00Z" },
    ]);
    const client = clientWith({
      artifact: vi.fn(() => Promise.resolve({
        kind: "plan", version: 1, approved: true, markdown: "", sections: [],
        proposal: { run_id: "r-plan", diff: "+ proposal", sections: [{ id: "T-001", title: "one", body: "", fields: { owns: "src/one.rs" } }] },
      })),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: ["plan"] })),
      projectNext: vi.fn(() => Promise.resolve([
        { kind: "run", id: "decide", ref: "r-plan", action: "Decide plan", reason: "waiting" },
        { kind: "task", id: "build", ref: "T-003", action: "Start T-003", reason: "ready" },
      ])),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-revision" })),
    } as unknown as Partial<EngineClient>);

    render(<Now client={client} projectId="p" />);
    expect(await screen.findByTestId("now-waiting-count")).toHaveTextContent("1");
    expect(screen.getAllByTestId("now-waiting-card")).toHaveLength(1);
    expect(screen.queryByTestId("now-plan-card")).toBeNull();
    expect(screen.queryByTestId("now-next-steps")).toBeNull();
    expect(screen.queryByTestId("now-failures")).toBeNull();
    expect(screen.getByTestId("now-waiting-card")).not.toHaveTextContent("requirements");
    expect(screen.getByTestId("now-waiting-card")).toHaveTextContent("plan proposal is ready");
    expect(screen.getByTestId("now-accept")).toBeInTheDocument();
    expect(screen.getByTestId("now-request-changes")).toBeInTheDocument();
    expect(screen.getByTestId("now-reject")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("now-request-changes"));
    fireEvent.change(screen.getByLabelText("requested changes"), { target: { value: "split the last acceptance slice" } });
    fireEvent.click(screen.getByRole("button", { name: "Start revision" }));
    await waitFor(() => expect(client.stageStart).toHaveBeenCalledWith("p", "plan", { revise: "split the last acceptance slice" }));
  });

  it("does not resurrect a consumed plan from an older artifact response", async () => {
    let resolveOld!: (artifact: Artifact) => void;
    const old = new Promise<Artifact>((resolve) => { resolveOld = resolve; });
    const approved: Artifact = { kind: "plan", version: 1, approved: true, markdown: "", sections: [] };
    const proposal: Artifact = {
      ...approved,
      approved: false,
      proposal: { diff: "+ stale", sections: [{ id: "T-201", title: "stale", body: "", fields: {} }] },
    };
    const client = clientWith({
      artifact: vi.fn((projectId: string) => projectId === "old" ? old : Promise.resolve(approved)),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
    } as Partial<EngineClient>);

    const view = render(<Now client={client} projectId="old" />);
    view.rerender(<Now client={client} projectId="current" />);
    await waitFor(() => expect(client.artifact).toHaveBeenCalledWith("current", "plan"));
    await act(async () => { resolveOld(proposal); await old; });

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
    expect(footer.textContent).toContain("Today $1.50");
    expect(footer.textContent).toContain("project total $10.50");
    expect(footer.textContent).toContain("1 of 2 finished runs passed");
    expect(footer.textContent).toContain("including experimental and unsuccessful attempts");
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
    body: "Steps: drag the red vertex; the angle field stays read-only.",
    created_at: "2026-07-30T23:00:00Z", updated_at: "2026-07-31T01:35:00Z",
    next: ["verified", "in_progress"],
  };
  const more = (n: number) => Array.from({ length: n }, (_, i) => ({ ...fixedBug, id: `B-${100 + i}`, title: `Report ${i}` }));

  // B-281: N fixed reports are one queue, not N decisions. Now's scroll must
  // not grow with the verification backlog.
  it("folds every fixed report into one ledger card whose drawer lists compact rows", async () => {
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve(more(24))) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    const ledger = await screen.findByTestId("now-verify-ledger");
    expect(screen.getAllByTestId("now-verify-ledger")).toHaveLength(1);
    expect(screen.queryByTestId("now-verify-row")).toBeNull();
    expect(ledger.textContent).toContain("24 fixed bugs await your verification");
    // The honest caveat, from the project that taught it: 21 accepted tasks
    // against a syntax gate and the feature never worked.
    expect(ledger.textContent).toContain("may prove much less");
    expect(ledger.textContent).toContain("the test that proves an internal fix");
    fireEvent.click(screen.getByTestId("now-verify-open"));
    const drawer = await screen.findByTestId("now-verify-drawer");
    const rows = within(drawer).getAllByTestId("now-verify-row");
    expect(rows).toHaveLength(24);
    expect(rows[0]!.textContent).toContain("B-100");
    expect(rows[0]!.textContent).toContain("fixed by T-026");
    expect(within(rows[0]!).getByTestId("now-verify-yes")).toBeTruthy();
    expect(within(rows[0]!).getByTestId("now-verify-no")).toBeTruthy();
  });

  // B-216, absorbed: an internal fix is proven by the test its accepted diff
  // added. The expanded row names that test and the exact command that runs
  // it, read from the run record, and the accept affordance says so.
  it("names the pinning test and its command when the accepted diff carries one", async () => {
    seed([{ ...base, id: "r-fix", task_id: "T-026", status: "done", verdict: "PASSED", accepted: true,
      commit_sha: "b01f4240deadbeef", pending_kind: undefined, started_at: "2026-07-31T01:20:20Z" }]);
    const runDiff = vi.fn(() => Promise.resolve({ diff: [
      "diff --git a/internal/service/recovery_test.go b/internal/service/recovery_test.go",
      "--- a/internal/service/recovery_test.go",
      "+++ b/internal/service/recovery_test.go",
      "@@ -10,0 +11,2 @@",
      "+func TestProjectRecoveryDoors(t *testing.T) {",
      "+}",
      "diff --git a/internal/service/recovery.go b/internal/service/recovery.go",
      "--- a/internal/service/recovery.go",
      "+++ b/internal/service/recovery.go",
      "@@ -1 +1 @@",
      "+// keep the id",
    ].join("\n") }));
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([fixedBug])), runDiff } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-open"));
    fireEvent.click(await screen.findByTestId("now-verify-expand"));
    const proof = await screen.findByTestId("now-verify-proof");
    expect(runDiff).toHaveBeenCalledWith("r-fix");
    expect(proof.textContent).toContain("a test that pins it");
    expect(proof.textContent).toContain("commit b01f424");
    expect(within(proof).getByTestId("shell-cmd").textContent).toBe("go test ./internal/service -run '^TestProjectRecoveryDoors$'");
    expect(screen.getByTestId("now-verify-yes").textContent).toContain("The test proves it");
    expect(screen.getByTestId("now-verify-row").getAttribute("data-proof")).toBe("test");
    expect(screen.queryByTestId("now-verify-try")).toBeNull();
    expect(screen.queryByText(/Try what the report describes/)).toBeNull();
    const guide = screen.getByTestId("now-verify-guide");
    expect(guide.textContent).toContain("recovery.go");
  });

  // Only a fix with observable behaviour gets the "try it" phrasing, and then
  // the row carries the report's own steps and the files the fix touched.
  it("falls back to the report's steps when the accepted diff carries no test", async () => {
    seed([{ ...base, id: "r-fix", task_id: "T-026", status: "done", verdict: "PASSED", accepted: true,
      pending_kind: undefined, started_at: "2026-07-31T01:20:20Z" }]);
    const runDiff = vi.fn(() => Promise.resolve({ diff: "diff --git a/ui/vertex.ts b/ui/vertex.ts\n--- a/ui/vertex.ts\n+++ b/ui/vertex.ts\n@@ -1 +1 @@\n+editable = true\n" }));
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([fixedBug])), runDiff } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-open"));
    fireEvent.click(await screen.findByTestId("now-verify-expand"));
    const guide = await screen.findByTestId("now-verify-guide");
    await screen.findByTestId("now-verify-try");
    expect(guide.textContent).toContain("landed without a pinning test");
    expect(guide.textContent).toContain("drag the red vertex");
    expect(guide.textContent).toContain("ui/vertex.ts");
    expect(screen.getByTestId("now-verify-yes").textContent).toBe("Verified — it works");
    expect(screen.queryByTestId("now-verify-proof")).toBeNull();
  });

  // A Rust or C fix whose tests Ducklab cannot run for the person must not be
  // told "no pinning test": the tests exist, and the row says which files.
  it("says which tests landed when it cannot derive a command, and never claims eyes-only", async () => {
    seed([{ ...base, id: "r-fix", task_id: "T-026", status: "done", verdict: "PASSED", accepted: true,
      pending_kind: undefined, started_at: "2026-07-31T01:20:20Z" }]);
    const runDiff = vi.fn(() => Promise.resolve({ diff: "diff --git a/tests/check_probe.c b/tests/check_probe.c\n--- /dev/null\n+++ b/tests/check_probe.c\n@@ -0,0 +1,2 @@\n+static void test_probe_reports_reason(void) {\n+}\n" }));
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([fixedBug])), runDiff } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-open"));
    fireEvent.click(await screen.findByTestId("now-verify-expand"));
    const section = await screen.findByTestId("now-verify-unknown-tests");
    expect(section.textContent).toContain("tests/check_probe.c");
    expect(section.textContent).toContain("cannot run for you yet");
    expect(screen.queryByTestId("now-verify-try")).toBeNull();
    expect(screen.queryByText(/only proof is your eyes/)).toBeNull();
    expect(screen.getByTestId("now-verify-yes").textContent).toBe("Verified — it works");
  });

  it("says so when the fix has no task or no accepted run in the record", async () => {
    const orphan = { ...fixedBug, id: "B-004", task_id: undefined };
    const unrecorded = { ...fixedBug, id: "B-005", task_id: "T-999" };
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([orphan, unrecorded])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-open"));
    const [first, second] = screen.getAllByTestId("now-verify-expand");
    fireEvent.click(first!);
    expect((await screen.findByTestId("now-verify-guide")).textContent).toContain("No task is recorded for this fix");
    fireEvent.click(second!);
    await waitFor(() => expect(screen.getAllByTestId("now-verify-guide").at(-1)!.textContent).toContain("No accepted run for T-999 is in the record"));
  });

  it("moves it with the person's verdict, either way, and the row leaves the ledger", async () => {
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([fixedBug, { ...fixedBug, id: "B-006" }])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-open"));
    fireEvent.click(screen.getAllByTestId("now-verify-yes")[0]!);
    await waitFor(() => expect(client.moveBug).toHaveBeenCalledWith("p", "B-003", "verified"));
    await waitFor(() => expect(screen.getAllByTestId("now-verify-row")).toHaveLength(1));
    fireEvent.click(screen.getByTestId("now-verify-no"));
    await waitFor(() => expect(client.moveBug).toHaveBeenCalledWith("p", "B-006", "in_progress"));
  });

  it("offers only what the engine states", async () => {
    const stuck = { ...fixedBug, next: [] as string[] };
    const client = clientWith({ bugs: vi.fn(() => Promise.resolve([stuck])) } as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("now-verify-open"));
    await screen.findByTestId("now-verify-row");
    expect(screen.queryByTestId("now-verify-yes")).toBeNull();
    expect(screen.queryByTestId("now-verify-no")).toBeNull();
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

// B-394: the ready-task launcher seeded its seats from the GLOBAL saved
// line-up (GET /v1/defaults/modes), labelled them "picked now" and sent them
// as an explicit request, so a project whose roster pins beelink-local / luna
// / dsv4flash launched terra / glm52 / k3 (Neocapture r-20260911-033800-axwh).
// The launcher seeds from the project's resolved roster for the mode it will
// run; untouched seats travel empty so the engine's roster resolution wins.
describe("Now launcher seats come from the project roster, not the global line-up", () => {
  const projectPair = [
    { role: "advisor", duckling: "luna", source: "project mode seat" },
    { role: "architect", duckling: "k3", source: "global mode seat" },
    { role: "consultant", duckling: "beelink-local", source: "project pin" },
    { role: "implementer", duckling: "beelink-local", source: "project mode seat" },
    { role: "judge", duckling: "beelink-local", source: "project pin" },
    { role: "reviewer", duckling: "dsv4flash", source: "project mode seat" },
    { role: "scribe", duckling: "beelink-local", source: "project pin" },
    { role: "triager", duckling: "beelink-local", source: "project pin" },
  ];
  const fleet = ["beelink-local", "luna", "dsv4flash", "terra", "k3", "glm52"].map((id) => ({ id, provider: "fake", model: id }));

  it("shows the project's pair seats with their provenance and launches without overriding them", async () => {
    const roster = vi.fn((_p: string, mode?: string) => Promise.resolve({ entries: mode === "pair" ? projectPair : [] }));
    const client = clientWith({
      taskNext: vi.fn(() => Promise.resolve({ id: "T-012", title: "Define the common capture backend interface", milestone: "M-03", status: "todo", next: ["run"] })),
      ducklings: vi.fn(() => Promise.resolve(fleet)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: { pair: ["terra", "glm52", "k3"] }, build_mode: "pair", test_mode: "solo" })),
      roster,
    } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    await screen.findByTestId("now-next");
    await waitFor(() => expect(roster).toHaveBeenCalledWith("p", "pair"));
    fireEvent.click(await screen.findByTestId("launch-modal-trigger"));
    const chips = await screen.findAllByTestId("seat-chip");
    await waitFor(() => expect(chips.map((c) => c.textContent).join(" | ")).toContain("beelink-local"));
    const text = chips.map((c) => c.textContent).join(" | ");
    expect(text).toContain("implementerbeelink-localproject");
    expect(text).toContain("advisorlunaproject");
    expect(text).toContain("reviewerdsv4flashproject");
    expect(text).not.toContain("picked now");
    expect(text).not.toContain("terra");
    fireEvent.click(screen.getByText("Run T-012"));
    await waitFor(() => expect(client.runStart).toHaveBeenCalled());
    const opts = (client.runStart as ReturnType<typeof vi.fn>).mock.calls[0]![2] as { mode: string; ducklings: string[] };
    expect(opts.mode).toBe("pair");
    expect(opts.ducklings).toEqual([]);
  });

  it("opening adjust seats & caps on the TDD chain without touching a seat sends no overrides", async () => {
    const roster = vi.fn((_p: string, mode?: string) => Promise.resolve({ entries: mode === "pair" ? projectPair : [] }));
    const testStart = vi.fn(() => Promise.resolve({ id: "r-10" }));
    const client = clientWith({
      taskNext: vi.fn(() => Promise.resolve({ id: "T-012", title: "Define the common capture backend interface", milestone: "M-03", status: "todo", next: ["test_first", "run"] })),
      ducklings: vi.fn(() => Promise.resolve(fleet)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: { pair: ["terra", "glm52", "k3"] }, build_mode: "pair", test_mode: "solo" })),
      roster,
      testStart,
    } as unknown as Partial<EngineClient>);
    render(<Now client={client} projectId="p" />);
    await screen.findByTestId("tdd-block");
    await waitFor(() => expect(roster).toHaveBeenCalledWith("p", "pair"));
    fireEvent.click(screen.getByTestId("tdd-tune"));
    await screen.findByTestId("tdd-tuning");
    await waitFor(() => expect(screen.getAllByTestId("seat-chip").map((c) => c.textContent).join(" | ")).toContain("beelink-local"));
    fireEvent.click(screen.getByTestId("tdd-start"));
    await waitFor(() => expect(testStart).toHaveBeenCalled());
    const opts = (testStart.mock.calls[0] as unknown[])[3] as Record<string, unknown>;
    expect(opts.testDucklings).toEqual([]);
    expect(opts.ducklings).toEqual([]);
    expect(opts.testSeats).toEqual({});
    expect(opts.seats).toEqual({});
  });
});
