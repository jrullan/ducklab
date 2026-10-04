import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient, type Run } from "../api/client";
import { buildTurns } from "../lib/runview";

// B-493: TI-36X T-014's chained build paused on an error whose fix the person
// knew. The decision card offered Resume and Abort and no way to say it.
const paused: Run = {
  id: "r-20261003-145618-bbdk", project_id: "ti36x", stage: "build", mode: "solo", task_id: "T-014",
  status: "paused", verdict: "", started_at: "2026-10-03T14:56:18Z",
  pending_kind: "error", pending_since: "2026-10-03T15:20:00Z", failure: "the run stopped",
  roster: { implementer: "pato-uno" },
  next: ["resume", "abort"],
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function recording(sent: { path?: string; body?: string | null }) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string, init?: RequestInit) => {
      const path = String(url).replace("http://engine", "");
      if (init?.method === "POST" && path.endsWith("/resume")) {
        sent.path = path;
        sent.body = init.body == null ? null : String(init.body);
        return json({ ...paused, status: "running", pending_kind: undefined, next: ["abort"] });
      }
      if (path.endsWith("/diff")) return json({ diff: "" });
      if (path.endsWith("/verify")) return json({ output: "" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    }) as unknown as typeof fetch,
  });
}

const show = (over: Partial<Run>, sent: { path?: string; body?: string | null }) => {
  useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "open" });
  useRuns.getState().setRun({ ...paused, ...over });
  render(<RunView runId={paused.id} client={recording(sent)} />);
};

beforeEach(() => cleanup());

describe("RunView — a note beside Resume", () => {
  // One cell per pause kind that offers Resume with a note, for both stages
  // that resume from the record.
  for (const stage of ["build", "test"]) {
    for (const kind of ["error", "budget", "provider"]) {
      it(`sends the note with Resume on a ${stage} run paused on ${kind}`, async () => {
        const sent: { path?: string; body?: string | null } = {};
        show({ stage, pending_kind: kind }, sent);
        fireEvent.change(await screen.findByLabelText("resume note"), {
          target: { value: "  2^-9 is 0.001953125; write the path without a leading slash  " },
        });
        expect(screen.getByTestId("resume-button").textContent).toBe("Resume with note");
        fireEvent.click(screen.getByTestId("resume-button"));
        await waitFor(() => expect(sent.path).toBe(`/v1/runs/${paused.id}/resume`));
        expect(JSON.parse(sent.body!)).toEqual({ note: "2^-9 is 0.001953125; write the path without a leading slash" });
      });
    }
  }

  it("resumes without a body when the note is left empty", async () => {
    const sent: { path?: string; body?: string | null } = {};
    show({}, sent);
    await screen.findByLabelText("resume note");
    expect(screen.getByTestId("resume-button").textContent).toBe("Resume");
    fireEvent.click(screen.getByTestId("resume-button"));
    await waitFor(() => expect(sent.path).toBe(`/v1/runs/${paused.id}/resume`));
    expect(sent.body ?? null).toBeNull();
  });

  // The engine refuses a note where it cannot ride: an engine restart has no
  // knowable fix to tell, and a document stage takes a revision instead.
  it("offers no note field where the engine takes none", async () => {
    for (const over of [{ pending_kind: "engine_restart" }, { stage: "plan" }] as Partial<Run>[]) {
      cleanup();
      show(over, {});
      await screen.findByTestId("resume-button");
      expect(screen.queryByLabelText("resume note")).toBeNull();
    }
  });
});

describe("conversation lane — what the person said on resume", () => {
  it("shows the resume note on the pause it answered", () => {
    const blocks = buildTurns([
      { type: "human_needed", run_id: "r", seq: 1, data: { kind: "error", detail: "the run stopped" } },
      { type: "checkpoint", run_id: "r", seq: 2, data: { reason: "resume", status: "running", note: "2^-9 is 0.001953125", actor: "human" } },
    ] as never);
    const pause = blocks.find((b) => b.role === "pause");
    expect(pause?.pause).toEqual({ reason: "the run stopped", resumed: true, note: "2^-9 is 0.001953125" });
  });
});
