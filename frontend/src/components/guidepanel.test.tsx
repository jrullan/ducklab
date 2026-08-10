import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { GuideRail } from "./GuidePanel";
import { useRuns } from "../store/runs";
import { EngineClient, type NextStep } from "../api/client";

const clientWith = (steps: NextStep[]) =>
  new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string) =>
      String(url).includes("/next")
        ? new Response(JSON.stringify({ items: steps, total: steps.length }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          })
        : new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
  });

const STEPS: NextStep[] = [
  { id: "answer-run", action: "Answer the question the spec run asked", reason: "a model waits on you", kind: "run", ref: "r-1" },
  { id: "plan", action: "Plan the work — break the spec into tasks", reason: "3 spec section(s) are not yet built and no plan exists", kind: "stage", ref: "plan" },
  { id: "triage", action: "Triage the open bugs", reason: "2 bug(s) are open", kind: "bug" },
  { id: "test-first", action: "Start T-003 (test first, then build)", reason: "next task whose dependencies are all accepted", kind: "task", ref: "T-003" },
];

describe("the guide rail", () => {
  beforeEach(() => {
    localStorage.clear();
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  // The rail lives beside EVERY view, so unlike the old in-Now panel it must
  // carry the whole guide: away from the inbox, the rail is the only place a
  // paused run or the next task is visible at all.
  it("shows every step in the engine's order, each with its why", async () => {
    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    const steps = screen.getAllByTestId("guide-step").map((s) => s.textContent ?? "");
    expect(steps).toHaveLength(4);
    expect(steps[0]).toContain("Answer the question"); // paused work first
    expect(steps[0]).toContain("a model waits");
    expect(steps[3]).toContain("Start T-003");
  });

  it("links each step to the surface whose buttons already do it", async () => {
    render(<GuideRail client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    const links = screen
      .getAllByTestId("guide-step")
      .map((s) => s.querySelector("a")?.getAttribute("href"));
    expect(links).toEqual(["#/runs/r-1", "#/cycle/plan", "#/board/bugs", "#/board"]);
  });

  // Guidance for the first weeks, a counted strip for after — and the choice
  // survives a reload.
  it("collapses to a counted strip, and stays collapsed", async () => {
    const client = clientWith(STEPS);
    const first = render(<GuideRail client={client} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-rail"));
    fireEvent.click(screen.getByTestId("guide-hide"));
    expect(screen.queryByTestId("guide-rail")).toBeNull();
    expect(screen.getByTestId("guide-pill").textContent).toContain("4");

    first.unmount();
    render(<GuideRail client={client} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-pill"));
    expect(screen.queryByTestId("guide-rail")).toBeNull();

    fireEvent.click(screen.getByTestId("guide-pill"));
    await waitFor(() => screen.getByTestId("guide-rail"));
  });

  it("renders nothing at all when the engine has no step to offer", async () => {
    const { container } = render(<GuideRail client={clientWith([])} projectId="p" />);
    await new Promise((r) => setTimeout(r, 20));
    expect(container.innerHTML).toBe("");
  });
});
