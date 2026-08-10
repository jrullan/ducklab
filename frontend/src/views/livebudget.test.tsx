import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient, type Run } from "../api/client";

const run: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-012",
  status: "running", verdict: "", started_at: "2026-07-30T02:53:52Z",
  roster: { implementer: "pato-local", reviewer: "pato-atom" },
  budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0 },
};

const client = new EngineClient({
  baseUrl: "http://engine",
  token: "t",
  fetchFn: (async () =>
    new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })) as never,
});

const budgetEvent = (data: Record<string, unknown>) =>
  ({ type: "budget", run_id: "r-1", data }) as never;

describe("the run's budget while it is running", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": run }, spend: {}, events: {}, deltas: {}, reasoning: {} });
  });

  // The totals only reached the run record when the run ended, so the meter read
  // zero for however long the work took and jumped to the final number at
  // exactly the moment knowing it stopped being useful.
  it("moves while the run is going", async () => {
    useRuns.getState().applyEvent(
      budgetEvent({
        usd: 0.42, tokens: 128000, turns: 3, wallclock_s: 90,
        limit: { usd: 2, tokens: 1500000, turns: 24, wallclock_s: 3600 },
        ducklings: {},
      }),
    );
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() =>
      expect(screen.getAllByTestId("budget-meter")[0]!.textContent).toContain("128"),
    );
  });

  // The limits were hardcoded, so a run started with a raised ceiling was drawn
  // against one it did not have: the bar looked full at a quarter spent.
  it("draws against the ceiling this run actually got", async () => {
    useRuns.getState().applyEvent(
      budgetEvent({
        usd: 0, tokens: 400000, turns: 1, wallclock_s: 10,
        limit: { usd: 2, tokens: 1500000, turns: 24, wallclock_s: 3600 },
        ducklings: {},
      }),
    );
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => expect(screen.getAllByTestId("budget-meter").length).toBeGreaterThan(0));
    expect(screen.getAllByTestId("budget-meter")[0]!.textContent).toContain("1.5M");
  });

  // One tracker serves every duckling and every turn, so the run total cannot
  // say which model is burning it — and in a two-model mode that is the only
  // question worth asking.
  it("breaks the spend down by model", async () => {
    useRuns.getState().applyEvent(
      budgetEvent({
        usd: 0.9, tokens: 300000, turns: 4, wallclock_s: 120,
        limit: { usd: 2, tokens: 1500000, turns: 24, wallclock_s: 3600 },
        ducklings: {
          "pato-local": { calls: 14, tokens: 250000, cost_usd: 0 },
          "pato-atom": { calls: 2, tokens: 50000, cost_usd: 0.9 },
        },
      }),
    );
    render(<RunView runId="r-1" client={client} />);
    const box = await screen.findByTestId("spend-by-duckling");
    expect(box.textContent).toContain("pato-local");
    expect(box.textContent).toContain("pato-atom");
    // Biggest spender first: that is who you would act on.
    expect(box.textContent!.indexOf("pato-local")).toBeLessThan(
      box.textContent!.indexOf("pato-atom"),
    );
  });

  // The line-up is known the moment the run starts — it is the roster — but
  // rows only appeared as each model's first call landed, so a pair run
  // opened showing nobody and the second seat materialised minutes later.
  // Every seat shows from the first frame, wearing its role; a seat that has
  // not spent says so instead of not existing.
  it("seats the whole roster from the start, each with its role", async () => {
    useRuns.getState().applyEvent(
      budgetEvent({
        usd: 0, tokens: 1000, turns: 1, wallclock_s: 5,
        limit: { usd: 2, tokens: 400000, turns: 24, wallclock_s: 3600 },
        ducklings: { "pato-local": { calls: 3, tokens: 1000, cost_usd: 0 } },
      }),
    );
    render(<RunView runId="r-1" client={client} />);
    const box = await screen.findByTestId("spend-by-duckling");
    expect(box.textContent).toContain("pato-local");
    expect(box.textContent).toContain("implementer");
    expect(box.textContent).toContain("pato-atom");
    expect(box.textContent).toContain("reviewer");
    expect(box.textContent).toContain("no calls yet");
  });

  // A solo run seats one model, and a breakdown of one row is noise — the
  // roster naming an architect and a scribe does not mean they ran.
  it("shows no breakdown for a solo run", async () => {
    useRuns.setState({
      runs: { "r-1": { ...run, mode: "solo", roster: { implementer: "pato-local", architect: "pato-atom" } } },
      spend: {}, events: {}, deltas: {}, reasoning: {},
    });
    useRuns.getState().applyEvent(
      budgetEvent({
        usd: 0, tokens: 1000, turns: 1, wallclock_s: 5,
        limit: { usd: 2, tokens: 400000, turns: 24, wallclock_s: 3600 },
        ducklings: { "pato-local": { calls: 3, tokens: 1000, cost_usd: 0 } },
      }),
    );
    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => expect(screen.getAllByTestId("budget-meter").length).toBeGreaterThan(0));
    expect(screen.queryByTestId("spend-by-duckling")).toBeNull();
  });
});

// The plan run's meter sat at zeros while luna drafted: the engine copies the
// aggregate onto the record only when the run ends, so the mount-time fetch
// served zeros, and no live event rescued the view until the NEXT call
// finished — for a slow local model, minutes of dead meter. The engine now
// serves the tracker's live numbers for an active run; this pins that a view
// opened mid-run renders them, and that a streamed budget event still moves
// the meter afterwards.
describe("a run view opened mid-run", () => {
  const midRun = {
    id: "r-live", project_id: "calculator", stage: "plan", mode: "council",
    status: "running", verdict: "", started_at: "2026-07-31T13:46:47Z",
    roster: { architect: "luna" },
    spend: {
      luna: { calls: 4, tokens: 59202, cost_usd: 0.013 },
      "pato-sonnet": { calls: 4, tokens: 69624, cost_usd: 0.234 },
    },
    budget: {
      usd: 0.013, tokens: 59202, turns: 4, wallclock_s: 60,
      limit: { usd: 5, tokens: 1500000, turns: 24, wallclock_s: 1800 },
    },
  } as unknown as Run;

  const client = {
    run: vi.fn(() => Promise.resolve({ run: midRun, events: [] })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([])),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    tasks: vi.fn(() => Promise.resolve([])),
  } as unknown as EngineClient;

  it("renders the fetched spend without waiting for a live event", async () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
    render(<RunView runId="r-live" client={client} />);
    await waitFor(() => screen.getByTestId("run-view"));
    const meters = screen.getAllByTestId("budget-meter").map((m) => m.textContent).join(" ");
    expect(meters).toContain("59.2k");
    expect(screen.getByTestId("spend-by-duckling").textContent).toContain("luna");
  });

  it("keeps moving on the next streamed budget event", async () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
    render(<RunView runId="r-live" client={client} />);
    await waitFor(() => screen.getByTestId("run-view"));
    useRuns.getState().applyEvent({
      type: "budget", run_id: "r-live", project_id: "calculator",
      data: {
        usd: 0.02, tokens: 80000, turns: 5, wallclock_s: 90,
        limit: { usd: 5, tokens: 1500000, turns: 24, wallclock_s: 1800 },
        ducklings: { luna: { calls: 5, tokens: 80000, cost_usd: 0.02 } },
      },
    } as never);
    await waitFor(() => {
      const meters = screen.getAllByTestId("budget-meter").map((m) => m.textContent).join(" ");
      expect(meters).toContain("80.0k");
    });
  });
});

// The last streamed budget frame can predate the final turn's accounting by a
// moment, and a paused run kept wearing that stale frame over an exact
// record: the meter said 3 turns while state.json said 4, and the person
// audited the arithmetic looking for the missing turn. Once a run stops
// running, the record is the truth.
describe("a run that stopped running", () => {
  const pausedRun = {
    id: "r-done", project_id: "p", stage: "build", mode: "pair",
    status: "paused", pending_kind: "gate", verdict: "PASSED",
    started_at: "2026-08-09T17:35:16Z",
    roster: { implementer: "deepseekv4pro", reviewer: "luna" },
    budget: {
      usd: 0.1, tokens: 1400000, turns: 4, wallclock_s: 700,
      limit: { usd: 5, tokens: 3000000, turns: 24, wallclock_s: 1800 },
    },
  } as unknown as Run;

  const client = {
    run: vi.fn(() => Promise.resolve({ run: pausedRun, events: [] })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([])),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    tasks: vi.fn(() => Promise.resolve([])),
  } as unknown as EngineClient;

  it("prefers the record over a stale live frame", async () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
    // The stale frame the stream left behind: a turn short of the record.
    useRuns.getState().applyEvent({
      type: "budget", run_id: "r-done", project_id: "p",
      data: {
        usd: 0.09, tokens: 1300000, turns: 3, wallclock_s: 650,
        limit: { usd: 5, tokens: 3000000, turns: 24, wallclock_s: 1800 },
        ducklings: {},
      },
    } as never);
    render(<RunView runId="r-done" client={client} />);
    await waitFor(() => screen.getByTestId("run-view"));
    await waitFor(() => {
      const meters = screen.getAllByTestId("budget-meter").map((m) => m.textContent).join(" ");
      expect(meters).toContain("4 / 24");
    });
  });
});

// The per-reply call cap — the ceiling a reviewer once died on at exactly
// call one hundred — gets the same live "no cap" the other ceilings have.
// The row shows the run's own cap and the lift posts kind "calls"; the
// engine applies it mid-reply, so checking it is the remedy that resuming
// into the same ceiling never was.
describe("the calls/reply cap on a live run", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": run }, spend: {}, events: {}, deltas: {}, reasoning: {} });
  });

  it("shows the cap with a lift, and posts the lift as kind calls", async () => {
    const posted: string[] = [];
    const liftClient = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string, init?: RequestInit) => {
        const path = String(url).replace("http://engine", "");
        if (init?.method === "POST" && path.includes("/budget/lift")) {
          posted.push(String(init.body));
          return new Response(JSON.stringify({ ...run, agent_turns: -1 }), {
            status: 200, headers: { "Content-Type": "application/json" },
          });
        }
        return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
      }) as unknown as typeof fetch,
    });
    useRuns.getState().applyEvent(
      budgetEvent({
        usd: 0.1, tokens: 1000, turns: 1, wallclock_s: 5,
        limit: { usd: 2, tokens: 400000, turns: 24, wallclock_s: 3600 },
        ducklings: {},
      }),
    );
    render(<RunView runId="r-1" client={liftClient} />);
    const row = await screen.findByTestId("calls-cap");
    expect(row.textContent).toContain("calls / reply");
    expect(row.textContent).toContain("default");

    fireEvent.click(screen.getByTestId("lift-calls"));
    await waitFor(() => expect(posted.length).toBe(1));
    expect(posted[0]).toContain("calls");
    // The response carries the lifted record; the row wears it.
    await waitFor(() => expect(screen.getByTestId("calls-cap").textContent).toContain("no cap"));
  });
});
