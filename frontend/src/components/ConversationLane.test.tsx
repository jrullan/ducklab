import { render, screen } from "@testing-library/react";
import { ConversationTurn } from "./ConversationLane";
import type { TurnBlock } from "../lib/runview";

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
