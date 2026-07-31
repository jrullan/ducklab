import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import type { EngineClient, LLMCall, Run } from "../api/client";

const run: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "solo", task_id: "T-011",
  status: "done", verdict: "PASSED", accepted: true, started_at: "2026-07-30T01:37:59Z",
};

const call: LLMCall = {
  seq: 1, ts: "2026-07-30T01:38:00Z", duckling: "k3", provider: "openrouter",
  model: "moonshotai/kimi-k3", role: "implementer",
  request: { messages: [{ role: "user", content: "Integrate locked constraints." }] },
  response: { content: "", finish_reason: "tool_calls" },
  usage: { prompt_tokens: 40832, completion_tokens: 512, reasoning_tokens: 300 },
  cost_usd: 0.14, latency_ms: 8200, finish_reason: "tool_calls",
};

const client = (calls: LLMCall[]) =>
  ({
    run: vi.fn(() => Promise.resolve({ run, events: [] })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve(calls)),
    ducklings: vi.fn(() => Promise.resolve([])),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    tasks: vi.fn(() => Promise.resolve([])),
  }) as unknown as EngineClient;

// The one place that shows what a model was actually given. A prompt is
// assembled from a task, a spec, a transcript and a toolbelt, and when an answer
// is wrong the question is almost always where to look — which was reachable
// only by opening llm.jsonl by hand.
describe("a run's model calls", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("lists them with what each one spent", async () => {
    render(<RunView runId="r-1" client={client([call])} />);
    fireEvent.click(await screen.findByTestId("tab-calls"));
    const row = await screen.findByTestId("call-row");
    expect(row.textContent).toContain("k3");
    expect(row.textContent).toContain("40.8k in");
    // Thinking is part of the output, not on top of it, and it is shown apart
    // because a budget spent thinking calls for a different action.
    expect(row.textContent).toContain("300 thinking");
    // A finish reason that is not "stop" is the first thing worth seeing.
    expect(row.textContent).toContain("tool_calls");
  });

  it("shows what was sent and what came back", async () => {
    render(<RunView runId="r-1" client={client([call])} />);
    fireEvent.click(await screen.findByTestId("tab-calls"));
    const row = await screen.findByTestId("call-row");
    expect(row.textContent).toContain("Integrate locked constraints.");
    expect(row.textContent).toContain("finish_reason");
  });

  it("says so rather than showing an empty list", async () => {
    render(<RunView runId="r-1" client={client([])} />);
    fireEvent.click(await screen.findByTestId("tab-calls"));
    await waitFor(() => expect(screen.getByTestId("calls-empty")).toBeTruthy());
  });
});

// A run with no task showed nothing at all: the header of a triage or a stage
// opened with an empty space where its name should be.
describe("a run's header", () => {
  it("names a run that has no task", async () => {
    const triage = { ...run, task_id: "", stage: "triage", mode: "solo" };
    useRuns.setState({ runs: { "r-1": triage } });
    const c = client([]);
    // The view resyncs from the engine on open, so the engine's answer is what
    // ends up on screen.
    (c as unknown as { run: unknown }).run = vi.fn(() =>
      Promise.resolve({ run: triage, events: [] }),
    );
    render(<RunView runId="r-1" client={c} />);
    const header = (await screen.findByTestId("run-view")).querySelector("header")!;
    expect(header.textContent).toContain("triage");
  });
});
