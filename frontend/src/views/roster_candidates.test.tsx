import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const rosterModule = "./Roster";
const loadRoster = () => import(/* @vite-ignore */ rosterModule);

const ducks = ["bench-best", "pass-cheap", "pass-slow", "pass-third", "local-cheap", "no-runs"];
const scorecards = [
  { id: "bench-best", provider: "cloud", model: "bench", locality: "remote", measured: { runs: 14, pass_rate: 0.92, avg_cost_usd: 0.31, avg_wallclock_s: 12 }, bench: { suite: { score: 0.91 } } },
  { id: "pass-cheap", provider: "cloud", model: "cheap", locality: "remote", measured: { runs: 14, pass_rate: 0.92, avg_cost_usd: 0.20, avg_wallclock_s: 8 }, bench: { suite: { score: 0.80 } } },
  { id: "pass-slow", provider: "cloud", model: "slow", locality: "remote", measured: { runs: 9, pass_rate: 0.88, avg_cost_usd: 0.10, avg_wallclock_s: 30 } },
  { id: "pass-third", provider: "cloud", model: "third", locality: "remote", measured: { runs: 6, pass_rate: 0.70, avg_cost_usd: 0.40, avg_wallclock_s: 5 } },
  { id: "local-cheap", provider: "local", model: "local", locality: "local", measured: { runs: 4, pass_rate: 0.99, avg_cost_usd: 0.01, avg_wallclock_s: 1 } },
  { id: "no-runs", provider: "cloud", model: "unknown", locality: "remote", measured: { runs: 0 } },
];

const seats: Record<string, { role: string; ducklings: string[]; source: string }[]> = {
  council: [{ role: "architect", ducklings: ["bench-best"], source: "global mode seat" }, { role: "reviewer", ducklings: ["pass-cheap"], source: "global mode seat" }],
  solo: [{ role: "implementer", ducklings: ["pass-third"], source: "global mode seat" }, { role: "advisor", ducklings: ["pass-slow"], source: "global mode seat" }],
  common: [{ role: "triager", ducklings: ["local-cheap"], source: "global role fallback" }, { role: "scribe", ducklings: ["no-runs"], source: "global role fallback" }],
};

async function renderRoster() {
  const { Roster } = await loadRoster();
  const client = {
    ducklings: vi.fn(() => Promise.resolve(ducks.map((id) => ({ id, provider: id === "local-cheap" ? "local" : "cloud", model: id })))),
    Scorecards: vi.fn(() => Promise.resolve(scorecards)),
    globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })),
    rosterGet: vi.fn((_: string, mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })),
    GlobalRosterSet: vi.fn(() => Promise.resolve({})), RosterSetManyMode: vi.fn(() => Promise.resolve({})),
  };
  render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
  await waitFor(() => expect(client.Scorecards).toHaveBeenCalled());
  return client;
}

const flockOrder = () => Array.from(screen.getByTestId("roster-flock").querySelectorAll("[data-testid^='roster-flock-card-']"))
  .map((node) => node.getAttribute("data-testid")!.replace("roster-flock-card-", ""));
const suggested = () => flockOrder().filter((id) => screen.queryByTestId(`roster-suggested-${id}`));

describe("Roster seat-aware candidates", () => {
  it("prioritizes evidence on seat selection, labels three candidates, restores the prior sort, and never assigns", async () => {
    const client = await renderRoster();
    const initial = flockOrder();

    fireEvent.click(screen.getByTestId("roster-drop-council-reviewer"));
    await waitFor(() => expect(suggested()).toEqual(["pass-cheap", "bench-best", "pass-slow"])); // reviewer: pass rate, then cost
    expect(screen.getByTestId("roster-suggested-pass-cheap").textContent).toMatch(/suggested for reviewer/i);
    expect(screen.getByTestId("roster-suggested-why-bench-best").textContent).toMatch(/pass rate 92% over 14 runs.*\$0\.31\/run/i);
    expect(suggested()).not.toContain("no-runs");
    expect(client.GlobalRosterSet).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    await waitFor(() => expect(flockOrder()).toEqual(initial));

    const architect = screen.getByTestId("roster-column-council-architect");
    architect.focus();
    fireEvent.keyDown(architect, { key: "Enter" });
    await waitFor(() => expect(suggested().slice(0, 3)).toEqual(["bench-best", "pass-cheap", "pass-slow"])); // architect: bench, then cost

    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    fireEvent.click(screen.getByTestId("roster-drop-common-triager"));
    await waitFor(() => expect(flockOrder()).toEqual(initial)); // local roles retain the operator's sort
    expect(screen.queryByTestId("roster-suggested-local-cheap")).toBeNull();
  });
});
