import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { buildTriage } from "../lib/runview";
import { useRuns } from "../store/runs";
import type { DucklabEvent } from "../api/events";
import type { EngineClient, Run } from "../api/client";

const run: Run = {
  id: "r-1", project_id: "p", stage: "triage", mode: "solo", task_id: "",
  status: "paused", verdict: "UNVERIFIED", pending_kind: "gate",
  started_at: "2026-07-30T14:16:07Z",
};

const ev = (data: Record<string, unknown>, seq = 1): DucklabEvent =>
  ({ type: "triage", seq, run_id: "r-1", data }) as DucklabEvent;

const client = () =>
  ({
    run: vi.fn(() => Promise.resolve({ run, events: [] })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([])),
    tasks: vi.fn(() => Promise.resolve([])),
  }) as unknown as EngineClient;

// The proposals were written to the event stream in full — severity, reason,
// suspected files — and nothing rendered them. The run paused at its human gate
// offering Accept and Reject with the thing being decided nowhere on screen.
describe("a triage run's proposals", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("shows what the gate is asking about", async () => {
    useRuns.getState().applyEvent(
      ev({
        bug: "B-001",
        severity: "critical",
        component: "vertex drag / hit test",
        reason: "toWorld returns an [x,y] array while getVertexAtWorldPos reads .x/.y",
        task_title: "Fix vertex hit test coordinates",
        suspected_files: ["index.html"],
      }),
    );
    render(<RunView runId="r-1" client={client()} />);
    const box = await screen.findByTestId("triage-proposals");
    expect(box.textContent).toContain("B-001");
    expect(box.textContent).toContain("critical");
    // The reason is the content of a triage; without it there is nothing to judge.
    expect(box.textContent).toContain("toWorld returns an");
    expect(box.textContent).toContain("Fix vertex hit test coordinates");
    expect(box.textContent).toContain("index.html");
  });

  it("shows nothing on a run that triaged nothing", async () => {
    render(<RunView runId="r-1" client={client()} />);
    await waitFor(() => screen.getByTestId("run-view"));
    expect(screen.queryByTestId("triage-proposals")).toBeNull();
  });
});

describe("buildTriage", () => {
  // A re-run triages the same bug again, and the newer answer is the one on the
  // table — not both, and not the stale one.
  it("keeps the newest proposal per bug", () => {
    const got = buildTriage([
      ev({ bug: "B-001", severity: "normal" }, 1),
      ev({ bug: "B-001", severity: "critical" }, 2),
      ev({ bug: "B-002", severity: "low" }, 3),
    ]);
    expect(got).toHaveLength(2);
    expect(got.find((t) => t.bug === "B-001")?.severity).toBe("critical");
  });

  it("ignores everything that is not a proposal", () => {
    expect(buildTriage([{ type: "turn_start", seq: 1, run_id: "r", data: {} } as DucklabEvent])).toEqual([]);
  });
});

// One bad report does not poison the others: the batch carries on and the
// failure is written down. Nothing rendered it, so a run that triaged two of
// three looked exactly like one that triaged two, and the third stayed open
// with no explanation anywhere a person would look.
describe("reports the triage could not classify", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-1": run }, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  it("names them and says they stay open", async () => {
    useRuns.getState().applyEvent({
      type: "triage_failed", run_id: "r-1", seq: 1,
      data: { bug: "B-002", error: "contract parse failed after 2 repairs" },
    } as never);
    render(<RunView runId="r-1" client={client()} />);
    const box = await screen.findByTestId("triage-failures");
    expect(box.textContent).toContain("B-002");
    expect(box.textContent).toContain("contract parse failed");
    // Accepting must not be read as accepting these too.
    expect(box.textContent).toContain("stay open");
  });

  it("says nothing when every report was classified", async () => {
    useRuns.getState().applyEvent({
      type: "triage", run_id: "r-1", seq: 1, data: { bug: "B-001", severity: "low" },
    } as never);
    render(<RunView runId="r-1" client={client()} />);
    await waitFor(() => screen.getByTestId("triage-proposals"));
    expect(screen.queryByTestId("triage-failures")).toBeNull();
  });
});
