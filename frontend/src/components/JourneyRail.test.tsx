import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { JourneyRail, useJourney } from "./JourneyRail";
import type { EngineClient, Journey } from "../api/client";

const triaged: Journey = {
  ref: "B-012",
  kind: "bug",
  rungs: [
    { id: "open", label: "reported", state: "done", at: "2026-08-28T01:51:00Z", actor: "engine" },
    { id: "triaged", label: "triaged", state: "current" },
    { id: "in_progress", label: "task", state: "next" },
    { id: "fixed", label: "fixed", state: "later" },
    { id: "verified", label: "verified", state: "later" },
  ],
  steps: [{ id: "promote", action: "Make it a task", reason: "it is classified and waiting for a decision", kind: "bug", ref: "B-012" }],
  door: { id: "promote", action: "Make it a task", reason: "it is classified and waiting for a decision", kind: "bug", ref: "B-012" },
};

describe("JourneyRail", () => {
  // The rail lights where you are and where the door leads, and states the
  // door in words — the host's control is the button.
  it("renders the ladder with the current rung lit and the door named", () => {
    render(<JourneyRail journey={triaged} />);
    const rungs = screen.getByTestId("journey-rail-rungs");
    expect(rungs.querySelector('[data-rung="open"]')?.getAttribute("data-state")).toBe("done");
    expect(rungs.querySelector('[data-rung="triaged"]')?.getAttribute("data-state")).toBe("current");
    expect(rungs.querySelector('[data-rung="in_progress"]')?.getAttribute("data-state")).toBe("next");
    expect(screen.getByTestId("journey-rail-door")).toHaveTextContent("next: Make it a task");
    expect(screen.getByTestId("journey-rail-door")).toHaveTextContent("waiting for a decision");
  });

  it("renders nothing without a journey", () => {
    const { container } = render(<JourneyRail journey={null} />);
    expect(container.querySelector('[data-testid="journey-rail"]')).toBeNull();
  });

  // Older fakes and older engines have no nextFor: the rail stays absent
  // instead of throwing inside every board test that mocks a client.
  it("useJourney tolerates a client without nextFor", async () => {
    const client = {} as unknown as EngineClient;
    function Host() {
      const j = useJourney(client, "p", "B-012", "triaged");
      return <JourneyRail journey={j} />;
    }
    const { container } = render(<Host />);
    await waitFor(() => expect(container.querySelector('[data-testid="journey-rail"]')).toBeNull());
  });

  it("useJourney fetches the ref's journey", async () => {
    const nextFor = vi.fn(() => Promise.resolve(triaged));
    const client = { nextFor } as unknown as EngineClient;
    function Host() {
      const j = useJourney(client, "p", "B-012", "triaged");
      return <JourneyRail journey={j} />;
    }
    render(<Host />);
    await waitFor(() => expect(screen.getByTestId("journey-rail-door")).toHaveTextContent("Make it a task"));
    expect(nextFor).toHaveBeenCalledWith("p", "B-012");
  });
});
