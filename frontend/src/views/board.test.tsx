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
    expect(rail.textContent).toContain("ducklab run T-003");
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
