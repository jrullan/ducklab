import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { RunView } from "./RunView";
import { ChatAbout } from "../components/ChatAbout";
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
  it("keeps the consultant reply beside a configuration amendment note", async () => {
    const amendmentEvents = [
      ...chatEvents,
      ev("config_amendment", 10, { key: "verify.link_deps", old: "missing", new: "frontend/node_modules", why: "the doctor found unavailable dependencies" }),
    ];
    useRuns.setState({
      runs: { "r-c": chatRun },
      events: { "r-c": amendmentEvents },
      spend: {}, deltas: {}, reasoning: {},
    });
    render(<RunView runId="r-c" client={client} />);

    await waitFor(() => expect(screen.getByTestId("chat-config-amendments")).toBeInTheDocument());
    const reply = screen.getByText("Yes on both counts.").closest("article");
    expect(reply).not.toBeNull();
    expect(screen.getByTestId("chat-config-amendments")).toBeInTheDocument();
    expect(
      reply!.compareDocumentPosition(screen.getByTestId("chat-config-amendments")) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

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
      const avatar = within(turn!).getByLabelText("human avatar");
      expect(avatar).toHaveTextContent("🧑");
      expect(avatar).not.toHaveTextContent("👤");
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

describe("ChatAbout roster seating", () => {
  it("pre-seats the resolved consultant when the chat opens", async () => {
    const roster = vi.fn().mockResolvedValue({ entries: [{ role: "consultant", duckling: "luna", source: "project" }] });
    const rosterClient = { roster } as unknown as EngineClient;
    const ducklings = [{ id: "luna", provider: "test", model: "test" }];
    render(<ChatAbout client={rosterClient} projectId="p" aboutKind="task" aboutId="T-1" ducklings={ducklings} />);
    fireEvent.click(screen.getByTestId("chat-about"));
    await waitFor(() => expect(screen.getByTestId("chat-duckling")).toHaveValue("luna"));
    expect(roster).toHaveBeenCalledWith("p");
  });
});

describe("ChatAbout image attachments", () => {
  const ducklings = [
    { id: "text-only", provider: "test", model: "text", caps: { native_tools: true, context_tokens: 1, vision: false } },
    { id: "seeing", provider: "test", model: "vision", caps: { native_tools: true, context_tokens: 1, vision: true } },
  ];

  function chatClient(requests: { body?: unknown }[]) {
    return new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async (url: string, init?: RequestInit) => {
        if (url.includes("/roster")) return new Response('{"entries":[]}', { status: 200, headers: { "Content-Type": "application/json" } });
        requests.push({ body: init?.body ? JSON.parse(String(init.body)) : undefined });
        return new Response('{"id":"new-chat"}', { status: 200, headers: { "Content-Type": "application/json" } });
      }) as never,
    });
  }

  it("shows removable image chips and starts the chat with data URLs", async () => {
    const requests: { body?: unknown }[] = [];
    render(<ChatAbout client={chatClient(requests)} projectId="p" aboutKind="bug" aboutId="B-1" ducklings={ducklings} />);
    fireEvent.click(screen.getByTestId("chat-about"));
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "seeing" } });
    fireEvent.change(screen.getByTestId("chat-message"), { target: { value: "please inspect this" } });

    const picker = screen.getByTestId("chat-image") as HTMLInputElement;
    expect(picker.accept).toBe("image/*");
    expect(picker.multiple).toBe(true);
    fireEvent.change(picker, { target: { files: [new File(["pixels"], "broken-ui.png", { type: "image/png" })] } });

    await waitFor(() => expect(screen.getByTestId("chat-image-chip")).toHaveTextContent("broken-ui.png"));
    fireEvent.click(screen.getByLabelText("remove image broken-ui.png"));
    expect(screen.queryByTestId("chat-image-chip")).toBeNull();

    fireEvent.change(picker, { target: { files: [new File(["pixels"], "broken-ui.png", { type: "image/png" })] } });
    await waitFor(() => expect(screen.getByTestId("chat-image-chip")).toHaveTextContent("broken-ui.png"));
    fireEvent.click(screen.getByTestId("chat-start"));

    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0]!.body).toMatchObject({
      duckling: "seeing", about_kind: "bug", about_id: "B-1", message: "please inspect this",
      images: ["data:image/png;base64,cGl4ZWxz"],
    });
    await waitFor(() => expect(screen.queryByTestId("chat-image-chip")).toBeNull());
  });

  it("disables image selection for a text-only duckling and refuses non-images", async () => {
    const requests: { body?: unknown }[] = [];
    render(<ChatAbout client={chatClient(requests)} projectId="p" aboutKind="task" aboutId="T-1" ducklings={ducklings} />);
    fireEvent.click(screen.getByTestId("chat-about"));
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "text-only" } });
    const picker = screen.getByTestId("chat-image") as HTMLInputElement;
    const add = screen.getByTestId("chat-add-image") as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    expect(add.title).toMatch(/vision|see/i);

    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "seeing" } });
    expect(add.disabled).toBe(false);
    fireEvent.change(picker, { target: { files: [new File(["not an image"], "notes.txt", { type: "text/plain" })] } });
    expect(await screen.findByTestId("chat-image-error")).toHaveTextContent(/image/i);
    expect(screen.queryByTestId("chat-image-chip")).toBeNull();
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

  it("sends reply attachments as data URLs", async () => {
    const send = vi.spyOn(client, "chatSend").mockResolvedValue(chatRun);
    useRuns.setState({
      runs: { "r-c": chatRun },
      events: { "r-c": [...chatEvents, ev("human_needed", 9, { kind: "chat" })] },
      spend: {}, deltas: {}, reasoning: {},
    });
    render(<RunView runId="r-c" client={client} />);
    await waitFor(() => screen.getByTestId("chat-reply"));
    fireEvent.change(screen.getByTestId("chat-message"), { target: { value: "look at this" } });
    fireEvent.change(screen.getByTestId("chat-image"), {
      target: { files: [new File(["reply pixels"], "reply.png", { type: "image/png" })] },
    });
    await waitFor(() => expect(screen.getByTestId("chat-image-chip")).toHaveTextContent("reply.png"));
    fireEvent.click(screen.getByTestId("chat-send"));
    await waitFor(() => expect(send).toHaveBeenCalledWith(
      "r-c", "look at this", ["data:image/png;base64,cmVwbHkgcGl4ZWxz"],
    ));
    send.mockRestore();
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
