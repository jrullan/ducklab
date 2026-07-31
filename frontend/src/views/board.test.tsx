import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Board } from "./Board";
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
    render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByText(/Nothing here yet/)).toBeTruthy());
    expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy();
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
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy());
    fireEvent.click(screen.getByTestId("board-toggle-bugs"));

    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(BUGS.length));
    expect(screen.getByTestId("board-col-open").textContent).toContain("B-001");
    expect(screen.getByTestId("board-col-triaged").textContent).toContain("B-002");
    expect(screen.getByTestId("board-col-in_progress").textContent).toContain("B-003");
  });

  it("filters by severity", async () => {
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy());
    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(BUGS.length));

    fireEvent.change(screen.getByTestId("board-severity"), { target: { value: "critical" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(1));
    expect(screen.getByTestId("board-card").dataset.task).toBe("B-001");
  });

  // The selection belongs to the board that made it: keeping it would leave
  // the rail describing a task while the bugs are on screen.
  it("drops the selection when the board changes", async () => {
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(TASKS.length));
    fireEvent.click(screen.getByText("Schema and migrations"));
    expect(screen.getByTestId("board-rail").textContent).toContain("T-001");

    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
    expect(screen.getByTestId("board-rail").textContent).toContain("Select a bug");
  });

  // The loop's rules live in the engine, so the rail shows the command that
  // fits rather than a button that might be refused.
  // These used to print the CLI command that fits — honest, but it made the
  // operate loop the one loop a desktop-only user could not run.
  it("offers the next step the bug's status allows, as something to click", async () => {
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy());
    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
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

    fireEvent.click(screen.getByTestId("run-duckling-pato-sonnet"));
    fireEvent.click(screen.getByTestId("run-duckling-pato-local"));
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "tournament" } });
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

  it("writes the test first, by the chosen duckling", async () => {
    const client = runClient();
    render(<Board client={client} projectId="p" />);
    await openRail();
    fireEvent.click(screen.getByTestId("run-duckling-pato-sonnet"));
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

  it("offers Review, and not the build apparatus", async () => {
    await open("accepted");
    expect(screen.getByTestId("review-start")).toBeTruthy();
    for (const id of ["run-start", "run-mode", "run-duckling-pato-atom", "test-first-start"]) {
      expect(screen.queryByTestId(id)).toBeNull();
    }
  });

  // Still possible — a result can be regretted — but it says what it is rather
  // than sitting there as the obvious next step.
  it("keeps building available, and says what it means", async () => {
    await open("accepted");
    expect(screen.getByTestId("run-again")).toBeTruthy();
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
    render(<Board client={c} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy());
    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
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
    render(<Board client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("board-toggle-bugs"));
    fireEvent.click(await screen.findByText("vertex drag never starts"));

    expect(screen.getByTestId("bug-move-fixed")).toBeTruthy();
    expect(screen.getByTestId("bug-move-triaged")).toBeTruthy();
    // And nothing the loop forbids.
    expect(screen.queryByTestId("bug-move-verified")).toBeNull();
  });

  it("moves it", async () => {
    const c = client();
    render(<Board client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("board-toggle-bugs"));
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
