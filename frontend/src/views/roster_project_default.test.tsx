import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Roster } from "./Roster";

// B-557: one duckling for every role the project does not pin, set and
// cleared from the project's Flock board.
describe("Flock project default", () => {
  const ducks = ["terra", "glm"].map((id) => ({ id, provider: "cloud", model: id }));
  const make = (initial: string) => {
    let current = initial;
    const set = vi.fn((_p: string, _m: string, _r: string, ids: string[]) => { current = ids[0] ?? ""; return Promise.resolve({}); });
    const unpin = vi.fn(() => { current = ""; return Promise.resolve({}); });
    const client = {
      ducklings: vi.fn(() => Promise.resolve(ducks)),
      Scorecards: vi.fn(() => Promise.resolve([])),
      globalRosterGet: vi.fn(() => Promise.resolve({ entries: [] })),
      rosterGet: vi.fn(() => Promise.resolve({ entries: [], project_default: current || undefined })),
      GlobalRosterSet: vi.fn(() => Promise.resolve({})),
      RosterSetManyMode: set,
      RosterUnpin: unpin,
    };
    return { client, set, unpin };
  };

  it("shows the current default and sets a new one for every mode", async () => {
    const { client, set } = make("glm");
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    fireEvent.click(screen.getByText("Project · Pond"));
    const select = await screen.findByTestId("roster-project-default-select");
    await waitFor(() => expect((select as HTMLSelectElement).value).toBe("glm"));
    fireEvent.change(select, { target: { value: "terra" } });
    await waitFor(() => expect(set).toHaveBeenCalledWith("pond", "", "default", ["terra"]));
    await waitFor(() => expect((screen.getByTestId("roster-project-default-select") as HTMLSelectElement).value).toBe("terra"));
  });

  it("clears it with none", async () => {
    const { client, unpin } = make("terra");
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    fireEvent.click(screen.getByText("Project · Pond"));
    const select = await screen.findByTestId("roster-project-default-select");
    await waitFor(() => expect((select as HTMLSelectElement).value).toBe("terra"));
    fireEvent.change(select, { target: { value: "" } });
    await waitFor(() => expect(unpin).toHaveBeenCalledWith("pond", "", "default"));
  });

  it("labels the seats the default fills, apart from pins and inherited seats", async () => {
    const { client } = make("terra");
    client.rosterGet = vi.fn((_: string, mode: string) => Promise.resolve({
      project_default: "terra",
      entries: mode === "pair" ? [
        { role: "implementer", duckling: "terra", ducklings: ["terra"], source: "project default" },
        { role: "reviewer", duckling: "glm", ducklings: ["glm"], source: "project pin" },
      ] : [],
    })) as never;
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    fireEvent.click(screen.getByText("Project · Pond"));
    const fromDefault = await screen.findByTestId("roster-seat-source-pair-implementer-terra");
    expect(fromDefault.textContent).toBe("project default");
    expect(screen.getByTestId("roster-card-pair-implementer-terra").dataset.ghost).toBeUndefined();
    expect(screen.getByTestId("roster-seat-source-pair-reviewer-glm").textContent).toBe("project pin");
    // A default seat is changed through the select, not removed card by card.
    expect(screen.queryByRole("button", { name: "remove terra from implementer" })).toBeNull();
    expect(screen.getByRole("button", { name: "remove glm from reviewer" })).toBeTruthy();
  });

  it("shows a default that names an unregistered duckling instead of hiding it", async () => {
    const { client } = make("gone");
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    fireEvent.click(screen.getByText("Project · Pond"));
    const select = await screen.findByTestId("roster-project-default-select");
    await waitFor(() => expect((select as HTMLSelectElement).value).toBe("gone"));
    expect(select.textContent).toContain("gone (not registered — ignored)");
  });

  it("is not offered on the global board", async () => {
    const { client } = make("");
    render(<Roster client={client as never} projectId="pond" projectName="Pond" />);
    await waitFor(() => expect(client.globalRosterGet).toHaveBeenCalled());
    expect(screen.queryByTestId("roster-project-default")).toBeNull();
  });
});
