import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { Board } from "./Board";
import { useRuns } from "../store/runs";
import { EngineClient, type Bug, type Task } from "../api/client";

const TASKS: Task[] = [
  { id: "T-001", title: "Schema and migrations", milestone: "M-01", status: "accepted", implements: ["SPEC-001"] },
  { id: "T-002", title: "Timesheet entry form", milestone: "M-01", status: "todo", complexity: "medium" },
  { id: "T-003", title: "Approval flow", milestone: "M-02", status: "review", depends_on: ["T-002"] },
  {
    id: "T-004", title: "Export to CSV", milestone: "M-02", status: "blocked",
    depends_on: ["T-003"], blocked: "waiting on T-003",
  },
];

function clientWith(handler: (path: string) => Response) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string) =>
      handler(String(url).replace("http://engine", ""))) as unknown as typeof fetch,
  });
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const okClient = () => clientWith((p) => (p.includes("/tasks") ? json({ items: TASKS, total: TASKS.length }) : json({}, 404)));

describe("Board", () => {
  it("puts each task in the column its run history says it is in", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(4));

    const col = (k: string) => screen.getByTestId(`board-col-${k}`);
    expect(col("accepted").textContent).toContain("T-001");
    expect(col("todo").textContent).toContain("T-002");
    expect(col("review").textContent).toContain("T-003");
    expect(col("blocked").textContent).toContain("T-004");
  });

  // Blocked was a column no task could ever enter: nothing in the engine
  // assigned it, and this test asserted it stayed empty. A task that had been
  // tried and had failed landed back in Todo, indistinguishable from one
  // nobody had touched.
  it("says on the card what blocked the work", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(4));
    expect(screen.getByTestId("blocked-reason").textContent).toBe("waiting on T-003");
  });

  it("filters by milestone without losing the total", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(TASKS.length));

    fireEvent.change(screen.getByTestId("board-milestone"), { target: { value: "M-02" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(2));
    expect(screen.getAllByTestId("board-card").map((c) => c.dataset.task)).toEqual(["T-004", "T-003"]);
  });

  it("filters by free text over id and title", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(TASKS.length));

    fireEvent.change(screen.getByTestId("board-search"), { target: { value: "approval" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(1));

    fireEvent.change(screen.getByTestId("board-search"), { target: { value: "T-001" } });
    await waitFor(() => expect(screen.getByTestId("board-card").dataset.task).toBe("T-001"));
  });

  it("shows the full record in the rail on selection", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(TASKS.length));

    fireEvent.click(screen.getByText("Approval flow"));
    const rail = screen.getByTestId("board-rail");
    await waitFor(() => expect(rail.textContent).toContain("T-003"));
    expect(rail.textContent).toContain("T-002"); // its dependency
    // The rail used to print the terminal command, because the desktop had no
    // way to start a run. It has one now.
    expect(screen.getByTestId("task-runner")).toBeTruthy();
    expect(rail.textContent).not.toContain("ducklab run");
  });

  // An empty project used to render ONLY this message, and the message named two
  // CLI commands — so the one state where you most need to file something was
  // the state with no controls at all, and the advice was to use another
  // program. It still says there is nothing; it no longer takes the board away.
  it("says there is nothing without removing the controls", async () => {
    // A project with no plan answers items: null, not [].
    const client = clientWith((p) =>
      p.includes("/bugs") || p.includes("/tasks") ? json({ items: null, total: 0 }) : json({}, 404),
    );
    render(<Board client={client} projectId="p" tab="bugs" />);
    await waitFor(() => expect(screen.getByText(/Nothing here yet/)).toBeTruthy());
    // The controls survive: filing a bug from an empty project is the state
    // that most needs them. The board switch lives in the Work subnav now.
    expect(screen.getByTestId("bug-file")).toBeTruthy();
    expect(screen.getByText(/Nothing here yet/).textContent).not.toContain("ducklab ");
  });

  it("shows a failure as a failure, not as an empty board", async () => {
    const client = clientWith(() => json({ error: { message: "engine exploded" } }, 500));
    render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-error").textContent).toContain("engine exploded"));
  });

  // One board failing must not blank the other: a project whose bugs cannot be
  // read still has tasks worth looking at, and losing both to one error tells
  // the reader less than showing what survived.
  it("keeps the tasks when only the bugs fail to load", async () => {
    const client = clientWith((p) =>
      p.includes("/bugs")
        ? json({ error: { message: "bug table is locked" } }, 500)
        : json({ items: TASKS, total: TASKS.length }),
    );
    render(<Board client={client} projectId="p" />);

    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(TASKS.length));
    const err = screen.getByTestId("board-error").textContent ?? "";
    expect(err).toContain("bugs");
    expect(err).toContain("bug table is locked");
    expect(err).not.toContain("tasks:");
  });
});

const BUGS: Bug[] = [
  { id: "B-001", title: "Login loops", severity: "critical", status: "open", source: "manual",
    created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-01T00:00:00Z" },
  { id: "B-002", title: "Typo in README", severity: "low", status: "triaged", source: "manual",
    created_at: "2026-07-02T00:00:00Z", updated_at: "2026-07-02T00:00:00Z" },
  { id: "B-003", title: "Slow export", severity: "normal", status: "in_progress", task_id: "T-009",
    source: "manual", created_at: "2026-07-03T00:00:00Z", updated_at: "2026-07-03T00:00:00Z" },
];

const bothClient = () =>
  clientWith((p) => {
    if (p.includes("/bugs")) return json({ items: BUGS, total: BUGS.length });
    if (p.includes("/tasks")) return json({ items: TASKS, total: TASKS.length });
    return json({}, 404);
  });

describe("Board, the bugs half", () => {
  it("puts each bug in its loop's column", async () => {
    render(<Board client={bothClient()} projectId="p" tab="bugs" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(BUGS.length));
    expect(screen.getByTestId("board-col-open").textContent).toContain("B-001");
    expect(screen.getByTestId("board-col-triaged").textContent).toContain("B-002");
    expect(screen.getByTestId("board-col-in_progress").textContent).toContain("B-003");
  });

  it("filters by severity", async () => {
    render(<Board client={bothClient()} projectId="p" tab="bugs" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(BUGS.length));

    fireEvent.change(screen.getByTestId("board-severity"), { target: { value: "critical" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(1));
    expect(screen.getByTestId("board-card").dataset.task).toBe("B-001");
  });

  // The selection belongs to the board that made it: keeping it would leave
  // the rail describing a task while the bugs are on screen.
  it("drops the selection when the board changes", async () => {
    // The switch arrives as a new route prop — the toggle navigates and the
    // router hands the board back — so the test switches the way App does.
    const client = bothClient();
    const view = render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(TASKS.length));
    fireEvent.click(screen.getByText("Schema and migrations"));
    expect(screen.getByTestId("board-rail").textContent).toContain("T-001");

    view.rerender(<Board client={client} projectId="p" tab="bugs" />);
    await waitFor(() =>
      expect(screen.getByTestId("board-rail").textContent).toContain("Select a bug"),
    );
  });

  // The loop's rules live in the engine, so the rail shows the command that
  // fits rather than a button that might be refused.
  // These used to print the CLI command that fits — honest, but it made the
  // operate loop the one loop a desktop-only user could not run.
  it("offers the next step the bug's status allows, as something to click", async () => {
    render(<Board client={bothClient()} projectId="p" tab="bugs" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(BUGS.length));

    fireEvent.click(screen.getByText("Login loops")); // open
    expect(screen.getByTestId("bug-next-triage")).toBeTruthy();
    expect(screen.queryByTestId("bug-next-promote")).toBeNull();

    fireEvent.click(screen.getByText("Typo in README")); // triaged
    expect(screen.getByTestId("bug-next-promote")).toBeTruthy();
    expect(screen.queryByTestId("bug-next-triage")).toBeNull();

    fireEvent.click(screen.getByText("Slow export")); // in progress, has a task
    expect(screen.getByTestId("bug-next").textContent).toContain("T-009");
  });

  // The board is scanned, not read: severity sits by the id, titles clamp to
  // three lines with the whole on hover, the foot says how long and who; the
  // verified archive folds to its count and empty columns shrink; the detail
  // puts the actions under the title and renders MCP-escaped bodies as
  // paragraphs.
  it("keeps cards scannable, folds the verified archive, and reads bodies as paragraphs", async () => {
    const many: Bug[] = [
      ...BUGS,
      { id: "B-010", title: "Long " + "title ".repeat(40), severity: "high", status: "open", source: "mcp", reporter: "mcp:claude-code",
        body: "What happened: the brake refused.\\n\\nExpected: it concludes.", created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-01T00:00:00Z" },
      { id: "B-020", title: "Done one", severity: "normal", status: "verified", source: "manual", created_at: "2026-06-01T00:00:00Z", updated_at: "2026-06-01T00:00:00Z" },
      { id: "B-021", title: "Done two", severity: "normal", status: "verified", source: "manual", created_at: "2026-06-01T00:00:00Z", updated_at: "2026-06-01T00:00:00Z" },
    ];
    const client = clientWith((p) => {
      if (p.includes("/bugs")) return json({ items: many, total: many.length });
      if (p.includes("/tasks")) return json({ items: TASKS, total: TASKS.length });
      return json({}, 404);
    });
    render(<Board client={client} projectId="p" tab="bugs" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card").length).toBeGreaterThan(3));
    // Verified is folded: its two cards are not on the board, its count is.
    expect(screen.getByTestId("board-col-verified").textContent).toContain("Verified · 2");
    expect(screen.queryByText("Done one")).toBeNull();
    fireEvent.click(screen.getByTestId("board-verified-toggle"));
    await waitFor(() => expect(screen.getByText("Done one")).toBeTruthy());
    // Empty Fixed is a strip; Open with work is wide.
    expect(screen.getByTestId("board-col-fixed").className).toContain("w-28");
    expect(screen.getByTestId("board-col-open").className).toContain("flex-1");
    // The long card: clamped title with the whole on hover, severity glyph, age · reporter.
    const long = screen.getAllByTestId("board-card").find((c) => c.dataset.task === "B-010")!;
    expect(long.querySelector(".line-clamp-3")!.getAttribute("title")).toContain("Long title");
    expect(long.querySelector('[data-testid="severity-chip"]')!.getAttribute("data-severity")).toBe("high");
    expect(long.querySelector('[data-testid="bug-card-meta"]')!.textContent).toMatch(/\d+d · mcp/);
    // Detail: actions under the title, body as paragraphs without literal \n.
    fireEvent.click(long);
    const rail = screen.getByTestId("bug-rail");
    const next = rail.querySelector('[data-testid="bug-next"]')!;
    const body = rail.querySelector('[data-testid="bug-body"]')!;
    expect(next.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(body.querySelectorAll("p")).toHaveLength(2);
    expect(body.textContent).not.toContain("\\n");
    // Toolbar: triage is the primary action, filters left, count reads "all N".
    expect(screen.getByTestId("bug-triage").className).toContain("font-medium");
    expect(screen.getByText(/^all 6$/)).toBeTruthy();
  });
});

describe("Board — starting the work", () => {
  // next comes from the engine; fixtures carry what it would state for a
  // fresh task under a tests gate.
  const task = {
    id: "T-001", title: "A thing", milestone: "M-001", status: "todo",
    next: ["run", "test_first", "remove"],
  };

  const runClient = (over: Record<string, unknown> = {}) =>
    ({
      tasks: vi.fn(() => Promise.resolve([task])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() =>
        Promise.resolve([
          { id: "pato-local", provider: "beelink", model: "qwen" },
          { id: "pato-sonnet", provider: "openrouter", model: "claude" },
        ]),
      ),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      gateRun: vi.fn(() => Promise.resolve({ green: false, exit_code: 1, output: "", command: "go test ./...", gate: "tests", duration_s: 0.1 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-9" })),
      testStart: vi.fn(() => Promise.resolve({ id: "r-10" })),
      reviewStart: vi.fn(() => Promise.resolve({ id: "r-11" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      ...over,
    }) as unknown as EngineClient;

  const openRail = async () => {
    fireEvent.click(await screen.findByText("A thing"));
    return screen.findByTestId("task-runner");
  };

  // A summary reload empties task bodies; the rail must read the selected
  // task in full rather than show a title over nothing.
  it("refills a body-less selected task from the full list", async () => {
    const summaryTask = { ...task, body: "" };
    const fullTask = { ...task, body: "- Add the file input\n- Post multipart" };
    // The list on screen came from a summary reload (bodies stripped); only
    // an explicit full fetch carries the body.
    let calls = 0;
    const tasks = vi.fn((_p: string, summary?: boolean) => {
      calls++;
      return Promise.resolve([calls === 1 || summary ? summaryTask : fullTask]);
    });
    const client = runClient({ tasks });
    render(<Board client={client} projectId="p" />);
    await openRail();
    expect(await screen.findByTestId("task-body")).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId("task-body").textContent).toContain("Add the file input"));
    expect(tasks).toHaveBeenCalledWith("p", false);
  });

  it("starts a build run in the chosen mode", async () => {
    const client = runClient();
    render(<Board client={client} projectId="p" />);
    await openRail();

    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "pair" } });
    fireEvent.click(screen.getByTestId("run-start"));
    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-001", {
        mode: "pair",
        ducklings: [],
        maxTokens: undefined,
      }),
    );
    expect((await screen.findByTestId("run-link")).getAttribute("href")).toBe("#/runs/r-9");
  });

  // tournament and split assign ducklings positionally, so the order the boxes
  // were ticked is the order they are sent.
  it("sends the chosen ducklings in the order they were picked", async () => {
    const client = runClient();
    render(<Board client={client} projectId="p" />);
    await openRail();

    // Mode first: solo shows one seat, tournament opens two.
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "tournament" } });
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "pato-sonnet" } });
    fireEvent.click(screen.getAllByTestId("seat-chip")[1]!);
    fireEvent.change(screen.getByTestId("seat-pick-1"), { target: { value: "pato-local" } });
    fireEvent.click(screen.getByTestId("run-start"));

    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-001", {
        mode: "tournament",
        ducklings: ["pato-sonnet", "pato-local"],
        maxTokens: undefined,
      }),
    );
  });

  // A run that hits the token ceiling fails with a number the person starting it
  // could not see or change. This raises it for one run; the default is in
  // Settings. Left empty it must not be sent at all — a zero would be a ceiling
  // of zero, and the engine fills unset limits from the defaults.
  it("raises this run's token ceiling without touching the default", async () => {
    const client = runClient();
    render(<Board client={client} projectId="p" />);
    await openRail();

    fireEvent.change(screen.getByTestId("run-max-tokens"), { target: { value: "1500000" } });
    fireEvent.click(screen.getByTestId("run-start"));

    await waitFor(() =>
      expect(client.runStart).toHaveBeenCalledWith("p", "T-001", {
        mode: "solo",
        ducklings: [],
        maxTokens: 1500000,
      }),
    );
  });

  // The card moves NOW: a person started test-first, watched the board not
  // move, changed views, and came back to find it had been in progress all
  // along. A board that needs a view change to notice is reporting the past.
  it("reloads the board the moment a run is started", async () => {
    const client = runClient();
    render(<Board client={client} projectId="p" />);
    await openRail();
    const callsBefore = (client.tasks as ReturnType<typeof vi.fn>).mock.calls.length;
    fireEvent.click(screen.getByTestId("run-start"));
    await waitFor(() =>
      expect((client.tasks as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(callsBefore),
    );
  });

  it("writes the test first, by the chosen duckling", async () => {
    const client = runClient();
    render(<Board client={client} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "pato-sonnet" } });
    fireEvent.click(screen.getByTestId("test-first-start"));
    await waitFor(() => expect(client.testStart).toHaveBeenCalledWith("p", "T-001", "pato-sonnet"));
  });

  // There is no commit to read until the work was accepted, and the engine
  // refuses with exactly that. A button that only ever errors is worse than none.
  it("offers Review only on work that was accepted", async () => {
    render(<Board client={runClient()} projectId="p" />);
    await openRail();
    expect(screen.queryByTestId("review-start")).toBeNull();
  });

  it("offers Review on an accepted task", async () => {
    const client = runClient({
      tasks: vi.fn(() => Promise.resolve([{ ...task, status: "accepted", next: ["review", "run"] }])),
    });
    render(<Board client={client} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getByTestId("review-start"));
    await waitFor(() => expect(client.reviewStart).toHaveBeenCalledWith("p", "T-001"));
  });

  it("shows the engine's refusal rather than failing silently", async () => {
    const client = runClient({
      runStart: vi.fn(() => Promise.reject(new Error("task T-001 is not ready"))),
    });
    render(<Board client={client} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getByTestId("run-start"));
    expect((await screen.findByTestId("run-error")).textContent).toContain("not ready");
  });
});

// Test-first needs a gate that runs tests. A compiler, a linter or a bespoke
// script gives a new test nothing to hook into — proved on a real project,
// where the model reasoned its way to patching the gate script itself because
// that was the only place an assertion could live.
describe("Board — test first renders only when the engine offers it", () => {
  // The gate-mode rule — a test is only worth writing where the gate can see
  // it — moved into the engine, which now states each task's legal actions.
  // The client's whole job is to draw that list faithfully; the rule itself is
  // covered engine-side by TestWhatATaskOffersMatchesTheGuards.
  const withNext = (next: string[]) =>
    ({
      tasks: vi.fn(() =>
        Promise.resolve([{ id: "T-001", title: "A thing", milestone: "M-001", status: "todo", next }]),
      ),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "sh check.sh" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      gateRun: vi.fn(() => Promise.resolve({ green: true, exit_code: 0, output: "", command: "sh check.sh", gate: "tests", duration_s: 0.1 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      testStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown as EngineClient;

  it("offers it when stated", async () => {
    render(<Board client={withNext(["run", "test_first"])} projectId="p" />);
    fireEvent.click(await screen.findByText("A thing"));
    expect(await screen.findByTestId("test-first-start")).toBeTruthy();
  });

  it("offers nothing when the engine did not state it", async () => {
    render(<Board client={withNext(["run"])} projectId="p" />);
    fireEvent.click(await screen.findByText("A thing"));
    await screen.findByTestId("task-runner");
    expect(screen.queryByTestId("test-first-start")).toBeNull();
    // Building still works; it is only the test that has nowhere to land.
    expect(screen.getByTestId("run-start")).toBeTruthy();
  });
});

// Knowing the gate is red before starting is what makes a green afterwards
// mean anything. It was only visible from a terminal.
describe("Board — the gate before the run", () => {
  const client = (mode: string, green: boolean) =>
    ({
      tasks: vi.fn(() => Promise.resolve([{ id: "T-001", title: "A thing", milestone: "M-001", status: "todo" }])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode, command: "sh scripts/check.sh" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      gateRun: vi.fn(() =>
        Promise.resolve({ green, exit_code: green ? 0 : 1, output: "", command: "sh scripts/check.sh", gate: mode, duration_s: 0.2 }),
      ),
      runStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown as EngineClient;

  const openRail = async () => {
    fireEvent.click(await screen.findByText("A thing"));
    return screen.findByTestId("gate-state");
  };

  it("names the command that will decide the run", async () => {
    render(<Board client={client("custom", false)} projectId="p" />);
    expect((await openRail()).textContent).toContain("sh scripts/check.sh");
  });

  // Not on load: a gate can be a whole test suite, and a panel that ran one on
  // every click would make looking expensive.
  it("does not run the gate until asked", async () => {
    const c = client("custom", false);
    render(<Board client={c} projectId="p" />);
    await openRail();
    expect(c.gateRun).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("gate-check"));
    await waitFor(() => expect(c.gateRun).toHaveBeenCalledWith("p"));
  });

  it("says red, and why that is worth knowing first", async () => {
    render(<Board client={client("custom", false)} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getByTestId("gate-check"));
    const state = await screen.findByTestId("gate-state");
    await waitFor(() => expect(state.textContent).toContain("red now"));
    expect(state.textContent).toContain("a green afterwards means something");
  });

  it("says green without the warning", async () => {
    render(<Board client={client("tests", true)} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getByTestId("gate-check"));
    const state = await screen.findByTestId("gate-state");
    await waitFor(() => expect(state.textContent).toContain("green now"));
    expect(state.textContent).not.toContain("means something");
  });

  // With no gate there is nothing to check and nothing a run can prove.
  it("says a run cannot pass at all without a gate", async () => {
    render(<Board client={client("none", false)} projectId="p" />);
    expect((await openRail()).textContent).toContain("UNVERIFIED");
    expect(screen.queryByTestId("gate-check")).toBeNull();
  });
});

// Reported from a real session: an accepted task still showed Build it, with a
// mode picker and duckling checkboxes — apparatus for a decision nobody is
// making. The controls follow the task's state now.
describe("Board — an accepted task", () => {
  const client = (status: string) =>
    ({
      tasks: vi.fn(() =>
        Promise.resolve([
          {
            id: "T-006", title: "A thing", milestone: "M-003", status,
            next: (
              {
                accepted: ["review", "run"],
                todo: ["run", "test_first", "remove"],
                in_progress: [],
                review: [],
              } as Record<string, string[]>
            )[status],
          },
        ]),
      ),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() =>
        Promise.resolve([
          { id: "pato-atom", provider: "p", model: "m" },
          { id: "pato-sonnet", provider: "p", model: "m" },
        ]),
      ),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      gateRun: vi.fn(() => Promise.resolve({ green: true, exit_code: 0, output: "", command: "", gate: "tests", duration_s: 0 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      reviewStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown as EngineClient;

  const open = async (status: string) => {
    render(<Board client={client(status)} projectId="p" />);
    fireEvent.click(await screen.findByText("A thing"));
    return screen.findByTestId("task-runner");
  };

  it("offers Review first, and no test-first", async () => {
    await open("accepted");
    expect(screen.getByTestId("review-start")).toBeTruthy();
    expect(screen.queryByTestId("test-first-start")).toBeNull();
  });

  // Rebuilding an accepted task is exactly when the person has something to
  // SAY — "the fix leaked, close every connection" — so the full launcher
  // stands here too: seats, tokens, calls/reply, and the note.
  it("keeps the full build apparatus available, note included", async () => {
    await open("accepted");
    expect(screen.getByTestId("run-again")).toBeTruthy();
    expect(screen.getByTestId("run-start").textContent).toContain("Build again");
    expect(screen.getByTestId("run-note-toggle")).toBeTruthy();
    expect(screen.getByTestId("accepted-note").textContent).toContain("already done");
  });

  it("leaves an unfinished task exactly as it was", async () => {
    await open("todo");
    expect(screen.getByTestId("run-start")).toBeTruthy();
    expect(screen.getByTestId("run-mode")).toBeTruthy();
    expect(screen.getByTestId("test-first-start")).toBeTruthy();
    expect(screen.queryByTestId("review-start")).toBeNull();
    expect(screen.queryByTestId("accepted-note")).toBeNull();
  });
});
// Two runs against one task edit the same tree at the same time, and the second
// one's diff contains the first one's changes — which is not a result anybody
// can judge. The board offered "Build it" on a task a run was already doing.
describe("Board — a task a run is already doing", () => {
  const busyTask = { id: "T-001", title: "A thing", milestone: "M-001", status: "in_progress" };

  const client = () =>
    ({
      tasks: vi.fn(() => Promise.resolve([busyTask])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([{ id: "pato-local", provider: "beelink", model: "qwen" }])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      gateRun: vi.fn(() => Promise.resolve({ green: false, exit_code: 1, output: "", command: "go test ./...", gate: "tests", duration_s: 0.1 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-9" })),
      testStart: vi.fn(() => Promise.resolve({ id: "r-10" })),
      reviewStart: vi.fn(() => Promise.resolve({ id: "r-11" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown as EngineClient;

  it("offers no way to start a second one", async () => {
    render(<Board client={client()} projectId="p" />);
    fireEvent.click(await screen.findByText("A thing"));
    await screen.findByTestId("task-runner");
    expect(screen.queryByTestId("run-start")).toBeNull();
    expect(screen.queryByTestId("test-first-start")).toBeNull();
    expect(screen.getByTestId("running-note").textContent).toContain("right now");
  });
});
// The engine has had POST /bugs since the operate loop was built, and the
// board's own empty state told you to go and run `ducklab bug add`. On a
// desktop-only setup the whole loop was unreachable.
describe("filing a bug from the desktop", () => {
  const client = () =>
    ({
      tasks: vi.fn(() => Promise.resolve([])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      bugAdd: vi.fn(() => Promise.resolve({ id: "B-010" })),
      triageBugs: vi.fn(() => Promise.resolve({ id: "r-7" })),
    }) as unknown as EngineClient;

  const openBugs = async (c: EngineClient) => {
    render(<Board client={c} projectId="p" tab="bugs" />);
    await waitFor(() => expect(screen.getByTestId("bug-file")).toBeTruthy());
  };

  it("files a report with the severity as given", async () => {
    const c = client();
    await openBugs(c);
    fireEvent.click(screen.getByTestId("bug-file"));
    fireEvent.change(screen.getByTestId("bug-title"), {
      target: { value: "vertex drag never starts" },
    });
    fireEvent.change(screen.getByTestId("bug-body"), {
      target: { value: "toWorld returns an array; the hit test reads .x" },
    });
    fireEvent.change(screen.getByTestId("bug-severity"), { target: { value: "critical" } });
    fireEvent.click(screen.getByTestId("bug-submit"));

    await waitFor(() =>
      expect(c.bugAdd).toHaveBeenCalledWith("p", {
        title: "vertex drag never starts",
        body: "toWorld returns an array; the hit test reads .x",
        severity: "critical",
      }),
    );
  });

  it("will not file an empty report", async () => {
    const c = client();
    await openBugs(c);
    fireEvent.click(screen.getByTestId("bug-file"));
    expect((screen.getByTestId("bug-submit") as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the engine's refusal instead of pretending it filed", async () => {
    const c = client();
    (c as unknown as { bugAdd: unknown }).bugAdd = vi.fn(() =>
      Promise.reject(new Error("project has no plan yet")),
    );
    await openBugs(c);
    fireEvent.click(screen.getByTestId("bug-file"));
    fireEvent.change(screen.getByTestId("bug-title"), { target: { value: "x" } });
    fireEvent.click(screen.getByTestId("bug-submit"));
    await waitFor(() =>
      expect(screen.getByTestId("bug-error").textContent).toContain("no plan yet"),
    );
  });
});
// The rail had a case for open and a case for triaged and nothing else, so a
// report that reached in_progress — which is where promoting one puts it — sat
// with no button on it and no way to move it by hand.
describe("moving a bug by hand", () => {
  const stuck = {
    id: "B-001", title: "vertex drag never starts", severity: "critical",
    status: "in_progress", task_id: "T-024", source: "desktop",
    created_at: "2026-07-30T14:14:05Z", updated_at: "2026-07-30T14:14:05Z",
    next: ["fixed", "triaged"],
  };
  const client = () =>
    ({
      tasks: vi.fn(() => Promise.resolve([])),
      bugs: vi.fn(() => Promise.resolve([stuck])),
      ducklings: vi.fn(() => Promise.resolve([])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      moveBug: vi.fn(() => Promise.resolve(stuck)),
    }) as unknown as EngineClient;

  it("offers every move the engine says is legal", async () => {
    const c = client();
    render(<Board client={c} projectId="p" tab="bugs" />);
    fireEvent.click(await screen.findByText("vertex drag never starts"));

    expect(screen.getByTestId("bug-move-fixed")).toBeTruthy();
    expect(screen.getByTestId("bug-move-triaged")).toBeTruthy();
    // And nothing the loop forbids.
    expect(screen.queryByTestId("bug-move-verified")).toBeNull();
  });

  it("moves it", async () => {
    const c = client();
    render(<Board client={c} projectId="p" tab="bugs" />);
    fireEvent.click(await screen.findByText("vertex drag never starts"));
    fireEvent.click(screen.getByTestId("bug-move-fixed"));

    await waitFor(() => expect(c.moveBug).toHaveBeenCalledWith("p", "B-001", "fixed"));
  });
});
// The board showed every task's state and never answered the question a person
// actually arrives with. The engine has computed it all along.
describe("what to start next", () => {
  const client = (next: unknown) =>
    ({
      tasks: vi.fn(() => Promise.resolve([
        { id: "T-010", title: "Implement basic constraint solver", milestone: "M-005", status: "todo" },
      ])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(next)),
    }) as unknown as EngineClient;

  it("names it", async () => {
    render(<Board client={client({ id: "T-010", title: "Implement basic constraint solver", milestone: "M-005", status: "todo" })} projectId="p" />);
    const line = await screen.findByTestId("task-next");
    expect(line.textContent).toContain("T-010");
    expect(line.textContent).toContain("Implement basic constraint solver");
  });

  it("selects it, so the answer is one click from the controls", async () => {
    render(<Board client={client({ id: "T-010", title: "Implement basic constraint solver", milestone: "M-005", status: "todo" })} projectId="p" />);
    fireEvent.click(await screen.findByTestId("task-next-select"));
    expect(screen.getByTestId("board-rail").textContent).toContain("T-010");
  });

  // Nothing ready is itself the answer: everything is done, running, or waiting
  // on something. A line saying so would be one more thing to read.
  it("says nothing when nothing is ready", async () => {
    render(<Board client={client(null)} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card").length).toBeGreaterThan(0));
    expect(screen.queryByTestId("task-next")).toBeNull();
  });
});
// Decided outcomes — closed, duplicate, wontfix — are rightly not columns. But
// they rendered NOWHERE: the record existed and no surface owned it, so a
// closed report was unfindable without opening the database.
describe("Board — decided bugs", () => {
  const decided = {
    id: "B-009", title: "Old crash on save", severity: "low", status: "closed",
    source: "desktop", created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-02T00:00:00Z",
  };
  const client = () =>
    ({
      tasks: vi.fn(() => Promise.resolve([])),
      bugs: vi.fn(() => Promise.resolve([BUGS[0], decided])),
      ducklings: vi.fn(() => Promise.resolve([])),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      report: vi.fn(() => Promise.resolve({ rows: [], deltas: [], rendered: "" })),
    }) as unknown as EngineClient;

  it("folds them under the board, out of every column", async () => {
    render(<Board client={client()} projectId="p" tab="bugs" />);
    const archive = await screen.findByTestId("bugs-decided");
    expect(archive.textContent).toContain("1 decided");
    expect(archive.textContent).toContain("B-009");
    // Not a column, and not in one.
    for (const card of screen.getAllByTestId("board-card")) {
      expect(card.textContent).not.toContain("B-009");
    }
  });

  it("selects into the rail like anything else", async () => {
    render(<Board client={client()} projectId="p" tab="bugs" />);
    fireEvent.click(await screen.findByTestId("decided-bug"));
    expect(screen.getByTestId("bug-rail").textContent).toContain("Old crash on save");
  });
});

// The rail renders the engine's actions in the order stated — the order IS
// the workflow. A fresh tests-gated task puts Test first at the top as the
// step to take; the fixed layout used to show it at the bottom, reading as an
// afterthought — the exact inversion of the TDD flow the person wanted.
describe("the rail follows the contract's order", () => {
  const railClient = (over: Record<string, unknown> = {}) =>
    ({
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() =>
        Promise.resolve([
          { id: "pato-local", provider: "beelink", model: "qwen" },
          { id: "pato-sonnet", provider: "openrouter", model: "claude" },
        ]),
      ),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "pytest -q" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      runStart: vi.fn(() => Promise.resolve({ id: "r-9" })),
      testStart: vi.fn(() => Promise.resolve({ id: "r-10" })),
      reviewStart: vi.fn(() => Promise.resolve({ id: "r-11" })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      ...over,
    }) as unknown as EngineClient;
  const openRail = async () => {
    fireEvent.click(await screen.findByText("A task"));
    return screen.findByTestId("task-runner");
  };

  it("puts the two-phase TDD block on top when the engine states test_first first", async () => {
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-001", title: "A task", milestone: "M-01", status: "todo",
            next: ["test_first", "run", "remove"] },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    const block = screen.getByTestId("tdd-block");
    // Each phase carries its own mode and seats; the secondary steps sit in
    // one aligned row.
    expect(block.textContent).toContain("1 · write the failing test");
    expect(block.textContent).toContain("2 · build until it passes");
    expect(screen.getAllByTestId("launch-config")).toHaveLength(2);
    expect(screen.getByTestId("tdd-start").textContent).toContain("Test first → Build");
    expect(screen.getByTestId("test-first-start").textContent).toBe("test only");
    expect(screen.getByTestId("build-only").textContent).toBe("build only");
  });

  // Mode defaults choose the opening modes, but seats remain omitted until
  // picked so the project roster decides the untouched launch.
  it("opens on the Settings defaults with roster-owned seats", async () => {
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-001", title: "A task", milestone: "M-01", status: "todo",
            next: ["test_first", "run", "remove"] },
        ]),
      ),
      modeDefaults: vi.fn(() =>
        Promise.resolve({
          rounds: {}, agent_max_turns: 24,
          build_mode: "pair", test_mode: "solo",
          ducklings: { pair: ["pato-sonnet", "pato-local"], solo: ["pato-local"] },
        }),
      ),
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    const [testCfg, buildCfg] = screen.getAllByTestId("launch-config");
    expect((testCfg!.querySelector("[data-testid=cfg-mode]") as HTMLSelectElement).value).toBe("solo");
    // Seats read from the chips now — the picker and the glance are one UI.
    const chipText = (el: Element, i: number) =>
      el.querySelectorAll('[data-testid="seat-chip"]')[i]!.textContent ?? "";
    expect(chipText(testCfg!, 0)).toContain("default");
    expect(chipText(testCfg!, 0)).toContain("roster");
    expect((buildCfg!.querySelector("[data-testid=cfg-mode]") as HTMLSelectElement).value).toBe("pair");
    expect(chipText(buildCfg!, 0)).toContain("default");
    expect(chipText(buildCfg!, 0)).toContain("roster");
    expect(chipText(buildCfg!, 1)).toContain("default");
  });

  // The plain launcher — a test-ready task where run is primary — opens on
  // the same Settings default as the TDD block: a habit that held in one
  // rendering of the rail and not the other was half a setting.
  it("opens the plain launcher on the Settings build default too", async () => {
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-012", title: "A task", milestone: "M-01", status: "todo",
            test_ready: true, next: ["run", "test_first", "remove"] },
        ]),
      ),
      modeDefaults: vi.fn(() =>
        Promise.resolve({
          rounds: {}, agent_max_turns: 24,
          build_mode: "pair",
          ducklings: { pair: ["pato-sonnet", "pato-local"] },
        }),
      ),
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    expect((screen.getByTestId("run-mode") as HTMLSelectElement).value).toBe("pair");
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips[0]!.textContent).toContain("pato-sonnet");
    expect(chips[1]!.textContent).toContain("pato-local");
  });

  // A committed failing test is a promise with two exits, and the rail must
  // offer both: build until green, or retire the test — the engine reverts
  // its commit and releases the queue the red suite was holding. Without the
  // button, a chain whose build kept failing left git surgery as the only
  // way out.
  it("offers retiring the test when the engine does", async () => {
    const testRetire = vi.fn(() => Promise.resolve({ id: "r-t", revert_sha: "abc" } as never));
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-022", title: "A task", milestone: "M-01", status: "todo",
            test_ready: true, next: ["run", "retire_test", "test_first", "remove"] },
        ]),
      ),
      testRetire,
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getByTestId("retire-test"));
    await waitFor(() => expect(testRetire).toHaveBeenCalledWith("p", "T-022"));
    // The click gets an answer, not just a reload: the person who retires a
    // test is owed a sentence saying it happened, with the revert commit.
    await waitFor(() =>
      expect(screen.getByTestId("retire-note").textContent).toContain("abc"),
    );
  });

  // And never on its own initiative: the button renders only from the
  // engine's list (the order IS the workflow).
  it("does not offer retiring when the engine does not", async () => {
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-012", title: "A task", milestone: "M-01", status: "todo",
            test_ready: true, next: ["run", "test_first", "remove"] },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    expect(screen.queryByTestId("retire-test")).toBeNull();
  });

  // The chain sends BOTH phases' configuration: a cheap solo test and a paid
  // pair build, each with its own seats.
  it("launches the chain with each phase's own mode and seats", async () => {
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-001", title: "A task", milestone: "M-01", status: "todo",
            next: ["test_first", "run", "remove"] },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    const [testCfg, buildCfg] = screen.getAllByTestId("launch-config");
    const pickIn = (cfg: Element, i: number, val: string) => {
      fireEvent.click(cfg.querySelectorAll('[data-testid="seat-chip"]')[i]!);
      fireEvent.change(cfg.querySelector(`[data-testid="seat-pick-${i}"]`)!, { target: { value: val } });
    };
    // Test phase: solo, written by pato-local (cheap).
    pickIn(testCfg!, 0, "pato-local");
    // Build phase: pair with its own seats.
    fireEvent.change(buildCfg!.querySelector("[data-testid=cfg-mode]")!, { target: { value: "pair" } });
    pickIn(buildCfg!, 0, "pato-sonnet");
    pickIn(buildCfg!, 1, "pato-local");
    fireEvent.click(screen.getByTestId("tdd-start"));
    await waitFor(() =>
      expect(client.testStart).toHaveBeenCalledWith("p", "T-001", "", {
        thenBuild: true,
        testMode: "solo",
        testDucklings: ["pato-local"],
        mode: "pair",
        ducklings: ["pato-sonnet", "pato-local"],
        maxTokens: undefined,
      }),
    );
  });

  it("puts the launcher on top when run is stated first", async () => {
    const client = railClient({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-001", title: "A task", milestone: "M-01", status: "todo",
            test_ready: true, next: ["run", "test_first", "remove"] },
        ]),
      ),
    } as Partial<EngineClient>);
    render(<Board client={client} projectId="p" />);
    await openRail();
    const actions = screen.getByTestId("task-actions");
    expect(actions.firstElementChild!.querySelector("[data-testid=run-launcher]")).toBeTruthy();
  });
});

// The person clicked the card to DO something: a promoted bug's body carries
// its whole report and triage, and it pushed the controls below the fold.
// Actions render before the prose, and the prose scrolls in its own pane.
// The rail's running note said "watch it" and offered no way to. The store
// already knows which run holds the task — fed by the stream, so the link is
// there whether this window started the run or an MCP operator did.
describe("the running task links its run", () => {
  it("offers the run the task is in progress on", async () => {
    useRuns.setState({
      runs: {
        "r-77": { id: "r-77", project_id: "p", task_id: "T-001", stage: "build",
          mode: "solo", status: "running", verdict: "" } as never,
      },
      events: {}, deltas: {}, reasoning: {}, spend: {},
    });
    const client = (({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-001", title: "Busy one", milestone: "M-01", status: "in_progress", next: [] },
        ]),
      ),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "pytest -q" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown) as EngineClient;
    render(<Board client={client} projectId="p" />);
    fireEvent.click(await screen.findByText("Busy one"));
    const link = await screen.findByTestId("running-link");
    expect(link.getAttribute("href")).toBe("#/runs/r-77");
  });
});

// The person launched from this rail; the decision comes back to it. The
// trip to the run view is for reading the diff, not for pressing Accept.
describe("deciding a gate from the task rail", () => {
  it("offers Accept and Reject on the task whose run waits", async () => {
    useRuns.setState({
      runs: {
        "r-88": { id: "r-88", project_id: "p", task_id: "T-001", stage: "build",
          mode: "pair", status: "paused", pending_kind: "gate", verdict: "PASSED",
          next: ["accept", "reject"] } as never,
      },
      events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {},
    });
    const accept = vi.fn(() => Promise.resolve({ commit_sha: "abc" }));
    const client = (({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-001", title: "Waiting one", milestone: "M-01", status: "review", next: [] },
        ]),
      ),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "pytest -q" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
      accept,
    }) as unknown) as EngineClient;
    render(<Board client={client} projectId="p" />);
    fireEvent.click(await screen.findByText("Waiting one"));
    await screen.findByTestId("now-waiting-card");
    fireEvent.click(screen.getByTestId("now-accept"));
    await waitFor(() => expect(accept).toHaveBeenCalledWith("r-88"));
  });
});

describe("the rail puts actions before the prose", () => {
  it("renders the runner above the body", async () => {
    const longBody = "Fixes B-007.\n" + "line of report\n".repeat(80);
    const client = (({
      tasks: vi.fn(() =>
        Promise.resolve([
          { id: "T-050", title: "Long one", milestone: "M-01", status: "todo",
            body: longBody, next: ["test_first", "run", "remove"] },
        ]),
      ),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "pytest -q" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown) as EngineClient;
    render(<Board client={client} projectId="p" />);
    fireEvent.click(await screen.findByText("Long one"));
    const runner = await screen.findByTestId("task-runner");
    const body = screen.getByTestId("task-body");
    // Document order: the runner precedes the body.
    expect(runner.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

// "When triage finishes the Bugs view should refresh and the running task
// should update too." The refetch key used to watch only which runs were
// ACTIVE, so a paused triage being accepted — the moment ApplyTriage rewrites
// every bug — changed nothing the key could see, and the board kept its
// pre-triage columns until something else forced a load.
describe("the board follows a run to its end", () => {
  it("refetches bugs when a run changes status, not only when one appears", async () => {
    useRuns.setState({
      runs: {
        "r-tri": { id: "r-tri", project_id: "p", task_id: "", stage: "triage",
          mode: "solo", status: "paused", pending_kind: "gate", verdict: "" } as never,
      },
      events: {}, deltas: {}, reasoning: {}, spend: {},
    });
    const bugs = vi.fn(() => Promise.resolve([]));
    const client = (({
      tasks: vi.fn(() => Promise.resolve([])),
      bugs,
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "pytest -q" })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown) as EngineClient;
    render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(bugs).toHaveBeenCalled());
    const before = bugs.mock.calls.length;

    act(() => {
      useRuns.setState((s) => ({
        runs: { ...s.runs, "r-tri": { ...s.runs["r-tri"], status: "done" } as never },
      }));
    });
    await waitFor(() => expect(bugs.mock.calls.length).toBeGreaterThan(before));
  });
});


// The run view's dock learned this first: a bounded scroller that bottoms
// out must NOT hand the wheel to the page. Same contract on every board
// rail — the UI is consistent about where a scroll ends.
describe("board rail contains its own scroll", () => {
  it("carries overscroll containment", async () => {
    const client = (({
      tasks: vi.fn(() => Promise.resolve([{ id: "T-001", title: "A thing", milestone: "M-1", status: "todo", next: [] }])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      taskNext: vi.fn(() => Promise.resolve(null)),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 24, ducklings: {} })),
    }) as unknown) as EngineClient;
    render(<Board client={client} projectId="p" />);
    fireEvent.click(await screen.findByText("A thing"));
    const rail = await screen.findByTestId("board-rail");
    expect(rail.className).toContain("overscroll-contain");
  });
});
