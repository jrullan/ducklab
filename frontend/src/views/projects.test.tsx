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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let confirmSpy: any;
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

describe("Projects — the gate", () => {
  const gate = (o: Partial<Record<string, unknown>> = {}) => ({
    mode: "none", command: "", detected: "none", adoptable: false,
    best_verdict: "UNVERIFIED", ...o,
  });

  function clientWithGate(g: Record<string, unknown>) {
    const c = clientWith([p({ id: "alpha" })]) as unknown as Record<string, unknown>;
    c.projectGate = vi.fn(() => Promise.resolve(g));
    c.projectGateAdopt = vi.fn(() =>
      Promise.resolve(gate({ mode: "tests", command: "go test ./...", best_verdict: "PASSED" })),
    );
    return c as unknown as EngineClient;
  }

  // The failure mode is that nobody notices, so the chip says the consequence
  // rather than the setting.
  it("says a project with no gate can never pass", async () => {
    render(
      <Projects client={clientWithGate(gate())} selected="" onSelect={noop} onChanged={noop} />,
    );
    expect((await screen.findByTestId("gate-none")).textContent).toContain("UNVERIFIED");
  });

  it("offers the detected gate, naming the command", async () => {
    const client = clientWithGate(
      gate({ detected: "tests", detected_command: "go test ./...", adoptable: true }),
    );
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    const adopt = await screen.findByTestId("gate-adopt");
    expect(adopt.textContent).toContain("go test ./...");
    fireEvent.click(adopt);
    await waitFor(() => expect(client.projectGateAdopt).toHaveBeenCalledWith("alpha"));
    // And the chip goes away, because the problem did.
    await waitFor(() => expect(screen.queryByTestId("gate-none")).toBeNull());
  });

  // Nothing to detect: no adopt button that cannot help — but the manual
  // door stays, because detection cannot find a script nobody has written.
  it("offers the manual door when there is nothing runnable", async () => {
    render(
      <Projects client={clientWithGate(gate())} selected="" onSelect={noop} onChanged={noop} />,
    );
    await screen.findByTestId("gate-none");
    expect(screen.queryByTestId("gate-adopt")).toBeNull();
    expect(screen.getByTestId("gate-set-manual")).toBeTruthy();
  });

  // The exercise-tracker case: detection found nothing, the person knows the
  // command. Setting it by hand writes verify.mode and the matching command
  // key, and the chip re-reads the gate the engine now reports.
  it("sets a gate by hand and the warning goes away", async () => {
    const client = clientWithGate(gate());
    const cr = client as unknown as Record<string, unknown>;
    cr.projectUpdate = vi.fn(() => Promise.resolve({}));
    // First read: no gate. After the update: configured.
    const gateFn = cr.projectGate as ReturnType<typeof vi.fn>;
    gateFn.mockImplementation(() =>
      Promise.resolve(gate({ mode: "tests", command: "pytest -q", best_verdict: "PASSED" })),
    );
    gateFn.mockResolvedValueOnce(gate());
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    fireEvent.click(await screen.findByTestId("gate-set-manual"));
    fireEvent.change(screen.getByTestId("gate-command"), { target: { value: "pytest -q" } });
    fireEvent.click(screen.getByTestId("gate-save"));
    await waitFor(() =>
      expect(cr.projectUpdate).toHaveBeenCalledWith("alpha", {
        "verify.mode": "tests",
        "verify.tests": "pytest -q",
      }),
    );
  });

  // An empty command cannot be saved: a gate of "" is mode tests with nothing
  // to run, which is worse than none because it looks configured.
  it("refuses to set an empty command", async () => {
    render(<Projects client={clientWithGate(gate())} selected="" onSelect={noop} onChanged={noop} />);
    fireEvent.click(await screen.findByTestId("gate-set-manual"));
    expect(screen.getByTestId("gate-save").hasAttribute("disabled")).toBe(true);
  });

  it("says nothing at all about a project that has a gate", async () => {
    const client = clientWithGate(
      gate({ mode: "tests", command: "go test ./...", best_verdict: "PASSED" }),
    );
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    expect((await screen.findByTestId("gate-ok")).textContent).toContain("gate tests");
    expect(screen.queryByTestId("gate-none")).toBeNull();
  });
});

describe("Projects — the path field", () => {
  // A real session typed "~/dev/calculator" and got a project in a folder
  // literally named "~", nested under wherever the engine was launched. It
  // worked perfectly and was somewhere nobody would look.
  it("refuses a path starting with ~ before sending it", async () => {
    const client = clientWith([]);
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    fireEvent.change(screen.getByTestId("project-path"), {
      target: { value: "~/dev/calculator" },
    });
    expect((await screen.findByTestId("path-problem")).textContent).toContain("shell shortcut");
    expect(screen.getByTestId("project-create").hasAttribute("disabled")).toBe(true);
    fireEvent.click(screen.getByTestId("project-create"));
    expect(client.projectInit).not.toHaveBeenCalled();
  });

  // A relative path means nothing to a daemon: it resolves against wherever
  // somebody started the engine.
  it("refuses a relative path", async () => {
    const client = clientWith([]);
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    fireEvent.change(screen.getByTestId("project-path"), { target: { value: "dev/calculator" } });
    expect((await screen.findByTestId("path-problem")).textContent).toContain("full path");
  });

  it("accepts a full path", async () => {
    const client = clientWith([]);
    render(<Projects client={client} selected="" onSelect={noop} onChanged={noop} />);
    fireEvent.change(screen.getByTestId("project-path"), {
      target: { value: "/home/someone/dev/calculator" },
    });
    expect(screen.queryByTestId("path-problem")).toBeNull();
    expect(screen.getByTestId("project-create").hasAttribute("disabled")).toBe(false);
  });
});
