import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App, consultantSubjectFor } from "./App";
import { EngineClient, type Duckling, type Run } from "../api/client";
import { useRuns } from "../store/runs";
import {
  PANE_DEFAULT_WIDTH, PANE_MAX_WIDTH, PANE_MIN_WIDTH, loadConsultantPane, useConsultant,
} from "../store/consultant";
import { ConsultantPane } from "../components/ConsultantPane";
import { ChatAbout } from "../components/ChatAbout";
import { RunView } from "../views/RunView";

// B-514: the consultant's own surface — an app-wide, hideable right pane that
// stays while the person navigates, with the full height for the conversation.

class EventSourceStub {
  addEventListener() {}
  close() {}
  onerror: ((e: unknown) => void) | null = null;
  onopen: ((e: unknown) => void) | null = null;
}

const fleet: Duckling[] = [
  { id: "blind", provider: "local", model: "text", vision_status: "none" },
  { id: "seer", provider: "cloud", model: "vision", vision_status: "verified" },
];

const chat = (id: string, note: string, status = "paused", consultant = "blind") => ({
  id, project_id: "p", stage: "chat", mode: "solo", task_id: "", status,
  pending_kind: status === "paused" ? "chat" : "", verdict: "", note,
  started_at: `2026-10-09T10:0${id.length % 10}:00Z`,
  roster: { consultant },
  budget: { usd: 0, tokens: 0, turns: 0, wallclock_s: 0 },
}) as unknown as Run;

const transcript = (runId: string, answer: string) => [
  { type: "message", seq: 1, run_id: runId, data: { role: "human", content: "Why did r-build fail?" } },
  { type: "turn_start", seq: 2, run_id: runId, data: { round: 1, turn: 0, role: "consultant", duckling: "blind" } },
  { type: "message", seq: 3, run_id: runId, data: { round: 1, turn: 0, role: "consultant", duckling: "blind", content: answer } },
  { type: "turn_end", seq: 4, run_id: runId, data: { round: 1, turn: 0, role: "consultant" } },
  { type: "human_needed", seq: 5, run_id: runId, data: { kind: "chat" } },
];

const runsById: Record<string, Run> = {
  "r-chat": chat("r-chat", "chat about run r-build"),
  "r-chat2": chat("r-chat2", "chat about bug B-7", "running", "seer"),
};
const answers: Record<string, string> = { "r-chat": "The gate failed on a missing fixture.", "r-chat2": "Still reading the bug." };

function json(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}

function engineFetch(url: string) {
  const path = url.replace(/^https?:\/\/[^/]+/, "");
  if (path === "/v1/projects") return json({ items: [{ id: "p", name: "Fledge", path: "/tmp/fledge" }] });
  if (path.startsWith("/v1/runs?")) return json({ items: Object.values(runsById) });
  const one = /^\/v1\/runs\/(r-[a-z0-9-]+)$/.exec(path);
  if (one && runsById[one[1]!]) return json({ run: runsById[one[1]!], events: transcript(one[1]!, answers[one[1]!]!) });
  if (path === "/v1/ducklings") return json({ items: fleet });
  if (path === "/v1/health") return json({ ok: true, version: "test" });
  if (path.includes("/roster")) return json({ entries: [] });
  if (path.includes("/defaults/diagnostics")) return json({ harness_project_id: "", available: false });
  return json({ items: [] });
}

function resetPane() {
  try { localStorage.clear(); } catch { /* none */ }
  useConsultant.setState(loadConsultantPane());
}

beforeEach(() => {
  resetPane();
  useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {} });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  delete window.ducklab;
  history.replaceState({}, "", "/");
  resetPane();
});

describe("the pane's remembered state", () => {
  it("remembers open/closed and width per viewer, clamped", () => {
    expect(useConsultant.getState()).toMatchObject({ open: false, width: PANE_DEFAULT_WIDTH });
    useConsultant.getState().show();
    useConsultant.getState().setWidth(10_000);
    expect(localStorage.getItem("ducklab.consultantPane.open")).toBe("true");
    expect(loadConsultantPane()).toMatchObject({ open: true, width: PANE_MAX_WIDTH });
    useConsultant.getState().setWidth(12);
    expect(loadConsultantPane().width).toBe(PANE_MIN_WIDTH);
    useConsultant.getState().toggle();
    expect(loadConsultantPane().open).toBe(false);
    useConsultant.getState().openChat("r-chat");
    expect(loadConsultantPane()).toMatchObject({ open: true, activeRunId: "r-chat" });
  });

  it("works when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    expect(loadConsultantPane()).toMatchObject({ open: false, width: PANE_DEFAULT_WIDTH, activeRunId: null });
    useConsultant.getState().show();
    useConsultant.getState().setWidth(600);
    expect(useConsultant.getState()).toMatchObject({ open: true, width: 600 });
  });
});

describe("the consultant pane in the app", () => {
  function renderApp(hash = "#/runs") {
    history.replaceState({}, "", `/?engine=http%3A%2F%2Fengine.test&token=t${hash}`);
    vi.stubGlobal("fetch", vi.fn(async (url: string) => engineFetch(url)));
    vi.stubGlobal("EventSource", EventSourceStub);
    return render(<App />);
  }

  it("opens with the shortcut, keeps the conversation while navigating, and hides again", async () => {
    renderApp("#/runs");
    await waitFor(() => expect(screen.getByTestId("consultant-pane-toggle")).toBeInTheDocument());
    expect(screen.queryByTestId("consultant-pane")).toBeNull();

    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    const pane = await screen.findByTestId("consultant-pane");
    // The open conversations: subject, duckling, where they stand.
    const first = await within(pane).findByTestId("consultant-conversation-r-chat");
    expect(first).toHaveTextContent("run r-build");
    expect(first).toHaveTextContent("blind");
    expect(first).toHaveTextContent("waiting for you");
    expect(within(pane).getByTestId("consultant-conversation-r-chat2")).toHaveTextContent("answering…");

    fireEvent.click(first);
    expect(await within(pane).findByText("The gate failed on a missing fixture.")).toBeInTheDocument();
    // The composer is pinned under the scrolling transcript.
    const transcriptBox = within(pane).getByTestId("consultant-pane-transcript");
    expect(transcriptBox.className).toContain("overflow-y-auto");
    expect(transcriptBox.className).toContain("flex-1");
    expect(transcriptBox.compareDocumentPosition(within(pane).getByTestId("chat-reply")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    expect(screen.getByTestId("runs-view")).toBeInTheDocument();
    act(() => { location.hash = "#/runs/r-build"; });
    await waitFor(() => expect(screen.queryByTestId("runs-view")).toBeNull());
    expect(screen.getByTestId("consultant-pane")).toBeInTheDocument();
    expect(within(screen.getByTestId("consultant-pane")).getByText("The gate failed on a missing fixture.")).toBeInTheDocument();
    act(() => { location.hash = "#/runs"; });
    await waitFor(() => expect(screen.getByTestId("runs-view")).toBeInTheDocument());
    expect(within(screen.getByTestId("consultant-pane")).getByText("The gate failed on a missing fixture.")).toBeInTheDocument();

    // Switch between the open conversations from the pane.
    fireEvent.change(within(screen.getByTestId("consultant-pane")).getByTestId("consultant-pane-switch"), { target: { value: "r-chat2" } });
    expect(await screen.findByText("Still reading the bug.")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("consultant-pane-close"));
    expect(screen.queryByTestId("consultant-pane")).toBeNull();
    expect(localStorage.getItem("ducklab.consultantPane.open")).toBe("false");
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    // Reopens where the person left it.
    expect(await screen.findByText("Still reading the bug.")).toBeInTheDocument();
  });

  it("comes back open after a reload when it was left open", async () => {
    useConsultant.getState().openChat("r-chat");
    useConsultant.setState(loadConsultantPane());
    renderApp("#/runs");
    expect(await screen.findByTestId("consultant-pane")).toBeInTheDocument();
    expect(await screen.findByText("The gate failed on a missing fixture.")).toBeInTheDocument();
  });

  it("starts a new conversation about what the person is looking at", async () => {
    renderApp("#/runs/r-other");
    useConsultant.getState().show();
    const pane = await screen.findByTestId("consultant-pane");
    const offer = await within(pane).findByTestId("consultant-pane-new");
    expect(offer).toHaveTextContent("New conversation about run r-other");
    expect(within(pane).getByTestId("consultant-pane-new-project")).toHaveTextContent("about the project");
    fireEvent.click(offer);
    const form = await within(pane).findByTestId("chat-about-form");
    expect(within(form).getByTestId("chat-duckling")).toBeInTheDocument();
    fireEvent.click(within(form).getByTestId("chat-cancel"));
    expect(within(pane).getByTestId("consultant-pane-list")).toBeInTheDocument();
  });

  // B-285's rule in the pane: a live conversation about the subject is the
  // door back to it, not an invitation to start a second one.
  it("continues the live conversation about the subject instead of starting another", async () => {
    renderApp("#/runs/r-build");
    useConsultant.getState().show();
    const pane = await screen.findByTestId("consultant-pane");
    const offer = await within(pane).findByText("Continue the conversation about run r-build");
    fireEvent.click(offer);
    expect(useConsultant.getState().activeRunId).toBe("r-chat");
    expect(await within(pane).findByText("The gate failed on a missing fixture.")).toBeInTheDocument();
  });

  it("resizes from its edge", async () => {
    renderApp("#/runs");
    useConsultant.getState().show();
    const handle = await screen.findByTestId("consultant-pane-resize");
    fireEvent.mouseDown(handle, { clientX: 1000 });
    fireEvent.mouseMove(window, { clientX: 900 });
    fireEvent.mouseUp(window);
    expect(useConsultant.getState().width).toBe(PANE_DEFAULT_WIDTH + 100);
    expect(screen.getByTestId("consultant-pane").style.width).toBe(`${PANE_DEFAULT_WIDTH + 100}px`);
    fireEvent.keyDown(handle, { key: "ArrowRight" });
    expect(useConsultant.getState().width).toBe(PANE_DEFAULT_WIDTH + 80);
    expect(localStorage.getItem("ducklab.consultantPane.width")).toBe(String(PANE_DEFAULT_WIDTH + 80));
  });
});

describe("entry points open the pane instead of navigating", () => {
  it("a started chat opens in the pane and the page stays where it was", async () => {
    history.replaceState({}, "", "/#/board");
    const client = { roster: vi.fn().mockResolvedValue({ entries: [] }), diagnosticDefaults: vi.fn().mockResolvedValue({ harness_project_id: "", available: false }), chatStart: vi.fn().mockResolvedValue({ id: "r-new" }) } as unknown as EngineClient;
    render(<ChatAbout client={client} projectId="p" aboutKind="task" aboutId="T-1" ducklings={fleet} />);
    fireEvent.click(screen.getByTestId("chat-about"));
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "seer" } });
    fireEvent.change(screen.getByTestId("chat-message"), { target: { value: "why is this task stuck?" } });
    fireEvent.click(screen.getByTestId("chat-start"));
    await waitFor(() => expect(useConsultant.getState()).toMatchObject({ open: true, activeRunId: "r-new" }));
    expect(location.hash).toBe("#/board");
  });

  it("a chat's record offers to open it in the pane", async () => {
    useRuns.setState({ runs: { "r-chat": runsById["r-chat"]! }, events: { "r-chat": transcript("r-chat", answers["r-chat"]!) as never }, deltas: {}, reasoning: {}, spend: {} });
    const client = new EngineClient({ baseUrl: "http://engine.test", token: "t", fetchFn: (async (url: string) => engineFetch(url)) as never });
    render(<RunView runId="r-chat" client={client} />);
    fireEvent.click(await screen.findByTestId("open-in-consultant-pane"));
    expect(useConsultant.getState()).toMatchObject({ open: true, activeRunId: "r-chat" });
  });

  it("the pane's composer carries the vision note and the duckling switch", async () => {
    useRuns.setState({ runs: { "r-chat": runsById["r-chat"]! }, events: {}, deltas: {}, reasoning: {}, spend: {} });
    const client = new EngineClient({ baseUrl: "http://engine.test", token: "t", fetchFn: (async (url: string) => engineFetch(url)) as never });
    const chatSwitch = vi.spyOn(client, "chatSwitch").mockResolvedValue({ ...runsById["r-chat"]!, roster: { consultant: "seer" } } as Run);
    useConsultant.getState().openChat("r-chat");
    render(<ConsultantPane client={client} projectId="p" subject={null} />);
    expect(await screen.findByTestId("chat-vision-note")).toHaveTextContent("blind can't see images");
    fireEvent.click(screen.getByTestId("chat-add-image"));
    fireEvent.click(await screen.findByTestId("chat-switch-to-seer"));
    await waitFor(() => expect(chatSwitch).toHaveBeenCalledWith("r-chat", "seer"));
  });
});

describe("the pane's subject follows the view", () => {
  it("names the run, the document section, a chat's own subject, or the project", () => {
    const runs = { "r-chat": runsById["r-chat"]! };
    expect(consultantSubjectFor({ name: "run", id: "r-9" }, runs, "p")).toMatchObject({ aboutKind: "run", aboutId: "r-9" });
    expect(consultantSubjectFor({ name: "run", id: "r-chat" }, runs, "p")).toMatchObject({ aboutKind: "run", aboutId: "r-build" });
    expect(consultantSubjectFor({ name: "cycle", section: "SPEC-002" }, runs, "p")).toMatchObject({ aboutKind: "document", aboutId: "SPEC-002" });
    expect(consultantSubjectFor({ name: "board" }, runs, "p")).toMatchObject({ aboutKind: "ducklab", aboutId: "p", label: "the project" });
    expect(consultantSubjectFor({ name: "board" }, runs, "")).toBeNull();
  });
});
