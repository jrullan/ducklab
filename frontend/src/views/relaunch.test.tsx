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
