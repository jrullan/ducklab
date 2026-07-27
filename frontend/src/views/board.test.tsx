import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Board } from "./Board";
import { EngineClient, type Task } from "../api/client";

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

  it("says there are no tasks rather than showing five empty columns", async () => {
    // A project with no plan answers items: null, not [].
    const client = clientWith((p) => (p.includes("/tasks") ? json({ items: null, total: 0 }) : json({}, 404)));
    render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(screen.queryByTestId("board-view")).toBeNull());
    expect(screen.getByText(/No tasks yet/)).toBeTruthy();
  });

  it("shows a failure as a failure, not as an empty board", async () => {
    const client = clientWith(() => json({ error: { message: "engine exploded" } }, 500));
    render(<Board client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("board-error").textContent).toContain("engine exploded"));
  });
});
