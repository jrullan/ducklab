import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Board } from "./Board";
import { Settings } from "./Settings";

const task = { id: "T-065", title: "Retire Settings lineups", milestone: "M-01", status: "todo", next: ["run"] };
const fleet = [
  { id: "luna", provider: "test", model: "luna", caps: { native_tools: true, context_tokens: 1 } },
  { id: "glm52", provider: "test", model: "glm", caps: { native_tools: true, context_tokens: 1 } },
  { id: "nova", provider: "test", model: "nova", caps: { native_tools: true, context_tokens: 1 } },
];

function runClient() {
  return {
    tasks: vi.fn(() => Promise.resolve([task])),
    bugs: vi.fn(() => Promise.resolve([])),
    ducklings: vi.fn(() => Promise.resolve(fleet)),
    taskNext: vi.fn(() => Promise.resolve(null)),
    modeDefaults: vi.fn(() => Promise.resolve({ ducklings: {}, build_mode: "pair", test_mode: "solo" })),
    report: vi.fn(() => Promise.resolve({ rows: [] })),
    projectGate: vi.fn(() => Promise.resolve({ mode: "", command: "" })),
    runStart: vi.fn(() => Promise.resolve({ id: "R-1" })),
    RosterGet: vi.fn(() => Promise.resolve({ entries: [
      { role: "implementer", duckling: "luna", ducklings: ["luna"], source: "project pin" },
      { role: "reviewer", duckling: "glm52", ducklings: ["glm52"], source: "global mode seat" },
    ] })),
    RosterSetManyMode: vi.fn(),
    GlobalRosterSet: vi.fn(),
  };
}

async function openRunner(client: ReturnType<typeof runClient>) {
  render(<Board client={client as any} projectId="p" />);
  await screen.findByText(task.title);
  fireEvent.click(screen.getByText(task.title));
  await screen.findByTestId("task-runner");
  fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "pair" } });
}

describe("Roster launchers", () => {
  it("moves positional mode line-ups out of Settings and links to the Roster board", async () => {
    const client = {
      budgetDefaults: vi.fn(() => Promise.resolve({ max_usd: 0, max_tokens: 0, max_turns: 0, max_wallclock_s: 0 })),
      modeDefaults: vi.fn(() => Promise.resolve({ rounds: {}, agent_max_turns: 0 })),
      ducklings: vi.fn(() => Promise.resolve(fleet)),
      roster: vi.fn(() => Promise.resolve({ entries: [] })),
      autopilotDefaults: vi.fn(() => Promise.resolve({ max_tasks: 0, max_fails: 0, autonomy: "" })),
    };
    render(<Settings theme="dark" onTheme={vi.fn()} engineVersion="test" connection="open" client={client as any} projectId="p" />);

    await waitFor(() => expect(screen.queryByTestId("seat-council-0")).not.toBeInTheDocument());
    expect(screen.getByRole("link", { name: /roster board/i })).toBeInTheDocument();
  });

  it("prefills a task run with canonical roster seats and their provenance", async () => {
    const client = runClient();
    await openRunner(client);

    await waitFor(() => expect(screen.getAllByTestId("seat-chip")).toHaveLength(2));
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips[0]!.textContent).toContain("luna");
    expect(chips[0]!.textContent).toMatch(/project/i);
    expect(chips[1]!.textContent).toContain("glm52");
    expect(chips[1]!.textContent).toMatch(/global/i);
  });

  it("keeps a task-runner seat pick local to the run", async () => {
    const client = runClient();
    await openRunner(client);

    await waitFor(() => expect(screen.getAllByTestId("seat-chip")).toHaveLength(2));
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "nova" } });
    expect(screen.getAllByTestId("seat-chip")[0]!.textContent).toMatch(/picked now/i);
    fireEvent.click(screen.getByTestId("run-start"));

    await waitFor(() => expect(client.runStart).toHaveBeenCalledWith("p", "T-065", expect.objectContaining({ ducklings: ["nova", "glm52"] })));
    expect(client.RosterSetManyMode).not.toHaveBeenCalled();
    expect(client.GlobalRosterSet).not.toHaveBeenCalled();
  });
});
