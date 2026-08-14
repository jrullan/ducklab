import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import type { EngineClient, Run } from "../api/client";

const failed: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-015",
  status: "failed", verdict: "ABORTED", started_at: "2026-07-30T10:44:36Z",
  roster: { implementer: "dsv4flash", reviewer: "pato-sonnet" },
  failure: "panic: runtime error: slice bounds out of range [92:78]",
};

const clientWith = (over: Partial<EngineClient> = {}) =>
  ({
    run: vi.fn(() => Promise.resolve({ run: failed, events: [] })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() =>
      Promise.resolve([
        { id: "dsv4flash", provider: "openrouter", model: "d" },
        { id: "pato-sonnet", provider: "openrouter", model: "s" },
      ]),
    ),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    runStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
    // T-015 is blocked: this failed run is still its most recent.
    tasks: vi.fn(() =>
      Promise.resolve([{ id: "T-015", title: "Handle angle input", milestone: "M-07", status: "blocked" }]),
    ),
    ...over,
  }) as unknown as EngineClient;

// The moment you most want to change a setting and go again is while looking at
// the run that just failed. Doing it meant leaving for the board and finding the
// task by hand, which is enough friction that a re-run tends to carry the same
// settings that just failed.
describe("relaunching from the run view", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": failed }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("offers the controls on a run that failed", async () => {
    render(<RunView runId="r-1" client={clientWith()} />);
    await waitFor(() => expect(screen.getByTestId("relaunch")).toBeTruthy());
    // Pre-set to what just ran, so one change is one change.
    expect((screen.getByTestId("run-mode") as HTMLSelectElement).value).toBe("pair");
  });

  it("starts the same task with the changed settings", async () => {
    const client = clientWith();
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => screen.getByTestId("run-mode"));
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "solo" } });
    fireEvent.change(screen.getByTestId("run-max-tokens"), { target: { value: "1500000" } });
    fireEvent.click(screen.getByTestId("run-start"));

    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-015", {
        mode: "solo",
        ducklings: ["dsv4flash", "pato-sonnet"],
        maxTokens: 1500000,
        // The relaunch panel's caveat states the situation when the task was
        // finished by a later run; clicking past it is the explicit consent
        // the engine's accepted-task door asks for.
        redo: true,
      }),
    );
    expect((await screen.findByTestId("relaunch-link")).getAttribute("href")).toBe("#/runs/r-2");
  });

  it("says so when the engine refuses", async () => {
    const client = clientWith({
      runStart: vi.fn(() => Promise.reject(new Error("no duckling for role implementer"))),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => screen.getByTestId("run-start"));
    fireEvent.click(screen.getByTestId("run-start"));
    await waitFor(() =>
      expect(screen.getByTestId("relaunch-error").textContent).toContain("no duckling"),
    );
  });

  // Nothing to learn from yet, and starting a second run against a task already
  // being worked on is how two runs end up fighting over the same tree.
  it("offers nothing while the run is still going", async () => {
    const running = { ...failed, status: "running" as const, verdict: "" };
    useRuns.setState({ runs: { "r-1": running } });
    render(<RunView runId="r-1" client={clientWith({ run: vi.fn(() => Promise.resolve({ run: running, events: [] })) } as Partial<EngineClient>)} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("relaunch")).toBeNull();
  });

  // An accepted run is a decision that was made; re-running it is a board
  // action, not something to offer beside the result.
  it("offers nothing on an accepted run", async () => {
    const done = { ...failed, status: "done" as const, verdict: "PASSED", accepted: true };
    useRuns.setState({ runs: { "r-1": done } });
    render(<RunView runId="r-1" client={clientWith({ run: vi.fn(() => Promise.resolve({ run: done, events: [] })) } as Partial<EngineClient>)} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("relaunch")).toBeNull();
  });
});

// A run that failed and whose task was finished by a LATER run still reports
// `accepted: false` — it was never accepted — so the panel sat on every old
// failure in the project's history offering to redo committed work.
describe("a failure whose task was finished later", () => {
  const clientDone = (over: Partial<EngineClient> = {}) =>
    clientWith({
      tasks: vi.fn(() =>
        Promise.resolve([{ id: "T-015", title: "Handle angle input", milestone: "M-07", status: "accepted" }]),
      ),
      ...over,
    } as Partial<EngineClient>);

  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": failed }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("says the task is already done instead of offering the controls", async () => {
    render(<RunView runId="r-1" client={clientDone()} />);
    await waitFor(() => expect(screen.getByTestId("relaunch-done")).toBeTruthy());
    expect(screen.queryByTestId("run-start")).toBeNull();
    expect(screen.getByTestId("relaunch-done").textContent).toContain("already committed");
  });

  // Still allowed — a result can be regretted — just not the obvious next step.
  it("gives the controls when asked anyway", async () => {
    render(<RunView runId="r-1" client={clientDone()} />);
    fireEvent.click(await screen.findByTestId("relaunch-anyway"));
    expect(screen.getByTestId("run-start")).toBeTruthy();
  });

  // A newer run is already working on the task. Two runs against one task edit
  // the same tree at the same time, and the second one's diff contains the
  // first one's changes — which is not a result anybody can judge.
  it("says another run is already on it", async () => {
    const client = clientWith({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-015", title: "Handle angle input", milestone: "M-07", status: "in_progress" },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => expect(screen.getByTestId("relaunch-done")).toBeTruthy());
    expect(screen.getByTestId("relaunch-done").textContent).toContain("same tree at the same time");
    expect(screen.queryByTestId("run-start")).toBeNull();
  });
});

// A run watched live said "waiting for you — gate" and showed nothing to
// decide with. The legal actions travel only in the run fetch, which happened
// once on mount — the pause event updates status but carries no buttons. The
// controls appeared only after leaving for Now and coming back, because coming
// back is a mount.
describe("a run that pauses while being watched", () => {
  it("fetches the new actions when the stream pauses the run", async () => {
    const running = { ...failed, status: "running" as const, verdict: "", failure: undefined, next: ["abort"] };
    const paused = {
      ...running, status: "paused" as const, pending_kind: "gate",
      next: ["accept", "request_changes", "reject"],
    };
    useRuns.setState({ runs: { "r-1": running }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    // First fetch answers "running"; every one after the pause answers with
    // the decision, as the engine does — it updates the run before emitting.
    let pausedNow = false;
    const client = clientWith({
      run: vi.fn(() => Promise.resolve({ run: pausedNow ? paused : running, events: [] })),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("decision-card")).toBeNull();

    pausedNow = true;
    // The live stream flips the status, exactly as applyEvent does on
    // human_needed.
    useRuns.setState({
      runs: { "r-1": { ...running, status: "paused" as const, pending_kind: "gate" } },
    });
    await waitFor(() => expect(screen.getByTestId("decision-card")).toBeTruthy());
  });
});

// Judging a run means reading what was done against what was asked, and the
// task's own words lived only on the board — a different screen from the
// decision. The rail now carries them next to the gate and the budget.
describe("the task's description in the run view", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": failed }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("shows the task body beside the run", async () => {
    const client = clientWith({
      tasks: vi.fn(() =>
        Promise.resolve([
          {
            id: "T-015", title: "Handle angle input", milestone: "M-07", status: "blocked",
            body: "Process angle input:\n- Parse numeric value in degrees\n- Validate angle sum compatibility",
          },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    const card = await screen.findByTestId("run-task-card");
    expect(card.textContent).toContain("T-015 — Handle angle input");
    expect(card.textContent).toContain("Parse numeric value in degrees");
  });

  // A fresh chat rendered the PREVIOUS run's task card: the loader
  // early-returned on an empty task_id instead of clearing, so "T-076 —
  // OAuth test suite" stood over a chat about nothing of the sort.
  it("clears the previous run's task when the next run has none", async () => {
    const chatRun: Run = {
      id: "r-c", project_id: "p", stage: "chat", mode: "solo", task_id: "",
      status: "running", verdict: "", started_at: "2026-08-11T12:45:27Z",
      roster: { implementer: "k3" },
    };
    const client = clientWith({
      run: vi.fn((id: string) =>
        Promise.resolve({ run: id === "r-c" ? chatRun : failed, events: [] }),
      ),
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-015", title: "Handle angle input", milestone: "M-07", status: "blocked", body: "Parse numeric value in degrees" },
        ]),
      ),
    } as unknown as Partial<EngineClient>);
    useRuns.setState({
      runs: { "r-1": failed, "r-c": chatRun },
      events: {}, deltas: {}, reasoning: {}, spend: {},
    });
    const { rerender } = render(<RunView runId="r-1" client={client} />);
    await screen.findByTestId("run-task-card");
    rerender(<RunView runId="r-c" client={client} />);
    await waitFor(() => expect(screen.queryByTestId("run-task-card")).toBeNull());
  });

  // Reversed on purpose: hiding the card on an empty body hid it for exactly
  // the malformed task whose actions the person was hunting — the phantom
  // with a title and nothing else. An empty brief is a fact worth stating.
  it("says so when the task has no body, instead of hiding the card", async () => {
    render(<RunView runId="r-1" client={clientWith()} />);
    await waitFor(() => screen.getByTestId("run-task-card"));
    expect(screen.getByTestId("task-empty-body").textContent).toContain("no body");
  });
});

// T-076: a failed test+build relaunched with a different model came back as a
// bare BUILD — the phase the person watched die was skipped, and the chain
// they authorized vanished. A test relaunches as a test, chain included: the
// launcher's picks drive the TEST phase, the promised build keeps its own
// recorded settings.
describe("relaunching a failed test-first", () => {
  const failedTest: Run = {
    id: "r-t", project_id: "p", stage: "test", mode: "solo", task_id: "T-076",
    status: "failed", verdict: "FAILED", started_at: "2026-08-11T12:13:00Z",
    roster: { implementer: "luna" },
    chain_build: { mode: "pair", ducklings: ["glm52", "qwen38-max"], agent_turns: -1 },
  };

  beforeEach(() => {
    useRuns.setState({ runs: { "r-t": failedTest }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("relaunches the TEST with its chain, not a bare build", async () => {
    const testStart = vi.fn(() => Promise.resolve({ id: "r-3" }));
    const client = clientWith({
      run: vi.fn(() => Promise.resolve({ run: failedTest, events: [] })),
      testStart,
      tasks: vi.fn(() =>
        Promise.resolve([{ id: "T-076", title: "x", milestone: "M-08", status: "blocked" }]),
      ),
    } as unknown as Partial<EngineClient>);
    render(<RunView runId="r-t" client={client} />);
    await waitFor(() => screen.getByTestId("relaunch"));
    // The panel says what it will actually do.
    expect(screen.getByTestId("relaunch").textContent).toContain("Test T-076 again → then build");

    // The changed model must be one the fleet actually offers.
    fireEvent.change(screen.getByTestId("run-seat-0"), { target: { value: "dsv4flash" } });
    fireEvent.click(screen.getByTestId("run-start"));
    await waitFor(() => expect(testStart).toHaveBeenCalled());
    const [, taskId, , chain] = testStart.mock.calls[0]! as unknown as [string, string, string, Record<string, unknown>];
    expect(taskId).toBe("T-076");
    expect(chain.thenBuild).toBe(true);
    expect(chain.testDucklings).toContain("dsv4flash");
    // The promised build keeps ITS recorded seats and settings.
    expect(chain.mode).toBe("pair");
    expect(chain.ducklings).toEqual(["glm52", "qwen38-max"]);
    expect(chain.agentTurns).toBe(-1);
  });
});

// The tab panel folds on an active-tab click: under a long transcript the
// calls list is most of a screen, and the person asked for it back. Clicking
// the active tab hides the panel; clicking it again (or any tab) shows it.
describe("folding the tab panel", () => {
  it("hides the calls list on an active-tab click and brings it back", async () => {
    const client = clientWith({
      runLLM: vi.fn(() =>
        Promise.resolve([
          { seq: 1, duckling: "dsv4flash", role: "implementer", tokens_in: 10, tokens_out: 5 },
        ]),
      ),
    } as unknown as Partial<EngineClient>);
    useRuns.setState({ runs: { "r-1": failed }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    render(<RunView runId="r-1" client={client} />);
    const tab = await screen.findByTestId("tab-calls");
    fireEvent.click(tab); // switch to calls
    await screen.findByTestId("calls");
    fireEvent.click(tab); // active again → fold
    expect(screen.queryByTestId("calls")).toBeNull();
    fireEvent.click(tab); // unfold
    await screen.findByTestId("calls");
  });
});

// The right rail folds like the guide rail: hidden to a strip, remembered
// across runs — budget and gate are glanced at, and on a small window they
// tax every transcript line.
describe("hiding the run rail", () => {
  it("folds to a pill, comes back, and remembers", async () => {
    localStorage.removeItem("ducklab.runrail");
    useRuns.setState({ runs: { "r-1": failed }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    const first = render(<RunView runId="r-1" client={clientWith()} />);
    fireEvent.click(await first.findByTestId("run-rail-hide"));
    expect(first.queryByTestId("run-rail")).toBeNull();
    first.unmount();

    // A fresh mount honours the remembered preference; the pill restores.
    useRuns.setState({ runs: { "r-1": failed }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    const second = render(<RunView runId="r-1" client={clientWith()} />);
    expect(second.queryByTestId("run-rail")).toBeNull();
    fireEvent.click(await second.findByTestId("run-rail-pill"));
    expect(second.getByTestId("run-rail")).toBeTruthy();
    second.unmount();
    localStorage.removeItem("ducklab.runrail");
  });
});

// Any legal manipulation, offerable where the task is on screen: removing
// the task whose failed run you are LOOKING AT meant leaving for Work →
// Tasks and finding it again. The card now carries the engine's own next
// actions — remove appears exactly when the engine would allow it.
describe("the in-flight token estimate", () => {
  // With long streams allowed to live, a ten-minute architect read as
  // frozen zeros while its words visibly flowed — settled usage lands only
  // when a call completes. The meter now wears an estimate of the stream.
  it("estimates the current stream beside the settled number", async () => {
    const running = {
      ...failed, status: "running" as const, verdict: "",
      budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 10,
        limit: { usd: 5, tokens: 3000000, turns: 40, wallclock_s: 1800 } },
    };
    const streamed = "x".repeat(4000); // ~1k tokens of arrived text
    useRuns.setState({
      runs: { "r-1": running },
      events: { "r-1": [
        { type: "turn_start", run_id: "r-1", ts: "t", data: { round: 1, turn: 0, role: "architect", duckling: "k3" } },
      ] as never },
      deltas: { "r-1": { "1:0": streamed } },
      reasoning: {}, spend: {},
    });
    render(<RunView runId="r-1" client={clientWith({
      run: vi.fn(() => Promise.resolve({ run: running, events: [
        { type: "turn_start", run_id: "r-1", ts: "t", data: { round: 1, turn: 0, role: "architect", duckling: "k3" } },
      ] })),
    } as Partial<EngineClient>)} />);
    const est = await screen.findByTestId("meter-inflight");
    expect(est.textContent).toContain("~1.0k");
    expect(est.textContent).toContain("streaming");
  });
});

describe("the reseat offer on a weather pause", () => {
  // k3's provider timed out for ten minutes while its declared fallback sat
  // configured and unoffered. A provider-paused run whose failing duckling
  // has a fallback now offers the swap in one click — recorded, resumed.
  it("offers the declared fallback and posts the reseat", async () => {
    const paused = {
      ...failed, status: "paused" as const, verdict: "", pending_kind: "provider",
      next: ["resume", "abort"],
      budget: { usd: 0.1, tokens: 700000, turns: 1, wallclock_s: 60,
        limit: { usd: 5, tokens: 3000000, turns: 40, wallclock_s: 1800 } },
    };
    const reseated: string[] = [];
    const client = clientWith({
      run: vi.fn(() => Promise.resolve({ run: paused, events: [
        { type: "provider_retry", run_id: "r-1", ts: "t", data: { duckling: "k3", attempt: 2, error: "timeout" } },
      ] })),
      ducklings: vi.fn(() => Promise.resolve([
        { id: "k3", provider: "openrouter", model: "kimi", fallback: "dsv4flash" },
        { id: "dsv4flash", provider: "openrouter", model: "d" },
      ])),
      runReseat: vi.fn((_id: string, from: string, to: string) => {
        reseated.push(`${from}->${to}`);
        return Promise.resolve(paused);
      }),
    } as Partial<EngineClient>);
    useRuns.setState({ runs: { "r-1": paused }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    render(<RunView runId="r-1" client={client} />);
    const btn = await screen.findByTestId("reseat-button");
    expect(btn.textContent).toContain("dsv4flash");
    fireEvent.click(btn);
    await waitFor(() => expect(reseated).toEqual(["k3->dsv4flash"]));
  });

  it("offers nothing when no fallback is declared", async () => {
    const paused = {
      ...failed, status: "paused" as const, verdict: "", pending_kind: "provider",
      next: ["resume", "abort"],
    };
    const client = clientWith({
      run: vi.fn(() => Promise.resolve({ run: paused, events: [
        { type: "provider_retry", run_id: "r-1", ts: "t", data: { duckling: "k3", attempt: 2, error: "timeout" } },
      ] })),
      ducklings: vi.fn(() => Promise.resolve([{ id: "k3", provider: "openrouter", model: "kimi" }])),
    } as Partial<EngineClient>);
    useRuns.setState({ runs: { "r-1": paused }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    render(<RunView runId="r-1" client={client} />);
    await screen.findByTestId("run-view");
    expect(screen.queryByTestId("reseat-button")).toBeNull();
  });
});

describe("the calls/reply row", () => {
  // "default" while the architect sat at 19 calls of an invisible 24 — the
  // loop's own count now rides the card while the run is live.
  it("shows the live count against the real cap", async () => {
    const running = {
      ...failed, status: "running" as const, verdict: "",
      budget: { usd: 0.1, tokens: 700000, turns: 1, wallclock_s: 60,
        limit: { usd: 5, tokens: 3000000, turns: 40, wallclock_s: 1800 } },
    };
    useRuns.setState({
      runs: { "r-1": running },
      events: { "r-1": [
        { type: "reply_call", run_id: "r-1", ts: "t", data: { role: "architect", n: 19, max: 24 } },
      ] as never },
      deltas: {}, reasoning: {}, spend: {},
    });
    render(<RunView runId="r-1" client={clientWith({
      run: vi.fn(() => Promise.resolve({ run: running, events: [
        { type: "reply_call", run_id: "r-1", ts: "t", data: { role: "architect", n: 19, max: 24 } },
      ] })),
    } as Partial<EngineClient>)} />);
    await waitFor(() => expect(screen.getByTestId("calls-cap-value").textContent).toBe("19 / 24"));
  });

  it("says no cap when the loop runs lifted", async () => {
    const running = {
      ...failed, status: "running" as const, verdict: "",
      budget: { usd: 0.1, tokens: 700000, turns: 1, wallclock_s: 60,
        limit: { usd: 5, tokens: 3000000, turns: 40, wallclock_s: 1800 } },
    };
    useRuns.setState({
      runs: { "r-1": running },
      events: { "r-1": [
        { type: "reply_call", run_id: "r-1", ts: "t", data: { role: "architect", n: 31, max: 10000 } },
      ] as never },
      deltas: {}, reasoning: {}, spend: {},
    });
    render(<RunView runId="r-1" client={clientWith({
      run: vi.fn(() => Promise.resolve({ run: running, events: [
        { type: "reply_call", run_id: "r-1", ts: "t", data: { role: "architect", n: 31, max: 10000 } },
      ] })),
    } as Partial<EngineClient>)} />);
    await waitFor(() => expect(screen.getByTestId("calls-cap-value").textContent).toBe("31 / no cap"));
  });
});

describe("the run header names the task", () => {
  // The header said "T-015" and the WHY lived a scan away in the card. The
  // title rides the header now, truncated, whole on hover.
  it("shows the task title beside its id", async () => {
    render(<RunView runId="r-1" client={clientWith()} />);
    const title = await screen.findByTestId("run-task-title");
    expect(title.textContent).toContain("Handle angle input");
    expect(title.getAttribute("title")).toBe("Handle angle input");
  });
});

describe("the task's coverage on the run view card", () => {
  it("names the spec sections a covered task implements", async () => {
    const client = clientWith({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-015", title: "Covered", milestone: "M-1", status: "accepted", body: "done", implements: ["SPEC-007", "SPEC-008"] },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    const cov = await screen.findByTestId("task-coverage");
    expect(cov.textContent).toContain("covered by SPEC-007, SPEC-008");
  });

  it("states the debt when nothing covers it", async () => {
    const client = clientWith({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-015", title: "Uncovered", milestone: "M-1", status: "accepted", body: "done", spec_debt: true },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    const debt = await screen.findByTestId("task-spec-debt");
    expect(debt.textContent).toContain("spec-debt");
    expect(screen.queryByTestId("task-coverage")).toBeNull();
  });
});

describe("task actions on the run view card", () => {
  it("offers remove when the engine lists it, and executes it", async () => {
    const removed: string[] = [];
    const client = clientWith({
      // Bodiless on purpose: the phantom task is exactly the one whose card
      // used to hide (it was gated on task.body), taking remove with it.
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-015", title: "Phantom", milestone: "M-1", status: "todo", next: ["run", "remove"] },
        ]),
      ),
      taskRemove: vi.fn((_p: string, id: string) => {
        removed.push(id);
        return Promise.resolve({ removed: id });
      }),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => screen.getByTestId("task-empty-body"));
    fireEvent.click(await screen.findByTestId("task-remove"));
    fireEvent.click(await screen.findByTestId("task-remove-yes"));
    await waitFor(() => expect(removed).toEqual(["T-015"]));
  });

  it("offers nothing when the engine lists nothing", async () => {
    const client = clientWith({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-015", title: "Delivered", milestone: "M-1", status: "accepted", body: "done", next: [] },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => screen.getByTestId("run-task-card"));
    expect(screen.queryByTestId("task-remove")).toBeNull();
  });
});
