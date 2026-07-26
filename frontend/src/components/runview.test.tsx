import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ConversationTurn } from "./ConversationLane";
import { ToolTimeline } from "./ToolTimeline";
import { GateCard } from "./GateCard";
import { CandidateCard } from "./CandidateCard";
import { DiffView } from "./DiffView";
import { buildTurns, anonymiseTurns, buildTimeline, buildGate, parseDiff } from "../lib/runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

const roster = ["pato-uno", "pato-dos"];

describe("ConversationTurn", () => {
  it("collapses tool calls to one line each", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("tool_call", 2, { tool: "fs_read", ok: true, ms: 3 }),
      ev("tool_call", 3, { tool: "fs_patch", ok: true, ms: 12 }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.getAllByTestId("tool-call")).toHaveLength(2);
  });

  // AC-32 in the UI: an anonymised turn must not contain the id anywhere in
  // the rendered DOM, not merely be styled to hide it.
  it("never renders a duckling id for an anonymised turn", () => {
    const blocks = anonymiseTurns(
      buildTurns([
        ev("turn_start", 1, { round: 1, turn: 0, role: "judge", duckling: "pato-uno" }),
      ]),
      true,
    );
    const { container } = render(<ConversationTurn block={blocks[0]!} roster={roster} />);
    expect(container.innerHTML).not.toContain("pato-uno");
    expect(screen.getByTestId("conversation-turn").getAttribute("data-anonymous")).toBe("true");
    expect(container.textContent).toContain("A");
  });

  it("shows an in-flight marker until the turn ends", () => {
    const [open] = buildTurns([ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" })]);
    const { rerender } = render(<ConversationTurn block={open!} roster={roster} />);
    expect(screen.queryByTestId("in-flight")).not.toBeNull();

    const [closed] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("turn_end", 2),
    ]);
    rerender(<ConversationTurn block={closed!} roster={roster} />);
    expect(screen.queryByTestId("in-flight")).toBeNull();
  });

  it("expands a policy violation by default", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("policy_violation", 2, { tool: "fs_write", detail: "path escapes root: ../etc" }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.getByTestId("tool-call").getAttribute("data-violation")).toBe("true");
    expect(screen.getByText(/path escapes root/)).toBeTruthy();
  });

  it("renders streamed text when present", () => {
    const [block] = buildTurns([ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" })]);
    render(<ConversationTurn block={block!} roster={roster} streamed="func Add" />);
    expect(screen.getByText("func Add")).toBeTruthy();
  });
});

describe("ToolTimeline", () => {
  it("renders one tick per call", () => {
    const calls = buildTimeline([
      ev("tool_call", 1, { tool: "fs_read" }),
      ev("tool_call", 2, { tool: "fs_read" }),
      ev("tool_call", 3, { tool: "verify_run" }),
    ]);
    render(<ToolTimeline calls={calls} />);
    expect(screen.getAllByTestId("timeline-tick")).toHaveLength(3);
  });

  it("renders nothing when no tools ran", () => {
    render(<ToolTimeline calls={[]} />);
    expect(screen.queryByTestId("tool-timeline")).toBeNull();
  });
});

describe("GateCard", () => {
  // P3: a none gate must never present as success.
  it("labels a none gate as unverified", () => {
    render(<GateCard gate={buildGate([ev("gate", 1, { gate: "none", exit: 0 })])} />);
    const card = screen.getByTestId("gate-card");
    expect(card.getAttribute("data-unverified")).toBe("true");
    expect(card.textContent).toContain("unverified");
    expect(card.textContent).not.toContain("passed");
  });

  it("shows the command and the result for a real gate", () => {
    render(<GateCard gate={buildGate([ev("gate", 1, { gate: "tests", cmd: "go test ./...", exit: 0 })])} />);
    expect(screen.getByTestId("gate-card").textContent).toContain("go test ./...");
    expect(screen.getByTestId("gate-card").textContent).toContain("passed");
  });

  it("says not run yet before a gate exists", () => {
    render(<GateCard gate={null} />);
    expect(screen.getByTestId("gate-card").textContent).toContain("not run yet");
  });
});

describe("CandidateCard", () => {
  // The Candidate type has no author field, so this cannot leak one.
  it("shows the label and gate but no authorship", () => {
    const { container } = render(
      <CandidateCard candidate={{ label: "A", diff: "+return a + b", gate: "green" }} applied />,
    );
    expect(container.textContent).toContain("Candidate A");
    expect(container.textContent).not.toContain("pato");
    expect(screen.getByTestId("applied-verbatim")).toBeTruthy();
  });

  it("marks a red candidate as failed verification", () => {
    render(<CandidateCard candidate={{ label: "B", diff: "+x", gate: "red" }} />);
    expect(screen.getByTestId("candidate-card").textContent).toContain("verification failed");
  });
});

describe("DiffView", () => {
  const diff = [
    "--- a/add.go", "+++ b/add.go", "@@ -1,3 +1,3 @@", "-return a - b", "+return a + b",
    "--- a/add_test.go", "+++ b/add_test.go", "@@ -5,3 +5,3 @@", "-want 5", "+want 6",
  ].join("\n");

  // 05 §5.3: never hide an edit to the thing that decides pass/fail.
  it("pins test files to the top under a banner", () => {
    render(<DiffView files={parseDiff(diff)} />);
    expect(screen.getByTestId("tests-modified-banner")).toBeTruthy();
    const sections = screen.getAllByTestId("diff-file");
    expect(sections[0]!.getAttribute("data-test-file")).toBe("true");
  });

  it("shows no banner when tests are untouched", () => {
    render(<DiffView files={parseDiff("--- a/add.go\n+++ b/add.go\n@@ -1 +1 @@\n+x")} />);
    expect(screen.queryByTestId("tests-modified-banner")).toBeNull();
  });

  it("renders an empty state before any change exists", () => {
    render(<DiffView files={[]} />);
    expect(screen.getByTestId("diff-empty")).toBeTruthy();
  });
});
