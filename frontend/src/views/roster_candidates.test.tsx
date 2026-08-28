import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Roster } from "./Roster";

const ducks = ["bench-best", "pass-cheap", "pass-slow", "no-runs"].map((id) => ({ id, provider: "cloud", model: id }));
const scorecards = ducks.map((duck, i) => ({ ...duck, locality: "remote", measured: { runs: i ? 10 : 14, pass_rate: 92 - i, avg_cost_usd: .2 + i / 10 }, index: { coding_score: 80 - i } }));
const candidates = [{ id: "pass-cheap", why: "pass rate 91% over 10 runs · $0.30/run" }, { id: "bench-best", why: "pass rate 92% over 14 runs · $0.20/run" }, { id: "pass-slow", why: "pass rate 90% over 10 runs · $0.40/run" }];
const seats: Record<string, unknown[]> = { council: [{ role: "reviewer", ducklings: ["pass-cheap"], source: "global mode seat", candidates }] };

describe("Flock seat-aware candidates", () => {
  it("preserves engine ranking and stages choices without writing until Apply", async () => {
    const set = vi.fn(() => Promise.resolve({}));
    const client = { ducklings: vi.fn(() => Promise.resolve(ducks)), Scorecards: vi.fn(() => Promise.resolve(scorecards)), globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })), rosterGet: vi.fn((_: string, mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })), GlobalRosterSet: set, RosterSetManyMode: vi.fn(() => Promise.resolve({})) };
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    await waitFor(() => expect(client.Scorecards).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("roster-drop-council-reviewer"));
    const drawer = screen.getByTestId("duckling-picker-drawer");
    const catalog = within(drawer).getByRole("heading", { name: "All candidates" }).parentElement as HTMLElement;
    const ranked = Array.from(catalog.querySelectorAll("button[data-testid^='roster-pick-suggested-']")).map((node) => node.getAttribute("data-testid")?.replace("roster-pick-suggested-", ""));
    expect(ranked).toEqual(["pass-cheap", "bench-best", "pass-slow"]);
    expect(within(catalog).getByTestId("roster-pick-suggested-bench-best")).toHaveTextContent("pass rate 92%");
    fireEvent.click(within(catalog).getByRole("button", { name: /bench-best/i }));
    expect(set).not.toHaveBeenCalled();
    fireEvent.click(within(drawer).getByRole("button", { name: /apply 2 ducklings/i }));
    await waitFor(() => expect(set).toHaveBeenCalledWith("council", "reviewer", ["pass-cheap", "bench-best"]));
  });
});
