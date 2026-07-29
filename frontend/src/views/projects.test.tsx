import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Projects } from "./Projects";
import type { EngineClient, Project } from "../api/client";

const p = (o: Partial<Project> & { id: string }): Project => ({
  path: `/repos/${o.id}`, name: o.id, ...o,
});

function clientWith(initial: Project[]) {
  const state = [...initial];
  return {
    projects: vi.fn(() => Promise.resolve([...state])),
    projectInit: vi.fn((path: string, name: string) => {
      const created = p({ id: name || "new", path, name });
      state.push(created);
      return Promise.resolve(created);
    }),
    projectUpdate: vi.fn((id: string, keys: Record<string, string>) => {
      const found = state.find((x) => x.id === id)!;
      found.name = keys.name ?? found.name;
      return Promise.resolve(found);
    }),
    projectForget: vi.fn((id: string) => {
      state.splice(state.findIndex((x) => x.id === id), 1);
      return Promise.resolve({});
    }),
  } as unknown as EngineClient;
}

const noop = () => {};

describe("Projects", () => {
  it("creates a project and selects it", async () => {
    const client = clientWith([]);
    const onSelect = vi.fn();
    render(<Projects client={client} selected="" onSelect={onSelect} onChanged={noop} />);

    fireEvent.change(screen.getByTestId("project-path"), { target: { value: "/repos/thing" } });
    fireEvent.change(screen.getByTestId("project-name"), { target: { value: "thing" } });
    fireEvent.click(screen.getByTestId("project-create"));

    await waitFor(() => expect(client.projectInit).toHaveBeenCalledWith("/repos/thing", "thing", true));
    // Selecting it is the point: creating a project and then having to find it
    // in a dropdown is the friction this view exists to remove.
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith("thing"));
  });

  it("will not create without a path", async () => {
    const client = clientWith([]);
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    expect(screen.getByTestId("project-create").hasAttribute("disabled")).toBe(true);
  });

  it("shows the engine's refusal rather than failing silently", async () => {
    const client = clientWith([]);
    (client.projectInit as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("not a git repository"),
    );
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    fireEvent.change(screen.getByTestId("project-path"), { target: { value: "/repos/x" } });
    fireEvent.click(screen.getByTestId("project-create"));
    expect((await screen.findByTestId("projects-error")).textContent).toContain("not a git repository");
  });

  it("renames in place", async () => {
    const client = clientWith([p({ id: "alpha" })]);
    render(<Projects client={client} selected="alpha" onSelect={noop} onChanged={noop} />);
    fireEvent.click(await screen.findByTestId("project-rename-alpha"));
    fireEvent.change(screen.getByTestId("rename-input"), { target: { value: "Alpha Renamed" } });
    fireEvent.click(screen.getByTestId("rename-save"));
    await waitFor(() =>
      expect(client.projectUpdate).toHaveBeenCalledWith("alpha", { name: "Alpha Renamed" }),
    );
  });

  describe("forget", () => {
    let confirmSpy: ReturnType<typeof vi.spyOn>;
    beforeEach(() => {
      confirmSpy = vi.spyOn(window, "confirm");
    });
    afterEach(() => confirmSpy.mockRestore());

    // A tool that can erase a person's work from a list view is one nobody
    // should point at a real repository.
    it("says the files are not touched, and asks first", async () => {
      confirmSpy.mockReturnValue(true);
      const client = clientWith([p({ id: "alpha" })]);
      render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
      fireEvent.click(await screen.findByTestId("project-forget-alpha"));

      const asked = confirmSpy.mock.calls[0]![0] as string;
      expect(asked).toContain("not touched");
      expect(asked).not.toMatch(/delete/i);
      await waitFor(() => expect(client.projectForget).toHaveBeenCalledWith("alpha"));
    });

    it("does nothing when the question is declined", async () => {
      confirmSpy.mockReturnValue(false);
      const client = clientWith([p({ id: "alpha" })]);
      render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
      fireEvent.click(await screen.findByTestId("project-forget-alpha"));
      expect(client.projectForget).not.toHaveBeenCalled();
    });
  });

  // A missing folder is the reason every other view looks empty.
  it("marks a project whose folder is gone", async () => {
    const client = clientWith([p({ id: "ghost", missing: true })]);
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    expect((await screen.findByTestId("project-row-ghost")).textContent).toContain("folder is gone");
  });

  // No native chooser outside the desktop, so the button must not be there
  // promising something that cannot happen.
  it("hides the folder chooser when there is no native binding", async () => {
    const client = clientWith([]);
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    expect(screen.queryByTestId("project-browse")).toBeNull();
  });
});
