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
      traceCheck: vi.fn(() => Promise.resolve({ errors: [], proposed: [] })),
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
    await waitFor(() => expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { from: "", mode: "council", rounds: 2 }));
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

  // Checking that requirements match what was asked for is the first thing
  // anyone does with them, and the brief was reachable only by digging it out
  // of a prompt in the run log.
  it("offers the brief of the run that produced the accepted document", async () => {
    const client = withBrief({
      kind: "requirements",
      markdown: "",
      sections: [{ id: "REQ-001", title: "A thing", body: "" }],
      run_id: "r-42",
    });
    render(<Cycle client={client} projectId="p" />);

    fireEvent.click(await screen.findByTestId("asked-for-toggle"));
    expect((await screen.findByTestId("asked-for")).textContent).toContain("triangle tool");
    expect(client.runBrief).toHaveBeenCalledWith("r-42");
  });

  // While a proposal is pending, the brief to compare against is the one that
  // produced the proposal, not the older accepted version's.
  it("prefers the pending proposal's run", async () => {
    const client = withBrief({
      kind: "requirements",
      markdown: "",
      sections: [],
      run_id: "r-old",
      proposal: { diff: "", sections: [{ id: "REQ-001", title: "x", body: "" }], run_id: "r-new" },
    });
    render(<Cycle client={client} projectId="p" />);
    await screen.findByTestId("asked-for-toggle");
    expect(client.runBrief).toHaveBeenCalledWith("r-new");
  });

  // Collapsed, because it is a reference and not the subject.
  it("starts collapsed", async () => {
    const client = withBrief({ kind: "requirements", markdown: "", sections: [], run_id: "r-42" });
    render(<Cycle client={client} projectId="p" />);
    await screen.findByTestId("asked-for-toggle");
    expect(screen.queryByTestId("asked-for")).toBeNull();
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
      expect(c.stageStart).toHaveBeenCalledWith("p", "intake", { from: "", mode: "solo", rounds: 2 }),
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
      }),
    );
  });

  // A ceiling, not a plan: the loop stops when the reviewer approves, so the
  // number is what it will do at most.
  it("says the extra rounds only happen without approval", async () => {
    render(<Cycle client={client()} projectId="p" />);
    expect((await screen.findByTestId("stage-who")).textContent).toContain("does not approve");
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
    await waitFor(() =>
      expect(screen.getByTestId("trace-scope").textContent).toContain("proposed plan"),
    );
  });

  it("stays quiet when the approved spine is what was checked", async () => {
    const client = clientWith((p) => {
      if (p.includes("/trace/check")) return json({ errors: null });
      if (p.includes("/artifacts/")) return json({ kind: "plan", sections: [] });
      return json({}, 404);
    });
    render(<Cycle client={client} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("trace-clean")).toBeTruthy());
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

  it("still asks when the gate is genuinely open", async () => {
    render(<Cycle client={decidedClient()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("cycle-proposal")).toBeTruthy());
    expect(screen.queryByTestId("cycle-rejected-draft")).toBeNull();
  });
});
