import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Skills } from "./Skills";
import type { EngineClient, SkillSummary } from "../api/client";

// The skills loop's first desktop surface (spec 08 §4.9): the engine, CLI and
// tool belt carried skills for a whole phase while the desktop showed nothing.

function client(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    // {items} is the engine's real envelope (handleSkillList) — the first
    // version of this mock mirrored the client's wrong assumption ({skills})
    // and the view shipped reading a field that never existed.
    skills: vi.fn(() =>
      Promise.resolve({
        items: [
          {
            name: "survey-map",
            description: "Read before surveying the code: module map",
            scope: "project",
            runnable: false,
          },
          {
            name: "pdf-extract",
            description: "Use when a PDF needs its text pulled out",
            scope: "global",
            runnable: true,
            args: [{ name: "file", type: "string", required: true }],
          },
          {
            name: "duck-authored",
            description: "Written by a duckling mid-run, awaiting the gate",
            scope: "project",
            runnable: true,
            pending: true,
          },
          {
            name: "broken",
            description: "short",
            scope: "project",
            runnable: false,
            problems: ["description: must say WHEN to use the skill"],
          },
        ] as SkillSummary[],
      }),
    ),
    skillGet: vi.fn((_p: string, name: string) =>
      Promise.resolve({
        name,
        body: "## Survey map\n\nRoutes live in api/src/routes.",
        raw: "---\nname: x\n---\n\n## Survey map\n\nRoutes live in api/src/routes.",
        dir: `/home/u/p/.ducklab/skills/${name}`,
        entry: name === "pdf-extract" ? "run.sh" : "",
        args: name === "pdf-extract" ? [{ name: "file", type: "string", required: true }] : [],
      }),
    ),
    skillNew: vi.fn(() => Promise.resolve({ name: "n" })),
    skillSave: vi.fn(() => Promise.resolve({ problems: [] })),
    skillDelete: vi.fn(() => Promise.resolve({ deleted: true })),
    skillRun: vi.fn(() => Promise.resolve({ output: "extracted 3 pages", failed: false })),
    ...overrides,
  } as unknown as EngineClient;
}

describe("Skills view", () => {
  it("lists skills with scope badges, greys the pending one, and shows problems", async () => {
    render(<Skills client={client()} projectId="p" />);
    const rows = await screen.findAllByTestId("skill-row");
    expect(rows).toHaveLength(4);
    expect(rows[0]!.textContent).toContain("project");
    expect(rows[1]!.textContent).toContain("global");
    expect(screen.getByTestId("skill-pending").textContent).toContain("pending acceptance");
    expect(screen.getByTestId("skill-problems").textContent).toContain("WHEN to use");
  });

  it("opens a skill to read its body — documentation is the default form", async () => {
    render(<Skills client={client()} projectId="p" />);
    fireEvent.click(await screen.findByTestId("skill-open-survey-map"));
    const detail = await screen.findByTestId("skill-detail");
    expect(detail.textContent).toContain("Routes live in api/src/routes.");
    expect(detail.querySelector('[data-testid="skill-run"]')).toBeNull();
  });

  it("runs a runnable skill with its args and shows exit code and output", async () => {
    const c = client();
    render(<Skills client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("skill-open-pdf-extract"));
    await screen.findByTestId("skill-detail");
    fireEvent.change(screen.getByPlaceholderText("file (required)"), { target: { value: "a.pdf" } });
    fireEvent.click(screen.getByTestId("skill-run"));
    await waitFor(() => expect(c.skillRun).toHaveBeenCalledWith("p", "pdf-extract", { file: "a.pdf" }));
    expect((await screen.findByTestId("skill-run-output")).textContent).toContain("✓ ran");
    expect(screen.getByTestId("skill-run-output").textContent).toContain("extracted 3 pages");
  });

  it("scaffolds a new skill and reloads the list", async () => {
    const c = client();
    render(<Skills client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("skill-new-toggle"));
    fireEvent.change(screen.getByTestId("skill-new-name"), { target: { value: "my-guide" } });
    fireEvent.click(screen.getByTestId("skill-new-create"));
    await waitFor(() => expect(c.skillNew).toHaveBeenCalledWith("p", "my-guide", false));
    expect(c.skills).toHaveBeenCalledTimes(2);
  });

  it("says what a good first skill is when there are none", async () => {
    render(<Skills client={client({ skills: vi.fn(() => Promise.resolve({ items: [] })) })} projectId="p" />);
    expect((await screen.findByTestId("skills-empty")).textContent).toContain("survey map");
  });

  it("edits the whole SKILL.md and reports the saved text's problems", async () => {
    const c = client({ skillSave: vi.fn(() => Promise.resolve({ problems: ["description: must say WHEN"] })) });
    render(<Skills client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("skill-open-survey-map"));
    await screen.findByTestId("skill-detail");
    expect(screen.getByTestId("skill-dir").textContent).toContain(".ducklab/skills/survey-map");
    fireEvent.click(screen.getByTestId("skill-edit-open"));
    const editor = screen.getByTestId("skill-edit") as HTMLTextAreaElement;
    expect(editor.value).toContain("---"); // the WHOLE file, frontmatter included
    fireEvent.change(editor, { target: { value: "---\nname: survey-map\ndescription: short\n---\nbody" } });
    fireEvent.click(screen.getByTestId("skill-save"));
    await waitFor(() =>
      expect(c.skillSave).toHaveBeenCalledWith("p", "survey-map", "---\nname: survey-map\ndescription: short\n---\nbody"),
    );
    expect((await screen.findByTestId("skill-save-problems")).textContent).toContain("WHEN");
  });

  it("deletes only after an explicit second click", async () => {
    const c = client();
    render(<Skills client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("skill-open-survey-map"));
    await screen.findByTestId("skill-detail");
    fireEvent.click(screen.getByTestId("skill-delete"));
    expect(c.skillDelete).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("skill-delete-confirm"));
    await waitFor(() => expect(c.skillDelete).toHaveBeenCalledWith("p", "survey-map"));
  });
});
