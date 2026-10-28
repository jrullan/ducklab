import { describe, it, expect, beforeEach } from "vitest";
import { reconcileAccept, useRuns } from "../store/runs";
import { acceptingLabel, ACCEPT_VERIFYING_LABEL } from "./accept";
import type { Run } from "../api/client";

// B-543: an OpenCode coordinator accepted T-018 over MCP; the engine replayed
// the gate for minutes, and the desktop kept the run at its gate offering
// Accept, then jumped to done. The engine announces every accept on the run's
// stream (accept_started) and on a run read (accept_in_progress); the
// desktop only believed the ones it had clicked itself.
const paused = { id: "r1", status: "paused", accepted: false, next: ["accept", "reject"] } as unknown as Run;
const started = (attempt: string, actor = "mcp:opencode") =>
  ({ type: "accept_started", run_id: "r1", seq: 40, data: { attempt, actor } }) as never;
const settled = (attempt: string) =>
  ({ type: "accept_settled", run_id: "r1", seq: 41, data: { attempt, outcome: "accepted", commit_sha: "fe2a9178" } }) as never;

describe("an accept started elsewhere", () => {
  beforeEach(() => useRuns.setState({ runs: { r1: paused }, events: {}, acceptState: {} }));

  it("shows as accepting, naming who decides", () => {
    useRuns.getState().applyEvent(started("acc-7"));
    const st = useRuns.getState().acceptState.r1;
    expect(st).toEqual({ kind: "pending", verifying: true, attempt: "acc-7", by: "mcp:opencode" });
    expect(acceptingLabel(st)).toBe("mcp:opencode is accepting… verifying from a clean checkout");
  });

  it("settles on its own attempt like any accept", () => {
    useRuns.getState().applyEvent(started("acc-7"));
    useRuns.getState().applyEvent(settled("acc-7"));
    expect(useRuns.getState().acceptState.r1).toEqual({ kind: "committed", sha: "fe2a9178" });
    expect(useRuns.getState().runs.r1!.status).toBe("done");
  });

  it("does not take over this client's own accept", () => {
    useRuns.getState().beginAccept("r1");
    useRuns.getState().applyEvent(started("acc-7", "human"));
    expect(useRuns.getState().acceptState.r1).toEqual({ kind: "pending" });
  });

  it("is not reopened by a start whose settlement is already in", () => {
    useRuns.setState({ events: { r1: [settled("acc-7")] } as never });
    useRuns.getState().applyEvent(started("acc-7"));
    expect(useRuns.getState().acceptState.r1).toBeUndefined();
  });

  it("is read from the run when the view opens mid-accept", () => {
    const reading = { ...paused, accept_in_progress: true, accept_attempt: "acc-7", accept_actor: "mcp:opencode" } as Run;
    const st = reconcileAccept({}, reading).r1;
    expect(st).toEqual({ kind: "pending", verifying: true, attempt: "acc-7", by: "mcp:opencode" });
    // And the next read, once it settled, closes it from last_accept.
    const after = { ...paused, accept_in_progress: false, accepted: true, commit_sha: "fe2a9178",
      last_accept: { attempt: "acc-7", outcome: "accepted", commit_sha: "fe2a9178" } } as Run;
    expect(reconcileAccept({ r1: st! }, after).r1).toEqual({ kind: "committed", sha: "fe2a9178" });
  });

  it("keeps the plain label for the person's own accept", () => {
    useRuns.getState().applyEvent(started("acc-7", "human"));
    expect(acceptingLabel(useRuns.getState().acceptState.r1)).toBe(ACCEPT_VERIFYING_LABEL);
  });
});
