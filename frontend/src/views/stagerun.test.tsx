import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import type { EngineClient, Run } from "../api/client";

// UX-4: a stage run's subject is a DOCUMENT. The view used to give it the
// code run's stack — diff, verify and candidates tabs, all empty by design —
// while the proposal being decided rendered nowhere.
describe("a spec run paused at its gate", () => {
  const specRun = {
    id: "r-spec", project_id: "p", stage: "spec", mode: "council",
    status: "paused", pending_kind: "gate", verdict: "UNVERIFIED",
    started_at: "2026-08-11T21:00:00Z",
    roster: { architect: "luna" },
    budget: { usd: 0.1, tokens: 50000, turns: 3, wallclock_s: 60,
      limit: { usd: 5, tokens: 3000000, turns: 40, wallclock_s: 1800 } },
  } as unknown as Run;

  const clientFor = (proposalRunId: string) =>
    ({
      run: vi.fn(() => Promise.resolve({ run: specRun, events: [] })),
      artifact: vi.fn(() =>
        Promise.resolve({
          markdown: "old approved spec",
          proposal: {
            run_id: proposalRunId,
            markdown: "## SPEC-030 — OAuth login\n\nThe app accepts Google sign-in.",
            diff: "",
          },
        }),
      ),
      runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
      runVerify: vi.fn(() => Promise.resolve("")),
      runCandidates: vi.fn(() => Promise.resolve([])),
      runLLM: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      tasks: vi.fn(() => Promise.resolve([])),
    }) as unknown as EngineClient;

  beforeEach(() => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("renders the proposed document where the decision happens", async () => {
    render(<RunView runId="r-spec" client={clientFor("r-spec")} />);
    const prop = await waitFor(() => screen.getByTestId("stage-proposal"));
    expect(prop.textContent).toContain("what Accept would approve");
    expect(prop.textContent).toContain("OAuth login");
  });

  // An older proposal still pending is someone else's question: showing it on
  // this run would attribute a document to a run that did not write it.
  it("shows nothing when the pending proposal belongs to another run", async () => {
    render(<RunView runId="r-spec" client={clientFor("r-someone-else")} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("stage-proposal")).toBeNull();
  });

  // Three tabs whose empty states truthfully said "none" taught the eye to
  // skip the bar entirely, including the one tab that mattered.
  it("offers only the calls tab — diff, verify and candidates are empty by design", async () => {
    render(<RunView runId="r-spec" client={clientFor("r-spec")} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.getByTestId("tab-calls")).toBeTruthy();
    for (const t of ["tab-diff", "tab-verify", "tab-candidates"]) {
      expect(screen.queryByTestId(t)).toBeNull();
    }
  });
});

// The header named the stage and assumed the reader carries the whole loop in
// their head. The cycle map states it: every station, this run's lit — asked
// for while an intake run said only "intake council · in progress".
describe("the cycle map in the run header", () => {
  const runWith = (stage: string) =>
    ({
      id: "r-x", project_id: "p", stage, mode: "solo",
      status: "running", started_at: "2026-08-12T10:00:00Z",
      budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0,
        limit: { usd: 5, tokens: 3000000, turns: 40, wallclock_s: 1800 } },
    }) as unknown as Run;

  const clientWith = (run: Run) =>
    ({
      run: vi.fn(() => Promise.resolve({ run, events: [] })),
      artifact: vi.fn(() => Promise.resolve({ markdown: "", proposal: null })),
      runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
      runVerify: vi.fn(() => Promise.resolve("")),
      runCandidates: vi.fn(() => Promise.resolve([])),
      runLLM: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      tasks: vi.fn(() => Promise.resolve([])),
    }) as unknown as EngineClient;

  it.each([
    ["intake", "intake"],
    ["spec", "spec"],
    ["test", "build"], // the failing test is the first half of building
  ])("a %s run lights the %s station", async (stage, station) => {
    render(<RunView runId="r-x" client={clientWith(runWith(stage))} />);
    const map = await waitFor(() => screen.getByTestId("cycle-map"));
    expect(map.dataset.at).toBe(station);
    expect(map.textContent).toContain("release"); // the whole cycle is visible
    // The one station whose artifact doesn't share its name says so.
    expect(map.textContent).toContain("intake (reqs)");
  });

  it("a chat run has no station and shows no map", async () => {
    render(<RunView runId="r-x" client={clientWith(runWith("chat"))} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("cycle-map")).toBeNull();
  });

  // Reversed from "triage lights the plan station": a triage is not IN the
  // pipeline — its classifications feed the plan, but the map with plan lit
  // read as "this run is planning", and its loudest chip was "unverified"
  // for a run that has no gate by design. The header says what the run did.
  it("a triage run shows its outcome instead of the map", async () => {
    render(<RunView runId="r-x" client={clientWith(runWith("triage"))} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("cycle-map")).toBeNull();
    expect(screen.getAllByTestId("status-chip").some((c) => /triag/i.test(c.textContent ?? ""))).toBe(true);
  });

  // A non-code run's only panel is the model-calls list — debugging
  // material. It opened by default under every triage and chat; folded now,
  // one click away, and the person's own choice still wins.
  // WHO is working, in the run header where it cannot scroll away — the
  // sticky turn-header attempt pinned itself mid-list instead.
  it("names the active duckling in a live run's header", async () => {
    const run = runWith("triage");
    const events = [
      { type: "turn_start", seq: 1, data: { role: "triager", duckling: "k3", round: 1, turn: 0 } },
    ];
    const client = ({
      ...clientWith(run),
      run: vi.fn(() => Promise.resolve({ run, events })),
    }) as unknown as EngineClient;
    render(<RunView runId="r-x" client={client} />);
    await waitFor(() => screen.getByTestId("run-active-duckling"));
    expect(screen.getByTestId("run-active-duckling").textContent).toContain("k3");
    expect(screen.getByTestId("run-active-duckling").textContent).toContain("triager");
  });

  it("starts with the calls panel folded on a triage", async () => {
    render(<RunView runId="r-x" client={clientWith(runWith("triage"))} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("calls-empty")).toBeNull();
    fireEvent.click(screen.getByTestId("tab-calls"));
    await waitFor(() => expect(screen.queryByTestId("calls-empty")).not.toBeNull());
  });
});
