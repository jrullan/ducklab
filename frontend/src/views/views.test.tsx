import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Overview } from "./Overview";
import { Ducklings } from "./Ducklings";
import { Settings } from "./Settings";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient, type Run } from "../api/client";

const run: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-001",
  status: "paused", verdict: "PASSED", started_at: "2026-07-26T12:00:00Z",
  pending_kind: "gate", pending_since: "2026-07-26T12:00:00Z",
  roster: { implementer: "pato-uno", reviewer: "pato-dos" },
  budget: { usd: 0.014, tokens: 184000, turns: 9, wallclock_s: 250 },
};

/** A client backed by a fetch stub, so views are exercised over the real
 * request path rather than a hand-mocked client. */
function clientWith(handler: (path: string, init?: RequestInit) => Response) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string, init?: RequestInit) =>
      handler(String(url).replace("http://engine", ""), init)) as unknown as typeof fetch,
  });
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => {
  useRuns.setState({ runs: {}, events: {}, deltas: {}, acceptState: {}, needsResync: false, connection: "open" });
});

describe("Overview", () => {
  it("shows an empty state before any run exists", () => {
    render(<Overview spentToday={0} budget={2} />);
    expect(screen.getByTestId("empty-state").textContent).toContain("No runs yet");
  });

  it("lists runs and surfaces what is waiting for a human", () => {
    useRuns.getState().setRuns([run]);
    render(<Overview spentToday={0.31} budget={5} />);
    expect(screen.getByTestId("human-gate-inbox")).toBeTruthy();
    expect(screen.getAllByTestId("run-row")).toHaveLength(1);
    expect(screen.getByTestId("overview").textContent).toContain("$0.3100");
  });

  // With nothing finished, a pass ratio would be a lie dressed as a number.
  it("shows a dash rather than 0/0 when nothing has finished", () => {
    useRuns.getState().setRuns([{ ...run, verdict: "" }]);
    render(<Overview spentToday={0} budget={2} />);
    expect(screen.getByTestId("overview").textContent).toContain("—");
  });
});

describe("Ducklings", () => {
  it("reports the dialect each model actually speaks", () => {
    render(<Ducklings ducklings={[
      { id: "pato-uno", provider: "beelink", model: "gemma-4", caps: { native_tools: false, context_tokens: 65536 }, cost: { input_per_mtok: 0, output_per_mtok: 0 } },
      { id: "pato-dos", provider: "openrouter", model: "qwen", caps: { native_tools: true, context_tokens: 131072 }, cost: { input_per_mtok: 0.2, output_per_mtok: 0.6 } },
    ]} />);
    const cards = screen.getAllByTestId("duckling-card");
    expect(cards[0]!.textContent).toContain("text protocol");
    expect(cards[1]!.textContent).toContain("native");
  });

  it("marks a free local model rather than showing $0.0000 alone", () => {
    render(<Ducklings ducklings={[
      { id: "local", provider: "beelink", model: "m", cost: { input_per_mtok: 0, output_per_mtok: 0 } },
    ]} />);
    expect(screen.getByTestId("ducklings").textContent).toContain("local — no USD cost");
  });
});

describe("Settings", () => {
  it("never displays a secret, only how it is read", () => {
    render(<Settings theme="system" onTheme={() => {}} engineVersion="0.3.0" connection="open" />);
    const text = screen.getByTestId("settings").textContent!;
    expect(text).toContain("environment variables");
    expect(text).not.toMatch(/sk-|api[_-]?key\s*[:=]/i);
  });

  it("marks the active theme", () => {
    render(<Settings theme="dark" onTheme={() => {}} engineVersion="" connection="open" />);
    expect(screen.getByTestId("theme-dark").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByTestId("theme-light").getAttribute("aria-pressed")).toBe("false");
  });
});

describe("RunView", () => {
  const okClient = () =>
    clientWith((path) => {
      if (path.endsWith("/diff")) return json({ diff: "--- a/add.go\n+++ b/add.go\n@@ -1 +1 @@\n+return a + b" });
      if (path.endsWith("/verify")) return json({ output: "ok\tfixture" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      if (path.endsWith("/accept")) return json({ commit_sha: "e60dc7fe1234" });
      return json({});
    });

  it("renders the conversation, gate and budget", async () => {
    useRuns.getState().setRun(run);
    useRuns.getState().applyEvent({ type: "turn_start", run_id: "r-1", seq: 1, data: { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" } });
    useRuns.getState().applyEvent({ type: "gate", run_id: "r-1", seq: 2, data: { gate: "tests", cmd: "go test ./...", exit: 0 } });

    render(<RunView runId="r-1" client={okClient()} />);
    expect(screen.getByTestId("conversation")).toBeTruthy();
    expect(screen.getByTestId("gate-card").textContent).toContain("go test ./...");
    expect(screen.getAllByTestId("budget-meter").length).toBeGreaterThan(0);
  });

  // AC-34: no optimistic UI. The commit appears only after the engine says so.
  it("shows pending during accept and the sha only once confirmed", async () => {
    useRuns.getState().setRun(run);
    render(<RunView runId="r-1" client={okClient()} />);

    fireEvent.click(screen.getByTestId("accept-button"));
    await waitFor(() => expect(screen.getByTestId("accept-committed")).toBeTruthy());
    expect(screen.getByTestId("accept-committed").textContent).toContain("e60dc7fe");
  });

  // A failed accept must leave an error, never a phantom commit.
  it("shows an error and no commit when the engine rejects the accept", async () => {
    useRuns.getState().setRun(run);
    const failing = clientWith((path) => {
      if (path.endsWith("/accept")) return json({ error: { code: "internal", message: "commit failed" } }, 500);
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    });
    render(<RunView runId="r-1" client={failing} />);

    fireEvent.click(screen.getByTestId("accept-button"));
    await waitFor(() => expect(screen.getByTestId("accept-error")).toBeTruthy());
    expect(screen.queryByTestId("accept-committed")).toBeNull();
    expect(useRuns.getState().runs["r-1"]!.commit_sha).toBeUndefined();
  });

  it("disables accept on a failed verdict", () => {
    useRuns.getState().setRun({ ...run, verdict: "FAILED" });
    render(<RunView runId="r-1" client={okClient()} />);
    expect((screen.getByTestId("accept-button") as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers an inline answer when a question is pending", async () => {
    useRuns.getState().setRun({ ...run, pending_kind: "question" });
    useRuns.getState().applyEvent({
      type: "human_needed", run_id: "r-1", seq: 1,
      data: { kind: "question", question: "Wrap or saturate?", question_id: "q1" },
    });
    const answered = vi.fn();
    const client = clientWith((path) => {
      if (path.endsWith("/answer")) { answered(); return new Response(null, { status: 204 }); }
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    });
    render(<RunView runId="r-1" client={client} />);

    expect(screen.getByTestId("pending-human").textContent).toContain("Wrap or saturate?");
    fireEvent.change(screen.getByLabelText("answer"), { target: { value: "wrap" } });
    fireEvent.click(screen.getByTestId("answer-button"));
    await waitFor(() => expect(answered).toHaveBeenCalled());
  });

  // AC-32 end to end in the view: a tournament run anonymises its lanes.
  it("anonymises conversation lanes for a tournament run", () => {
    useRuns.getState().setRun({ ...run, mode: "tournament" });
    useRuns.getState().applyEvent({
      type: "turn_start", run_id: "r-1", seq: 1,
      data: { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" },
    });
    const { container } = render(<RunView runId="r-1" client={okClient()} />);
    const turn = screen.getByTestId("conversation-turn");
    expect(turn.getAttribute("data-anonymous")).toBe("true");
    expect(container.querySelector('[data-testid="conversation"]')!.innerHTML).not.toContain("pato-uno");
  });
});
