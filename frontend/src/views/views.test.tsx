import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
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
  // What the engine states for a build run paused at a green gate. Fixtures
  // carry it because the view renders from this list and nothing else.
  next: ["accept", "reject"],
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

  // A failed run showed FAILED and nothing else: the reason went to an `error`
  // event, and the timeline renders only tool calls and policy violations. The
  // only way to learn why a run died was to open events.jsonl. Some of these
  // messages are written to be acted on — split names the file two subtasks
  // both claimed, which is useless in a log nobody reads.
  it("says why a failed run failed", () => {
    useRuns.getState().setRun({
      ...run,
      status: "failed",
      verdict: "FAILED",
      pending_kind: undefined,
      failure: '"index.html" is claimed by both "Solver" and "Renderer"',
    });
    render(<RunView runId="r-1" client={okClient()} />);
    expect(screen.getByTestId("run-failure").textContent).toContain("claimed by both");
  });

  it("shows no failure banner on a run that did not fail", () => {
    useRuns.getState().setRun(run);
    render(<RunView runId="r-1" client={okClient()} />);
    expect(screen.queryByTestId("run-failure")).toBeNull();
  });

  it("offers Clean or Commit recovery from a dirty-tree retire refusal", () => {
    useRuns.getState().setRun({
      ...run,
      stage: "test",
      status: "failed",
      verdict: "FAILED",
      pending_kind: undefined,
      failure: "retire-test refused: working tree is dirty; commit or clean them",
    });
    render(<RunView runId="r-1" client={okClient()} />);

    const failure = screen.getByTestId("run-failure");
    expect(failure.textContent).toContain("commit or clean them");
    expect(
      failure.querySelectorAll("button, a").length,
      "the recovery instruction must have an actionable Clean or Commit control",
    ).toBeGreaterThan(0);
    expect(failure.textContent).toMatch(/clean|commit/i);
  });

  it("does not offer Clean or Commit recovery for unrelated failures", () => {
    useRuns.getState().setRun({
      ...run,
      status: "failed",
      verdict: "FAILED",
      pending_kind: undefined,
      failure: '"index.html" is claimed by both "Solver" and "Renderer"',
    });
    render(<RunView runId="r-1" client={okClient()} />);

    const failure = screen.getByTestId("run-failure");
    expect(failure.textContent).not.toMatch(/\b(clean|commit)\b/i);
    expect(failure.querySelectorAll("button, a")).toHaveLength(0);
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

    fireEvent.click(screen.getByTestId("cycle-accept"));
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

    fireEvent.click(screen.getByTestId("cycle-accept"));
    await waitFor(() => expect(screen.getByTestId("accept-error")).toBeTruthy());
    expect(screen.queryByTestId("accept-committed")).toBeNull();
    expect(useRuns.getState().runs["r-1"]!.commit_sha).toBeUndefined();
  });

  // A FAILED verdict has nothing to accept, and now the ENGINE says so: its
  // next list omits the verb, so the button does not exist at all rather than
  // existing disabled.
  it("offers no accept on a failed verdict", () => {
    useRuns.getState().setRun({ ...run, verdict: "FAILED", next: ["reject"] });
    render(<RunView runId="r-1" client={okClient()} />);
    expect(screen.queryByTestId("cycle-accept")).toBeNull();
    expect(screen.getByTestId("reject-button")).toBeTruthy();
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

  it("shows recorded wall clock in the spent header", () => {
    useRuns.getState().setRun({
      ...run,
      status: "done",
      wallclock_ms: 47 * 60 * 1000,
    });
    render(<RunView runId="r-1" client={okClient()} />);
    expect(screen.getByTestId("spent-header").textContent).toContain("· 47m");
  });

  it("ticks the live budget header wall clock", () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-07-26T12:00:00Z"));
      useRuns.getState().setRun({
        ...run,
        status: "running",
        started_at: "2026-07-26T12:00:00Z",
      });
      render(<RunView runId="r-1" client={okClient()} />);
      expect(screen.getByTestId("budget-header").textContent).toContain("· 0m");
      act(() => vi.advanceTimersByTime(60_000));
      expect(screen.getByTestId("budget-header").textContent).toContain("· 1m");
    } finally {
      vi.useRealTimers();
    }
  });
});

// A gate is only worth what the tests are worth. When a run edits tests the
// task never asked about, those hunks go in front of the person deciding — not
// somewhere below in a diff they may not scroll (05 §5.3).
describe("RunView and a run that edited tests", () => {
  const testDiff = "diff --git a/add_test.go b/add_test.go\n--- a/add_test.go\n+++ b/add_test.go\n@@ -1 +1 @@\n-want := 4\n+want := 6\n";

  const tamperClient = () =>
    clientWith((path) => {
      if (path.endsWith("/diff"))
        return json({ diff: "diff --git a/add.go b/add.go\n--- a/add.go\n+++ b/add.go\n@@ -1 +1 @@\n+return a * b\n" + testDiff, tests: testDiff });
      if (path.endsWith("/verify")) return json({ output: "ok" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    });

  beforeEach(() => useRuns.setState({ runs: {}, connection: "open" }));

  it("puts the test hunks in front of the reader, with the reason", async () => {
    useRuns.getState().setRun(run);
    render(<RunView runId="r-1" client={tamperClient()} />);

    const flagged = await screen.findByTestId("tests-modified");
    expect(flagged.textContent).toContain("read these hunks before accepting");
    expect(flagged.textContent).toContain("want := 6");
    // Above the full diff, not buried in it.
    const views = screen.getAllByTestId("diff-view");
    expect(views).toHaveLength(2);
    expect(flagged.contains(views[0]!)).toBe(true);
    expect(flagged.compareDocumentPosition(views[1]!)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(screen.getByTestId("tab-diff").textContent).toContain("edits tests");
  });

  // Sometimes a test is genuinely wrong. Ducklab reports; the human decides.
  it("does not block accepting", async () => {
    useRuns.getState().setRun(run);
    render(<RunView runId="r-1" client={tamperClient()} />);
    await screen.findByTestId("tests-modified");
    expect(screen.getByTestId("cycle-accept").hasAttribute("disabled")).toBe(false);
  });

  it("says nothing when the run touched no tests", async () => {
    useRuns.getState().setRun(run);
    render(<RunView runId="r-1" client={okClientForTamper()} />);
    await screen.findByTestId("diff-view");
    expect(screen.queryByTestId("tests-modified")).toBeNull();
  });

  const okClientForTamper = () =>
    clientWith((path) => {
      if (path.endsWith("/diff")) return json({ diff: "diff --git a/add.go b/add.go\n--- a/add.go\n+++ b/add.go\n@@ -1 +1 @@\n+return a + b\n" });
      if (path.endsWith("/verify")) return json({ output: "ok" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    });
});

// The buttons belong to a decision, and a decision that has been made is not
// still open. Reported from a real session: an accepted run went on offering
// Accept, Reject and Abort, as if nothing had happened.
// The buttons render from run.next — the engine's own list — and never from
// this view's opinion of the state. These tests hand the view what the engine
// would state and assert it draws exactly that.
describe("RunView — what can still be decided", () => {
  const client = () =>
    clientWith((path) => {
      if (path.endsWith("/diff")) return json({ diff: "" });
      if (path.endsWith("/verify")) return json({ output: "" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    });

  const show = (over: Record<string, unknown>) => {
    useRuns.setState({ runs: {}, connection: "open" });
    useRuns.getState().setRun({ ...run, ...over });
    render(<RunView runId="r-1" client={client()} />);
  };

  it("offers nothing to decide on a run that was accepted", async () => {
    show({ status: "done", verdict: "PASSED", accepted: true, commit_sha: "e60dc7fe1234", next: [] });
    await screen.findByTestId("run-view");
    for (const id of ["accept-button", "reject-button", "abort-button"]) {
      expect(screen.queryByTestId(id)).toBeNull();
    }
    // And says what became of it, which is the thing worth knowing.
    expect(screen.getByTestId("run-outcome").textContent).toContain("e60dc7f");
  });

  it("offers nothing on a rejected or failed run", async () => {
    show({ status: "done", verdict: "FAILED", accepted: false, next: [] });
    await screen.findByTestId("run-view");
    expect(screen.queryByTestId("cycle-accept")).toBeNull();
    expect(screen.queryByTestId("abort-button")).toBeNull();
  });

  // A run still working can be stopped, and nothing else: there is no result
  // to accept yet.
  it("offers only Abort while a run is working", async () => {
    show({ status: "running", verdict: "", pending_kind: undefined, next: ["abort"] });
    await screen.findByTestId("run-view");
    expect(screen.getByTestId("abort-button")).toBeTruthy();
    expect(screen.queryByTestId("cycle-accept")).toBeNull();
  });

  // At a gate the run is not working — it is waiting for this decision — so
  // Abort has nothing to stop. Offering it beside Reject made them look like
  // two ways to say no, when only one records a decision.
  it("offers accept and reject at the gate, and not abort", async () => {
    show({ status: "paused", pending_kind: "gate", verdict: "PASSED", stage: "build", next: ["accept", "reject"] });
    await screen.findByTestId("run-view");
    expect(screen.getByTestId("cycle-accept")).toBeTruthy();
    expect(screen.getByTestId("reject-button")).toBeTruthy();
    expect(screen.queryByTestId("abort-button")).toBeNull();
  });

  // A run paused on a question needs an answer, not a verdict: accepting work
  // that has not finished would commit a half-done change.
  it("does not offer Accept while a question is unanswered", async () => {
    show({ status: "paused", pending_kind: "question", verdict: "", next: ["answer", "abort"] });
    await screen.findByTestId("run-view");
    expect(screen.queryByTestId("cycle-accept")).toBeNull();
    expect(screen.getByTestId("abort-button")).toBeTruthy();
  });
});

// The human gate of a stage run appears in two places — the Cycle view and the
// run itself — and only one of them had the third answer. Someone watching the
// work happen had to go and find another screen to say "almost".
describe("RunView — asking a stage for changes", () => {
  const show = (over: Record<string, unknown>) => {
    useRuns.setState({ runs: {}, connection: "open" });
    useRuns.getState().setRun({
      ...run, status: "paused", pending_kind: "gate",
      next: ["accept", "request_changes", "reject"], ...over,
    });
  };

  // The real request path, so the body is asserted as the engine would see it.
  const recording = (sent: { path?: string; body?: string }) =>
    clientWith((path, init) => {
      if (init?.method === "POST" && path.includes("/stages/")) {
        sent.path = path;
        sent.body = String(init.body);
        return json({ id: "r-2" });
      }
      if (path.endsWith("/diff")) return json({ diff: "" });
      if (path.endsWith("/verify")) return json({ output: "" });
      if (path.endsWith("/candidates")) return json({ items: [] });
      return json({});
    });

  it("sends the note as a revision of that stage", async () => {
    show({ stage: "spec", project_id: "p" });
    const sent: { path?: string; body?: string } = {};
    render(<RunView runId="r-1" client={recording(sent)} />);

    fireEvent.change(await screen.findByTestId("change-note"), {
      target: { value: "SPEC-004 should lock the opposite vertex too" },
    });
    fireEvent.click(screen.getByTestId("request-changes-button"));

    await waitFor(() => expect(sent.path).toBe("/v1/projects/p/stages/spec"));
    expect(JSON.parse(sent.body!).revise).toBe("SPEC-004 should lock the opposite vertex too");
  });

  // A build run produces code. There is no draft to send back to anyone.
  it("offers nothing to revise on a build run", async () => {
    show({ stage: "build", project_id: "p" });
    render(<RunView runId="r-1" client={recording({})} />);
    await screen.findByTestId("run-view");
    expect(screen.queryByTestId("request-changes-button")).toBeNull();
  });

  it("offers nothing to revise once the gate is answered", async () => {
    show({ stage: "spec", project_id: "p", status: "done", accepted: true, pending_kind: "", next: [] });
    render(<RunView runId="r-1" client={recording({})} />);
    await screen.findByTestId("run-view");
    expect(screen.queryByTestId("request-changes-button")).toBeNull();
  });
});

// The settings sub-menu: one concern on screen at a time (the user's own
// mock). Config cards stay mounted — hidden, not unmounted — so switching
// sections never loses unsaved edits or the single Save.
describe("the settings sub-menu", () => {
  it("shows one section at a time and keeps the others mounted", () => {
    render(
      <Settings theme="system" onTheme={() => {}} engineVersion="1" connection="open" />,
    );
    // Default section is the team; the engine card exists but is hidden.
    const engineBtn = screen.getByTestId("settings-nav-engine");
    const engineCard = screen.getByText(/environment variables/).closest("div")!;
    expect(engineCard.className).toContain("hidden");
    fireEvent.click(engineBtn);
    expect(screen.getByText(/environment variables/).closest("div")!.className).not.toContain(
      "hidden",
    );
  });
});
