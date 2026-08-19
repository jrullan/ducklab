import { describe, it, expect } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";
import { EngineClient, type Run } from "../api/client";
import { buildTurns } from "../lib/runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown>) =>
  ({ type, seq, run_id: "r-c", data }) as unknown as DucklabEvent;

// The T-064 chat's exact event shape: human messages carry no round/turn.
const chatEvents = [
  ev("message", 1, { role: "human", content: "Does T-064 implement the preferences?" }),
  ev("turn_start", 2, { round: 1, turn: 0, role: "consultant", duckling: "k3" }),
  ev("message", 3, { round: 1, turn: 0, role: "consultant", duckling: "k3", content: "Yes on both counts." }),
  ev("turn_end", 4, { round: 1, turn: 0, role: "consultant" }),
  ev("message", 5, { role: "human", content: "Write me a bug report." }),
  ev("turn_start", 6, { round: 2, turn: 0, role: "consultant", duckling: "k3" }),
  ev("message", 7, { round: 2, turn: 0, role: "consultant", duckling: "k3", content: "Filed B-016." }),
  ev("turn_end", 8, { round: 2, turn: 0, role: "consultant" }),
];

// A chat's human messages carry no coordinates, and the fallback routed them
// into the last open turn: the human's next question OVERWROTE the
// consultant's recorded reply, and the lane read as one garbled monologue.
describe("buildTurns and a chat's human messages", () => {
  it("keeps every message in order without overwriting anyone", () => {
    const turns = buildTurns(chatEvents);
    expect(turns.map((t) => [t.role, t.text])).toEqual([
      ["human", "Does T-064 implement the preferences?"],
      ["consultant", "Yes on both counts."],
      ["human", "Write me a bug report."],
      ["consultant", "Filed B-016."],
    ]);
  });

  // The first human bubble used to take key "1:0" — the consultant's own
  // coordinates — so the delta stores served the consultant's thinking to
  // both blocks and every thought rendered twice.
  it("gives loose messages keys of their own, never a turn's coordinates", () => {
    const turns = buildTurns(chatEvents);
    const keys = turns.map((t) => t.key);
    expect(new Set(keys).size).toBe(turns.length);
    for (const t of turns.filter((x) => x.role === "human")) {
      expect(t.messageOnly).toBe(true);
    }
  });
});

const chatRun = {
  id: "r-c", project_id: "p", stage: "chat", mode: "solo", task_id: "T-064",
  status: "paused", pending_kind: "chat", verdict: "",
  started_at: "2026-08-10T18:30:26Z",
  roster: { architect: "k3" },
  budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0 },
} as unknown as Run;

const client = new EngineClient({
  baseUrl: "http://engine",
  token: "t",
  fetchFn: (async () =>
    new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })) as never,
});

// The reply box lived in the pending card at the TOP while the conversation
// grew downward away from it: read at the bottom, answer at the top, every
// turn. It is pinned to the bottom now, like every conversation the person
// already knows, and stays present-but-disabled while the consultant thinks
// so the box never jumps around.
describe("chat transcript author avatars", () => {
  it("uses a human avatar for every recorded human message and ducks for consultant replies", async () => {
    useRuns.setState({
      runs: { "r-c": chatRun },
      // These are already-recorded entries when the transcript is opened,
      // including a human question before and after a consultant reply.
      events: { "r-c": [...chatEvents] },
      spend: {}, deltas: {}, reasoning: {},
    });
    render(<RunView runId="r-c" client={client} />);

    await waitFor(() => expect(screen.getAllByTestId("conversation-turn")).toHaveLength(4));

    for (const message of ["Does T-064 implement the preferences?", "Write me a bug report."]) {
      const turn = screen.getByText(message).closest("article");
      expect(turn).not.toBeNull();
      expect(within(turn!).getByLabelText("human avatar")).toBeTruthy();
      expect(within(turn!).queryByTestId("duck-avatar")).toBeNull();
    }

    for (const reply of ["Yes on both counts.", "Filed B-016."]) {
      const turn = screen.getByText(reply).closest("article");
      expect(turn).not.toBeNull();
      expect(within(turn!).getByTestId("duck-avatar")).toBeTruthy();
      expect(within(turn!).queryByLabelText("human avatar")).toBeNull();
    }
  });
});

describe("the chat composer", () => {
  it("waits at the bottom section, not in the pending card", async () => {
    useRuns.setState({
      runs: { "r-c": chatRun },
      events: { "r-c": [...chatEvents, ev("human_needed", 9, { kind: "chat" })] },
      spend: {}, deltas: {}, reasoning: {},
    });
    render(<RunView runId="r-c" client={client} />);
    await waitFor(() => screen.getByTestId("chat-reply"));
    expect(screen.queryByTestId("pending-human")).toBeNull();
    const box = screen.getByLabelText("chat message") as HTMLTextAreaElement;
    expect(box.disabled).toBe(false);
    // The composer sits after the conversation in document order.
    const conversation = screen.getByTestId("conversation");
    expect(
      conversation.compareDocumentPosition(screen.getByTestId("chat-reply")) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("stays present but disabled while the consultant is thinking", async () => {
    useRuns.setState({
      runs: { "r-c": { ...chatRun, status: "running", pending_kind: "" } as unknown as Run },
      events: { "r-c": [...chatEvents] },
      spend: {}, deltas: {}, reasoning: {},
    });
    render(<RunView runId="r-c" client={client} />);
    await waitFor(() => screen.getByTestId("chat-reply"));
    const box = screen.getByLabelText("chat message") as HTMLTextAreaElement;
    expect(box.disabled).toBe(true);
    expect(box.placeholder).toContain("thinking");
    expect((screen.getByTestId("chat-send") as HTMLButtonElement).disabled).toBe(true);
  });
});
