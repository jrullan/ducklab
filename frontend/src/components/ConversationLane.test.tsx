import { render, screen } from "@testing-library/react";
import { ConversationTurn } from "./ConversationLane";
import { buildTurns, type TurnBlock } from "../lib/runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

function turn(role: string): TurnBlock {
  return {
    key: `1:${role}`,
    round: 1,
    turn: 1,
    role,
    duckling: role === "human" ? "human" : "implementer-1",
    toolCalls: [],
    text: "message",
    done: true,
  };
}

// B-519: the lane shows the notice the model saw instead of the reference.
describe("ConversationTurn unreadable reference", () => {
  it("renders the notice as an alert, not a quiet divider", () => {
    const block = turn("refs-warning");
    block.text = "notes.docx: unreadable binary reference (Word document, 3 KB); its content is not text and cannot be extracted; ask the person for a text or PDF version";
    render(<ConversationTurn block={block} roster={[]} />);
    const alert = screen.getByTestId("refs-unreadable");
    expect(alert).toHaveAttribute("role", "alert");
    expect(alert).toHaveTextContent("Reference not read");
    expect(alert).toHaveTextContent("notes.docx: unreadable binary reference (Word document, 3 KB)");
    expect(screen.queryByTestId("refs-divider")).not.toBeInTheDocument();
  });
});

describe("ConversationTurn avatars", () => {
  it("renders the current human avatar, not the legacy person icon, for human transcript turns", () => {
    render(<ConversationTurn block={turn("human")} roster={[]} />);

    const avatar = screen.getByRole("img", { name: "human avatar" });
    expect(avatar).toHaveTextContent("🧑");
    expect(avatar).not.toHaveTextContent("👤");
    expect(screen.queryByTestId("duck-avatar")).not.toBeInTheDocument();
  });

  it("renders an advisor auto-answer as a distinct yolo state", () => {
    const block = turn("human");
    block.author = "advisor:k3 (yolo)";
    render(<ConversationTurn block={block} roster={[]} />);

    expect(screen.getByRole("img", { name: "advisor avatar" })).toBeInTheDocument();
    expect(screen.getByText("advisor:k3")).toBeInTheDocument();
    expect(screen.getByText("answered under yolo")).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "human avatar" })).not.toBeInTheDocument();
  });

  it("renders a duck avatar for duckling transcript turns", () => {
    render(<ConversationTurn block={turn("implementer")} roster={["implementer-1"]} />);

    expect(screen.getByTestId("duck-avatar")).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "human avatar" })).not.toBeInTheDocument();
  });
});

// Second review of #177: an accept's words are recorded before the accept
// runs, and the clean-checkout gate replay can still refuse it. The lane
// showed a finished "accept · …" over a run still waiting at its gate.
describe("ConversationTurn decision record", () => {
  it.each([
    [buildTurns([ev("decision", 1, { action: "accept", actor: "mcp:opencode", reason: "no jsdom" }),
      ev("accept_refused", 2, { reason: "gate did not reproduce" }),
      ev("decision_outcome", 3, { action: "accept", outcome: "refused", error: "gate did not reproduce" })]),
      "refused", "accept refused: gate did not reproduce · the MCP operator mcp:opencode"],
    [buildTurns([ev("decision", 1, { action: "accept", actor: "mcp:opencode", reason: "no jsdom" }),
      ev("decision_outcome", 2, { action: "accept", outcome: "taken" })]),
      "taken", "accept · the MCP operator mcp:opencode"],
    [buildTurns([ev("decision", 1, { action: "reject", actor: "human", reason: "not a bug" })]),
      "pending", "reject attempted — no outcome recorded · the person"],
  ])("shows how the decision ended (%#)", (blocks, outcome, label) => {
    expect(blocks).toHaveLength(1);
    render(<ConversationTurn block={blocks[0]!} roster={[]} />);
    const record = screen.getByTestId("decision-record");
    expect(record).toHaveAttribute("data-outcome", outcome);
    expect(record).toHaveTextContent(label);
    if (outcome !== "taken") expect(record.textContent).not.toMatch(/^accept · /);
  });
});
