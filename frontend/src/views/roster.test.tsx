import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";

// Kept as a dynamic import while the view does not exist: this specifies the
// public view without making TypeScript require its implementation first.
const rosterModule = "./Roster";
const loadRoster = () => import(/* @vite-ignore */ rosterModule);

type Seat = {
  role: string;
  ducklings: string[];
  source: "global mode seat" | "project pin" | "global role fallback";
  global_ducklings?: string[];
};

const global: Record<string, Seat[]> = {
  council: [
    { role: "architect", ducklings: ["architect"], source: "global mode seat" },
    { role: "reviewer", ducklings: ["critic-a", "critic-b"], source: "global mode seat" },
  ],
  solo: [
    { role: "implementer", ducklings: ["builder"], source: "global mode seat" },
    { role: "advisor", ducklings: ["advisor"], source: "global mode seat" },
  ],
  pair: [
    { role: "implementer", ducklings: ["builder"], source: "global mode seat" },
    { role: "advisor", ducklings: ["advisor"], source: "global mode seat" },
    { role: "reviewer", ducklings: ["reviewer"], source: "global mode seat" },
  ],
  split: [
    { role: "architect", ducklings: ["architect"], source: "global mode seat" },
    { role: "implementer", ducklings: ["worker-a", "worker-b"], source: "global mode seat" },
    { role: "reviewer", ducklings: ["reviewer"], source: "global mode seat" },
  ],
  tournament: [
    { role: "implementer", ducklings: ["contestant-a", "contestant-b"], source: "global mode seat" },
    { role: "judge", ducklings: ["judge"], source: "global mode seat" },
  ],
  common: [
    { role: "triager", ducklings: ["triager"], source: "global role fallback" },
    { role: "scribe", ducklings: ["scribe"], source: "global role fallback" },
  ],
};

const project: Record<string, Seat[]> = {
  ...global,
  council: [
    { role: "architect", ducklings: ["architect"], source: "global mode seat" },
    { role: "reviewer", ducklings: ["critic-a", "critic-b"], source: "global mode seat" },
  ],
  pair: [
    { role: "implementer", ducklings: ["project-builder"], source: "project pin", global_ducklings: ["builder"] },
    { role: "advisor", ducklings: ["advisor"], source: "global mode seat" },
    { role: "reviewer", ducklings: ["reviewer"], source: "global mode seat" },
  ],
};

async function renderRoster() {
  const { Roster } = await loadRoster();
  const client = {
    ducklings: vi.fn(() => Promise.resolve([
      { id: "architect", provider: "local", model: "qwen", cost: { input_per_mtok: 0, output_per_mtok: 0 } },
      { id: "critic-a", provider: "remote", model: "sonnet", cost: { input_per_mtok: 3, output_per_mtok: 15 } },
      { id: "critic-b", provider: "remote", model: "gpt", cost: { input_per_mtok: 2, output_per_mtok: 8 } },
      { id: "builder", provider: "local", model: "qwen" },
      { id: "project-builder", provider: "remote", model: "opus", cost: { input_per_mtok: 15, output_per_mtok: 75 } },
      { id: "advisor", provider: "local", model: "qwen" },
      { id: "reviewer", provider: "remote", model: "sonnet" },
      { id: "worker-a", provider: "local", model: "qwen" },
      { id: "worker-b", provider: "remote", model: "gpt" },
      { id: "contestant-a", provider: "local", model: "qwen" },
      { id: "contestant-b", provider: "remote", model: "sonnet" },
      { id: "judge", provider: "remote", model: "sonnet" },
      { id: "triager", provider: "local", model: "qwen" },
      { id: "scribe", provider: "remote", model: "gpt" },
    ])),
    globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: global[mode] })),
    rosterGet: vi.fn((_projectId: string, mode: string) => Promise.resolve({ entries: project[mode] })),
  };
  render(<Roster client={client as never} projectId="p-1" projectName="Pond" />);
  return client;
}

describe("Roster", () => {
  it("inspects every effective mode in global and project scope without assignment controls", async () => {
    const client = await renderRoster();

    await waitFor(() => expect(client.globalRosterGet).toHaveBeenCalledWith("council"));
    expect(screen.getByTestId("roster-scope").textContent).toContain("Global");
    expect(screen.getByTestId("roster-scope").textContent).toContain("Project · Pond");

    // The flock is the available fleet, not just whoever happens to have a seat.
    const flock = screen.getByTestId("roster-flock");
    expect(flock.textContent).toContain("architect");
    expect(flock.textContent).toMatch(/local/i);
    expect(flock.textContent).toContain("critic-a");
    expect(flock.textContent).toMatch(/remote/i);
    // List price rides the card's tooltip (compact cards; the value column
    // shows whatever the sort is by).
    expect(screen.getByTestId("roster-flock-card-critic-a").getAttribute("title")).toContain("$3 / $15 per Mtok");

    for (const mode of ["council", "solo", "pair", "split", "tournament", "common"]) {
      expect(screen.getByTestId(`roster-board-${mode}`)).toBeTruthy();
    }
    // Each board shows only the roles its mode seats, named for real roles.
    const columns: Record<string, string[]> = {
      council: ["architect", "reviewer"],
      solo: ["implementer", "advisor"],
      pair: ["implementer", "advisor", "reviewer"],
      split: ["architect", "implementer", "reviewer"],
      tournament: ["implementer", "judge"],
      common: ["triager", "scribe"],
    };
    for (const [mode, roles] of Object.entries(columns)) {
      const board = screen.getByTestId(`roster-board-${mode}`);
      for (const role of roles) {
        expect(within(board).getByRole("heading", { name: role, level: 3 })).toBeTruthy();
      }
      expect(within(board).queryByRole("heading", { name: "scribe", level: 3 })).toBe(mode === "common" ? within(board).getByRole("heading", { name: "scribe", level: 3 }) : null);
    }

    // Repeated seats retain the resolver's ordered values, rather than being
    // sorted or collapsed by the board.
    const critics = screen.getByTestId("roster-column-council-reviewer");
    expect(Array.from(critics.querySelectorAll("[data-testid^='roster-card-council-reviewer-']")).map((n) => n.textContent))
      .toEqual(expect.arrayContaining([expect.stringContaining("critic-a"), expect.stringContaining("critic-b")]));
    expect(screen.getByTestId("roster-card-council-reviewer-critic-a").compareDocumentPosition(
      screen.getByTestId("roster-card-council-reviewer-critic-b"),
    ) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /Project · Pond/i }));
    await waitFor(() => expect(client.rosterGet).toHaveBeenCalledWith("p-1", "pair"));

    const ghost = screen.getByTestId("roster-card-council-reviewer-critic-a");
    expect(ghost.dataset.ghost).toBe("true");
    expect(ghost.textContent).toContain("global");

    const pinned = screen.getByTestId("roster-card-pair-implementer-project-builder");
    expect(pinned.dataset.ghost).not.toBe("true");
    expect(pinned.textContent).toContain("pinned");
    expect(pinned.title).toContain("builder");
    expect(screen.getByTestId("roster-board-common").textContent).toMatch(/no pins/i);

    expect(screen.queryByRole("button", { name: /assign|pin|save/i })).toBeNull();
  });
});
