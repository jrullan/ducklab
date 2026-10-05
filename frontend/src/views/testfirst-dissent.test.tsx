import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { RunView } from "./RunView";
import { Runs } from "./Runs";
import { Now } from "./Now";
import { WaitingCard } from "../components/WaitingCard";
import { useRuns } from "../store/runs";
import type { EngineClient, Run } from "../api/client";
import type { DucklabEvent } from "../api/events";

// B-501: TI-36X T-005's test-first pair ended both rounds on request-changes
// (two majors open) with a red gate, and every surface read a bare PASSED —
// or worse, "the tests pass; the reviewer only advises", false on both counts
// for a test that fails by design and becomes the build's oracle (B-490).
const build: Run = {
  id: "r-1", project_id: "p", stage: "build", mode: "pair", task_id: "T-005",
  status: "paused", verdict: "PASSED", started_at: "2026-10-04T21:27:15Z",
  pending_kind: "gate", pending_since: "2026-10-04T21:50:00Z",
  roster: { implementer: "luna", reviewer: "qwen38-max" },
  next: ["accept", "reject"],
};
const testFirst: Run = { ...build, stage: "test" };

const ev = (type: string, seq: number, data: Record<string, unknown>) =>
  ({ type, seq, run_id: "r-1", data }) as unknown as DucklabEvent;

const events = [
  ev("turn_start", 1, { round: 2, turn: 0, role: "implementer", duckling: "luna" }),
  ev("turn_end", 2, { round: 2, turn: 0, role: "implementer" }),
  ev("turn_start", 3, { round: 2, turn: 1, role: "reviewer", duckling: "qwen38-max" }),
  ev("message", 4, { round: 2, turn: 1, content: "…", verdict: "request-changes", findings: [
    { severity: "major", file: "tests/lcd-flow.test.mjs", line: 283, issue: "13 tests pin T-003 semantics", fix: "assert only the LCD flow" },
    { severity: "major", file: "tests/lcd-flow.test.mjs", line: 104, issue: "cursor actions not asserted inert", fix: "assert inert outside entry" },
  ] }),
  ev("turn_end", 5, { round: 2, turn: 1, role: "reviewer" }),
  ev("gate", 6, { gate: "tests", cmd: "npm test", exit: 1, phase: "after" }),
  ev("verdict", 7, { verdict: "PASSED" }),
];

const clientFor = (r: Run) =>
  ({
    run: vi.fn(() => Promise.resolve({ run: r, events })),
    runDiff: vi.fn(() => Promise.resolve({ diff: "", tests: "" })),
    runVerify: vi.fn(() => Promise.resolve("")),
    runCandidates: vi.fn(() => Promise.resolve([])),
    runLLM: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve([])),
    report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    tasks: vi.fn(() => Promise.resolve([])),
    runFileFindings: vi.fn(() => Promise.resolve({ items: [{ id: "B-77" }, { id: "B-78" }] })),
    nextFor: vi.fn(() => Promise.resolve({ ref: "T-005", kind: "task", rungs: [], steps: [] })),
  }) as unknown as EngineClient;

const seed = (r: Run) =>
  useRuns.setState({
    runs: { [r.id]: r },
    events: { "r-1": events }, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, connection: "open",
  });

describe("RunView decision card — a test-first whose reviewer still requests changes", () => {
  it("frames the objection as the oracle it would lock in, findings offered for filing", async () => {
    seed(testFirst);
    render(<RunView runId="r-1" client={clientFor(testFirst)} />);
    const card = await screen.findByTestId("decision-card");
    const dissent = within(card).getByTestId("decision-dissent");
    expect(dissent.textContent).toContain("reviewer still requests changes");
    expect(dissent.textContent).toContain("build's oracle");
    expect(dissent.textContent).not.toContain("The tests pass");
    expect(within(dissent).getByTestId("dissent-findings-list").textContent).toContain("13 tests pin T-003 semantics");
    expect(within(card).getByTestId("file-findings-button").textContent).toContain("File 2 findings as bugs");
  });

  it("shows it on an UNVERIFIED test-first too — Accept is still offered there", async () => {
    const unverified: Run = { ...testFirst, verdict: "UNVERIFIED" };
    seed(unverified);
    render(<RunView runId="r-1" client={clientFor(unverified)} />);
    const card = await screen.findByTestId("decision-card");
    expect(within(card).getByTestId("decision-dissent").textContent).toContain("build's oracle");
  });

  it("a build keeps its own wording", async () => {
    seed(build);
    render(<RunView runId="r-1" client={clientFor(build)} />);
    const card = await screen.findByTestId("decision-card");
    expect(within(card).getByTestId("decision-dissent").textContent).toContain("The tests pass");
    expect(within(card).getByTestId("decision-dissent").textContent).not.toContain("oracle");
  });
});

describe("Runs table — a test-first passed over standing objections", () => {
  it("names the standing objection in the outcome", () => {
    const objected: Run = {
      ...testFirst, status: "done",
      review_evidence: { status: "dissent", independence: "independent", verdict: "request-changes", findings: 3 },
    };
    render(<Runs runs={[objected]} />);
    expect(screen.getByTestId("run-outcome")).toHaveTextContent("passed · reviewer still requests changes");
  });
});

describe("WaitingCard — standing reviewer objection from the engine's gate record", () => {
  const objected: Run = {
    ...testFirst,
    id: "r-tf",
    pending_data: {
      kind: "test_first",
      dissent: "request-changes",
      dissent_total: 3,
      dissent_findings: [
        { severity: "major", file: "tests/lcd-flow.test.mjs", line: 283, issue: "13 tests pin T-003 semantics" },
        { severity: "major", file: "tests/lcd-flow.test.mjs", line: 104, issue: "cursor actions not asserted inert" },
      ],
    },
  };

  it("shows the objection and files the findings through the engine", async () => {
    const runFileFindings = vi.fn(() => Promise.resolve({ items: [{ id: "B-9", title: "x" }] }));
    const client = { runFileFindings } as unknown as EngineClient;
    render(<WaitingCard client={client} run={objected} accepting={false} onAccept={vi.fn()} onReject={vi.fn()} onAbort={vi.fn()} />);
    const block = screen.getByTestId("waiting-dissent");
    expect(block).toHaveTextContent("reviewer still requests changes");
    expect(block).toHaveTextContent("3 findings, 2 blocking");
    expect(screen.getByTestId("waiting-dissent-findings")).toHaveTextContent("13 tests pin T-003 semantics");
    expect(screen.getByTestId("waiting-explanation")).toHaveTextContent("build's oracle");
    // The person still decides: Accept stays where the engine offered it.
    expect(screen.getByTestId("now-accept")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("waiting-file-findings"));
    expect(runFileFindings).toHaveBeenCalledWith("r-tf");
    expect(await screen.findByTestId("waiting-findings-filed")).toHaveTextContent("B-9");
  });

  it("an approved gate draws no objection", () => {
    render(<WaitingCard run={{ ...objected, pending_data: { kind: "test_first" } }} accepting={false} onAccept={vi.fn()} onReject={vi.fn()} onAbort={vi.fn()} />);
    expect(screen.queryByTestId("waiting-dissent")).toBeNull();
    expect(screen.getByTestId("waiting-explanation")).not.toHaveTextContent("oracle");
  });

  // Now's inbox is where the card lives; without the client it renders the
  // objection but cannot offer the findings for filing.
  it("is decidable and fileable from Now's inbox", async () => {
    const runFileFindings = vi.fn(() => Promise.resolve({ items: [{ id: "B-9", title: "x" }] }));
    const client = {
      taskNext: vi.fn(() => Promise.resolve(null)),
      projectNext: vi.fn(() => Promise.resolve([])),
      appStatus: vi.fn(() => Promise.resolve({ configured: false, running: false })),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      runFileFindings,
    } as unknown as EngineClient;
    useRuns.setState({
      runs: { [objected.id]: { ...objected, project_id: "p" } },
      events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {},
    });
    render(<Now client={client} projectId="p" />);
    const card = await screen.findByTestId("now-waiting-card");
    expect(within(card).getByTestId("waiting-dissent")).toHaveTextContent("reviewer still requests changes");
    fireEvent.click(within(card).getByTestId("waiting-file-findings"));
    expect(runFileFindings).toHaveBeenCalledWith("r-tf");
  });
});

describe("RunView verdict chip — the record's standing objection", () => {
  it("says the reviewer still requests changes, not a bare passed", async () => {
    const recorded: Run = {
      ...testFirst, status: "done", next: [],
      review_evidence: { status: "dissent", independence: "independent", verdict: "request-changes", findings: 2 },
    };
    seed(recorded);
    render(<RunView runId="r-1" client={clientFor(recorded)} />);
    expect(await screen.findByText("gate · passed · reviewer still requests changes")).toBeInTheDocument();
  });
});
