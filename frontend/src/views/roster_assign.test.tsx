import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";

const rosterModule = "./Roster";
const loadRoster = () => import(/* @vite-ignore */ rosterModule);

type Seat = {
  role: string;
  ducklings: string[];
  source: "global mode seat" | "project pin" | "global role fallback";
  global_ducklings?: string[];
};

const ducklings = ["architect", "critic-a", "critic-b", "builder", "advisor", "reviewer", "worker-a", "worker-b", "contestant-a", "contestant-b", "judge", "triager", "scribe"];

function entries(pairOverlap = false): Record<string, Seat[]> {
  return {
    council: [
      { role: "architect", ducklings: ["architect"], source: "global mode seat" },
      { role: "reviewer", ducklings: ["critic-a"], source: "global mode seat" },
    ],
    solo: [
      { role: "implementer", ducklings: ["builder"], source: "global mode seat" },
      { role: "advisor", ducklings: ["advisor"], source: "global mode seat" },
    ],
    pair: [
      { role: "implementer", ducklings: ["builder"], source: "global mode seat" },
      { role: "advisor", ducklings: ["advisor"], source: "global mode seat" },
      { role: "reviewer", ducklings: [pairOverlap ? "builder" : "reviewer"], source: "global mode seat" },
    ],
    split: [
      { role: "architect", ducklings: ["architect"], source: "global mode seat" },
      { role: "implementer", ducklings: ["worker-a"], source: "global mode seat" },
      { role: "reviewer", ducklings: ["reviewer"], source: "global mode seat" },
    ],
    tournament: [
      { role: "implementer", ducklings: ["contestant-a"], source: "global mode seat" },
      { role: "judge", ducklings: ["judge"], source: "global mode seat" },
    ],
    common: [
      { role: "triager", ducklings: ["triager"], source: "global role fallback" },
      { role: "scribe", ducklings: ["scribe"], source: "global role fallback" },
    ],
  };
}

async function renderRoster(options: { rejectSet?: boolean; pairOverlap?: boolean; pinnedCouncilCritics?: boolean } = {}) {
  const { Roster } = await loadRoster();
  const global = entries(options.pairOverlap);
  const project = entries(options.pairOverlap);
  if (options.pinnedCouncilCritics) {
    project.council = (project.council ?? []).map((seat) =>
      seat.role === "reviewer" ? { ...seat, source: "project pin", global_ducklings: ["critic-a"] } : seat,
    );
    project.solo = (project.solo ?? []).map((seat) =>
      seat.role === "implementer" ? { ...seat, source: "project pin", global_ducklings: ["builder"] } : seat,
    );
  }
  project.pair = [
    { role: "implementer", ducklings: ["builder"], source: "project pin", global_ducklings: ["builder"] },
    { role: "advisor", ducklings: ["advisor"], source: "global mode seat" },
    { role: "reviewer", ducklings: [options.pairOverlap ? "builder" : "reviewer"], source: "global mode seat" },
  ];
  const set = options.rejectSet
    ? vi.fn(() => Promise.reject(new Error("field ducklings: split requires at least two workers; next: choose another worker")))
    : vi.fn(() => Promise.resolve({}));
  const client = {
    ducklings: vi.fn(() => Promise.resolve(ducklings.map((id) => ({ id, provider: "local", model: "qwen" })))),
    globalRosterGet: vi.fn((mode: string) => Promise.resolve({ entries: global[mode] })),
    rosterGet: vi.fn((_projectId: string, mode: string) => Promise.resolve({ entries: project[mode] })),
    // Unpinning is real on the fake engine too: the next re-read shows the
    // seat inherited from Global. The board must not guess the outcome.
    RosterSetManyMode: set,
    GlobalRosterSet: vi.fn(() => Promise.resolve({})),
    RosterUnpin: vi.fn((_projectId: string, mode: string, role: string) => {
      project[mode] = (project[mode] ?? []).map((seat) =>
        seat.role === role ? { ...seat, source: "global mode seat", ducklings: seat.global_ducklings ?? seat.ducklings, global_ducklings: undefined } : seat,
      );
      return Promise.resolve({});
    }),
  };
  render(<Roster client={client as never} projectId="p-1" projectName="Pond" />);
  await waitFor(() => expect(client.globalRosterGet).toHaveBeenCalledWith("pair"));
  return client;
}

async function projectScope() {
  fireEvent.click(screen.getByRole("button", { name: /Project · Pond/i }));
  await waitFor(() => expect(screen.getByTestId("roster-card-pair-implementer-builder").textContent).toContain("builder"));
}

function openSeat(mode: string, role: string) {
  fireEvent.click(screen.getByTestId(`roster-drop-${mode}-${role}`));
  return screen.getByTestId("duckling-picker-drawer");
}

function choose(id: string) {
  const catalog = screen.getByRole("heading", { name: "All candidates" }).parentElement as HTMLElement;
  fireEvent.click(within(catalog).getByRole("button", { name: new RegExp(id) }));
}

describe("Roster assignment", () => {
  it("assigns a duckling chosen for a project seat", async () => {
    const client = await renderRoster();
    await projectScope();
    const drawer = openSeat("council", "reviewer");
    choose("critic-b");
    fireEvent.click(within(drawer).getByRole("button", { name: /apply 2 ducklings/i }));
    await waitFor(() => expect(client.RosterSetManyMode).toHaveBeenCalledWith("p-1", "council", "reviewer", ["critic-a", "critic-b"]));
  });

  it("opens the comparison drawer by keyboard and applies a multi-seat selection", async () => {
    const client = await renderRoster();
    await projectScope();
    const seat = screen.getByTestId("roster-column-council-reviewer");
    seat.focus();
    fireEvent.keyDown(seat, { key: "Enter" });
    fireEvent.click(screen.getByRole("button", { name: "critic-b" }));
    fireEvent.click(screen.getByRole("button", { name: /apply 2 ducklings/i }));
    await waitFor(() => expect(client.RosterSetManyMode).toHaveBeenCalledWith("p-1", "council", "reviewer", ["critic-a", "critic-b"]));
  });

  // The SEAT decides append vs replace, never a drag flag (B-067): a pinned
  // multi-slot seat appends in displayed order; an inherited one starts the
  // pin fresh; a single-slot seat always replaces.
  it("stages additions to a pinned multi-slot seat in displayed order", async () => {
    const client = await renderRoster({ pinnedCouncilCritics: true });
    await projectScope();
    const drawer = openSeat("council", "reviewer");
    choose("critic-b");
    fireEvent.click(within(drawer).getByRole("button", { name: /apply 2 ducklings/i }));
    await waitFor(() => expect(client.RosterSetManyMode).toHaveBeenCalledWith("p-1", "council", "reviewer", ["critic-a", "critic-b"]));
  });

  it("replaces a single-slot seat even when it is already pinned", async () => {
    const client = await renderRoster({ pinnedCouncilCritics: true });
    await projectScope();
    const drawer = openSeat("solo", "implementer");
    choose("worker-b");
    fireEvent.click(within(drawer).getByRole("button", { name: /^apply$/i }));
    await waitFor(() => expect(client.RosterSetManyMode).toHaveBeenCalledWith("p-1", "solo", "implementer", ["worker-b"]));
  });

  it("removes an assigned card by writing the remaining ordered seat", async () => {
    const client = await renderRoster();
    await projectScope();
    fireEvent.click(screen.getByRole("button", { name: /remove critic-a from reviewer/i }));
    await waitFor(() => expect(client.RosterSetManyMode).toHaveBeenCalledWith("p-1", "council", "reviewer", []));
  });

  it("unpins a project seat and returns it to its inherited ghost", async () => {
    const client = await renderRoster();
    await projectScope();
    fireEvent.click(screen.getByRole("button", { name: /unpin builder from implementer/i }));
    await waitFor(() => expect(client.RosterUnpin).toHaveBeenCalledWith("p-1", "pair", "implementer"));
    await waitFor(() => expect(screen.getByTestId("roster-card-pair-implementer-builder").dataset.ghost).toBe("true"));
  });

  it("writes global edits only through the global roster API", async () => {
    const client = await renderRoster();
    const drawer = openSeat("council", "reviewer");
    choose("critic-b");
    fireEvent.click(within(drawer).getByRole("button", { name: /apply 2 ducklings/i }));
    await waitFor(() => expect(client.GlobalRosterSet).toHaveBeenCalledWith("council", "reviewer", ["critic-a", "critic-b"]));
    expect(client.RosterSetManyMode).not.toHaveBeenCalled();
  });

  it("renders engine validation errors beside the board", async () => {
    const client = await renderRoster({ rejectSet: true });
    await projectScope();
    const drawer = openSeat("split", "implementer");
    choose("worker-b");
    fireEvent.click(within(drawer).getByRole("button", { name: /apply 2 ducklings/i }));
    const err = await screen.findByRole("alert");
    expect(err.textContent).toMatch(/field ducklings: split requires at least two workers/i);
    expect(client.RosterSetManyMode).toHaveBeenCalled();
  });

  it("warns when pair assigns the same duckling to implementer and reviewer", async () => {
    await renderRoster({ pairOverlap: true });
    expect(await screen.findByText(/implementer.*reviewer.*same duckling|same duckling.*implementer.*reviewer/i)).toBeTruthy();
  });
});
