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

  it("shows nothing when the task has no body to show", async () => {
    render(<RunView runId="r-1" client={clientWith()} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("run-task-card")).toBeNull();
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
