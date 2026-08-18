import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const rosterModule = "./Roster";
const loadRoster = () => import(/* @vite-ignore */ rosterModule);

const ducks = [{ id: "terra", provider: "cloud", model: "t" }, { id: "luna", provider: "cloud", model: "l" }, { id: "k3", provider: "cloud", model: "k" }];
const scorecards = [
  { id: "terra", provider: "cloud", model: "t", locality: "remote", measured: { runs: 76, pass_rate: 51 }, measured_by_role: { implementer: { runs: 40, pass_rate: 55 } }, index: { coding_score: 76.7 } },
  { id: "luna", provider: "cloud", model: "l", locality: "remote", measured: { runs: 547, pass_rate: 64 }, measured_by_role: { implementer: { runs: 500, pass_rate: 66 } }, index: { coding_score: 71.4 } },
  { id: "k3", provider: "cloud", model: "k", locality: "remote" },
];
const seats: Record<string, unknown[]> = {
  solo: [
    { role: "implementer", ducklings: ["terra"], source: "global mode seat", candidates: [{ id: "luna", why: "coding 71.4 · 66% over 500 runs as implementer" }, { id: "terra", why: "coding 76.7" }] },
    { role: "advisor", ducklings: [], source: "unseated", candidates: [{ id: "k3", why: "$0.10/run" }] },
  ],
  pair: [{ role: "implementer", ducklings: ["luna"], source: "global mode seat", candidates: [{ id: "luna", why: "coding 71.4" }] }, { role: "advisor", ducklings: [] }, { role: "reviewer", ducklings: [] }],
};

async function renderRoster() {
  const { Roster } = await loadRoster();
  const set = vi.fn(() => Promise.resolve({}));
  const client = {
    ducklings: vi.fn(() => Promise.resolve(ducks)),
    Scorecards: vi.fn(() => Promise.resolve(scorecards)),
    globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: seats[mode] ?? [], warning: mode === "pair" ? "not runnable yet: pair requires a reviewer" : "" })),
    rosterGet: vi.fn((_: string, mode: string) => Promise.resolve({ entries: seats[mode] ?? [] })),
    GlobalRosterSet: set, RosterSetManyMode: vi.fn(() => Promise.resolve({})),
  };
  render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
  await waitFor(() => expect(client.Scorecards).toHaveBeenCalled());
  await screen.findByTestId("roster-card-solo-implementer-terra");
  return { client, set };
}

// The board is a decision surface: a seated duckling shows its evidence IN
// THAT SEAT, a seat whose top suggestion is not seated says so and offers it,
// and a seated top suggestion is marked. Occupied seats get one small "+";
// only empty seats get the dashed drop zone. The mode bar's colour means
// runnable / not.
describe("Roster board as a decision surface", () => {
  it("shows in-seat evidence, the seat's suggestion, and one control per seat state", async () => {
    const { set } = await renderRoster();
    // In-seat evidence, in the one grammar the Flock uses; the column header
    // already says the seat, so the line does not repeat it.
    expect(screen.getByTestId("roster-seat-evidence-solo-implementer-terra").textContent).toBe("55% · 40 runs · coding 76.7");
    // terra is seated but luna is the top suggestion: the seat says so and one
    // click assigns her.
    const hint = screen.getByTestId("roster-seat-suggestion-solo-implementer");
    expect(hint.textContent).toContain("suggested: luna");
    expect(screen.queryByTestId("roster-seat-top-solo-implementer-terra")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /assign suggested luna to implementer in solo/i }));
    await waitFor(() => expect(set).toHaveBeenCalledWith("solo", "implementer", ["luna"]));
    // On pair, luna IS the top suggestion and is seated: marked, no hint.
    expect(screen.getByTestId("roster-seat-top-pair-implementer-luna").textContent).toContain("suggested");
    expect(screen.queryByTestId("roster-seat-suggestion-pair-implementer")).toBeNull();

    // Controls: the occupied seat has the small "+", the empty one the drop
    // zone; both answer to the same testid so keyboard and click paths hold.
    expect(screen.getByTestId("roster-drop-solo-implementer").textContent).toBe("+");
    expect(screen.getByTestId("roster-drop-solo-advisor").textContent).toContain("drop here");
    // The remove control is a quiet ×, still reachable by name.
    expect(screen.getByRole("button", { name: /remove terra from implementer/i }).textContent).toBe("×");

    // Colour with meaning: solo runs (green), pair does not (amber), common
    // has no bar at all.
    expect(screen.getByTestId("roster-board-solo").getAttribute("data-runnable")).toBe("true");
    expect(screen.getByTestId("roster-board-pair").getAttribute("data-runnable")).toBe("false");
    expect(screen.getByTestId("roster-warning-pair").textContent).toContain("not runnable yet");
    expect(screen.getByTestId("roster-board-common").getAttribute("data-runnable")).toBeNull();
    expect(screen.getByTestId("roster-board-common").textContent).toContain("shared by every mode");
    // Every mode says what it is for, one line, beside its title.
    expect(screen.getByTestId("roster-blurb-pair").textContent).toContain("driver and navigator");
  });
});

// Dragging shows a small name chip at the cursor (the browser's whole-card
// ghost landed far from the pointer on WebKitGTK and looked like the drop was
// going elsewhere), and the column under the pointer says it is the target.
describe("Roster drag feedback", () => {
  it("uses a name chip as the drag image and highlights the hovered column", async () => {
    await renderRoster();
    const setDragImage = vi.fn();
    const card = screen.getByTestId("roster-flock-card-k3");
    fireEvent.dragStart(card, { dataTransfer: { setData: vi.fn(), setDragImage, effectAllowed: "", dropEffect: "" } });
    expect(setDragImage).toHaveBeenCalledTimes(1);
    const ghost = setDragImage.mock.calls[0]![0] as HTMLElement;
    expect(ghost.textContent).toBe("k3");
    expect(setDragImage.mock.calls[0]![1]).toBe(12); // pointer at the chip's left edge
    const column = screen.getByTestId("roster-column-solo-advisor");
    fireEvent.dragOver(column, { dataTransfer: { dropEffect: "" } });
    await waitFor(() => expect(column.getAttribute("data-dragover")).toBe("true"));
    fireEvent.dragLeave(column, { relatedTarget: document.body });
    await waitFor(() => expect(column.getAttribute("data-dragover")).toBeNull());
  });
});
