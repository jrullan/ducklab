import { describe, it, expect, vi } from "vitest";
import { act } from "@testing-library/react";
import { saveChipFacts } from "../lib/chipfacts";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Cycle, traceMarkers } from "./Cycle";
import { Ledger } from "./Ledger";
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
    fetchFn: (async (url: string, init?: RequestInit) => {
      const path = String(url).replace("http://engine", "");
      // The proposal card asks the engine for the proposing run's legal
      // actions. Answered here once so every test's gate renders its buttons;
      // a test about something else should not have to know the contract.
      if (/^\/v1\/runs\/[^/]+$/.test(path.split("?")[0] ?? "")) {
        return json({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        });
      }
      return handler(path, init);
    }) as unknown as typeof fetch,
  });
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("Cycle", () => {
  it("narrates intent and the three generated stages in house voice", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/")) return json({ ...REQUIREMENTS, sections: [] });
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    const narrative = await screen.findByTestId("cycle-stage-narrative");
    expect(narrative).toHaveTextContent("your briefs, preserved as the product evolves");
    expect(narrative).toHaveTextContent("the council refines intent into a contract");
    expect(narrative).toHaveTextContent("ducklings draft; you agree behavior");
    expect(narrative).toHaveTextContent("cut into tasks; you birth them");
  });

  it("shows Intent as its own immutable, addressable document", async () => {
    const intent = {
      kind: "intent", version: 3, approved: true, markdown: "",
      sections: [{ id: "INT-002", title: "Add measurements HUD", body: "**Run:** r-2\n**Outcome:** accepted\n**Requirements:** REQ-008\n\n### Original brief\n\nShow live measurements." }],
    };
    const client = clientWith((p) => {
      if (p.includes("/artifacts/intent")) return json(intent);
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/INT-002")) return json({ id: "INT-002", kind: "intent", title: "Add measurements HUD", down: ["REQ-008"] });
      if (p.includes("/trace/REQ-008")) return json({ id: "REQ-008", kind: "requirement", title: "Live measurements", up: ["INT-002"] });
      if (p.includes("/trace/check")) return json({ errors: [] });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" stage="intent" section="INT-002" />);

    expect(await screen.findByTestId("cycle-section")).toHaveTextContent("Show live measurements");
    expect(screen.getByTestId("cycle-index-row")).toHaveAttribute("aria-current", "true");
    expect(screen.getByTestId("cycle-inspector")).toHaveTextContent("Recorded intention");
    expect(screen.queryByTestId("cycle-propose-section")).toBeNull();
    expect(screen.queryByRole("group", { name: "section state filters" })).toBeNull();
  });

  it("adds an intention without exposing Intake or leaving the Intent document", async () => {
    const intent = {
      kind: "intent", version: 1, approved: true, markdown: "",
      sections: [{ id: "INT-001", title: "Initial product", body: "### Original brief\n\nBuild it." }],
    };
    const client = clientWith((p) => {
      if (p.includes("/artifacts/intent")) return json(intent);
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json({ errors: [] });
      return json({}, 404);
    });
    const stageStart = vi.spyOn(client, "stageStart").mockResolvedValue({
      id: "r-intent", project_id: "p", stage: "intake", mode: "council", task_id: "",
      status: "running", verdict: "", started_at: "2026-08-29T00:00:00Z",
    });
    render(<Cycle client={client} projectId="p" stage="intent" />);

    await screen.findByTestId("cycle-primary-action");
    fireEvent.click(screen.getByTestId("cycle-primary-action"));

    expect(screen.getByTestId("cycle-tab-intent")).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("heading", { name: "Add intention" })).toBeInTheDocument();
    expect(screen.getByText("Intent operation")).toBeInTheDocument();
    expect(screen.queryByText("Requirements operation")).toBeNull();

    fireEvent.change(screen.getByTestId("cycle-brief"), { target: { value: "Add offline mode." } });
    fireEvent.click(screen.getByTestId("cycle-run"));
    await waitFor(() => expect(stageStart).toHaveBeenCalledWith("p", "intake", expect.objectContaining({
      from: "Add offline mode.",
      adopt: false,
    })));
    expect(screen.getByTestId("cycle-tab-intent")).toHaveAttribute("aria-selected", "true");
  });

  it("presents Documents as a pipeline with contextual inspection and an operational drawer", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json(TRACE);
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    expect(await screen.findByRole("heading", { name: "Documents", level: 1 })).toBeInTheDocument();
    expect(screen.getByTestId("cycle-stage-control").querySelectorAll('[role="tab"]')).toHaveLength(4);
    const inspector = screen.getByTestId("cycle-inspector");
    expect(inspector).toHaveTextContent("No section selected");
    expect(screen.getByTestId("cycle-selection-empty")).toHaveTextContent("Select a requirement");
    expect(screen.queryByTestId("cycle-section")).not.toBeInTheDocument();
    fireEvent.click(screen.getAllByTestId("cycle-index-row")[1]!);
    expect(inspector).toHaveTextContent("REQ-002");
    expect(inspector).toHaveTextContent("no spec section implements this requirement");
    expect(screen.getAllByTestId("cycle-index-row")[1]).toHaveAttribute("aria-current", "true");
    expect(screen.getAllByTestId("cycle-section")).toHaveLength(1);
    expect(screen.getByTestId("cycle-section")).toHaveTextContent("REQ-002");
    expect(screen.getByTestId("cycle-section-details")).toHaveTextContent("Export to CSV");
    expect(screen.queryByText("More detail")).not.toBeInTheDocument();
    expect(screen.getByTestId("cycle-start")).not.toHaveAttribute("open");
    expect(screen.getByTestId("cycle-show-all")).toHaveTextContent("Clear selection");

    fireEvent.click(screen.getByTestId("cycle-show-all"));
    expect(screen.queryByTestId("cycle-section")).not.toBeInTheDocument();
    expect(screen.getByTestId("cycle-selection-empty")).toBeInTheDocument();
    expect(screen.getByTestId("cycle-start")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("cycle-primary-action"));
    expect(screen.getByTestId("cycle-start")).toHaveAttribute("open");
    expect(screen.getByRole("heading", { name: "Enter requirements" })).toBeInTheDocument();
    expect(screen.getByText(/approved requirements remain unchanged/i)).toBeInTheDocument();
  });

  it("lists sections and marks the ones the spine reports broken", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json(TRACE);
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    const rows = await waitFor(() => screen.getAllByTestId("cycle-index-row"));
    expect(rows[0]).not.toHaveTextContent("break");
    expect(rows[1]).toHaveTextContent("break");
    fireEvent.click(rows[1]!);
    expect(screen.getByTestId("cycle-section").dataset.broken).toBe("true");
    expect(screen.getByTestId("cycle-health").textContent).toContain("1 break");
  });

  it("turns a selected requirement into a navigable chain with contextual actions", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.endsWith("/trace/REQ-001")) return json({ id: "REQ-001", kind: "requirement", title: "Mobile-first timesheet", down: ["SPEC-001"] });
      if (p.endsWith("/trace/SPEC-001")) return json({ id: "SPEC-001", kind: "spec_section", title: "Responsive time entry", up: ["REQ-001"], down: ["T-001"] });
      if (p.endsWith("/trace/T-001")) return json({ id: "T-001", kind: "task", title: "Build time entry", up: ["SPEC-001"] });
      if (p.includes("/trace/check")) return json({ errors: null });
      if (p.endsWith("/ducklings")) return json({ items: [{ id: "k3", provider: "openrouter", model: "k3", caps: {} }] });
      if (p.includes("/roster")) return json({ entries: [{ role: "consultant", duckling: "k3" }] });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    const row = (await screen.findAllByTestId("cycle-index-row"))[0]!;
    fireEvent.click(row);
    expect(row.className).toContain("border-warning");
    await waitFor(() => expect(screen.getAllByTestId("cycle-chain-node")).toHaveLength(3));
    expect(screen.getByTestId("cycle-document-chain")).toHaveTextContent("SPEC-001");
    expect(screen.getByTestId("cycle-document-chain")).toHaveTextContent("T-001");
    expect(screen.queryByText("Review traceability issue")).not.toBeInTheDocument();
    expect(await screen.findByText("Chat about REQ-001")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("cycle-propose-section"));
    expect((screen.getByTestId("cycle-brief") as HTMLTextAreaElement).value).toContain("REQ-001 — Mobile-first timesheet");
  });

  it("restores the exact selected section from a document deep link", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/spec")) return json({ kind: "spec", version: 1, approved: true, markdown: "", sections: [{ id: "SPEC-005", title: "Configurable timer", body: "Timers support selectable units.", implements: ["REQ-004"] }] });
      if (p.endsWith("/trace/SPEC-005")) return json({ id: "SPEC-005", kind: "spec_section", title: "Configurable timer", up: ["REQ-004"] });
      if (p.endsWith("/trace/REQ-004")) return json({ id: "REQ-004", kind: "requirement", title: "Timer units", down: ["SPEC-005"] });
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" stage="spec" section="SPEC-005" />);
    expect(await screen.findByTestId("cycle-section")).toHaveTextContent("Configurable timer");
    expect(screen.getByTestId("cycle-index-row")).toHaveAttribute("aria-current", "true");
  });

  it("counts the API trace errors array, not only errors joined to visible sections", async () => {
    const apiPayload = {
      errors: Array.from({ length: 16 }, (_, i) => ({
        kind: "unimplemented_spec",
        id: `SPEC-${String(i + 61).padStart(3, "0")}`,
        detail: "no task implements this spec section",
      })),
    };
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json(apiPayload);
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-health")).toHaveTextContent("16 breaks in the spine"));
    expect(screen.getByTestId("cycle-coverage-line")).toHaveTextContent("16 sections have no task yet");
  });

  it("says the spine is clean only when it is", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-health")).toHaveTextContent("0 breaks in the spine"));
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
            : { ...REQUIREMENTS, proposal: { diff: "--- a\n+++ b\n@@ -1 +1 @@\n+new", ducklings: ["pato-atom"], run_id: "r-1" } },
        );
      }
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    // The card's buttons render only once the engine has stated the run's
    // legal actions, which is its own fetch: wait for the button, not just the
    // section around it.
    fireEvent.click(await screen.findByTestId("cycle-accept"));
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

  it("pins the frame, narrows the index, and jumps to a selected section", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json(TRACE);
      return json({}, 404);
    });
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
    render(<Cycle client={client} projectId="p" />);

    expect(screen.getByTestId("cycle-frame-header").className).toContain("sticky");
    expect(screen.getByTestId("cycle-index").className).toContain("sticky");
    await waitFor(() => expect(screen.getAllByTestId("cycle-index-row")).toHaveLength(2));
    fireEvent.change(screen.getByTestId("cycle-index-filter"), { target: { value: "Invoice" } });
    expect(screen.getAllByTestId("cycle-index-row")).toHaveLength(1);
    expect(screen.getByTestId("cycle-index-row")).toHaveAttribute("data-id", "REQ-002");
    fireEvent.click(screen.getByTestId("cycle-index-row"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "start" });
  });

  it("uses the joined trace markers for health and chip filters", async () => {
    const c = clientWith((p) => {
      if (p.includes("/artifacts/plan")) return json(PLAN);
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json({ errors: [{ kind: "unjustified_task", id: "T-002", detail: "task implements no spec section" }] });
      if (p.includes("/trace/M-001")) return json({ down: ["T-001"] });
      if (p.includes("/tasks")) return json({ items: [{ id: "T-001", title: "Authentication", milestone: "M-001", status: "done" }] });
      return json({}, 404);
    });
    render(<Cycle client={c} projectId="p" stage="plan" />);
    const joined = traceMarkers(PLAN.sections.flatMap((section) => [section, ...(section.children ?? [])]), [{ kind: "unjustified_task", id: "T-002", detail: "task implements no spec section" }], [{ id: "T-001", title: "Authentication", milestone: "M-001", status: "done" }], { "M-001": { down: ["T-001"] }, "T-002": { down: [] } }, "plan", new Set(["REQ-001"]));
    expect(joined.find((marker) => marker.id === "T-002")).toMatchObject({ break: true, noTask: true });
    await waitFor(() => expect(screen.getByTestId("cycle-health")).toHaveTextContent("1 break in the spine"));
    fireEvent.click(screen.getByTestId("cycle-filter-breaks"));
    expect(screen.getAllByTestId("cycle-index-row")).toHaveLength(1);
    expect(screen.getByTestId("cycle-index-row")).toHaveAttribute("data-id", "T-002");
    fireEvent.click(screen.getByTestId("cycle-filter-no-task"));
    expect(screen.getAllByTestId("cycle-index-row")).toHaveLength(2);
    expect(screen.getAllByTestId("cycle-index-row").map((row) => row.getAttribute("data-id"))).toEqual(["T-001", "T-002"]);
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

  it("filters mixed stages and does not render an overlapping section twice", async () => {
    const mixed = {
      ...REQUIREMENTS,
      sections: [
        { id: "REQ-001", title: "Accepted requirement", body: "Keep this." },
        { id: "SPEC-008", title: "Spec detail", body: "Do not leak this." },
        { id: "M-001", title: "Plan milestone", body: "Do not leak this either." },
      ],
      proposal: {
        diff: "",
        run_id: "r-1",
        sections: [
          { id: "REQ-001", title: "Accepted requirement", body: "Keep this." },
          { id: "SPEC-008", title: "Spec detail", body: "Do not leak this." },
        ],
      },
    };
    const client = clientWith((p) => {
      if (p.includes("/artifacts/")) return json(mixed);
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    const proposal = await screen.findByTestId("cycle-proposal");
    expect(proposal).toHaveTextContent("REQ-001");
    expect(proposal).not.toHaveTextContent("SPEC-008");
    expect(proposal).not.toHaveTextContent("M-001");
    expect(screen.getAllByTestId("cycle-section")).toHaveLength(1);
    expect(screen.getAllByTestId("cycle-index-row")).toHaveLength(1);
    expect(screen.queryByText("SPEC-008", { exact: true })).toBeNull();

    fireEvent.click(screen.getByTestId("cycle-tab-spec"));
    await waitFor(() => expect(screen.getAllByTestId("cycle-section")).toHaveLength(1));
    expect(screen.getByTestId("cycle-index-row")).toHaveAttribute("data-id", "SPEC-008");
  });
});

describe("Ledger", () => {
  it("lists breaks with both settlement paths and meaningful timing", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json(TRACE);
      if (p.includes("/tasks")) return json({ items: [] });
      return json({}, 404);
    });
    render(<Ledger client={client} projectId="p" />);
    expect(await screen.findByTestId("cycle-ledger-table")).toBeTruthy();
    expect(screen.getByTestId("cycle-ledger-row")).toHaveTextContent("Invoice export");
    expect(screen.getByTestId("cycle-ledger-row")).toHaveTextContent("orphan requirement");
    expect(screen.getByTestId("cycle-ledger-row")).toHaveTextContent("Create missing piece");
    expect(screen.getByTestId("cycle-ledger-row")).toHaveTextContent("Mark non-normative or amend");
  });

  it("states when the spine has no breaks", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json({ errors: null });
      if (p.includes("/tasks")) return json({ items: [] });
      return json({}, 404);
    });
    render(<Ledger client={client} projectId="p" />);
    expect(await screen.findByTestId("cycle-ledger-empty")).toHaveTextContent("No breaks");
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
  const client = (trace: unknown = { down: ["T-001"] }, taskItems: unknown[] = [
    { id: "T-001", title: "Authentication", milestone: "M-001", status: "done" },
    { id: "T-002", title: "Orphan work", milestone: "M-001", status: "queued" },
  ]) =>
    clientWith((p) => {
      if (p.includes("/artifacts/plan")) return json(PLAN);
      if (p.includes("/artifacts/")) return json({ ...REQUIREMENTS, sections: null });
      if (p.includes("/trace/check"))
        return json({ errors: [{ kind: "unjustified_task", id: "T-002", detail: "task implements no spec section" }] });
      if (p.includes("/trace/M-001")) return json(trace);
      if (p.includes("/tasks")) return json({ items: taskItems });
      return json({}, 404);
    });

  // A task's Implements line is the edge that makes the plan traceable. Showing
  // only id and title made the tab look like the plan referenced nothing.
  it("shows live task state from the Down walk and task status", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    fireEvent.click((await screen.findAllByTestId("cycle-index-row"))[0]!);
    await waitFor(() => expect(screen.getByTestId("cycle-live-state")).toHaveTextContent("T-001 landed"));
    expect(screen.getByTestId("cycle-live-state").dataset.traceLoaded).toBe("true");
  });

  it("shows no task born yet when the Down walk has no tasks", async () => {
    render(<Cycle client={client({ down: [] }, [])} projectId="p" />);
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    fireEvent.click((await screen.findAllByTestId("cycle-index-row"))[0]!);
    await waitFor(() => expect(screen.getByTestId("cycle-live-state")).toHaveTextContent("no task born yet"));
  });

  it("shows what each task implements", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    fireEvent.click((await screen.findAllByTestId("cycle-index-row"))[0]!);
    await waitFor(() => expect(screen.getAllByTestId("cycle-child")).toHaveLength(2));
    const t1 = screen.getAllByTestId("cycle-child").find((el) => el.dataset.id === "T-001")!;
    expect(t1.textContent).toContain("SPEC-007");
  });

  // The plan's prefix is M but its breaks land on tasks, so a prefix test never
  // matched and the one tab that could show the problem never did.
  it("marks a task the spine flagged, not just top-level sections", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.click(screen.getByTestId("cycle-tab-plan"));
    fireEvent.click((await screen.findAllByTestId("cycle-index-row"))[0]!);
    await waitFor(() => expect(screen.getAllByTestId("cycle-child")).toHaveLength(2));
    const kids = screen.getAllByTestId("cycle-child");
    expect(kids.find((el) => el.dataset.id === "T-002")!.dataset.broken).toBe("true");
    expect(kids.find((el) => el.dataset.id === "T-001")!.dataset.broken).toBe("false");
  });
});

describe("Cycle — verified detail pane", () => {
  it("shows missing requirement claims as breaks consistently in card and index", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/spec")) return json({
        kind: "spec", version: 1, approved: true, markdown: "",
        sections: [{ id: "SPEC-001", title: "A behavior", body: "The tool records it.", implements: ["REQ-008", "SPEC-001"] }],
      });
      if (p.includes("/artifacts/requirements")) return json({
        ...REQUIREMENTS, sections: [{ id: "REQ-001", title: "Existing", body: "It exists." }],
      });
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" stage="spec" />);

    fireEvent.click((await screen.findAllByTestId("cycle-index-row"))[0]!);
    const card = await waitFor(() => screen.getByTestId("cycle-section"));
    expect(card.dataset.broken).toBe("true");
    expect(card).toHaveTextContent("claims REQ-008 — no such requirement exists");
    expect(card).toHaveTextContent("implements SPEC-001");
    expect(screen.getByTestId("cycle-index-row")).toHaveTextContent("break");
  });

  it("shows the complete clean section once when it is selected", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json({
        ...REQUIREMENTS,
        sections: [{ id: "REQ-001", title: "Timesheets", body: "The app records hours. As-built: yes\nInternal implementation notes." }],
      });
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    fireEvent.click((await screen.findAllByTestId("cycle-index-row"))[0]!);
    const card = await waitFor(() => screen.getByTestId("cycle-section"));
    expect(card).toHaveTextContent("The app records hours.");
    expect(card).toHaveTextContent("Internal implementation notes");
    expect(card).not.toHaveTextContent("As-built");
    expect(card.querySelector("details")).toBeNull();
    expect(card.textContent?.match(/The app records hours\./g)).toHaveLength(1);
  });

  it("computes the redraft line from the seated models' measured spend", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json({ ...REQUIREMENTS, sections: [] });
      if (p.includes("/trace/check")) return json({ errors: null });
      if (p.includes("/roster")) return json({ entries: [
        { role: "architect", duckling: "k3", source: "project" },
        { role: "reviewer", duckling: "glm52", source: "project" },
      ] });
      if (p.includes("/report")) return json({ rows: [
        { key: "k3", cost_usd: 0.4, runs: 1 },
        { key: "glm52", cost_usd: 0.6, runs: 1 },
      ] });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);

    const start = await waitFor(() => screen.getByTestId("cycle-start"));
    expect(start.querySelector("summary")?.textContent).toBe("k3 drafts, glm52 critiques until it approves — about $1");
    expect(start.querySelector("summary")?.textContent).not.toContain("round");
    expect(start.querySelector("summary")?.textContent).not.toContain("cap");
    expect(start.querySelector("details")).toBeNull();
  });
});

describe("Cycle — starting a stage", () => {
  const client = (over: Record<string, unknown> = {}) =>
    ({
      artifact: vi.fn(() => Promise.resolve({ kind: "requirements", body: "", sections: [] })),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
      projects: vi.fn(() => Promise.resolve([])),
      run: vi.fn(() =>
        Promise.resolve({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        }),
      ),
      roster: vi.fn(() => Promise.resolve({ entries: [] })),
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
        mode: "council",
        rounds: 2,
        adopt: false,
      }),
    );
    // The run is where the work is visible, so it is offered — not jumped to,
    // which would lose the place of someone re-reading what they wrote.
    expect((await screen.findByTestId("cycle-run-link")).getAttribute("href")).toBe("#/runs/r-42");
  });

  // References are a door, not a form field: closed, one quiet line; open,
  // the same surface2 input the brief is, and the launch carries the paths.
  it("attaches reference documents from their door", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-start");
    fireEvent.click(screen.getByTestId("cycle-refs-door"));
    fireEvent.change(screen.getByTestId("cycle-refs"), {
      target: { value: "~/wiki/Desarrollo/miempresa/MiEmpresa.md\n\n~/wiki/Desarrollo/miempresa/feedback-pipeline.md\n" },
    });
    fireEvent.click(screen.getByTestId("cycle-run"));
    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", expect.objectContaining({
        refs: ["~/wiki/Desarrollo/miempresa/MiEmpresa.md", "~/wiki/Desarrollo/miempresa/feedback-pipeline.md"],
      })),
    );
  });

  it("starts intake with no brief at all", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("cycle-run"));
    await waitFor(() => expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { from: "", mode: "council", rounds: 2, adopt: false }));
  });

  // A project initialised on an existing repo went mute here: the brief asked
  // what to build as if the product were an idea, while the code already ran.
  // The empty state now offers the second door.
  it("offers adoption when the project already has code", async () => {
    const c = client({
      projects: vi.fn(() => Promise.resolve([{ id: "p", path: "/x", name: "X", has_code: true }])),
    });
    render(<Cycle client={c} projectId="p" />);
    // ONE choice, ONE button: the old layout had "Survey the code" boxed at
    // the top and "Draft it" at the bottom with the shared inputs (brief,
    // references) attached to the second — a person who filled references
    // was led straight past the survey and the code was never read.
    const door = await screen.findByTestId("cycle-adopt-door");
    expect(door.textContent).toContain("already has code");
    expect((screen.getByTestId("cycle-adopt") as HTMLInputElement).checked).toBe(true); // the default
    expect(screen.getByTestId("cycle-run").textContent).toBe("Survey the code");
    fireEvent.click(screen.getByTestId("cycle-run"));
    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", {
        from: "",
        mode: "council",
        rounds: 2,
        adopt: true,
      }),
    );
  });

  it("offers a configuration consultant when adoption completes with a finding", async () => {
    const c = client({
      projects: vi.fn(() => Promise.resolve([{ id: "p", path: "/x", name: "X", has_code: true }])),
      ducklings: vi.fn(() => Promise.resolve([{ id: "consultant", provider: "test", model: "test" }])),
      configDoctor: vi.fn(() => Promise.resolve([{ key: "verify.mode", proposed: "tests", reason: "no gate is configured" }])),
      chatStart: vi.fn(() => Promise.resolve({ id: "consultation-1" })),
    });
    render(<Cycle client={c} projectId="p" />);

    fireEvent.click(await screen.findByTestId("cycle-run"));
    const offer = await screen.findByTestId("adopt-config-offer");
    expect(offer).toHaveTextContent("no gate is configured");
    fireEvent.click(screen.getByTestId("chat-about"));
    fireEvent.change(screen.getByTestId("chat-duckling"), { target: { value: "consultant" } });
    fireEvent.click(screen.getByTestId("chat-start"));

    await waitFor(() => expect(c.chatStart).toHaveBeenCalledWith("p", expect.objectContaining({
      duckling: "consultant",
      aboutKind: "ducklab",
      aboutId: "configuration",
      message: expect.stringContaining("verify.mode"),
    })));
  });

  it("starts from the brief alone only when that path is chosen", async () => {
    const c = client({
      projects: vi.fn(() => Promise.resolve([{ id: "p", path: "/x", name: "X", has_code: true }])),
    });
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-adopt-door");
    fireEvent.click(screen.getByTestId("cycle-greenfield"));
    expect(screen.getByTestId("cycle-run").textContent).toBe("Draft it");
    fireEvent.click(screen.getByTestId("cycle-run"));
    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", expect.objectContaining({ adopt: false })),
    );
  });

  it("offers no adoption door to a greenfield", async () => {
    const c = client({
      projects: vi.fn(() => Promise.resolve([{ id: "p", path: "/x", name: "X" }])),
    });
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-start");
    expect(screen.queryByTestId("cycle-adopt-door")).toBeNull();
  });

  it("offers no adoption door once requirements exist", async () => {
    const c = client({
      artifact: vi.fn(() =>
        Promise.resolve({
          kind: "requirements", body: "",
          sections: [{ id: "REQ-001", title: "A", body: "" }],
        }),
      ),
      projects: vi.fn(() => Promise.resolve([{ id: "p", path: "/x", name: "X", has_code: true }])),
    });
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-start");
    expect(screen.queryByTestId("cycle-adopt-door")).toBeNull();
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

describe("Cycle — reading a proposal", () => {
  const withProposal = (proposal: Record<string, unknown>) =>
    ({
      artifact: vi.fn(() =>
        Promise.resolve({ kind: "requirements", markdown: "", sections: [], proposal }),
      ),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
      projects: vi.fn(() => Promise.resolve([])),
      run: vi.fn(() =>
        Promise.resolve({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        }),
      ),
      roster: vi.fn(() => Promise.resolve({ entries: [] })),
      promote: vi.fn(() => Promise.resolve({})),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
    }) as unknown as EngineClient;

  // Deciding whether to accept requirements means reading them. A first draft
  // has nothing to diff against, so the diff was 78 lines of "+" — and before
  // this it was not even that, because the headerless diff parsed to nothing
  // and the panel said "No changes yet." over a whole document.
  it("shows the proposed document, not a diff", async () => {
    const client = withProposal({
      diff: "@@ -1,1 +1,3 @@\n-\n+## REQ-001 — Add a sighting\n",
      sections: [
        { id: "REQ-001", title: "Add a sighting", body: "The tool shall record a sighting." },
        { id: "REQ-002", title: "List the tally", body: "Sorted by count." },
      ],
    });
    render(<Cycle client={client} projectId="p" />);
    const proposal = await screen.findByTestId("cycle-proposal");
    expect(proposal.textContent).toContain("REQ-001");
    expect(proposal.textContent).toContain("The tool shall record a sighting.");
    expect(screen.getByTestId("proposal-sections")).toBeTruthy();
    expect(proposal).toHaveTextContent("A run proposes changing this section — read it and decide");
    expect(screen.getByTestId("proposal-coverage-line")).toHaveTextContent("Every normative section has work behind it.");
  });

  // The diff answers a different question — what changed — which matters on a
  // redraft and not on a first one.
  it("offers the diff as a second view", async () => {
    const client = withProposal({
      diff: "@@ -1,1 +1,2 @@\n-old line\n+new line\n",
      sections: [{ id: "REQ-001", title: "A thing", body: "" }],
    });
    render(<Cycle client={client} projectId="p" />);
    fireEvent.click(await screen.findByTestId("proposal-view-toggle"));
    await waitFor(() => expect(screen.queryByTestId("proposal-sections")).toBeNull());
    expect(screen.getByTestId("cycle-proposal").textContent).toContain("new line");
  });

  // A draft the section parser did not understand is still a draft a person
  // can read and reject.
  it("falls back to the whole document when no sections parsed", async () => {
    const client = withProposal({
      diff: "",
      markdown: "---\nkind: requirements\n---\n\n## Something the parser missed\n",
      sections: [],
    });
    render(<Cycle client={client} projectId="p" />);
    const proposal = await screen.findByTestId("cycle-proposal");
    expect(proposal.textContent).toContain("Something the parser missed");
    // And not the frontmatter, which is for machines.
    expect(proposal.textContent).not.toContain("kind: requirements");
  });
});

describe("Cycle — what was asked for", () => {
  const withBrief = (artifact: Record<string, unknown>, brief = "Build a triangle tool.") =>
    ({
      artifact: vi.fn(() => Promise.resolve(artifact)),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
      projects: vi.fn(() => Promise.resolve([])),
      run: vi.fn(() =>
        Promise.resolve({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        }),
      ),
      roster: vi.fn(() => Promise.resolve({ entries: [] })),
      runBrief: vi.fn(() => Promise.resolve(brief)),
      promote: vi.fn(() => Promise.resolve({})),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
    }) as unknown as EngineClient;

  // A whole requirements document points at its latest producer. That is not
  // evidence that every section came from that run, so Requirements must not
  // present its brief as the origin of the selected section.
  it("does not present the latest requirements run as every requirement's intent", async () => {
    const client = withBrief({
      kind: "requirements",
      markdown: "",
      sections: [{ id: "REQ-001", title: "A thing", body: "" }],
      run_id: "r-42",
    });
    render(<Cycle client={client} projectId="p" />);

    await screen.findByTestId("cycle-view");
    expect(screen.queryByTestId("asked-for")).toBeNull();
    expect(client.runBrief).not.toHaveBeenCalled();
  });

  it("keeps a pending requirements brief in Intent rather than above the proposal", async () => {
    const client = withBrief({
      kind: "requirements",
      markdown: "",
      sections: [],
      run_id: "r-old",
      proposal: { diff: "", sections: [{ id: "REQ-001", title: "x", body: "" }], run_id: "r-new" },
    });
    render(<Cycle client={client} projectId="p" />);
    await screen.findByTestId("cycle-proposal");
    expect(screen.queryByTestId("asked-for")).toBeNull();
    expect(client.runBrief).not.toHaveBeenCalled();
  });

  // Most stages are not seeded with one, and an empty panel promising a brief
  // is worse than no panel.
  it("shows nothing when the run had no brief", async () => {
    const client = withBrief(
      { kind: "requirements", markdown: "", sections: [], run_id: "r-42" },
      "",
    );
    render(<Cycle client={client} projectId="p" />);
    await screen.findByTestId("cycle-view");
    expect(screen.queryByTestId("asked-for-panel")).toBeNull();
  });
});

describe("Cycle — asking for a change", () => {
  const pending = {
    kind: "requirements",
    markdown: "",
    sections: [],
    proposal: {
      diff: "@@ -1 +1 @@\n+x\n",
      sections: [
        { id: "SPEC-001", title: "Dragging", body: "" },
        { id: "SPEC-004", title: "Locking", body: "" },
      ],
      run_id: "r-1",
    },
  };
  const client = (over: Record<string, unknown> = {}) =>
    ({
      artifact: vi.fn(() => Promise.resolve(pending)),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
      projects: vi.fn(() => Promise.resolve([])),
      run: vi.fn(() =>
        Promise.resolve({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        }),
      ),
      roster: vi.fn(() => Promise.resolve({ entries: [] })),
      runBrief: vi.fn(() => Promise.resolve("")),
      promote: vi.fn(() => Promise.resolve({})),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-2" })),
      ...over,
    }) as unknown as EngineClient;

  // Accept and reject are a verdict on a document that is usually almost
  // right, and "almost" had no button: rejecting left the draft alone, and
  // redrafting regenerated the sections you were happy with.
  it("sends the note as a revision, not a fresh draft", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("request-changes");

    fireEvent.change(screen.getByTestId("change-note"), {
      target: { value: "SPEC-004 should also stop the opposite vertex dragging" },
    });
    fireEvent.click(screen.getByTestId("request-changes-button"));

    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", {
        revise: "SPEC-004 should also stop the opposite vertex dragging",
      }),
    );
    expect((await screen.findByTestId("revision-run-link")).getAttribute("href")).toBe("#/runs/r-2");
  });

  it("will not ask for nothing", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("request-changes");
    expect(screen.getByTestId("request-changes-button").hasAttribute("disabled")).toBe(true);
    fireEvent.click(screen.getByTestId("request-changes-button"));
    expect(c.stageStart).not.toHaveBeenCalled();
  });

  // The promise is that everything unmentioned survives, and the way to check
  // it is the diff. Said where the promise is made.
  it("points at how to check what actually changed", async () => {
    render(<Cycle client={client()} projectId="p" />);
    expect((await screen.findByTestId("request-changes")).textContent).toContain(
      "check what changed",
    );
  });

  // No draft on the table, nothing to revise.
  it("offers nothing to revise when no proposal is pending", async () => {
    const c = client({
      artifact: vi.fn(() => Promise.resolve({ kind: "requirements", markdown: "", sections: [] })),
    });
    render(<Cycle client={c} projectId="p" />);
    await screen.findByTestId("cycle-start");
    expect(screen.queryByTestId("request-changes")).toBeNull();
  });

  it("shows the engine's refusal rather than failing silently", async () => {
    const c = client({
      stageStart: vi.fn(() => Promise.reject(new Error("no ducklings available"))),
    });
    render(<Cycle client={c} projectId="p" />);
    fireEvent.change(await screen.findByTestId("change-note"), { target: { value: "change it" } });
    fireEvent.click(screen.getByTestId("request-changes-button"));
    expect((await screen.findByTestId("cycle-error")).textContent).toContain("no ducklings");
  });
});

describe("Cycle — what the run will actually do", () => {
  const client = (roster: unknown[], over: Record<string, unknown> = {}) =>
    ({
      artifact: vi.fn(() => Promise.resolve({ kind: "requirements", markdown: "", sections: [] })),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
      projects: vi.fn(() => Promise.resolve([])),
      run: vi.fn(() =>
        Promise.resolve({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        }),
      ),
      runBrief: vi.fn(() => Promise.resolve("")),
      roster: vi.fn(() => Promise.resolve({ entries: roster })),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      promote: vi.fn(() => Promise.resolve({})),
      ...over,
    }) as unknown as EngineClient;

  const twoDucks = [
    { role: "architect", duckling: "pato-sonnet", source: "project" },
    { role: "reviewer", duckling: "pato-local", source: "default" },
  ];

  // "Draft it" hid the two things worth knowing before spending minutes and
  // tokens: which models, and whether one will critique the other.
  it("names who drafts and who critiques", async () => {
    render(<Cycle client={client(twoDucks)} projectId="p" />);
    expect((await screen.findByTestId("stage-who")).textContent).toContain(
      "pato-sonnet drafts, pato-local critiques",
    );
  });

  it("says nothing reviews a solo draft", async () => {
    render(<Cycle client={client(twoDucks)} projectId="p" />);
    fireEvent.change(await screen.findByTestId("stage-mode"), { target: { value: "solo" } });
    expect(screen.getByTestId("stage-who").textContent).toBe(
      "pato-sonnet drafts, and nothing reviews it",
    );
  });

  // The engine lists one reviewer entry per critic; the preview must name them
  // all — a preview naming one of three critics says the setting did not take.
  it("names every critic when the council seats several", async () => {
    const three = [
      { role: "architect", duckling: "pato-k3", source: "council line-up" },
      { role: "reviewer", duckling: "pato-sonnet", source: "council line-up" },
      { role: "reviewer", duckling: "pato-luna", source: "council line-up" },
    ];
    render(<Cycle client={client(three)} projectId="p" />);
    const who = (await screen.findByTestId("stage-who")).textContent;
    expect(who).toContain("pato-k3 drafts, pato-sonnet and pato-luna each critique");
    expect(who).toContain("unless every critic approves");
  });

  // One duckling on both sides measures self-consistency, not review.
  it("warns when the same duckling would critique its own draft", async () => {
    const same = [
      { role: "architect", duckling: "pato-local", source: "default" },
      { role: "reviewer", duckling: "pato-local", source: "default" },
    ];
    render(<Cycle client={client(same)} projectId="p" />);
    expect((await screen.findByTestId("stage-who")).textContent).toContain("its own draft");
  });

  it("sends the chosen mode", async () => {
    const c = client(twoDucks);
    render(<Cycle client={c} projectId="p" />);
    fireEvent.change(await screen.findByTestId("stage-mode"), { target: { value: "solo" } });
    fireEvent.click(screen.getByTestId("cycle-run"));
    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { from: "", mode: "solo", rounds: 2, adopt: false }),
    );
  });

  it("defaults to council, two rounds", async () => {
    const c = client(twoDucks);
    render(<Cycle client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("cycle-run"));
    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", {
        from: "",
        mode: "council",
        rounds: 2,
        adopt: false,
      }),
    );
  });
});

describe("Cycle — how many rounds", () => {
  const twoDucks = [
    { role: "architect", duckling: "pato-sonnet", source: "project" },
    { role: "reviewer", duckling: "pato-local", source: "default" },
  ];
  const client = () =>
    ({
      artifact: vi.fn(() => Promise.resolve({ kind: "requirements", markdown: "", sections: [] })),
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
      projects: vi.fn(() => Promise.resolve([])),
      run: vi.fn(() =>
        Promise.resolve({
          run: { id: "r-1", status: "paused", pending_kind: "gate", next: ["accept", "request_changes", "reject"] },
          events: [],
        }),
      ),
      runBrief: vi.fn(() => Promise.resolve("")),
      roster: vi.fn(() => Promise.resolve({ entries: twoDucks })),
      stageStart: vi.fn(() => Promise.resolve({ id: "r-1" })),
      promote: vi.fn(() => Promise.resolve({})),
    }) as unknown as EngineClient;

  it("sends the chosen limit", async () => {
    const c = client();
    render(<Cycle client={c} projectId="p" />);
    fireEvent.change(await screen.findByTestId("stage-rounds"), { target: { value: "4" } });
    fireEvent.click(screen.getByTestId("cycle-run"));
    await waitFor(() =>
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", {
        from: "",
        mode: "council",
        rounds: 4,
        adopt: false,
      }),
    );
  });

  // A ceiling, not a plan: the loop stops when the reviewer approves, so the
  // number is what it will do at most.
  it("says the extra rounds only happen without approval", async () => {
    render(<Cycle client={client()} projectId="p" />);
    expect((await screen.findByTestId("stage-who")).textContent).toContain("unless pato-local approves");
  });

  it("says nothing about going round again at one round", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.change(await screen.findByTestId("stage-rounds"), { target: { value: "1" } });
    expect(screen.getByTestId("stage-who").textContent).not.toContain("round again");
  });

  // Solo has no reviewer, so there is nothing for a second round to react to.
  it("offers no round count for solo", async () => {
    render(<Cycle client={client()} projectId="p" />);
    fireEvent.change(await screen.findByTestId("stage-mode"), { target: { value: "solo" } });
    expect(screen.queryByTestId("stage-rounds")).toBeNull();
  });

  it("refuses zero", async () => {
    render(<Cycle client={client()} projectId="p" />);
    const input = await screen.findByTestId("stage-rounds");
    fireEvent.change(input, { target: { value: "0" } });
    expect((input as HTMLInputElement).value).toBe("1");
  });
});

// The trace rail sits beside the Accept button, and it was reporting on the
// document the human had already accepted — LoadSpine read approved artifacts
// only, so a proposal at its gate was invisible to the one check that could
// have changed the decision.
describe("the trace rail's scope", () => {
  it("says when it is checking what you are about to accept", async () => {
    const client = clientWith((p) => {
      if (p.includes("/trace/check")) return json({ errors: null, proposed: ["plan"] });
      if (p.includes("/artifacts/")) return json({ kind: "plan", sections: [] });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-health")).toHaveTextContent("0 breaks in the spine"));
  });

  it("stays quiet when the approved spine is what was checked", async () => {
    const client = clientWith((p) => {
      if (p.includes("/trace/check")) return json({ errors: null });
      if (p.includes("/artifacts/")) return json({ kind: "plan", sections: [] });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-health")).toHaveTextContent("0 breaks in the spine"));
    expect(screen.queryByTestId("trace-scope")).toBeNull();
  });
});

// "Do features have to arrive as fake bug reports?" — asked by the user,
// because the flow that answers it (re-run intake with a brief; the cycle
// carries it to spec and plan) existed and nothing named it. "Redraft the
// requirements" undersold the normal case: growing a project.
describe("adding a feature to a grown project", () => {
  it("names the flow and promises the survivals", async () => {
    const client = clientWith((p) => {
      if (p.includes("/artifacts/requirements")) return json(REQUIREMENTS);
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-start")).toBeTruthy());
    const start = screen.getByTestId("cycle-start");
    expect(start.textContent).toContain("Add to the requirements");
    expect(
      (screen.getByTestId("cycle-brief") as HTMLTextAreaElement).placeholder,
    ).toContain("Existing requirements survive");
    expect(start.textContent).toContain("full traceability");
  });
});

// The lifecycle keeps a rejected proposal on disk — a failed attempt is a
// record (05 §1.1) — and this view read "file exists" as "decision pending":
// a person who had just rejected a draft came back to a screen still awaiting
// their decision, with the run right there showing it was made.
describe("a proposal whose run was already decided", () => {
  const decidedClient = () =>
    clientWith((p) => {
      if (p.includes("/artifacts/requirements"))
        return json({
          ...REQUIREMENTS,
          proposal: { diff: "--- a\n+++ b\n@@ -1 +1 @@\n+new", run_id: "r-rejected" },
        });
      if (p.includes("/trace/check")) return json({ errors: null });
      return json({}, 404);
    });

  // The shared stub answers /v1/runs/{id} with a paused gate; this suite needs
  // a decided one, so it overrides the method directly.
  const withDecidedRun = () => {
    const c = decidedClient();
    (c as unknown as { run: unknown }).run = vi.fn(() =>
      Promise.resolve({ run: { id: "r-rejected", status: "done", verdict: "FAILED", next: [] }, events: [] }),
    );
    return c;
  };

  it("says the decision was made instead of asking again", async () => {
    render(<Cycle client={withDecidedRun()} projectId="p" />);
    const note = await screen.findByTestId("cycle-rejected-draft");
    expect(note.textContent).toContain("You already decided this one");
    expect(screen.queryByTestId("cycle-proposal")).toBeNull();
    // And the way forward is open: the start controls are back.
    expect(screen.getByTestId("cycle-start")).toBeTruthy();
  });

  it("lets the person discard it as their own act", async () => {
    const c = withDecidedRun();
    (c as unknown as { artifactDiscard: unknown }).artifactDiscard = vi.fn(() =>
      Promise.resolve({ discarded: "requirements" }),
    );
    render(<Cycle client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("discard-draft"));
    await waitFor(() =>
      expect(
        (c as unknown as { artifactDiscard: { mock: { calls: unknown[][] } } }).artifactDiscard.mock
          .calls[0],
      ).toEqual(["p", "requirements"]),
    );
  });

  // The brief lives in the run's record, not in the draft — but the only path
  // back to it was retyping from memory while the originals sat on disk.
  it("hands the original brief back into the textarea", async () => {
    const c = withDecidedRun();
    (c as unknown as { runBrief: unknown }).runBrief = vi.fn(() =>
      Promise.resolve("Add undo: ctrl-z reverts the last drag."),
    );
    render(<Cycle client={c} projectId="p" />);
    fireEvent.click(await screen.findByTestId("reuse-brief"));
    await waitFor(() =>
      expect((screen.getByTestId("cycle-brief") as HTMLTextAreaElement).value).toContain(
        "ctrl-z reverts the last drag",
      ),
    );
  });

  it("still asks when the gate is genuinely open", async () => {
    render(<Cycle client={decidedClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-proposal")).toBeTruthy());
    expect(screen.queryByTestId("cycle-rejected-draft")).toBeNull();
  });
});

// Review's light exit, in the place plans live: a small change becomes a
// plan amendment — one to three tasks from an architect — without the
// reqs→spec→plan pass. The only doors before this were the full brief or
// filing the enhancement as a fake bug.
describe("the plan amendment", () => {
  const PLAN = {
    kind: "plan",
    version: 1,
    approved: true,
    markdown: "---\nkind: plan\n---\n",
    sections: [
      { id: "M-001", title: "Core", body: "", fields: {}, children: [
        { id: "T-001", title: "Schema", body: "", fields: {} },
      ] },
    ],
  };

  it("offers the form on an existing plan and posts the extend field", async () => {
    let extendSent = "";
    const client = clientWith((p, init) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p === "/v1/projects/p/stages/plan" && init?.method === "POST") {
        extendSent = String(JSON.parse(String(init.body)).extend ?? "");
        return json({ id: "r-amend", status: "running" });
      }
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="plan" />);
    // Folded until chosen: the panel opens from its action tab.
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    const box = await waitFor(() => screen.getByTestId("plan-extend-text"));
    fireEvent.change(box, { target: { value: "add a CSV export button" } });
    fireEvent.click(screen.getByTestId("plan-extend-start"));
    await waitFor(() => expect(extendSent).toBe("add a CSV export button"));
  });

  it("keeps the door shut when no plan exists yet", async () => {
    const client = clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/plan") return json({ kind: "plan", sections: null });
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="plan" />);
    await waitFor(() => screen.getByTestId("cycle-view"));
    expect(screen.queryByTestId("plan-extend")).toBeNull();
  });
});

// Settling the debt is one click, not an essay: the engine assembles the
// revision from the debt itself; the person's job is the diff at the gate.
describe("the spec-debt settle button", () => {
  it("appears with the count and posts settle", async () => {
    let settleSent = false;
    const client = clientWith((p, init) => {
      if (p === "/v1/projects/p/artifacts/spec") return json({ kind: "spec", sections: [{ id: "SPEC-001", title: "S", body: "", fields: {} }] });
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p === "/v1/projects/p/tasks") return json({ items: [
        { id: "T-110", title: "t", milestone: "M-001", status: "accepted", spec_debt: true },
        { id: "T-111", title: "t2", milestone: "M-001", status: "accepted" },
      ] });
      if (p === "/v1/projects/p/stages/spec" && init?.method === "POST") {
        settleSent = Boolean(JSON.parse(String(init.body)).settle);
        return json({ id: "r-settle", status: "running" });
      }
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="spec" />);
    const btn = await waitFor(() => screen.getByTestId("spec-settle-start"));
    expect(btn.textContent).toContain("(1)");
    fireEvent.click(btn);
    await waitFor(() => expect(settleSent).toBe(true));
  });

  it("stays away when nothing owes the spec", async () => {
    const client = clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/spec") return json({ kind: "spec", sections: [] });
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p === "/v1/projects/p/tasks") return json({ items: [{ id: "T-110", title: "t", milestone: "M", status: "accepted" }] });
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="spec" />);
    await waitFor(() => screen.getByTestId("cycle-view"));
    expect(screen.queryByTestId("spec-settle")).toBeNull();
  });
});

// A warning delivered in the run's event log after launch is a warning
// delivered after the decision it should have informed. The seat's vision is
// checked the moment the screenshot is added.
describe("the amendment's vision warning", () => {
  const PLAN2 = {
    kind: "plan", version: 1, approved: true, markdown: "---\nkind: plan\n---\n",
    sections: [{ id: "M-001", title: "Core", body: "", fields: {}, children: [{ id: "T-001", title: "S", body: "", fields: {} }] }],
  };
  const clientBlind = () =>
    clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN2);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: [{ role: "architect", duckling: "glm52", source: "project" }] });
      if (p === "/v1/ducklings") return json({ items: [{ id: "glm52", provider: "z", model: "glm", caps: { native_tools: true, context_tokens: 128000 } }] });
      return json({ items: [] });
    });

  it("warns when the screenshot lands on a blind architect", async () => {
    render(<Cycle client={clientBlind()} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    const input = await waitFor(() => screen.getByTestId("plan-extend-image"));
    const file = new File(["png"], "mock.png", { type: "image/png" });
    fireEvent.change(input, { target: { files: [file] } });
    await waitFor(() => screen.getByTestId("plan-extend-image-chip"));
    expect(screen.getByTestId("plan-extend-vision-warn").textContent).toContain("cannot see images");
  });

  it("stays quiet with no image attached", async () => {
    render(<Cycle client={clientBlind()} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    await waitFor(() => screen.getByTestId("plan-extend"));
    expect(screen.queryByTestId("plan-extend-vision-warn")).toBeNull();
  });

  // Both panels folded until chosen — stacked open they read as noise — and
  // the chosen one names its seat with the chips that matter.
  it("folds both actions until one is chosen, then names the seat", async () => {
    render(<Cycle client={clientBlind()} projectId="p" stage="plan" />);
    await waitFor(() => screen.getByTestId("plan-actions"));
    expect(screen.queryByTestId("plan-extend")).toBeNull();
    expect(screen.queryByTestId("stage-mode")).toBeNull();
    fireEvent.click(screen.getByTestId("plan-action-amend"));
    await waitFor(() => screen.getByTestId("plan-extend"));
    const chip = await waitFor(() => screen.getAllByTestId("seat-chip")[0]);
    expect(chip?.textContent).toContain("glm52");
    // Toggle off folds it again.
    fireEvent.click(screen.getByTestId("plan-action-amend"));
    expect(screen.queryByTestId("plan-extend")).toBeNull();
  });
});

// Chips are a promise about who participates. The full roster also lists
// implementer, judge, triager and scribe — none of whom a plan redraft ever
// calls — and rendering them claimed models the run would not use.
describe("every document stage shows its seats", () => {
  // The plan's panel had chips; Requirements and Spec asked the same
  // question — who will do this — with no answer on screen. Same format
  // everywhere: mode first, then the seats the mode decides.
  it("the spec tab wears the chips under its mode row", async () => {
    const client = clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/spec") return json({ kind: "spec", sections: [{ id: "SPEC-001", title: "S", body: "", fields: {} }] });
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: [
        { role: "architect", duckling: "k3", source: "project" },
        { role: "reviewer", duckling: "deepseekv4pro", source: "project" },
      ] });
      if (p === "/v1/ducklings") return json({ items: [] });
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="spec" />);
    const chips = await waitFor(() => screen.getAllByTestId("seat-chip"));
    expect(chips.map((c) => c.textContent).join(" ")).toContain("k3");
    // Order: the mode select renders before the chips in the document flow.
    const modeEl = screen.getByTestId("stage-mode");
    const chipsEl = screen.getByTestId("seat-chips");
    expect(modeEl.compareDocumentPosition(chipsEl) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

describe("the extend panel's seats", () => {
  const FULL_ROSTER = [
    { role: "architect", duckling: "glm52", source: "project" },
    { role: "reviewer", duckling: "deepseekv4pro", source: "project" },
    { role: "implementer", duckling: "luna", source: "project" },
    { role: "judge", duckling: "dsv4flash", source: "project" },
    { role: "triager", duckling: "luna", source: "project" },
    { role: "scribe", duckling: "dsv4flash", source: "project" },
  ];
  const PLAN3 = {
    kind: "plan", version: 1, approved: true, markdown: "---\nkind: plan\n---\n",
    sections: [{ id: "M-001", title: "Core", body: "", fields: {}, children: [{ id: "T-001", title: "S", body: "", fields: {} }] }],
  };

  it("shows only the drafter and its critics", async () => {
    const client = clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN3);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: FULL_ROSTER });
      if (p === "/v1/ducklings") return json({ items: [] });
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-extend")));
    const chips = await waitFor(() => screen.getAllByTestId("seat-chip"));
    const text = chips.map((c) => c.textContent).join(" | ");
    expect(text).toContain("architect");
    expect(text).toContain("critic");
    for (const ghost of ["implementer", "judge", "triager", "scribe"]) {
      expect(text).not.toContain(ghost);
    }
  });
});

// The chip is a DOOR: click it, pick a different duckling, and THIS run
// seats the pick — the team's saved seats never move.
describe("picking a seat from its chip", () => {
  const PLAN4 = {
    kind: "plan", version: 1, approved: true, markdown: "---\nkind: plan\n---\n",
    sections: [{ id: "M-001", title: "Core", body: "", fields: {}, children: [{ id: "T-001", title: "S", body: "", fields: {} }] }],
  };
  it("carries the calls-per-reply cap on an amendment", async () => {
    let sent: number | undefined;
    const client = clientWith((p, init) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN4);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: [{ role: "architect", duckling: "glm52", source: "project" }] });
      if (p === "/v1/ducklings") return json({ items: [] });
      if (p === "/v1/projects/p/stages/plan" && init?.method === "POST") {
        sent = JSON.parse(String(init.body)).agent_turns;
        return json({ id: "r-x", status: "running" });
      }
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-extend")));
    fireEvent.change(await waitFor(() => screen.getByTestId("stage-agent-turns")), { target: { value: "30" } });
    fireEvent.click(screen.getByTestId("plan-action-extend")); // fold extend
    fireEvent.click(screen.getByTestId("plan-action-amend"));
    fireEvent.change(screen.getByTestId("plan-extend-text"), { target: { value: "quick" } });
    fireEvent.click(screen.getByTestId("plan-extend-start"));
    await waitFor(() => expect(sent).toBe(30));
  });

  it("amends with the picked duckling, this run only", async () => {
    let sent: string[] | undefined;
    const client = clientWith((p, init) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN4);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: [{ role: "architect", duckling: "glm52", source: "project" }] });
      if (p === "/v1/ducklings") return json({ items: [
        { id: "glm52", provider: "z", model: "g", caps: { native_tools: true, context_tokens: 1000000 } },
        { id: "luna", provider: "l", model: "l", caps: { native_tools: true, context_tokens: 1100000, vision: true } },
      ] });
      if (p === "/v1/projects/p/stages/plan" && init?.method === "POST") {
        sent = JSON.parse(String(init.body)).ducklings;
        return json({ id: "r-x", status: "running" });
      }
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    // Click the chip, pick luna.
    fireEvent.click(await waitFor(() => screen.getAllByTestId("seat-chip")[0]!));
    fireEvent.change(await waitFor(() => screen.getByTestId("seat-pick-0")), { target: { value: "luna" } });
    // The chip now wears the pick — and the vision warning logic follows it.
    await waitFor(() => {
      const chip = screen.getAllByTestId("seat-chip")[0]!;
      expect(chip.textContent).toContain("luna");
    });
    fireEvent.change(screen.getByTestId("plan-extend-text"), { target: { value: "quick change" } });
    fireEvent.click(screen.getByTestId("plan-extend-start"));
    await waitFor(() => expect(sent).toEqual(["luna"]));
  });
});

// Which facts ride a chip is the person's own pick: "if what I care about is
// vision and average price, the chip carries those two and nothing else."
describe("chip facts follow the appearance preference", () => {
  const PLAN5 = {
    kind: "plan", version: 1, approved: true, markdown: "---\nkind: plan\n---\n",
    sections: [{ id: "M-001", title: "Core", body: "", fields: {}, children: [{ id: "T-001", title: "S", body: "", fields: {} }] }],
  };
  const clientPriced = () =>
    clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN5);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: [{ role: "architect", duckling: "luna", source: "project" }] });
      if (p === "/v1/ducklings") return json({ items: [
        { id: "luna", provider: "l", model: "l", caps: { native_tools: true, context_tokens: 1100000, vision: true }, cost: { input_per_mtok: 2, output_per_mtok: 4 } },
      ] });
      return json({ items: [] });
    });

  it("carries the measured cost per run when chosen", async () => {
    localStorage.setItem("ducklab.chipfacts", JSON.stringify(["mprice"]));
    const client = clientWith((p) => {
      if (p === "/v1/projects/p/artifacts/plan") return json(PLAN5);
      if (p === "/v1/projects/p/trace") return json({ errors: [] });
      if (p.startsWith("/v1/projects/p/roster")) return json({ entries: [{ role: "architect", duckling: "luna", source: "project" }] });
      if (p === "/v1/ducklings") return json({ items: [{ id: "luna", provider: "l", model: "l", caps: { native_tools: true, context_tokens: 1100000 } }] });
      if (p.startsWith("/v1/projects/p/reports/duckling") || p.includes("duckling")) {
        return json({ rows: [{ key: "luna", cost_usd: 0.9, runs: 30 }], deltas: [], rendered: "" });
      }
      return json({ items: [] });
    });
    render(<Cycle client={client} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    const chip = (await waitFor(() => screen.getAllByTestId("chip-mprice")))[0]!;
    expect(chip.textContent).toContain("~$0.030/run"); // 0.9 usd over 30 runs, measured
    localStorage.removeItem("ducklab.chipfacts");
  });

  it("shows only the chosen facts", async () => {
    localStorage.setItem("ducklab.chipfacts", JSON.stringify(["vision", "price"]));
    render(<Cycle client={clientPriced()} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    const chip = (await waitFor(() => screen.getAllByTestId("seat-chip")))[0]!;
    expect(chip.textContent).toContain("👁️");
    expect(chip.textContent).toContain("$3.00/M"); // avg of 2 in / 4 out
    expect(chip.textContent).not.toContain("1.1M"); // context unchecked
    localStorage.removeItem("ducklab.chipfacts");
  });

  it("a settings change lands on chips already on screen", async () => {
    localStorage.removeItem("ducklab.chipfacts");
    render(<Cycle client={clientPriced()} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    const chip = (await waitFor(() => screen.getAllByTestId("seat-chip")))[0]!;
    expect(chip.textContent).toContain("1.1M"); // context, by default
    // The person unticks everything but vision in Settings — no save, no
    // remount: the mounted chip must follow.
    act(() => saveChipFacts(["vision"]));
    await waitFor(() => {
      const c = screen.getAllByTestId("seat-chip")[0]!;
      expect(c.textContent).not.toContain("1.1M");
      expect(c.textContent).toContain("👁️");
    });
    localStorage.removeItem("ducklab.chipfacts");
  });

  it("defaults to context and vision when nothing is saved", async () => {
    localStorage.removeItem("ducklab.chipfacts");
    render(<Cycle client={clientPriced()} projectId="p" stage="plan" />);
    fireEvent.click(await waitFor(() => screen.getByTestId("plan-action-amend")));
    const chip = (await waitFor(() => screen.getAllByTestId("seat-chip")))[0]!;
    expect(chip.textContent).toContain("1.1M");
    expect(chip.textContent).toContain("👁️");
    expect(chip.textContent).not.toContain("/M$"); // no price by default
  });
});
