import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Board } from "./Board";
import { EngineClient, type Bug, type Task } from "../api/client";

const TASKS: Task[] = [
  { id: "T-001", title: "Schema and migrations", milestone: "M-01", status: "accepted", implements: ["SPEC-001"] },
  { id: "T-002", title: "Timesheet entry form", milestone: "M-01", status: "todo", complexity: "medium" },
  { id: "T-003", title: "Approval flow", milestone: "M-02", status: "review", depends_on: ["T-002"] },
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

const okClient = () => clientWith((p) => (p.includes("/tasks") ? json({ items: TASKS, total: 3 }) : json({}, 404)));

describe("Board", () => {
  it("puts each task in the column its run history says it is in", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));

    const col = (k: string) => screen.getByTestId(`board-col-${k}`);
    expect(col("accepted").textContent).toContain("T-001");
    expect(col("todo").textContent).toContain("T-002");
    expect(col("review").textContent).toContain("T-003");
    expect(col("blocked").querySelectorAll("[data-testid=board-card]")).toHaveLength(0);
  });

  it("filters by milestone without losing the total", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));

    fireEvent.change(screen.getByTestId("board-milestone"), { target: { value: "M-02" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(1));
    expect(screen.getByTestId("board-card").dataset.task).toBe("T-003");
  });

  it("filters by free text over id and title", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));

    fireEvent.change(screen.getByTestId("board-search"), { target: { value: "approval" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(1));

    fireEvent.change(screen.getByTestId("board-search"), { target: { value: "T-001" } });
    await waitFor(() => expect(screen.getByTestId("board-card").dataset.task).toBe("T-001"));
  });

  it("shows the full record in the rail on selection", async () => {
    render(<Board client={okClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));

    fireEvent.click(screen.getByText("Approval flow"));
    const rail = screen.getByTestId("board-rail");
    await waitFor(() => expect(rail.textContent).toContain("T-003"));
    expect(rail.textContent).toContain("T-002"); // its dependency
    // The rail used to print the terminal command, because the desktop had no
    // way to start a run. It has one now.
    expect(screen.getByTestId("task-runner")).toBeTruthy();
    expect(rail.textContent).not.toContain("ducklab run");
  });

  it("says there is nothing rather than showing five empty columns", async () => {
    // A project with no plan answers items: null, not [].
    const client = clientWith((p) =>
      p.includes("/bugs") || p.includes("/tasks") ? json({ items: null, total: 0 }) : json({}, 404),
    );
    render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(screen.queryByTestId("board-view")).toBeNull());
    expect(screen.getByText(/Nothing here yet/)).toBeTruthy();
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

    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));
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

    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));
    expect(screen.getByTestId("board-col-open").textContent).toContain("B-001");
    expect(screen.getByTestId("board-col-triaged").textContent).toContain("B-002");
    expect(screen.getByTestId("board-col-in_progress").textContent).toContain("B-003");
  });

  it("filters by severity", async () => {
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy());
    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));

    fireEvent.change(screen.getByTestId("board-severity"), { target: { value: "critical" } });
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(1));
    expect(screen.getByTestId("board-card").dataset.task).toBe("B-001");
  });

  // The selection belongs to the board that made it: keeping it would leave
  // the rail describing a task while the bugs are on screen.
  it("drops the selection when the board changes", async () => {
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));
    fireEvent.click(screen.getByText("Schema and migrations"));
    expect(screen.getByTestId("board-rail").textContent).toContain("T-001");

    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
    expect(screen.getByTestId("board-rail").textContent).toContain("Select a bug");
  });

  // The loop's rules live in the engine, so the rail shows the command that
  // fits rather than a button that might be refused.
  it("offers the next step the bug's status allows", async () => {
    render(<Board client={bothClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-toggle-bugs")).toBeTruthy());
    fireEvent.click(screen.getByTestId("board-toggle-bugs"));
    await waitFor(() => expect(screen.getAllByTestId("board-card")).toHaveLength(3));

    fireEvent.click(screen.getByText("Login loops")); // open
    expect(screen.getByTestId("bug-rail").textContent).toContain("ducklab bug triage");

    fireEvent.click(screen.getByText("Typo in README")); // triaged
    expect(screen.getByTestId("bug-rail").textContent).toContain("ducklab bug promote B-002");

    fireEvent.click(screen.getByText("Slow export")); // in progress, has a task
    expect(screen.getByTestId("bug-rail").textContent).toContain("ducklab run T-009");
  });
});

describe("Board — starting the work", () => {
  const task = { id: "T-001", title: "A thing", milestone: "M-001", status: "todo" };

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
      gateRun: vi.fn(() => Promise.resolve({ green: false, exit_code: 1, output: "", command: "go test ./...", gate: "tests", duration_s: 0.1 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-9" })),
      testStart: vi.fn(() => Promise.resolve({ id: "r-10" })),
      reviewStart: vi.fn(() => Promise.resolve({ id: "r-11" })),
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
      expect(client.runStart).toHaveBeenCalledWith("p", "T-001", { mode: "pair", ducklings: [] }),
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
      tasks: vi.fn(() => Promise.resolve([{ ...task, status: "accepted" }])),
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
describe("Board — test first only where it can work", () => {
  const withGate = (mode: string) =>
    ({
      tasks: vi.fn(() => Promise.resolve([{ id: "T-001", title: "A thing", milestone: "M-001", status: "todo" }])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() => Promise.resolve([])),
      projectGate: vi.fn(() => Promise.resolve({ mode, command: "sh check.sh" })),
      gateRun: vi.fn(() => Promise.resolve({ green: true, exit_code: 0, output: "", command: "sh check.sh", gate: mode, duration_s: 0.1 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      testStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
    }) as unknown as EngineClient;

  it("offers it under a tests gate", async () => {
    render(<Board client={withGate("tests")} projectId="p" />);
    fireEvent.click(await screen.findByText("A thing"));
    expect(await screen.findByTestId("test-first-start")).toBeTruthy();
  });

  for (const mode of ["custom", "build", "lint", "none"]) {
    it(`offers nothing under a ${mode} gate`, async () => {
      render(<Board client={withGate(mode)} projectId="p" />);
      fireEvent.click(await screen.findByText("A thing"));
      await screen.findByTestId("task-runner");
      expect(screen.queryByTestId("test-first-start")).toBeNull();
      // Building still works; it is only the test that has nowhere to land.
      expect(screen.getByTestId("run-start")).toBeTruthy();
    });
  }
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
      gateRun: vi.fn(() =>
        Promise.resolve({ green, exit_code: green ? 0 : 1, output: "", command: "sh scripts/check.sh", gate: mode, duration_s: 0.2 }),
      ),
      runStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
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
      tasks: vi.fn(() => Promise.resolve([{ id: "T-006", title: "A thing", milestone: "M-003", status }])),
      bugs: vi.fn(() => Promise.resolve([])),
      ducklings: vi.fn(() =>
        Promise.resolve([
          { id: "pato-atom", provider: "p", model: "m" },
          { id: "pato-sonnet", provider: "p", model: "m" },
        ]),
      ),
      projectGate: vi.fn(() => Promise.resolve({ mode: "tests", command: "go test ./..." })),
      gateRun: vi.fn(() => Promise.resolve({ green: true, exit_code: 0, output: "", command: "", gate: "tests", duration_s: 0 })),
      runStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      reviewStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
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
