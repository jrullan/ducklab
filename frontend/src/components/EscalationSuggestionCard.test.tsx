import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
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
