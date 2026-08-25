import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import type { EngineClient, Task } from "../api/client";
import { OriginLine } from "./OriginLine";

const task = (overrides: Partial<Task> = {}): Task => ({
  id: "T-175",
  title: "Carry task origin",
  milestone: "M-02",
  status: "todo",
  ...overrides,
});

function traceClient(nodes: Record<string, Record<string, unknown>>): EngineClient {
  return {
    traceShow: vi.fn(async (_projectId: string, id: string) => {
      const node = nodes[id];
      if (!node) throw new Error("not found");
      return node;
    }),
  } as unknown as EngineClient;
}

describe("OriginLine", () => {
  it("renders the plan origin as a clickable line", async () => {
    const client = traceClient({
      "T-175": { id: "T-175", kind: "task", title: "Carry task origin", up: [] },
      "M-02": { id: "M-02", kind: "milestone", title: "Documents phase 1" },
    });

    render(<OriginLine client={client} projectId="project-1" task={task()} />);

    const link = await screen.findByRole("link", { name: /Documents phase 1/ });
    expect(link).toHaveAttribute("href", "#/cycle/plan");
    expect(link).toHaveTextContent("plan §M-02");
  });

  it("says when the task has no document behind it", async () => {
    const client = traceClient({
      "T-175": { id: "T-175", kind: "task", title: "Carry task origin", up: [] },
    });

    render(<OriginLine client={client} projectId="project-1" task={task({ milestone: "" })} />);

    await waitFor(() => expect(screen.getByTestId("origin-line")).toHaveTextContent("no document behind this task"));
  });

  it("shows both parents when a task was promoted from a bug", async () => {
    const client = traceClient({
      "T-175": { id: "T-175", kind: "task", title: "Carry task origin", up: [] },
      "M-02": { id: "M-02", kind: "milestone", title: "Documents phase 1" },
    });

    render(
      <OriginLine
        client={client}
        projectId="project-1"
        task={task({ body: "Fixes B-188: carry the origin" })}
      />,
    );

    expect(await screen.findByRole("link", { name: /from bug B-188/ })).toHaveTextContent(
      "from bug B-188 · plan §M-02 · Documents phase 1",
    );
  });
});
