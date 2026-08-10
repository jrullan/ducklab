import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { GuidePanel } from "./GuidePanel";
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
  // The inbox's own kinds — the guide must NOT repeat them.
  { id: "answer-run", action: "Answer the question", reason: "a model waits", kind: "run", ref: "r-1" },
  { id: "test-first", action: "Start T-003", reason: "next task", kind: "task", ref: "T-003" },
  // The lifecycle the inbox never surfaces — the guide's whole job.
  { id: "plan", action: "Plan the work — break the spec into tasks", reason: "3 spec section(s) are not yet built and no plan exists", kind: "stage", ref: "plan" },
  { id: "triage", action: "Triage the open bugs", reason: "2 bug(s) are open", kind: "bug" },
];

describe("the guide panel", () => {
  beforeEach(() => {
    localStorage.clear();
    useRuns.setState({ runs: {}, events: {}, deltas: {}, reasoning: {}, spend: {} });
  });

  // Paused runs are Now's waiting cards; the next task is Now's launcher. A
  // guide that repeats them is a second inbox — it shows only the lifecycle.
  it("shows the lifecycle steps and never the inbox's", async () => {
    render(<GuidePanel client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-panel"));
    const steps = screen.getAllByTestId("guide-step").map((s) => s.textContent ?? "");
    expect(steps).toHaveLength(2);
    expect(steps[0]).toContain("Plan the work");
    expect(steps[0]).toContain("no plan exists"); // the why rides along
    expect(steps[1]).toContain("Triage");
    expect(screen.queryByText("Answer the question")).toBeNull();
    expect(screen.queryByText("Start T-003")).toBeNull();
  });

  it("links each step to the surface whose buttons already do it", async () => {
    render(<GuidePanel client={clientWith(STEPS)} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-panel"));
    const links = screen.getAllByTestId("guide-step").map((s) => s.querySelector("a")?.getAttribute("href"));
    expect(links[0]).toBe("#/cycle/plan");
    expect(links[1]).toBe("#/board/bugs");
  });

  // Guidance for the first weeks, a pill for after — and the choice survives
  // a reload.
  it("dismisses to a pill, and stays dismissed", async () => {
    const client = clientWith(STEPS);
    const first = render(<GuidePanel client={client} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-panel"));
    fireEvent.click(screen.getByTestId("guide-hide"));
    expect(screen.queryByTestId("guide-panel")).toBeNull();
    expect(screen.getByTestId("guide-pill")).toBeTruthy();

    first.unmount();
    render(<GuidePanel client={client} projectId="p" />);
    await waitFor(() => screen.getByTestId("guide-pill"));
    expect(screen.queryByTestId("guide-panel")).toBeNull();

    // The pill brings it back.
    fireEvent.click(screen.getByTestId("guide-pill"));
    await waitFor(() => screen.getByTestId("guide-panel"));
  });

  it("renders nothing at all when the lifecycle has no step to offer", async () => {
    const { container } = render(
      <GuidePanel client={clientWith([STEPS[0]!, STEPS[1]!])} projectId="p" />,
    );
    await waitFor(() => expect(container.innerHTML).toBe(""));
  });
});
