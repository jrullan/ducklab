import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient, type Run } from "../api/client";

// B-517, TI-36X: the intake r-20261010-115519-tpfn FAILED its post-composition
// review and paused at the gate with a redo note. "Retry with this note" sent
// POST /runs — a build with no task, mode council — three times; each build
// died at once on `unknown mode "council"`. The correct retry was the
// revision r-20261010-121543-eett: POST /stages/intake with `revise`.
const NOTE =
  "Revise the intake draft to address the failure.\n\nReviewer's blocking findings:\n" +
  "- [major] REQ-008 invents behavior (polar conversion of a real scalar raising an LCD error)";

const failedIntake: Run = {
  id: "r-20261010-115519-tpfn", project_id: "ti-36x-pro", stage: "intake", mode: "council", task_id: "",
  status: "paused", verdict: "FAILED", started_at: "2026-10-10T11:55:19Z",
  pending_kind: "gate", pending_since: "2026-10-10T11:57:47Z",
  roster: { architect: "k3", reviewer: "glm53flash" },
  next: ["request_changes", "reject"],
  redo_note: { draft: NOTE, origin: "ducklab", reason: "Reviewer's blocking findings:\n- [major] REQ-008", editable: true },
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

type Sent = { method: string; path: string; body: Record<string, unknown> };

function recording(sent: Sent[], refuse?: string) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string, init?: RequestInit) => {
      const path = String(url).replace("http://engine", "");
      if (init?.method === "POST") {
        sent.push({ method: "POST", path, body: init.body ? JSON.parse(String(init.body)) : {} });
        if (refuse) return json({ error: { code: "invalid_request", message: refuse } }, 400);
        return json({ ...failedIntake, id: "r-new", status: "running", next: ["abort"] });
      }
      if (path.endsWith("/diff")) return json({ diff: "" });
      if (path.endsWith("/verify")) return json({ output: "" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    }) as unknown as typeof fetch,
  });
}

const show = (run: Run, sent: Sent[], refuse?: string) => {
  useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "open" });
  useRuns.getState().setRun(run);
  render(<RunView runId={run.id} client={recording(sent, refuse)} />);
};

const retryButton = () => screen.findByRole("button", { name: "Retry with this note" });

beforeEach(() => cleanup());

describe("RunView — Retry with this note goes through the run's own door", () => {
  it("revises the failed intake with the note, never starting a build", async () => {
    const sent: Sent[] = [];
    show(failedIntake, sent);
    fireEvent.click(await retryButton());
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]!.path).toBe("/v1/projects/ti-36x-pro/stages/intake");
    expect(sent[0]!.body.stage).toBe("intake");
    expect(sent[0]!.body.revise).toBe(NOTE);
    expect(sent.some((s) => s.path.endsWith("/runs"))).toBe(false);
  });

  // The whole class: every document stage that waits at a gate with a note.
  for (const [stage, status, next] of [
    ["spec", "paused", ["request_changes", "reject"]],
    ["plan", "paused", ["request_changes", "reject"]],
  ] as const) {
    it(`revises a ${status} ${stage} run`, async () => {
      const sent: Sent[] = [];
      show({ ...failedIntake, stage, status, next: [...next] }, sent);
      fireEvent.click(await retryButton());
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0]!.path).toBe(`/v1/projects/ti-36x-pro/stages/${stage}`);
      expect(sent[0]!.body.revise).toBe(NOTE);
    });
  }

  it("revises a release draft through the release door", async () => {
    const sent: Sent[] = [];
    show({ ...failedIntake, stage: "release" }, sent);
    fireEvent.click(await retryButton());
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]!.path).toBe("/v1/projects/ti-36x-pro/releases");
    expect(sent[0]!.body.revise).toBe(NOTE);
  });

  it("replays a plan amendment's own request on retry", async () => {
    const sent: Sent[] = [];
    show({ ...failedIntake, stage: "plan", mode: "solo", stage_request: { stage: "plan", extend: "add a CSV export", mode: "solo", rounds: 1 } }, sent);
    fireEvent.click(await retryButton());
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]!.body).toMatchObject({ extend: "add a CSV export", mode: "solo", rounds: 1, revise: NOTE });
  });

  it("still relaunches a failed build as a build of its task", async () => {
    const sent: Sent[] = [];
    show({ ...failedIntake, stage: "build", mode: "pair", task_id: "T-009", status: "paused", pending_kind: "gate", next: ["reject"], roster: { implementer: "luna", reviewer: "glm52" } }, sent);
    fireEvent.click(await retryButton());
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]!.path).toBe("/v1/projects/ti-36x-pro/runs");
    expect(sent[0]!.body).toMatchObject({ task_id: "T-009", mode: "pair", note: NOTE, redo: true });
  });

  it("still relaunches a failed test-first run as a test", async () => {
    const sent: Sent[] = [];
    show({ ...failedIntake, stage: "test", mode: "solo", task_id: "T-009", status: "paused", pending_kind: "gate", next: ["reject"], roster: { implementer: "luna" } }, sent);
    fireEvent.click(await retryButton());
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]!.path).toBe("/v1/projects/ti-36x-pro/tests");
    expect(sent[0]!.body.task_id).toBe("T-009");
    expect(sent[0]!.body.note).toBe(NOTE);
  });

  // The broken builds themselves (r-20261010-120758-gjs4): no task, nothing
  // to build. The note stays readable; no button offers a doomed launch.
  it("offers no retry on a code run without a task, nor on a stage with no relaunch", async () => {
    for (const over of [
      { stage: "build", task_id: "", status: "paused", pending_kind: "gate", next: ["reject"] },
      { stage: "triage", task_id: "", status: "paused", pending_kind: "gate", next: ["reject"] },
    ] as Partial<Run>[]) {
      cleanup();
      show({ ...failedIntake, ...over }, []);
      await screen.findByTestId("redo-note");
      expect(screen.queryByRole("button", { name: "Retry with this note" })).toBeNull();
    }
  });

  it("shows the engine's refusal instead of a silent failed run", async () => {
    const sent: Sent[] = [];
    const refusal = "a build needs a task: name the task to build.";
    show({ ...failedIntake, stage: "build", mode: "solo", task_id: "T-009", status: "paused", pending_kind: "gate", next: ["reject"], roster: { implementer: "luna" } }, sent, refusal);
    fireEvent.click(await retryButton());
    expect(await screen.findByText(new RegExp(refusal))).toBeTruthy();
  });

  it("shows a refused stage revision too", async () => {
    const refusal = "no intake draft to revise";
    show(failedIntake, [], refusal);
    fireEvent.click(await retryButton());
    expect(await screen.findByText(new RegExp(refusal))).toBeTruthy();
  });
});
