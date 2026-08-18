import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const rosterModule = "./Roster";
const loadRoster = () => import(/* @vite-ignore */ rosterModule);

const catalog = [
  { key: "coding_index", label: "coding index", direction: "desc", source: "external" },
  { key: "pass_rate", label: "pass rate in this seat", direction: "desc", source: "runs" },
  { key: "cost_per_run", label: "cost per run", direction: "asc", source: "runs" },
  { key: "wallclock", label: "wallclock per run", direction: "asc", source: "runs" },
];
const defaults = { implementer: ["coding_index", "pass_rate", "cost_per_run"], advisor: ["cost_per_run", "wallclock"] };

// The criteria are the person's; the engine ships defaults and shows which
// roles are overridden. Editing is in place, saved on every change, and the
// board reloads so the suggestions follow.
describe("Roster suggestion criteria", () => {
  it("shows the effective order per role, reorders and saves, and resets to default", async () => {
    const { Roster } = await loadRoster();
    let configured: Record<string, string[]> = {};
    const viewNow = () => ({ criteria: { ...defaults, ...configured }, configured: Object.keys(configured), defaults, catalog });
    const client = {
      ducklings: vi.fn(() => Promise.resolve([])),
      Scorecards: vi.fn(() => Promise.resolve([])),
      globalRosterGet: vi.fn(() => Promise.resolve({ entries: [] })),
      rosterGet: vi.fn(() => Promise.resolve({ entries: [] })),
      candidateCriteria: vi.fn(() => Promise.resolve(viewNow())),
      candidateCriteriaSet: vi.fn((c: Record<string, string[]>) => { configured = c; return Promise.resolve(viewNow()); }),
    };
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    // Folded until asked for; the toggle says what it is.
    expect(screen.queryByTestId("roster-criteria")).toBeNull();
    fireEvent.click(screen.getByTestId("roster-criteria-toggle"));
    await screen.findByTestId("roster-criteria");
    const impl = screen.getByTestId("roster-criteria-implementer");
    expect(impl.textContent).toContain("1.coding index");
    expect(impl.textContent).toContain("default");

    // Cost first: move it earlier twice; each move is a save of the FULL
    // configured map, and the role now reads as the person's.
    fireEvent.click(screen.getByRole("button", { name: /move cost per run earlier for implementer/i }));
    await waitFor(() => expect(client.candidateCriteriaSet).toHaveBeenLastCalledWith({ implementer: ["coding_index", "cost_per_run", "pass_rate"] }));
    fireEvent.click(await screen.findByRole("button", { name: /move cost per run earlier for implementer/i }));
    await waitFor(() => expect(client.candidateCriteriaSet).toHaveBeenLastCalledWith({ implementer: ["cost_per_run", "coding_index", "pass_rate"] }));
    await waitFor(() => expect(screen.getByTestId("roster-criteria-implementer").textContent).toContain("1.cost per run"));
    expect(screen.getByTestId("roster-criteria-reset-implementer")).toBeTruthy();
    // The boards reload so suggestions follow the new order.
    expect(client.globalRosterGet.mock.calls.length).toBeGreaterThan(6);

    // Removing every criterion turns the seat's suggestions off; adding one
    // back appends; a role untouched (advisor) is never sent.
    fireEvent.click(screen.getByRole("button", { name: /remove cost per run from implementer/i }));
    fireEvent.click(await screen.findByRole("button", { name: /remove coding index from implementer/i }));
    fireEvent.click(await screen.findByRole("button", { name: /remove pass rate in this seat from implementer/i }));
    await waitFor(() => expect(client.candidateCriteriaSet).toHaveBeenLastCalledWith({ implementer: [] }));
    await waitFor(() => expect(screen.getByTestId("roster-criteria-implementer").textContent).toContain("suggestions off"));
    fireEvent.change(screen.getByTestId("roster-criteria-add-implementer"), { target: { value: "wallclock" } });
    await waitFor(() => expect(client.candidateCriteriaSet).toHaveBeenLastCalledWith({ implementer: ["wallclock"] }));

    // Reset drops the role from the configured map: the default is back.
    fireEvent.click(await screen.findByTestId("roster-criteria-reset-implementer"));
    await waitFor(() => expect(client.candidateCriteriaSet).toHaveBeenLastCalledWith({}));
    await waitFor(() => expect(screen.getByTestId("roster-criteria-implementer").textContent).toContain("1.coding index"));
  });
});
