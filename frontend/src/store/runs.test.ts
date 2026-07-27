import { describe, it, expect, beforeEach } from "vitest";
import { useRuns, pendingForHuman } from "./runs";
import type { Run } from "../api/client";

const baseRun: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-001",
  status: "running", verdict: "", started_at: "2026-07-26T12:00:00Z",
};

beforeEach(() => {
  useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "connecting" });
});

describe("event application", () => {
  it("appends persisted events", () => {
    const s = useRuns.getState();
    s.applyEvent({ type: "turn_start", run_id: "r-1", seq: 1 });
    s.applyEvent({ type: "gate", run_id: "r-1", seq: 2 });
    expect(useRuns.getState().events["r-1"]).toHaveLength(2);
  });

  // A reconnect overlaps the backlog with live events; the same seq must not
  // appear twice in the conversation.
  it("deduplicates by seq", () => {
    const s = useRuns.getState();
    s.applyEvent({ type: "turn_start", run_id: "r-1", seq: 1 });
    s.applyEvent({ type: "turn_start", run_id: "r-1", seq: 1 });
    expect(useRuns.getState().events["r-1"]).toHaveLength(1);
  });

  // token_delta is display state. Putting it in the event log would make the
  // UI's record diverge from events.jsonl.
  it("accumulates token_delta separately and never in the event log", () => {
    const s = useRuns.getState();
    s.applyEvent({ type: "token_delta", run_id: "r-1", data: { duckling: "pato-uno", round: 1, turn: 0, text: "func " } });
    s.applyEvent({ type: "token_delta", run_id: "r-1", data: { duckling: "pato-uno", round: 1, turn: 0, text: "Add" } });

    const st = useRuns.getState();
    expect(st.deltas["r-1"]?.["1:0"]).toBe("func Add");
    expect(st.events["r-1"] ?? []).toHaveLength(0);
  });

  it("keeps deltas per turn so lanes do not interleave", () => {
    const s = useRuns.getState();
    s.applyEvent({ type: "token_delta", run_id: "r-1", data: { duckling: "a", round: 1, turn: 0, text: "one" } });
    s.applyEvent({ type: "token_delta", run_id: "r-1", data: { duckling: "b", round: 1, turn: 1, text: "two" } });
    const d = useRuns.getState().deltas["r-1"]!;
    expect(d["1:0"]).toBe("one");
    expect(d["1:1"]).toBe("two");
  });

  // The same duckling speaking twice — a council's architect drafts and then
  // revises. Keyed by duckling, the revision appended to the draft.
  it("keeps two turns by the same duckling apart", () => {
    const s = useRuns.getState();
    s.applyEvent({ type: "token_delta", run_id: "r-1", data: { duckling: "a", round: 1, turn: 0, text: "draft" } });
    s.applyEvent({ type: "token_delta", run_id: "r-1", data: { duckling: "a", round: 1, turn: 3, text: "revision" } });
    const d = useRuns.getState().deltas["r-1"]!;
    expect(d["1:0"]).toBe("draft");
    expect(d["1:3"]).toBe("revision");
  });

  it("ignores heartbeats", () => {
    useRuns.getState().applyEvent({ type: "heartbeat", run_id: "r-1" });
    expect(useRuns.getState().events["r-1"] ?? []).toHaveLength(0);
  });

  it("reflects a human gate on the run", () => {
    const s = useRuns.getState();
    s.setRun(baseRun);
    s.applyEvent({ type: "human_needed", run_id: "r-1", seq: 1, data: { kind: "gate" } });
    const run = useRuns.getState().runs["r-1"]!;
    expect(run.status).toBe("paused");
    expect(run.pending_kind).toBe("gate");
  });
});

describe("accept flow", () => {
  // AC-34: optimistic UI is forbidden for anything the engine owns.
  it("stays pending until the engine confirms a commit", () => {
    const s = useRuns.getState();
    s.setRun(baseRun);
    s.beginAccept("r-1");

    let st = useRuns.getState();
    expect(st.acceptState["r-1"]).toEqual({ kind: "pending" });
    expect(st.runs["r-1"]!.commit_sha).toBeUndefined();
    expect(st.runs["r-1"]!.accepted).toBeUndefined();

    s.confirmAccept("r-1", "e60dc7fe");
    st = useRuns.getState();
    expect(st.acceptState["r-1"]).toEqual({ kind: "committed", sha: "e60dc7fe" });
    expect(st.runs["r-1"]!.commit_sha).toBe("e60dc7fe");
  });

  it("shows an error and no commit when accept fails", () => {
    const s = useRuns.getState();
    s.setRun(baseRun);
    s.beginAccept("r-1");
    s.failAccept("r-1", "engine unreachable");

    const st = useRuns.getState();
    expect(st.acceptState["r-1"]).toEqual({ kind: "error", message: "engine unreachable" });
    expect(st.runs["r-1"]!.commit_sha).toBeUndefined();
    expect(st.runs["r-1"]!.accepted).toBeUndefined();
  });
});

describe("connection and resync", () => {
  // AC-30: a drop changes the indicator, never the data.
  it("keeps run data through a disconnect", () => {
    const s = useRuns.getState();
    s.setRun(baseRun);
    s.applyEvent({ type: "turn_start", run_id: "r-1", seq: 1 });

    s.setConnection("reconnecting");
    const st = useRuns.getState();
    expect(st.connection).toBe("reconnecting");
    expect(st.runs["r-1"]).toBeDefined();
    expect(st.events["r-1"]).toHaveLength(1);
  });

  it("flags a resync after overflow and clears it once handled", () => {
    const s = useRuns.getState();
    s.markOverflow();
    expect(useRuns.getState().needsResync).toBe(true);
    s.clearResync();
    expect(useRuns.getState().needsResync).toBe(false);
  });
});

describe("human gate inbox", () => {
  it("lists only paused runs with something pending, oldest wait first", () => {
    const runs: Record<string, Run> = {
      "r-1": { ...baseRun, id: "r-1", status: "running" },
      "r-2": { ...baseRun, id: "r-2", status: "paused", pending_kind: "gate", pending_since: "2026-07-26T12:05:00Z" },
      "r-3": { ...baseRun, id: "r-3", status: "paused", pending_kind: "question", pending_since: "2026-07-26T12:01:00Z" },
      "r-4": { ...baseRun, id: "r-4", status: "paused" },
    };
    const pending = pendingForHuman(runs);
    expect(pending.map((r) => r.id)).toEqual(["r-3", "r-2"]);
  });
});

// A run that begins while this client is connected. The store only ever
// updated runs it already knew, so starting a run from the CLI with the
// desktop open left it invisible until a refetch — and the desktop exists to
// watch runs happen.
describe("a run that starts while we are watching", () => {
  it("appears from its run_start event", () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "open" });
    useRuns.getState().applyEvent({
      type: "run_start", run_id: "r-new", project_id: "p", seq: 1,
      ts: "2026-07-27T22:00:00Z",
      data: { mode: "pair", task_id: "T-001" },
    });

    const run = useRuns.getState().runs["r-new"];
    expect(run).toBeTruthy();
    expect(run!.status).toBe("running");
    expect(run!.mode).toBe("pair");
    expect(run!.task_id).toBe("T-001");
  });

  it("does not invent a run from any other event", () => {
    useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "open" });
    useRuns.getState().applyEvent({ type: "turn_start", run_id: "r-ghost", seq: 1, data: {} });
    expect(useRuns.getState().runs["r-ghost"]).toBeUndefined();
  });
});
