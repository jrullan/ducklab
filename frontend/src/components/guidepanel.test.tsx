import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { GuideRail, shortAction } from "./GuidePanel";
import { useRuns } from "../store/runs";
import { EngineClient, type NextStep } from "../api/client";

const clientWith = (steps: NextStep[]) =>
  new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string) =>
      String(url).includes("/next")
        ? new Response(JSON.stringify({ items: steps, total: steps.length }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          })
        : new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
  });

const STEPS: NextStep[] = [
  { id: "answer-run", action: "Answer the question the spec run asked", reason: "a model waits on you", kind: "run", ref: "r-1" },
  { id: "plan", action: "Plan the work — break the spec into tasks", reason: "3 spec section(s) are not yet built and no plan exists", kind: "stage", ref: "plan" },
  { id: "triage", action: "Triage the open bugs", reason: "2 bug(s) are open", kind: "bug" },
  { id: "test-first", action: "Start T-003 (test first, then build)", reason: "next task whose dependencies are all accepted", kind: "task", ref: "T-003" },
];

describe("the guide rail", () => {
  beforeEach(() => {
    localStorage.clear();
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  // The rail lives beside EVERY view, so unlike the old in-Now panel it must
  // carry the whole guide: away from the inbox, the rail is the only place a
  // paused run or the next task is visible at all.
  it("shows every step in the engine's order, each with its why", async () => {
    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    const steps = screen.getAllByTestId("guide-step").map((s) => s.textContent ?? "");
    expect(steps).toHaveLength(4);
    expect(steps[0]).toContain("Answer the question"); // paused work first
    expect(steps[0]).toContain("a model waits");
    expect(steps[3]).toContain("Start T-003");
  });

  it("links each step to the surface whose buttons already do it", async () => {
    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    const links = screen
      .getAllByTestId("guide-step")
      .map((s) => s.querySelector("a")?.getAttribute("href"));
    expect(links).toEqual(["#/runs/r-1", "#/cycle/plan", "#/board/bugs", "#/board"]);
  });

  // Guidance for the first weeks, a counted strip for after — and the choice
  // survives a reload.
  it("collapses to a counted strip, and stays collapsed", async () => {
    const client = clientWith(STEPS);
    const first = render(<GuideRail client={client} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    fireEvent.click(screen.getByTestId("guide-hide"));
    expect(screen.queryByTestId("guide-rail")).toBeNull();
    expect(screen.getByTestId("guide-pill").textContent).toContain("4");

    first.unmount();
    render(<GuideRail client={client} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-pill"));
    expect(screen.queryByTestId("guide-rail")).toBeNull();

    fireEvent.click(screen.getByTestId("guide-pill"));
    await waitFor(() => screen.getByTestId("guide-rail"));
  });

  // The live pulse sits above the plan: what is happening outranks what is
  // next, and the rail is the one place both survive every view change.
  it("shows running work at the top, even with no steps to offer", async () => {
    useRuns.setState({
      runs: {
        "r-live": {
          id: "r-live", project_id: "p", stage: "build", mode: "pair",
          task_id: "T-079", status: "running", verdict: "", started_at: "2026-08-11T10:00:00Z",
        } as never,
      },
      events: {}, deltas: {}, reasoning: {},
      spend: { "r-live": { usd: 0, tokens: 189900, turns: 1, wallclock_s: 5, ducklings: {} } as never },
    });
    render(<GuideRail client={clientWith([])} projectId="p" />);
    const pulse = await waitFor(() => screen.getByTestId("rail-running"));
    expect(pulse.textContent).toContain("T-079");
    expect(pulse.textContent).toContain("189.9k");
    // The stage beside the task: "T-079 pair" says who but not WHAT — test
    // or build decides whether you expect a red gate or a diff.
    expect(pulse.textContent).toContain("build · pair");
    // Above the steps in document order when both exist.
    render(<GuideRail client={clientWith(STEPS)} projectId="p2" />);
    await waitFor(() => screen.getAllByTestId("guide-step"));
    const rails = screen.getAllByTestId("guide-rail");
    const withBoth = rails[rails.length - 1]!;
    const running = withBoth.querySelector('[data-testid="rail-running"]')!;
    const firstStep = withBoth.querySelector('[data-testid="guide-step"]')!;
    expect(
      running.compareDocumentPosition(firstStep) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  // The rail says WHAT; the chat at its foot explains WHY — same client,
  // same engine introspection, one story.
  it("offers the ask-why chat at its foot", async () => {
    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    // The chat teaches the how as well as the why — its dossier walks the
    // idea-to-release path — and the label says so.
    expect(screen.getByTestId("guide-ask").textContent).toContain("ask how & why");
  });

  it("preselects the Common consultant for the guide chat, but leaves an unpinned chat free", async () => {
    const consultant = { id: "sage", provider: "test", model: "consultant" };
    const pinned = clientWith(STEPS);
    vi.spyOn(pinned, "ducklings").mockResolvedValue([consultant]);
    const pinnedRoster = vi.spyOn(pinned, "RosterGet").mockResolvedValue({
      entries: [{ role: "consultant", duckling: "sage", source: "project pin" }],
    });
    const pinnedRail = render(<GuideRail client={pinned} projectId="p" />);
    await waitFor(() => expect(pinnedRoster).toHaveBeenCalledWith("p", "common"));
    fireEvent.click(screen.getByTestId("chat-about"));
    expect((screen.getByTestId("chat-duckling") as HTMLSelectElement).value).toBe("sage");
    pinnedRail.unmount();

    // No Common consultant pin preserves the deliberate free choice.
    const unpinned = clientWith(STEPS);
    vi.spyOn(unpinned, "ducklings").mockResolvedValue([consultant]);
    vi.spyOn(unpinned, "RosterGet").mockResolvedValue({ entries: [] });
    render(<GuideRail client={unpinned} projectId="p-unpinned" />);
    await waitFor(() => expect(unpinned.RosterGet).toHaveBeenCalledWith("p-unpinned", "common"));
    fireEvent.click(screen.getByTestId("chat-about"));
    expect((screen.getByTestId("chat-duckling") as HTMLSelectElement).value).toBe("");
  });

  it("renders nothing at all when the engine has no step to offer", async () => {
    const { container } = render(<GuideRail client={clientWith([])} projectId="p" />);
    await new Promise((r) => setTimeout(r, 20));
    expect(container.innerHTML).toBe("");
  });

  it("shows an empty recent-runs state when guidance exists but no project run is complete", async () => {
    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    const recent = screen.getByTestId("rail-recent");
    expect(recent.textContent).toContain("no completed runs");
    expect(recent.querySelectorAll("a")).toHaveLength(0);
  });

  it("pins the ten newest completed project runs below the scrollable guide", async () => {
    const completed = Array.from({ length: 12 }, (_, index) => {
      const number = index + 1;
      const id = `r-${number}`;
      return {
        id,
        project_id: "p",
        stage: number === 11 ? "review" : "build",
        mode: "pair",
        task_id: number === 11 ? "" : number === 8 ? "" : `T-${number}`,
        status: number === 10 ? "failed" : "done",
        verdict: number === 12 ? "PASSED" : number === 9 ? "FAILED" : number === 6 ? "BUDGET_EXCEEDED" : "",
        accepted: number === 11,
        started_at: `2026-08-${String(number).padStart(2, "0")}T10:00:00Z`,
      };
    });
    useRuns.setState({
      runs: Object.fromEntries(completed.map((run) => [run.id, run])) as never,
      events: {}, deltas: {}, reasoning: {}, spend: {},
    });

    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    const rail = await waitFor(() => screen.getByTestId("guide-rail"));
    const recent = screen.getByTestId("rail-recent");
    const links = Array.from(recent.querySelectorAll("a"));

    // The two oldest records are omitted; the remaining runs follow the Runs
    // view's started_at ordering, rather than object insertion order.
    expect(links).toHaveLength(10);
    expect(links.map((link) => link.getAttribute("href"))).toEqual(
      [12, 11, 10, 9, 8, 7, 6, 5, 4, 3].map((number) => `#/runs/r-${number}`),
    );
    expect(links.map((link) => link.textContent)).toEqual([
      "T-12", "review", "T-10", "T-9", "r-8", "T-7", "T-6", "T-5", "T-4", "T-3",
    ]);

    const rowFor = (id: string) => links.find((link) => link.getAttribute("href") === `#/runs/${id}`)!.parentElement!;
    expect(rowFor("r-12").textContent).toContain("✓"); // PASSED
    expect(rowFor("r-11").textContent).toContain("✓"); // accepted without a verdict
    expect(rowFor("r-10").textContent).toContain("✕"); // failed status
    expect(rowFor("r-9").textContent).toContain("✕"); // FAILED verdict
    expect(rowFor("r-8").textContent).toContain("UNVERIFIED");
    expect(rowFor("r-7").textContent).toContain("UNVERIFIED");
    expect(rowFor("r-6").textContent).toContain("✕"); // budget exceeded

    // The footer is a sibling of, not content inside, the scrolling guide.
    expect(recent.parentElement).toBe(rail);
    expect(rail.className).toContain("flex");
    expect(rail.className).toContain("flex-col");
    const scrollable = Array.from(rail.children).find((child) => child.className.includes("overflow-y-auto"));
    expect(scrollable).toBeTruthy();
    expect(scrollable!.contains(recent)).toBe(false);

    fireEvent.click(screen.getByTestId("chat-about"));
    expect(screen.getByTestId("chat-about-form")).toBeTruthy();
    expect(screen.getByTestId("rail-recent")).toBe(recent);
  });
});

// Less noise, same thread: the first step is the headline with its why, the
// rest are one line each (verb and object), grouped steps show their objects
// as chips, more than four fold behind "+N more", and beside a view where
// nothing is acted on in place (roster, settings) the rail starts as its strip.
describe("the guide rail, quiet", () => {
  beforeEach(() => {
    localStorage.clear();
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });
  const MANY: NextStep[] = [
    { id: "release", action: "Cut a release — 45 accepted task(s) await shipping", reason: "45 accepted task(s) await a release", kind: "release" },
    { id: "triage", action: "Triage the open bugs", reason: "5 bug(s) are open and unclassified", kind: "bug" },
    { id: "promote", action: "Promote B-057 to a task, or park it", reason: "it is triaged and waiting for a decision", kind: "bug", ref: "B-057" },
    { id: "verify-bug", action: "Verify 3 fixed bugs — confirm each fix answers its report; reopen any that does not", reason: "3 fixes are waiting for human verification", kind: "bug", ref: "B-063", refs: ["B-063", "B-068", "B-069"] },
    { id: "test-first", action: "Start T-070 (test first, then build)", reason: "it is the next task whose dependencies are all accepted", kind: "task", ref: "T-070" },
  ];
  it("headlines the first step, shortens the rest, chips grouped refs, folds the tail", async () => {
    render(<GuideRail client={clientWith(MANY)} projectId="p" view="board" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    const steps = screen.getAllByTestId("guide-step");
    expect(steps).toHaveLength(4);
    expect(steps[0]!.getAttribute("data-primary")).toBe("true");
    expect(steps[0]!.textContent).toContain("45 accepted task(s) await a release"); // the why, first step only
    expect(steps[1]!.textContent).toBe("Triage the open bugs");
    expect(steps[3]!.querySelector("a")!.textContent).toBe("Verify 3 fixed bugs");
    expect(steps[3]!.querySelector("a")!.getAttribute("title")).toContain("reopen any that does not");
    const chips = Array.from(steps[3]!.querySelectorAll('[data-testid="guide-step-refs"] a')).map((a) => a.textContent);
    expect(chips).toEqual(["B-063", "B-068", "B-069"]);
    fireEvent.click(screen.getByTestId("guide-more"));
    expect(screen.getAllByTestId("guide-step")).toHaveLength(5);
    expect(screen.getAllByTestId("guide-step")[4]!.textContent).toBe("Start T-070");
  });
  it("starts folded beside a view where nothing is acted on in place, and remembers being opened there", async () => {
    render(<GuideRail client={clientWith(MANY)} projectId="p" view="roster" />);
    await waitFor(() => screen.getByTestId("guide-pill"));
    expect(screen.queryByTestId("guide-rail")).toBeNull();
    fireEvent.click(screen.getByTestId("guide-pill"));
    await waitFor(() => screen.getByTestId("guide-rail"));
    expect(localStorage.getItem("ducklab.guide.roster")).toBe("on");
    // The global choice is untouched: an actionable view still opens.
    expect(localStorage.getItem("ducklab.guide")).toBeNull();
  });
  it("shortAction keeps verb and object", () => {
    expect(shortAction("Cut a release — 45 accepted task(s) await shipping")).toBe("Cut a release");
    expect(shortAction("Start T-070 (test first, then build)")).toBe("Start T-070");
    expect(shortAction("Promote B-057 to a task, or park it")).toBe("Promote B-057 to a task, or park it");
  });
});

// The self-hosted install step is a button — the whole point is not leaving
// ducklab for a terminal. It runs the declared chain and reports; the engine
// restart stays a separate, deliberate click.
it("runs the declared install chain from the rail", async () => {
  localStorage.clear();
  useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  const steps: NextStep[] = [
    { id: "install", action: "Reinstall ducklab — the repo is 3 commit(s) ahead of the running engine", reason: "run `make install`, then Restart engine and relaunch the app; accepted work is invisible until then", kind: "project" },
  ];
  const base = clientWith(steps) as unknown as Record<string, unknown>;
  base.projectInstall = vi.fn(() => Promise.resolve({ ok: true, seconds: 42, exit_code: 0, output: "", command: "make install" }));
  render(<GuideRail client={base as never} projectId="p" view="board" />);
  await waitFor(() => screen.getByTestId("guide-install"));
  fireEvent.click(screen.getByTestId("guide-install"));
  await waitFor(() => expect(screen.getByTestId("guide-install-result").textContent).toContain("Restart engine"));
  expect(base.projectInstall).toHaveBeenCalledWith("p");
});
