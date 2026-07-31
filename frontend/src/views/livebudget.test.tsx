import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
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

  // A solo run has one model, and a breakdown of one row is noise.
  it("shows no breakdown when only one model has spent", async () => {
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
