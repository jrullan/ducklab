import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Roster } from "./Roster";

const ducks = ["atlas", "bravo", "cedar", "delta", "echo"].map((id) => ({ id, provider: id === "bravo" || id === "echo" ? "local" : "cloud", model: id }));
const scorecards = ducks.map((duck, index) => ({ ...duck, locality: duck.provider === "local" ? "local" : "remote", measured: { runs: 5 + index, pass_rate: 60 + index, avg_cost_usd: .1 + index / 10 }, index: { coding_score: 70 + index } }));
const seats: Record<string, unknown[]> = { council: [{ role: "reviewer", ducklings: ["atlas"], source: "global mode seat", candidates: [{ id: "cedar", why: "best measured trade-off" }] }] };

describe("Flock candidate catalog", () => {
  it("moves the complete duckling inventory into the contextual seat drawer", async () => {
    const set = vi.fn(() => Promise.resolve({}));
    const client = { ducklings: vi.fn(() => Promise.resolve(ducks)), Scorecards: vi.fn(() => Promise.resolve(scorecards)), globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })), rosterGet: vi.fn((_: string, mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })), GlobalRosterSet: set, RosterSetManyMode: vi.fn(() => Promise.resolve({})) };
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    await waitFor(() => expect(client.Scorecards).toHaveBeenCalled());
    expect(screen.queryByTestId("roster-flock")).toBeNull();
    fireEvent.click(screen.getByTestId("roster-drop-council-reviewer"));
    const drawer = screen.getByTestId("duckling-picker-drawer");
    const catalog = within(drawer).getByRole("heading", { name: "All candidates" }).parentElement as HTMLElement;
    for (const duck of ducks) expect(within(catalog).getByRole("button", { name: new RegExp(duck.id) })).toBeTruthy();
    expect(within(drawer).getByTestId("duckling-map")).toBeTruthy();
    expect(set).not.toHaveBeenCalled();
  });
});
