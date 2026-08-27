import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EscalationSuggestionCard } from "./EscalationSuggestionCard";
import { RunView } from "../views/RunView";
import { EngineClient, type Run } from "../api/client";
import { useRuns } from "../store/runs";
import type { DucklabEvent } from "../api/events";

const suggestion: DucklabEvent = {
  type: "escalation_suggestion",
  run_id: "r-1",
  data: {
    thresholds_fired: ["stuck_deliverable", "turns_over_2x_mode_median"],
    diagnoses: {
      seat_at_capacity: { turns: 12, mode_median: 4, consecutive_red_gates: 3 },
      task_brief_quality: "the task body needs detail",
    },
    candidate: { id: "stronger-seat", wilson_floor: 82.5 },
    current_stage: "reviewer mid-read, round 1, no red gates",
  },
};

describe("EscalationSuggestionCard", () => {
  it("renders escalation thresholds and its stronger-seat candidate", () => {
    render(<EscalationSuggestionCard event={suggestion} onRelaunch={() => {}} onOpenTask={() => {}} onContinue={() => {}} />);

    const card = screen.getByTestId("escalation-suggestion");
    expect(card).toHaveTextContent("stuck deliverable");
    expect(card).toHaveTextContent("turns over 2x mode median");
    expect(card).toHaveTextContent("stronger-seat");
    expect(card).toHaveTextContent("Wilson floor 82.5%");
    expect(card).toHaveTextContent("turns 12");
    expect(card).toHaveTextContent("reviewer mid-read, round 1, no red gates");
  });

  it("replaces earlier suggestions with one card for the latest trigger", async () => {
    const run: Run = {
      id: "r-1", project_id: "p-1", stage: "build", mode: "solo", task_id: "T-1",
      status: "paused", verdict: "", started_at: "2026-01-01T00:00:00Z", next: [],
    };
    const earlier: DucklabEvent = {
      ...suggestion,
      seq: 10,
      data: { ...suggestion.data, candidate: { id: "earlier-seat", wilson_floor: 60 } },
    };
    const latest: DucklabEvent = {
      ...suggestion,
      seq: 11,
      data: { ...suggestion.data, candidate: { id: "latest-seat", wilson_floor: 90 } },
    };
    const client = new EngineClient({
      baseUrl: "http://engine", token: "test",
      fetchFn: (async (url: string) => new Response(JSON.stringify(
        String(url).endsWith("/v1/runs/r-1") ? { run, events: [earlier, latest] } : { items: [], rows: [] },
      ), { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
    });
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });

    render(<RunView runId="r-1" client={client} />);

    await waitFor(() => expect(screen.getAllByTestId("escalation-suggestion")).toHaveLength(1));
    expect(screen.getByTestId("escalation-suggestion")).toHaveTextContent("latest-seat");
    expect(screen.queryByText("earlier-seat")).toBeNull();
  });

  it.each([
    ["escalation-relaunch", "Relaunch with stronger seat"],
    ["escalation-task-body", "Open task body"],
    ["escalation-continue", "Continue as is"],
  ])("closes the active suggestion after choosing %s", async (testId) => {
    const run: Run = {
      id: "r-1", project_id: "p-1", stage: "build", mode: "solo", task_id: "T-1",
      status: "paused", verdict: "", started_at: "2026-01-01T00:00:00Z", next: [],
    };
    const client = new EngineClient({
      baseUrl: "http://engine", token: "test",
      fetchFn: (async (url: string) => new Response(JSON.stringify(
        String(url).endsWith("/v1/runs/r-1") ? { run, events: [suggestion] } : { items: [], rows: [] },
      ), { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
    });
    const resume = vi.spyOn(client, "runResume").mockResolvedValue({ ...run, status: "running" });
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });

    render(<RunView runId="r-1" client={client} />);
    await waitFor(() => expect(screen.getByTestId("escalation-suggestion")).toBeInTheDocument());
    fireEvent.click(screen.getByTestId(testId));

    await waitFor(() => expect(screen.queryByTestId("escalation-suggestion")).toBeNull());
    if (testId === "escalation-continue") expect(resume).toHaveBeenCalledWith("r-1");
  });

  it("renders RunView's card only when its event history has a suggestion", async () => {
    const run: Run = {
      id: "r-1", project_id: "p-1", stage: "build", mode: "solo", task_id: "T-1",
      status: "paused", verdict: "", started_at: "2026-01-01T00:00:00Z", next: [],
    };
    const client = new EngineClient({
      baseUrl: "http://engine", token: "test",
      fetchFn: (async (url: string) => new Response(JSON.stringify(
        String(url).endsWith("/v1/runs/r-1") ? { run, events: [suggestion] } : { items: [], rows: [] },
      ), { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
    });
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });

    const { rerender } = render(<RunView runId="r-1" client={client} />);
    await waitFor(() => expect(screen.getByTestId("escalation-suggestion")).toHaveTextContent("stronger-seat"));

    useRuns.getState().resyncRun(run, []);
    rerender(<RunView runId="r-1" client={client} />);
    expect(screen.queryByTestId("escalation-suggestion")).toBeNull();
  });
});
