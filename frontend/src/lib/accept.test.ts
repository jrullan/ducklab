import { describe, it, expect, beforeEach } from "vitest";
import { useRuns } from "../store/runs";
import { acceptAndSettle, acceptingLabel, AcceptSettledError, ACCEPT_VERIFYING_LABEL } from "./accept";
import { buildTurns, decisionLabel } from "./runview";
import type { AcceptResult, EngineClient, Run } from "../api/client";

const paused: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "solo", task_id: "T-004",
  status: "paused", verdict: "PASSED", started_at: "2026-10-27T19:44:56Z",
};

beforeEach(() => {
  useRuns.setState({ runs: { "r-1": paused }, events: {}, deltas: {}, reasoning: {}, acceptState: {}, needsResync: false, connection: "open" });
});

// No run read unless a test gives one: these settle through the stream.
const answering = (res: AcceptResult) =>
  ({ accept: async () => res, run: () => new Promise(() => {}) }) as unknown as Pick<EngineClient, "accept" | "run">;

// B-529: ti36xpro's suite outlived the request that accepted it. The engine
// now answers in_progress and keeps going; the desktop must keep saying
// "accepting… verifying from a clean checkout" and then show the outcome the
// stream brings — never a commit the engine has not confirmed (AC-34), and
// never a failure for an accept that is still running.
describe("an accept that outlives its request", () => {
  it("waits through in_progress to the commit the stream confirms", async () => {
    const settled = acceptAndSettle(answering({ commit_sha: "", in_progress: true, info: "accept in progress" }), "r-1");
    await Promise.resolve();
    await Promise.resolve();
    const pending = useRuns.getState().acceptState["r-1"];
    expect(pending).toEqual({ kind: "pending", verifying: true });
    expect(acceptingLabel(pending)).toBe(ACCEPT_VERIFYING_LABEL);
    expect(useRuns.getState().runs["r-1"]?.accepted).toBeUndefined();

    useRuns.getState().applyEvent({
      type: "accept_settled", run_id: "r-1", seq: 9,
      data: { outcome: "accepted", commit_sha: "abc1234", info: "no remote configured" },
    });
    await expect(settled).resolves.toEqual({ commit_sha: "abc1234", warning: undefined, info: "no remote configured" });
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "committed", sha: "abc1234" });
    expect(useRuns.getState().runs["r-1"]).toMatchObject({ accepted: true, commit_sha: "abc1234", status: "done" });
  });

  it("reports an interrupted accept as interrupted, not failed", async () => {
    const settled = acceptAndSettle(answering({ commit_sha: "", in_progress: true }), "r-1");
    await Promise.resolve();
    await Promise.resolve();
    useRuns.getState().applyEvent({
      type: "accept_settled", run_id: "r-1", seq: 9,
      data: { outcome: "interrupted", error: "decision interrupted: the run was aborted" },
    });
    const err = await settled.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AcceptSettledError);
    expect((err as AcceptSettledError).interrupted).toBe(true);
    expect(useRuns.getState().acceptState["r-1"]).toMatchObject({ kind: "error", interrupted: true });
  });

  it("shows a refusal with the engine's reason", async () => {
    const settled = acceptAndSettle(answering({ commit_sha: "", in_progress: true }), "r-1");
    await Promise.resolve();
    await Promise.resolve();
    useRuns.getState().applyEvent({
      type: "accept_settled", run_id: "r-1", seq: 9,
      data: { outcome: "refused", error: "rebased commit failed its gate" },
    });
    await expect(settled).rejects.toThrow("rebased commit failed its gate");
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "error", message: "rebased commit failed its gate" });
  });

  it("settles at once when the engine answers with the outcome", async () => {
    await expect(acceptAndSettle(answering({ commit_sha: "def5678" }), "r-1")).resolves.toEqual({ commit_sha: "def5678" });
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "committed", sha: "def5678" });
  });

  it("says verifying as soon as the replay is announced", () => {
    useRuns.getState().beginAccept("r-1");
    expect(acceptingLabel(useRuns.getState().acceptState["r-1"])).toBe("Accepting…");
    useRuns.getState().applyEvent({ type: "gate_started", run_id: "r-1", seq: 3, data: { phase: "accept" } });
    expect(acceptingLabel(useRuns.getState().acceptState["r-1"])).toBe(ACCEPT_VERIFYING_LABEL);
  });

  it("never reopens an accept the stream already settled", () => {
    useRuns.getState().beginAccept("r-1");
    useRuns.getState().applyEvent({ type: "accept_settled", run_id: "r-1", seq: 4, data: { outcome: "accepted", commit_sha: "abc" } });
    useRuns.getState().acceptInProgress("r-1");
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "committed", sha: "abc" });
  });
});

// Review of #181: after an overflow the resync replaces the run's events
// without replaying them, so a missed accept_settled left the button at
// "Accepting…" forever. The resynced run record settles it.
describe("an accept whose settlement was missed by the stream", () => {
  const verifying = async () => {
    const settled = acceptAndSettle(answering({ commit_sha: "", in_progress: true }), "r-1");
    await Promise.resolve();
    await Promise.resolve();
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "pending", verifying: true });
    return { settled };
  };

  it("settles accepted from the resynced run", async () => {
    const { settled } = await verifying();
    useRuns.getState().resyncRun(
      { ...paused, status: "done", accepted: true, commit_sha: "abc1234", last_accept: { outcome: "accepted", commit_sha: "abc1234" } },
      [],
    );
    await expect(settled).resolves.toMatchObject({ commit_sha: "abc1234" });
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "committed", sha: "abc1234" });
  });

  it("settles refused from the resynced run", async () => {
    const { settled } = await verifying();
    useRuns.getState().resyncRun({ ...paused, last_accept: { outcome: "refused", error: "rebased commit failed its gate" } }, []);
    await expect(settled).rejects.toThrow("rebased commit failed its gate");
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "error", message: "rebased commit failed its gate" });
  });

  it("settles interrupted from the resynced run", async () => {
    const { settled } = await verifying();
    useRuns.getState().resyncRun({ ...paused, last_accept: { outcome: "interrupted", error: "the run was aborted" } }, []);
    const err = await settled.catch((e: unknown) => e);
    expect((err as AcceptSettledError).interrupted).toBe(true);
    expect(useRuns.getState().acceptState["r-1"]).toMatchObject({ kind: "error", interrupted: true });
  });

  it("keeps waiting while the engine says the accept is still running", async () => {
    void (await verifying()).settled.catch(() => {});
    useRuns.getState().resyncRun({ ...paused, accept_in_progress: true, last_accept: { outcome: "refused", error: "an earlier attempt" } }, []);
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "pending", verifying: true });
  });

  it("does not settle an accept the engine has not confirmed from an older outcome", () => {
    useRuns.getState().beginAccept("r-1");
    useRuns.getState().resyncRun({ ...paused, last_accept: { outcome: "refused", error: "an earlier attempt" } }, []);
    expect(useRuns.getState().acceptState["r-1"]).toEqual({ kind: "pending" });
  });
});

describe("the lane after an interrupted accept", () => {
  it("closes the replay as interrupted and the decision as not made", () => {
    const turns = buildTurns([
      { type: "decision", run_id: "r-1", seq: 1, data: { action: "accept", actor: "mcp:opencode", reason: "suite passes" } },
      { type: "gate_started", run_id: "r-1", seq: 2, data: { phase: "accept", detail: "reproducing rebased abc from a clean checkout" } },
      { type: "decision_outcome", run_id: "r-1", seq: 3, data: { action: "accept", outcome: "interrupted", error: "the run was aborted" } },
      { type: "accept_settled", run_id: "r-1", seq: 4, data: { outcome: "interrupted", error: "the run was aborted" } },
    ]);
    const gate = turns.find((t) => t.role === "gate");
    expect(gate).toMatchObject({ done: true, gate: "interrupted" });
    const decision = turns.find((t) => t.role === "decision")!;
    expect(decision.decisionOutcome?.outcome).toBe("interrupted");
    expect(decisionLabel(decision)).toContain("interrupted before it finished — nothing was decided");
    expect(decisionLabel(decision)).not.toContain("refused");
  });
});
