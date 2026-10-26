import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EscalationSuggestionCard, escalationState } from "./EscalationSuggestionCard";
import { RunView } from "../views/RunView";
import { EngineClient, type Run } from "../api/client";
import { useRuns } from "../store/runs";
import type { DucklabEvent } from "../api/events";

// B-522 (calculator r-20261026-171819-zzgs): a triage's card said "The run
// paused" while the next bug's turn started, diagnosed a task brief the run
// did not have, offered "Open task body", and rendered "Relaunch with
// stronger seat" disabled with nothing to say why.

const triageCard: DucklabEvent = {
  type: "escalation_suggestion",
  run_id: "r-1",
  seq: 83,
  data: {
    point: "wallclock_history",
    thresholds_fired: ["wallclock_over_history"],
    current_stage: "triage",
    role: "triager",
    seat: "gufoqwen3827b",
    pause: "requested",
    actions: ["relaunch_with_another_seat", "continue_as-is", "abort"],
    diagnoses: { seat_at_capacity: "the current seat may be saturated" },
  },
};
const pauseRequested: DucklabEvent = {
  type: "pause_requested", run_id: "r-1", seq: 84,
  data: { kind: "history_duration", detail: "pausing when the current bug's turn ends; resume continues with the bugs not yet triaged" },
};

describe("escalation card on a task-less run", () => {
  it("never says paused while the run is working", () => {
    const pausing = escalationState(triageCard, [triageCard, pauseRequested], { status: "running" });
    expect(pausing.kind).toBe("pausing");
    expect(pausing.text).not.toMatch(/run paused/i);
    expect(pausing.text).toContain("current bug's turn ends");

    const deferred = escalationState(
      { ...triageCard, data: { ...triageCard.data, pause: "deferred", pause_detail: "the release draft continues to its gate; it has no safe point to pause at" } },
      [], { status: "running" },
    );
    expect(deferred.kind).toBe("continuing");
    expect(deferred.text).toContain("did not pause");

    expect(escalationState(triageCard, [triageCard, pauseRequested], { status: "paused", pending_kind: "history_duration" }).kind).toBe("paused");
    expect(escalationState(triageCard, [triageCard], { status: "failed" }).kind).toBe("ended");
  });

  it("offers no task actions and no brief diagnosis — even from an old record that had them", () => {
    const legacy: DucklabEvent = {
      ...triageCard,
      data: {
        ...triageCard.data,
        actions: ["relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"],
        diagnoses: { seat_at_capacity: "the current seat may be saturated", task_brief_quality: "the brief may be too wide" },
      },
    };
    render(<EscalationSuggestionCard event={legacy} hasTask={false} stage="triage" onOpenTask={() => {}} onContinue={() => {}} />);
    const card = screen.getByTestId("escalation-suggestion");
    expect(screen.queryByTestId("escalation-task-body")).toBeNull();
    expect(card).not.toHaveTextContent("task brief quality");
    expect(card).toHaveTextContent("seat at capacity");
  });

  it("explains a missing candidate and lets the person pick a seat for the role that ran", () => {
    const onRelaunch = vi.fn();
    render(
      <EscalationSuggestionCard
        event={triageCard}
        state={escalationState(triageCard, [triageCard, pauseRequested], { status: "running" })}
        hasTask={false}
        stage="triage"
        seatChoices={["gufoqwen3827b", "pato-dos", "pato-tres"]}
        onRelaunch={onRelaunch}
        onOpenTask={() => {}}
        onContinue={() => {}}
        onAbort={() => {}}
      />,
    );
    expect(screen.getByTestId("escalation-no-candidate")).toHaveTextContent("No stronger triager with evidence for this role");
    const relaunch = screen.getByTestId("escalation-relaunch");
    expect(relaunch).toHaveTextContent("Relaunch triage with another triager");
    expect(relaunch).toBeDisabled();
    const options = Array.from((screen.getByTestId("escalation-seat-pick") as HTMLSelectElement).options).map((o) => o.value);
    // The seat that ran is not offered as its own replacement.
    expect(options).toEqual(["", "pato-dos", "pato-tres"]);
    fireEvent.change(screen.getByTestId("escalation-seat-pick"), { target: { value: "pato-dos" } });
    fireEvent.click(screen.getByTestId("escalation-relaunch"));
    expect(onRelaunch).toHaveBeenCalledWith("pato-dos");
    expect(screen.getByTestId("escalation-abort")).toBeInTheDocument();
    expect(screen.getByTestId("escalation-continue")).toHaveTextContent("Keep it running");
  });

  it("names a candidate with evidence as a stronger seat for the run's own role", () => {
    const onRelaunch = vi.fn();
    const withCandidate: DucklabEvent = { ...triageCard, data: { ...triageCard.data, candidate: { id: "pato-dos", wilson_floor: 71.2 } } };
    render(<EscalationSuggestionCard event={withCandidate} hasTask={false} stage="triage" onRelaunch={onRelaunch} onOpenTask={() => {}} onContinue={() => {}} />);
    expect(screen.getByTestId("escalation-candidate")).toHaveTextContent("stronger triager candidate · pato-dos");
    fireEvent.click(screen.getByTestId("escalation-relaunch"));
    expect(onRelaunch).toHaveBeenCalledWith("pato-dos");
  });

  it("relaunches a triage through the triage door with the picked triager, and keeps working runs unresumed", async () => {
    const run: Run = {
      id: "r-1", project_id: "p-1", stage: "triage", mode: "solo", task_id: "",
      status: "running", verdict: "", started_at: "2026-01-01T00:00:00Z", next: ["abort"],
      roster: { triager: "gufoqwen3827b" },
    };
    const ducklings = [
      { id: "gufoqwen3827b", provider: "local", model: "q" },
      { id: "pato-dos", provider: "local", model: "p" },
    ];
    const client = new EngineClient({
      baseUrl: "http://engine", token: "test",
      fetchFn: (async (url: string) => new Response(JSON.stringify(
        String(url).endsWith("/v1/runs/r-1") ? { run, events: [triageCard, pauseRequested] }
          : String(url).endsWith("/v1/ducklings") ? { items: ducklings }
            : { items: [], rows: [] },
      ), { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
    });
    const relaunch = vi.spyOn(client, "triageRelaunch").mockResolvedValue({ ...run, id: "r-2" });
    const runStart = vi.spyOn(client, "runStart");
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });

    render(<RunView runId="r-1" client={client} />);
    const card = await screen.findByTestId("escalation-suggestion");
    expect(card).not.toHaveTextContent("The run paused");
    expect(screen.getByTestId("escalation-state")).toHaveAttribute("data-state", "pausing");
    expect(screen.queryByTestId("escalation-task-body")).toBeNull();
    await waitFor(() => expect(screen.getByTestId("escalation-seat-pick")).toBeInTheDocument());
    fireEvent.change(screen.getByTestId("escalation-seat-pick"), { target: { value: "pato-dos" } });
    fireEvent.click(screen.getByTestId("escalation-relaunch"));

    await waitFor(() => expect(relaunch).toHaveBeenCalledWith("p-1", "r-1", "pato-dos"));
    expect(runStart).not.toHaveBeenCalled();
    expect(await screen.findByTestId("relaunch-link")).toHaveTextContent("r-2");
  });

  it("does not resume a run that never paused when the person keeps it running", async () => {
    const run: Run = {
      id: "r-1", project_id: "p-1", stage: "release", mode: "solo", task_id: "",
      status: "running", verdict: "", started_at: "2026-01-01T00:00:00Z", next: ["abort"],
    };
    const deferred: DucklabEvent = {
      ...triageCard,
      data: { ...triageCard.data, role: "scribe", seat: "", pause: "deferred", pause_detail: "the release draft continues to its gate; it has no safe point to pause at", actions: ["continue_as-is", "abort"] },
    };
    const client = new EngineClient({
      baseUrl: "http://engine", token: "test",
      fetchFn: (async (url: string) => new Response(JSON.stringify(
        String(url).endsWith("/v1/runs/r-1") ? { run, events: [deferred] } : { items: [], rows: [] },
      ), { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
    });
    const resume = vi.spyOn(client, "runResume");
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });

    render(<RunView runId="r-1" client={client} />);
    const card = await screen.findByTestId("escalation-suggestion");
    expect(card).toHaveTextContent("did not pause");
    expect(screen.queryByTestId("escalation-relaunch")).toBeNull();
    fireEvent.click(screen.getByTestId("escalation-continue"));
    await waitFor(() => expect(screen.queryByTestId("escalation-suggestion")).toBeNull());
    expect(resume).not.toHaveBeenCalled();
  });

  // The replacement takes the chair of the role the evidence is about — not
  // position 0, which put a triager's replacement in an implementer's chair.
  it.each([
    ["implementer", { implementer: "strong", advisor: "adv", reviewer: "rev" }],
    ["reviewer", { implementer: "weak", advisor: "adv", reviewer: "strong" }],
  ])("seats a stronger %s in that role's position", async (role, want) => {
    const run: Run = {
      id: "r-1", project_id: "p-1", stage: "build", mode: "pair", task_id: "T-1",
      status: "paused", verdict: "FAILED", started_at: "2026-01-01T00:00:00Z", next: [],
      roster: { implementer: "weak", advisor: "adv", reviewer: "rev" },
    };
    const buildCard: DucklabEvent = {
      type: "escalation_suggestion", run_id: "r-1", seq: 5,
      data: { role, candidate: { id: "strong", wilson_floor: 80 }, actions: ["relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"] },
    };
    const client = new EngineClient({
      baseUrl: "http://engine", token: "test",
      fetchFn: (async (url: string) => new Response(JSON.stringify(
        String(url).endsWith("/v1/runs/r-1") ? { run, events: [buildCard] } : { items: [], rows: [], entries: [] },
      ), { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
    });
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });
    render(<RunView runId="r-1" client={client} />);
    await screen.findByTestId("escalation-suggestion");
    fireEvent.click(screen.getByTestId("escalation-relaunch"));
    expect(await screen.findByTestId("relaunch")).toBeInTheDocument();
    const runStart = vi.spyOn(client, "runStart").mockResolvedValue({ ...run, id: "r-2" });
    fireEvent.click(screen.getByTestId("rerun-without-note"));
    await waitFor(() => expect(runStart).toHaveBeenCalled());
    expect(runStart).toHaveBeenCalledWith("p-1", "T-1", expect.objectContaining({ seats: want }));
  });
});
