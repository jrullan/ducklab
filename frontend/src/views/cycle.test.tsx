import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Cycle } from "./Cycle";
import { EngineClient } from "../api/client";

/** Shapes copied from a live engine response, not invented: sections is null
 * rather than [] on an empty project, and trace errors carry kind/id/detail. */
const REQUIREMENTS = {
  kind: "requirements",
  version: 1,
  approved: true,
  markdown: "---\nkind: requirements\n---\n",
  sections: [
    { id: "REQ-001", title: "Mobile-first timesheet", body: "A responsive app.", fields: {} },
    { id: "REQ-002", title: "Invoice export", body: "Export to CSV.", fields: {} },
  ],
};

const TRACE = {
  errors: [{ kind: "orphan_requirement", id: "REQ-002", detail: "no spec section implements this requirement" }],
};

function clientWith(handler: (path: string, init?: RequestInit) => Response) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string, init?: RequestInit) =>
      handler(String(url).replace("http://engine", ""), init)) as unknown as typeof fetch,
  });
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("Cycle", () => {
  it("lists sections and marks the ones the spine reports broken", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json(TRACE);
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    await waitFor(() => expect(screen.getAllByTestId("cycle-section")).toHaveLength(2));
    const cards = screen.getAllByTestId("cycle-section") as HTMLElement[];
    // The orphan is flagged and the covered requirement is not: a rail that
    // marked everything, or nothing, would carry no information.
    expect(cards[0]!.dataset.broken).toBe("false");
    expect(cards[1]!.dataset.broken).toBe("true");
    expect(screen.getByTestId("trace-rail").textContent).toContain("1 break");
  });

  it("says the spine is clean only when it is", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("trace-clean")).toBeTruthy());
  });

  it("promotes a proposal through the artifact endpoint, not a stage run", async () => {
    const calls: string[] = [];
    let promoted = false;
    const client = clientWith((p, init) => {
      calls.push(`${init?.method ?? "GET"} ${p}`);
      if (p.endsWith("/promote")) {
        promoted = true;
        return json(REQUIREMENTS);
      }
      if (p.includes("/artifacts/")) {
        return json(
          promoted
            ? REQUIREMENTS
            : { ...REQUIREMENTS, proposal: { diff: "--- a\n+++ b\n@@ -1 +1 @@\n+new", ducklings: ["pato-atom"] } },
        );
      }
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    await waitFor(() => expect(screen.getByTestId("cycle-proposal")).toBeTruthy());
    fireEvent.click(screen.getByTestId("cycle-accept"));
    await waitFor(() => expect(screen.queryByTestId("cycle-proposal")).toBeNull());

    // Accepting must never start a run — that was a real CLI bug, where the
    // accept path re-ran the whole stage against live models.
    expect(calls.some((c) => c.includes("/stages/"))).toBe(false);
    expect(calls).toContain("POST /v1/projects/p/artifacts/requirements/promote");
  });

  it("shows a failure as a failure, never as an empty artifact", async () => {
    const client = clientWith(() => json({ error: { message: "engine exploded" } }, 500));
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-error").textContent).toContain("engine exploded"));
    expect(screen.queryByTestId("cycle-section")).toBeNull();
  });

  it("switches stage when a tab is clicked", async () => {
    const asked: string[] = [];
    const client = clientWith((p) => {
      if (p.includes("/artifacts/")) {
        asked.push(p);
        return json({ ...REQUIREMENTS, sections: null });
      }
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(asked.length).toBeGreaterThan(0));
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    await waitFor(() => expect(asked.some((p) => p.includes("/artifacts/plan"))).toBe(true));
  });
});

const PLAN = {
  kind: "plan",
  version: 1,
  approved: true,
  markdown: "",
  sections: [
    {
      id: "M-001",
      title: "Foundation",
      body: "",
      children: [
        { id: "T-001", title: "Authentication", body: "", implements: ["SPEC-007"] },
        { id: "T-002", title: "Orphan work", body: "" },
      ],
    },
  ],
};

describe("Cycle, plan tab", () => {
  const client = () =>
    clientWith((p) => {
      if (p.includes("/artifacts/plan")) return json(PLAN);
      if (p.includes("/artifacts/")) return json({ ...REQUIREMENTS, sections: null });
      if (p.includes("/trace/check"))
        return json({ errors: [{ kind: "unjustified_task", id: "T-002", detail: "task implements no spec section" }] });
      return json({}, 404);
    });

  // A task's Implements line is the edge that makes the plan traceable. Showing
  // only id and title made the tab look like the plan referenced nothing.
  it("shows what each task implements", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    await waitFor(() => expect(screen.getAllByTestId("cycle-child")).toHaveLength(2));
    const t1 = screen.getAllByTestId("cycle-child").find((el) => el.dataset.id === "T-001")!;
    expect(t1.textContent).toContain("SPEC-007");
  });

  // The plan's prefix is M but its breaks land on tasks, so a prefix test never
  // matched and the one tab that could show the problem never did.
  it("marks a task the spine flagged, not just top-level sections", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    await waitFor(() => expect(screen.getAllByTestId("cycle-child")).toHaveLength(2));
    const kids = screen.getAllByTestId("cycle-child");
    expect(kids.find((el) => el.dataset.id === "T-002")!.dataset.broken).toBe("true");
    expect(kids.find((el) => el.dataset.id === "T-001")!.dataset.broken).toBe("false");
  });
});

describe("Cycle — starting a stage", () => {
  const client = (over: Record<string, unknown> = {}) =>
    ({
      artifact: vi.fn(() => Promise.resolve({ kind: "requirements", body: "", sections: [] })),
      traceCheck: vi.fn(() => Promise.resolve([])),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-42" })),
      promote: vi.fn(() => Promise.resolve({})),
      ...over,
    }) as unknown as EngineClient;

  // The view could accept a proposal but not produce one, so the first thing
  // anyone wants to do had to be done from a terminal.
  it("starts intake with a pasted brief", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-start");

    fireEvent.change(screen.getByTestId("cycle-brief"), {
      target: { value: "A tool that tracks bird sightings." },
    });
    fireEvent.click(screen.getByTestId("cycle-run"));

    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", {
        from: "A tool that tracks bird sightings.",
      }),
    );
    // The run is where the work is visible, so it is offered — not jumped to,
    // which would lose the place of someone re-reading what they wrote.
    expect((await screen.findByTestId("cycle-run-link")).getAttribute("href")).toBe("#/runs/r-42");
  });

  it("starts intake with no brief at all", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("cycle-run"));
    await waitFor(() => expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { from: "" }));
  });

  // Spec and plan read what came before; there is nothing to paste.
  it("offers no brief on spec or plan", async () => {
    render(<Cycle client={client()} projectId="p" stage="spec" />);
    await screen.findByTestId("cycle-start");
    expect(screen.queryByTestId("cycle-brief")).toBeNull();
  });

  it("shows the engine's refusal rather than failing silently", async () => {
    const c = client({
      stageStart: vi.fn(() => Promise.reject(new Error("requirements are not accepted yet"))),
    });
    render(<Cycle client={c} projectId="p" stage="spec" />);
    fireEvent.click(await screen.findByTestId("cycle-run"));
    expect((await screen.findByTestId("cycle-error")).textContent).toContain("not accepted yet");
  });

  // A proposal is already waiting for a decision; offering to make another
  // would bury the one that needs answering.
  it("hides the start control while a proposal is pending", async () => {
    const c = client({
      artifact: vi.fn(() =>
        Promise.resolve({
          kind: "requirements",
          body: "",
          sections: [],
          proposal: { diff: "diff --git a/x b/x\n" },
        }),
      ),
    });
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-proposal");
    expect(screen.queryByTestId("cycle-start")).toBeNull();
  });

  // Redrafting an accepted document must not read as overwriting it.
  it("says a redraft leaves the accepted document alone", async () => {
    const c = client({
      artifact: vi.fn(() =>
        Promise.resolve({
          kind: "requirements",
          body: "",
          sections: [{ id: "REQ-001", title: "A thing", body: "" }],
        }),
      ),
    });
    render(<Cycle client={c} projectId="p" />);
    const start = await screen.findByTestId("cycle-start");
    expect(start.textContent).toContain("leaves the accepted document alone");
    expect(screen.getByTestId("cycle-run").textContent).toContain("Redraft");
  });
});
