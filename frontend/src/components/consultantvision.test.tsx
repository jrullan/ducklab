import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ChatAbout } from "./ChatAbout";
import { ConversationTurn } from "./ConversationLane";
import { DucklingPickerDrawer } from "./DucklingPickerDrawer";
import { RunView } from "../views/RunView";
import { useRuns } from "../store/runs";
import { EngineClient, type Duckling, type Run } from "../api/client";
import { buildTurns } from "../lib/runview";
import { canSeeImages, pickerLabel, visionState } from "../lib/vision";
import type { DucklabEvent } from "../api/events";

// B-512 / B-513: which consultant can see a screenshot, said everywhere a
// person picks one or talks to one; and the switch that keeps the
// conversation when the one they are talking to cannot.

const fleet: Duckling[] = [
  { id: "blind", provider: "local", model: "text", vision_status: "none", caps: { native_tools: true, context_tokens: 1, vision: false } },
  { id: "claimed", provider: "local", model: "maybe", vision_status: "declared", caps: { native_tools: true, context_tokens: 1, vision: true } },
  { id: "seer", provider: "cloud", model: "vision", vision_status: "verified", caps: { native_tools: true, context_tokens: 1, vision: true } },
  { id: "no-projector", provider: "local", model: "mmproj-less", vision_status: "refuted", caps: { native_tools: true, context_tokens: 1, vision: false } },
];

const png = () => new File(["pixels"], "screen.png", { type: "image/png" });

describe("vision states", () => {
  it("follows the engine's rule and falls back to caps on an older engine", () => {
    expect(fleet.map(visionState)).toEqual(["none", "declared", "verified", "refuted"]);
    expect(fleet.map(canSeeImages)).toEqual([false, true, true, false]);
    expect(visionState({ id: "old", provider: "p", model: "m", caps: { native_tools: true, context_tokens: 1, vision: true } })).toBe("declared");
    expect(visionState(undefined)).toBe("unknown");
    expect(pickerLabel(fleet[2]!)).toBe("seer · 👁 sees images");
    expect(pickerLabel(fleet[1]!)).toContain("not yet tested");
    expect(pickerLabel(fleet[0]!)).toBe("blind");
    expect(pickerLabel(fleet[3]!)).toContain("no vision support");
  });
});

function startClient(requests: unknown[]) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string, init?: RequestInit) => {
      const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
      if (url.includes("/roster")) return json({ entries: [] });
      if (url.includes("/defaults/diagnostics")) return json({ harness_project_id: "", available: false });
      requests.push(init?.body ? JSON.parse(String(init.body)) : undefined);
      return json({ id: "new-chat" });
    }) as never,
  });
}

describe("the chat start form", () => {
  it("marks seeing ducklings in the picker, with a legend", () => {
    render(<ChatAbout client={startClient([])} projectId="p" aboutKind="bug" aboutId="B-1" ducklings={fleet} startOpen />);
    const picker = screen.getByTestId("chat-duckling");
    const options = within(picker).getAllByRole("option").map((o) => [o.getAttribute("value"), o.textContent]);
    expect(options).toContainEqual(["seer", "seer · 👁 sees images"]);
    expect(options).toContainEqual(["claimed", "claimed · 👁 sees images (not yet tested)"]);
    expect(options).toContainEqual(["blind", "blind"]);
    expect(screen.getByTestId("chat-duckling-legend")).toHaveTextContent("👁 sees images");
  });

  it("holds a pasted screenshot for a blind duckling, explains, and starts on the duckling it offers", async () => {
    const requests: unknown[] = [];
    render(<ChatAbout client={startClient(requests)} projectId="p" aboutKind="bug" aboutId="B-1" ducklings={fleet} startOpen />);
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "blind" } });
    expect(screen.getByTestId("chat-vision-note")).toHaveTextContent("blind can't see images");
    const box = screen.getByTestId("chat-message");
    fireEvent.change(box, { target: { value: "what is wrong here?" } });
    fireEvent.paste(box, { clipboardData: { files: [png()], types: ["Files"] } });
    await waitFor(() => expect(screen.getByTestId("chat-image-chip")).toHaveTextContent("screen.png"));
    expect(screen.getByTestId("chat-switch-offer")).toHaveTextContent("would not see this screenshot");
    expect((screen.getByTestId("chat-start") as HTMLButtonElement).disabled).toBe(true);
    // Confirmed seers are offered first; the blind and the refuted are not offered.
    const offered = within(screen.getByTestId("chat-switch-offer")).getAllByRole("button").map((b) => b.getAttribute("data-testid"));
    expect(offered).toEqual(["chat-switch-to-seer", "chat-switch-to-claimed"]);

    fireEvent.click(screen.getByTestId("chat-switch-to-seer"));
    expect(screen.getByTestId("chat-duckling")).toHaveValue("seer");
    expect(screen.queryByTestId("chat-switch-offer")).toBeNull();
    fireEvent.click(screen.getByTestId("chat-start"));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0]).toMatchObject({ duckling: "seer", images: ["data:image/png;base64,cGl4ZWxz"] });
  });

  it("takes a dropped screenshot the same way", async () => {
    render(<ChatAbout client={startClient([])} projectId="p" aboutKind="bug" aboutId="B-1" ducklings={fleet} startOpen />);
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "no-projector" } });
    expect(screen.getByTestId("chat-vision-note")).toHaveTextContent("rejected a test image");
    fireEvent.drop(screen.getByTestId("chat-about-form"), { dataTransfer: { files: [png()], types: ["Files"] } });
    await waitFor(() => expect(screen.getByTestId("chat-image-chip")).toBeInTheDocument());
    expect(screen.getByTestId("chat-switch-offer")).toBeInTheDocument();
  });

  it("leaves a plain-text paste alone", () => {
    render(<ChatAbout client={startClient([])} projectId="p" aboutKind="bug" aboutId="B-1" ducklings={fleet} startOpen />);
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "blind" } });
    fireEvent.paste(screen.getByTestId("chat-message"), { clipboardData: { files: [], types: ["text/plain"], getData: () => "hello" } });
    expect(screen.queryByTestId("chat-image-chip")).toBeNull();
    expect(screen.queryByTestId("chat-switch-offer")).toBeNull();
  });
});

const ev = (type: string, seq: number, data: Record<string, unknown>) =>
  ({ type, seq, run_id: "r-v", data }) as unknown as DucklabEvent;

const transcript = [
  ev("message", 1, { role: "human", content: "Why the 401?" }),
  ev("turn_start", 2, { round: 1, turn: 0, role: "consultant", duckling: "blind" }),
  ev("message", 3, { round: 1, turn: 0, role: "consultant", duckling: "blind", content: "Probably the proxy." }),
  ev("turn_end", 4, { round: 1, turn: 0, role: "consultant" }),
  ev("human_needed", 5, { kind: "chat" }),
];

// The roster's FIRST entry is a seeing duckling; the consultant seat is blind.
// chatCanSee read Object.values(run.roster)[0] and called this chat sighted.
const visionRun = (status = "paused") => ({
  id: "r-v", project_id: "p", stage: "chat", mode: "solo", status,
  pending_kind: status === "paused" ? "chat" : "", verdict: "", note: "chat about bug B-1",
  started_at: "2026-10-09T10:00:00Z",
  roster: { architect: "seer", consultant: "blind" },
  budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0 },
}) as unknown as Run;

function liveClient() {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string) => {
      const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
      if (/\/v1\/ducklings$/.test(url)) return json({ items: fleet, total: fleet.length });
      return json({});
    }) as never,
  });
}

describe("the live chat composer", () => {
  beforeEach(() => {
    useRuns.setState({ runs: { "r-v": visionRun() }, events: { "r-v": transcript }, spend: {}, deltas: {}, reasoning: {} });
  });

  it("reads vision from the consultant seat, not the roster's first entry", async () => {
    render(<RunView runId="r-v" client={liveClient()} />);
    expect(await screen.findByTestId("chat-vision-note")).toHaveTextContent("blind can't see images");
    expect(screen.getByTestId("chat-consultant")).toHaveValue("blind");
    expect(within(screen.getByTestId("chat-consultant")).getByRole("option", { name: "seer · 👁 sees images" })).toBeInTheDocument();
  });

  it("offers a seeing duckling when a screenshot is attached, switches, and sends it there", async () => {
    const client = liveClient();
    const chatSwitch = vi.spyOn(client, "chatSwitch").mockResolvedValue({ ...visionRun(), roster: { architect: "seer", consultant: "seer" } } as Run);
    const chatSend = vi.spyOn(client, "chatSend").mockResolvedValue(visionRun("running"));
    render(<RunView runId="r-v" client={client} />);
    await screen.findByTestId("chat-vision-note");
    fireEvent.change(screen.getByTestId("chat-message"), { target: { value: "here it is" } });
    fireEvent.change(screen.getByTestId("chat-image"), { target: { files: [png()] } });
    await waitFor(() => expect(screen.getByTestId("chat-image-chip")).toBeInTheDocument());
    expect(screen.getByTestId("chat-switch-offer")).toHaveTextContent("blind can't see images, so it would not see this screenshot");
    expect((screen.getByTestId("chat-send") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByTestId("chat-switch-to-seer"));
    await waitFor(() => expect(chatSwitch).toHaveBeenCalledWith("r-v", "seer"));
    await waitFor(() => expect(screen.queryByTestId("chat-vision-note")).toBeNull());
    expect(useRuns.getState().runs["r-v"]!.roster!.consultant).toBe("seer");
    expect(screen.queryByTestId("chat-switch-offer")).toBeNull();

    fireEvent.click(screen.getByTestId("chat-send"));
    await waitFor(() => expect(chatSend).toHaveBeenCalledWith("r-v", "here it is", ["data:image/png;base64,cGl4ZWxz"]));
  });

  it("switches from the picker, and shows the engine's refusal", async () => {
    const client = liveClient();
    const chatSwitch = vi.spyOn(client, "chatSwitch").mockRejectedValue(new Error("the consultant is still answering; wait for its reply, then try again (status running)"));
    render(<RunView runId="r-v" client={client} />);
    await screen.findByTestId("chat-vision-note");
    fireEvent.change(screen.getByTestId("chat-consultant"), { target: { value: "claimed" } });
    await waitFor(() => expect(chatSwitch).toHaveBeenCalledWith("r-v", "claimed"));
    expect(await screen.findByTestId("chat-error")).toHaveTextContent("still answering");
  });

  // Codex on #165 (P1): with the switch still in flight, Send stayed live and
  // the message — meant for the duckling just picked — could reach the old
  // one. Send (button, Enter) waits for the switch to resolve.
  it("holds Send while a switch is unresolved", async () => {
    const client = liveClient();
    let finish: (run: Run) => void = () => {};
    const chatSwitch = vi.spyOn(client, "chatSwitch").mockImplementation(() => new Promise<Run>((resolve) => { finish = resolve; }));
    const chatSend = vi.spyOn(client, "chatSend").mockResolvedValue(visionRun("running"));
    render(<RunView runId="r-v" client={client} />);
    await screen.findByTestId("chat-vision-note");
    fireEvent.change(screen.getByTestId("chat-message"), { target: { value: "for the new one" } });
    expect((screen.getByTestId("chat-send") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.change(screen.getByTestId("chat-consultant"), { target: { value: "seer" } });
    await waitFor(() => expect(chatSwitch).toHaveBeenCalledWith("r-v", "seer"));
    const send = screen.getByTestId("chat-send") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
    expect(send.title).toMatch(/switch/i);
    fireEvent.click(send);
    fireEvent.keyDown(screen.getByTestId("chat-message"), { key: "Enter" });
    expect(chatSend).not.toHaveBeenCalled();
    // A second switch cannot be started over the first.
    expect((screen.getByTestId("chat-consultant") as HTMLSelectElement).disabled).toBe(true);

    await act(async () => { finish({ ...visionRun(), roster: { architect: "seer", consultant: "seer" } } as Run); });
    await waitFor(() => expect((screen.getByTestId("chat-send") as HTMLButtonElement).disabled).toBe(false));
    fireEvent.keyDown(screen.getByTestId("chat-message"), { key: "Enter" });
    await waitFor(() => expect(chatSend).toHaveBeenCalledWith("r-v", "for the new one", []));
    expect(useRuns.getState().runs["r-v"]!.roster!.consultant).toBe("seer");
  });

  it("cannot switch while the consultant is answering", async () => {
    useRuns.setState({ runs: { "r-v": visionRun("running") }, events: { "r-v": transcript.slice(0, 4) }, spend: {}, deltas: {}, reasoning: {} });
    render(<RunView runId="r-v" client={liveClient()} />);
    const picker = await screen.findByTestId("chat-consultant");
    expect((picker as HTMLSelectElement).disabled).toBe(true);
    expect(picker.title).toMatch(/once the consultant has replied/);
  });

  it("follows a switch announced by the event stream", () => {
    act(() => {
      useRuns.getState().applyEvent({ type: "consultant_switched", run_id: "r-v", seq: 9, data: { from: "blind", to: "seer", actor: "human" } });
    });
    expect(useRuns.getState().runs["r-v"]!.roster).toEqual({ architect: "seer", consultant: "seer" });
  });
});

describe("the switch in the transcript", () => {
  it("is its own divider, and the replies keep their authors", () => {
    const turns = buildTurns([
      ...transcript,
      ev("consultant_switched", 6, { from: "blind", to: "seer", actor: "mcp:elena" }),
      ev("message", 7, { role: "human", content: "now look", images: ["data:image/png;base64,AA=="] }),
      ev("turn_start", 8, { round: 2, turn: 0, role: "consultant", duckling: "seer" }),
      ev("message", 9, { round: 2, turn: 0, role: "consultant", duckling: "seer", content: "I see a 401 banner." }),
      ev("turn_end", 10, { round: 2, turn: 0, role: "consultant" }),
    ]);
    expect(turns.map((t) => [t.role, t.role === "consultant" ? t.duckling : t.text])).toEqual([
      ["human", "Why the 401?"],
      ["consultant", "blind"],
      ["switch", "consultant switched from blind to seer"],
      ["human", "now look"],
      ["consultant", "seer"],
    ]);
    const block = turns[2]!;
    render(<ConversationTurn block={block} roster={[]} />);
    expect(screen.getByTestId("consultant-switch-divider")).toHaveTextContent("consultant switched from blind to seer · by the MCP operator mcp:elena");
    // The person's own switch says so — and an operator's never does.
    const [own] = buildTurns([ev("consultant_switched", 1, { from: "seer", to: "blind", actor: "human" })]);
    render(<ConversationTurn block={own!} roster={[]} />);
    expect(screen.getAllByTestId("consultant-switch-divider")[1]).toHaveTextContent("consultant switched from seer to blind · by the person");
    expect(screen.getAllByTestId("consultant-switch-divider")[0]).not.toHaveTextContent("the person");
  });
});

describe("the roster's consultant seat", () => {
  it("marks which candidates can see, only for the consultant", () => {
    const { unmount } = render(<DucklingPickerDrawer mode="common" role="consultant" ducklings={fleet} scorecards={[]} current={[]} multiple={false} scope="global" onClose={vi.fn()} onApply={vi.fn()} />);
    expect(screen.getByTestId("roster-pick-vision-seer")).toHaveTextContent("👁 sees images");
    expect(screen.getByTestId("roster-pick-vision-claimed")).toHaveTextContent("not yet tested");
    expect(screen.queryByTestId("roster-pick-vision-blind")).toBeNull();
    expect(screen.getByTestId("roster-vision-legend")).toBeInTheDocument();
    unmount();
    render(<DucklingPickerDrawer mode="solo" role="implementer" ducklings={fleet} scorecards={[]} current={[]} multiple={false} scope="global" onClose={vi.fn()} onApply={vi.fn()} />);
    expect(screen.queryByTestId("roster-pick-vision-seer")).toBeNull();
  });
});
