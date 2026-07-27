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
