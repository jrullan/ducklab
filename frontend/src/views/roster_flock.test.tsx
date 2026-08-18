import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const rosterModule = "./Roster";
const loadRoster = () => import(/* @vite-ignore */ rosterModule);

const ducks = [
  { id: "atlas", provider: "cloud", model: "atlas-3" },
  { id: "bravo", provider: "local", model: "bravo-v" },
  { id: "cedar", provider: "cloud", model: "cedar-tools" },
  { id: "delta", provider: "cloud", model: "delta-long" },
  { id: "echo", provider: "local", model: "echo-mini" },
];

// These are deliberately heterogeneous: every evidence category has a real
// ordering as well as an absent value, and a filter can isolate a distinct card.
const scorecards = [
  { id: "atlas", provider: "cloud", model: "atlas-3", locality: "remote", cost: { input_per_mtok: 1, output_per_mtok: 10 }, caps: { context_tokens: 128000, vision: false, native_tools: false }, measured: { runs: 14, pass_rate: 0.92, avg_cost_usd: 0.12 }, bench: { arena: { score: 0.81 } }, index: { coding: 0.72, source: "index", as_of: "2026-01-01" } },
  { id: "bravo", provider: "local", model: "bravo-v", locality: "local", cost: { input_per_mtok: 3, output_per_mtok: 1 }, caps: { context_tokens: 64000, vision: true, native_tools: false }, measured: { runs: 0 } },
  { id: "cedar", provider: "cloud", model: "cedar-tools", locality: "remote", cost: { input_per_mtok: 2, output_per_mtok: 4 }, caps: { context_tokens: 32000, vision: false, native_tools: true }, measured: { runs: 8, pass_rate: 0.5, avg_cost_usd: 0.2 }, bench: { arena: { score: 0.65 } }, index: { coding: 0.91, source: "index", as_of: "2026-01-01" } },
  { id: "delta", provider: "cloud", model: "delta-long", locality: "remote", caps: { context_tokens: 256000, vision: false, native_tools: false }, measured: { runs: 4, pass_rate: 0.7, avg_cost_usd: 0.3 }, bench: { arena: { score: 0.95 } } },
  { id: "echo", provider: "local", model: "echo-mini", locality: "local", cost: { input_per_mtok: 4, output_per_mtok: 2 }, caps: { context_tokens: 16000, vision: false, native_tools: true }, index: { coding: 0.55, source: "index", as_of: "2026-01-01" } },
];

const seats = { council: [{ role: "reviewer", ducklings: ["atlas"], source: "global mode seat" }] };

async function renderRoster() {
  const { Roster } = await loadRoster();
  const set = vi.fn(() => Promise.resolve({}));
  const client = {
    ducklings: vi.fn(() => Promise.resolve(ducks)),
    Scorecards: vi.fn(() => Promise.resolve(scorecards)),
    globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: seats[mode as keyof typeof seats] ?? [] })),
    rosterGet: vi.fn((_: string, mode: string) => Promise.resolve({ entries: seats[mode as keyof typeof seats] ?? [] })),
    RosterSetManyMode: set,
    GlobalRosterSet: vi.fn(() => Promise.resolve({})),
  };
  render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
  await waitFor(() => expect(client.Scorecards).toHaveBeenCalled());
  return { client, set };
}

function flockOrder() {
  return Array.from(screen.getByTestId("roster-flock").querySelectorAll("[data-testid^='roster-flock-card-']"))
    .map((node) => node.getAttribute("data-testid")!.replace("roster-flock-card-", ""));
}

async function expectOrder(sort: string, ascending: string[], descending: string[]) {
  fireEvent.change(screen.getByTestId("roster-flock-sort"), { target: { value: sort } });
  fireEvent.change(screen.getByTestId("roster-flock-sort-dir"), { target: { value: "asc" } });
  await waitFor(() => expect(flockOrder()).toEqual(ascending));
  fireEvent.change(screen.getByTestId("roster-flock-sort-dir"), { target: { value: "desc" } });
  await waitFor(() => expect(flockOrder()).toEqual(descending));
}

describe("Roster flock evidence", () => {
  it("filters scorecard candidates, orders evidence with unknowns last, exposes values, and keeps filtered cards seatable", async () => {
    const { set } = await renderRoster();

    fireEvent.change(screen.getByTestId("roster-flock-filter-text"), { target: { value: "cedar-tools" } });
    await waitFor(() => expect(flockOrder()).toEqual(["cedar"]));
    fireEvent.change(screen.getByTestId("roster-flock-filter-text"), { target: { value: "" } });
    fireEvent.change(screen.getByTestId("roster-flock-filter-provider"), { target: { value: "local" } });
    await waitFor(() => expect(flockOrder()).toEqual(["bravo", "echo"]));
    fireEvent.change(screen.getByTestId("roster-flock-filter-provider"), { target: { value: "" } });
    fireEvent.change(screen.getByTestId("roster-flock-filter-locality"), { target: { value: "remote" } });
    await waitFor(() => expect(flockOrder()).toEqual(["atlas", "cedar", "delta"]));
    fireEvent.change(screen.getByTestId("roster-flock-filter-locality"), { target: { value: "" } });
    fireEvent.click(screen.getByTestId("roster-flock-filter-vision"));
    await waitFor(() => expect(flockOrder()).toEqual(["bravo"]));
    fireEvent.click(screen.getByTestId("roster-flock-filter-vision"));
    fireEvent.click(screen.getByTestId("roster-flock-filter-native-tools"));
    await waitFor(() => expect(flockOrder()).toEqual(["cedar", "echo"]));
    fireEvent.click(screen.getByTestId("roster-flock-filter-native-tools"));
    fireEvent.change(screen.getByTestId("roster-flock-filter-context"), { target: { value: "100000" } });
    await waitFor(() => expect(flockOrder()).toEqual(["atlas", "delta"]));
    fireEvent.change(screen.getByTestId("roster-flock-filter-context"), { target: { value: "" } });

    await expectOrder("input-cost", ["atlas", "cedar", "bravo", "echo", "delta"], ["echo", "bravo", "cedar", "atlas", "delta"]);
    await expectOrder("output-cost", ["bravo", "echo", "cedar", "atlas", "delta"], ["atlas", "cedar", "echo", "bravo", "delta"]);
    await expectOrder("pass-rate", ["cedar", "delta", "atlas", "bravo", "echo"], ["atlas", "delta", "cedar", "bravo", "echo"]);
    await expectOrder("avg-cost", ["atlas", "cedar", "delta", "bravo", "echo"], ["delta", "cedar", "atlas", "bravo", "echo"]);
    await expectOrder("bench:arena", ["cedar", "atlas", "delta", "bravo", "echo"], ["delta", "atlas", "cedar", "bravo", "echo"]);
    await expectOrder("coding-index", ["echo", "atlas", "cedar", "bravo", "delta"], ["cedar", "atlas", "echo", "bravo", "delta"]);
    await expectOrder("context", ["echo", "cedar", "bravo", "atlas", "delta"], ["delta", "atlas", "bravo", "cedar", "echo"]);

    expect(screen.getByTestId("roster-flock-value-delta").textContent).toContain("256");
    expect(screen.getByTestId("roster-flock-card-bravo").textContent).toMatch(/no runs yet|no bench|no index/i);
    expect(screen.getByTestId("roster-flock-card-echo").textContent).toMatch(/no runs yet|no bench/i);
    expect(screen.getByTestId("roster-flock-card-delta").textContent).toMatch(/no index/i);

    fireEvent.change(screen.getByTestId("roster-flock-filter-text"), { target: { value: "cedar" } });
    await waitFor(() => expect(flockOrder()).toEqual(["cedar"]));
    fireEvent.click(screen.getByRole("button", { name: /Project · Pond/i }));
    fireEvent.drop(screen.getByTestId("roster-column-council-reviewer"), { dataTransfer: { getData: vi.fn(() => "cedar") } });
    await waitFor(() => expect(set).toHaveBeenCalledWith("pond", "council", "reviewer", ["cedar"]));
  });
});
