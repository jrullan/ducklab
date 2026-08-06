import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RunLauncher } from "./RunLauncher";
import type { Duckling } from "../api/client";

const fleet = [
  { id: "pato-atom", provider: "aitopatom", model: "q" },
  { id: "pato-sonnet", provider: "openrouter", model: "s" },
] as Duckling[];

// A combination of models that works is a finding, and re-ticking the same
// boxes on every run is how a finding gets lost.
describe("the run launcher's saved line-ups", () => {
  it("fills the boxes when a mode with a saved line-up is picked", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        preferred={{ pair: ["pato-sonnet", "pato-atom"] }}
        onLaunch={onLaunch}
      />,
    );
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "pair" } });
    fireEvent.click(screen.getByTestId("run-start"));

    // In the saved order: pair takes the first as implementer and the second as
    // reviewer, so the order is the preference, not a set.
    expect(onLaunch).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "pair", ducklings: ["pato-sonnet", "pato-atom"] }),
    );
  });

  // Clearing them for a mode with no line-up would throw away a selection the
  // person had just made by hand.
  it("leaves a hand-made selection alone when the mode has none saved", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher ducklings={fleet} preferred={{ pair: ["pato-sonnet"] }} onLaunch={onLaunch} />,
    );
    fireEvent.change(screen.getByTestId("run-seat-0"), { target: { value: "pato-atom" } });
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "tournament" } });
    fireEvent.click(screen.getByTestId("run-start"));

    expect(onLaunch).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "tournament", ducklings: ["pato-atom"] }),
    );
  });

  it("tells its caller as the boxes change, not only on launch", () => {
    const onDucklingsChange = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        preferred={{ split: ["pato-atom"] }}
        onLaunch={() => {}}
        onDucklingsChange={onDucklingsChange}
      />,
    );
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "split" } });
    expect(onDucklingsChange).toHaveBeenCalledWith(["pato-atom"]);
  });
});

// The person deciding how to run something is deciding what to spend, and that
// number used to live in Reports, consulted after the money was gone.
describe("the launcher's cost estimates", () => {
  it("shows each mode's measured average beside it", () => {
    render(
      <RunLauncher
        ducklings={fleet}
        estimates={{ pair: { usd: 0.87, runs: 3 }, solo: { usd: 0.1, runs: 2 } }}
        onLaunch={() => {}}
      />,
    );
    const options = [...screen.getByTestId("run-mode").querySelectorAll("option")].map(
      (o) => o.textContent,
    );
    expect(options.find((o) => o?.startsWith("pair"))).toContain("~$0.29");
    expect(options.find((o) => o?.startsWith("solo"))).toContain("~$0.05");
  });

  // A mode never run here has no number, and inventing one would be worse.
  it("stays quiet about modes with no history", () => {
    render(<RunLauncher ducklings={fleet} estimates={{}} onLaunch={() => {}} />);
    const options = [...screen.getByTestId("run-mode").querySelectorAll("option")].map(
      (o) => o.textContent,
    );
    for (const o of options) expect(o).not.toContain("$");
  });
});
