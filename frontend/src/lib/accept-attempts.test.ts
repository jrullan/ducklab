import { describe, it, expect, beforeEach } from "vitest";
import { reconcileAccept, useRuns, upsertApproval, type AcceptState } from "../store/runs";
import type { ApprovalRequest, Run } from "../api/client";

// Adversarial review of #181 (R3): after an engine died mid-accept, the
// desktop adopted an older attempt's settlement as this accept's outcome,
// and with no settlement at all it stayed "verifying" forever.
describe("an accept is settled only by its own attempt", () => {
  const verifying: Record<string, AcceptState> = { r1: { kind: "pending", verifying: true, attempt: "acc-2" } };
  const paused = { id: "r1", status: "paused", accepted: false, accept_in_progress: false } as unknown as Run;

  it("does not present an older attempt's refusal as this accept's outcome", () => {
    const run = { ...paused, last_accept: { attempt: "acc-1", outcome: "refused", error: "attempt one: failed its gate" } } as Run;
    const st = reconcileAccept(verifying, run).r1;
    expect(st).not.toEqual({ kind: "error", message: "attempt one: failed its gate" });
    expect(st).toMatchObject({ kind: "error", interrupted: true });
  });

  it("settles from its own attempt's settlement", () => {
    const run = { ...paused, last_accept: { attempt: "acc-2", outcome: "interrupted", error: "the engine stopped" } } as Run;
    expect(reconcileAccept(verifying, run).r1).toEqual({ kind: "error", message: "the engine stopped", interrupted: true });
  });

  it("stops waiting once the engine says no accept is in progress and none settled it", () => {
    const st = reconcileAccept(verifying, paused).r1;
    expect(st?.kind).not.toBe("pending");
  });

  it("keeps waiting while the engine still has it in progress", () => {
    const run = { ...paused, accept_in_progress: true, last_accept: { attempt: "acc-1", outcome: "refused" } } as Run;
    expect(reconcileAccept(verifying, run).r1).toEqual(verifying.r1);
  });

  it("ignores a live settlement of another attempt", () => {
    useRuns.setState({ runs: { r1: paused }, events: {}, acceptState: { ...verifying } });
    useRuns.getState().applyEvent({ type: "accept_settled", run_id: "r1", seq: 3, data: { attempt: "acc-1", outcome: "refused", error: "old" } });
    expect(useRuns.getState().acceptState.r1).toEqual(verifying.r1);
    useRuns.getState().applyEvent({ type: "accept_settled", run_id: "r1", seq: 4, data: { attempt: "acc-2", outcome: "accepted", commit_sha: "abc" } });
    expect(useRuns.getState().acceptState.r1).toEqual({ kind: "committed", sha: "abc" });
  });
});

// A settled accept does not regress when the HTTP answer arrives after the
// stream settled it (review of #181, question 4).
describe("a settled accept never regresses", () => {
  it("keeps committed when the HTTP call then fails", () => {
    useRuns.setState({ runs: {}, events: {}, acceptState: { r1: { kind: "committed", sha: "abc" } } });
    useRuns.getState().failAccept("r1", "request timed out");
    expect(useRuns.getState().acceptState.r1).toEqual({ kind: "committed", sha: "abc" });
  });
  it("keeps a settled refusal when a late answer claims a commit", () => {
    useRuns.setState({ runs: {}, events: {}, acceptState: { r1: { kind: "error", message: "refused" } } });
    useRuns.getState().confirmAccept("r1", "abc");
    expect(useRuns.getState().acceptState.r1).toEqual({ kind: "error", message: "refused" });
  });
  it("a new accept starts fresh", () => {
    useRuns.setState({ runs: {}, events: {}, acceptState: { r1: { kind: "error", message: "refused" } } });
    useRuns.getState().beginAccept("r1");
    useRuns.getState().confirmAccept("r1", "def");
    expect(useRuns.getState().acceptState.r1).toEqual({ kind: "committed", sha: "def" });
  });
});

// Adversarial review of #181 (R4): the approval_resolved{landed} event can
// arrive before the approve's HTTP answer ("approved — accept in progress");
// overwriting with the answer moved a landed request back to approved.
describe("an approval request never regresses", () => {
  const req0 = { id: "ar-1", action: "accept", status: "approved", reason: "x", requested_by: "mcp:t" } as ApprovalRequest;
  beforeEach(() => {
    useRuns.setState({ runs: { r1: { id: "r1", status: "paused", approval_requests: [req0] } as unknown as Run }, events: {} });
  });

  it("keeps landed when the HTTP answer arrives after approval_resolved", () => {
    useRuns.getState().applyEvent({ type: "approval_resolved", run_id: "r1", data: {
      request: { ...req0, status: "landed", outcome: "accepted as abc123" } } } as never);
    const s = useRuns.getState();
    const run = s.runs.r1!;
    useRuns.setState({ runs: { ...s.runs, r1: { ...run, approval_requests: upsertApproval(run.approval_requests,
      { ...req0, outcome: "accept in progress" } as never) } } });
    expect(useRuns.getState().runs.r1!.approval_requests![0]!.status).toBe("landed");
  });

  it("keeps landed when a stale run snapshot replaces the run", () => {
    useRuns.getState().applyEvent({ type: "approval_resolved", run_id: "r1", data: {
      request: { ...req0, status: "landed", outcome: "accepted as abc123" } } } as never);
    const stale = { id: "r1", status: "paused", approval_requests: [{ ...req0, outcome: "accept in progress" }] } as unknown as Run;
    useRuns.getState().setRuns([stale]);
    expect(useRuns.getState().runs.r1!.approval_requests![0]!.status).toBe("landed");
    useRuns.getState().setRun(stale);
    expect(useRuns.getState().runs.r1!.approval_requests![0]!.status).toBe("landed");
    useRuns.getState().resyncRun(stale, []);
    expect(useRuns.getState().runs.r1!.approval_requests![0]!.status).toBe("landed");
  });

  it("still moves forward: pending → approved → landed", () => {
    const list = upsertApproval([{ ...req0, status: "pending" }], { ...req0, status: "approved" });
    expect(list[0]!.status).toBe("approved");
    expect(upsertApproval(list, { ...req0, status: "landed" })[0]!.status).toBe("landed");
  });
});
