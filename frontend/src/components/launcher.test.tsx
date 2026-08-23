import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RunLauncher } from "./RunLauncher";
import { TddLaunch } from "./TddLaunch";
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
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "pato-atom" } });
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
// The chained build is deliberately different from a standalone build: until
// the person chooses its mode, the engine must resolve it from settings rather
// than receive the mode displayed by the launcher as a request override.
describe("the TDD chain build mode", () => {
  it("omits the build mode until the person picks one", () => {
    const onTdd = vi.fn();
    render(
      <TddLaunch
        ducklings={fleet}
        preferred={{}}
        phaseDefaults={{ test: "solo", build: "pair" }}
        busy={false}
        onTdd={onTdd}
        onTestOnly={() => {}}
        onBuildOnly={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId("tdd-start"));
    expect(onTdd).toHaveBeenLastCalledWith(
      expect.objectContaining({ mode: "solo" }),
      expect.objectContaining({ mode: "" }),
    );
  });

  it("carries a build mode after the person explicitly chooses it", () => {
    const onTdd = vi.fn();
    render(
      <TddLaunch
        ducklings={fleet}
        preferred={{}}
        phaseDefaults={{ test: "solo", build: "pair" }}
        busy={false}
        onTdd={onTdd}
        onTestOnly={() => {}}
        onBuildOnly={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId("tdd-tune"));
    fireEvent.change(screen.getAllByTestId("cfg-mode")[1]!, { target: { value: "pair" } });
    fireEvent.click(screen.getByTestId("tdd-start"));
    expect(onTdd).toHaveBeenLastCalledWith(
      expect.any(Object),
      expect.objectContaining({ mode: "pair" }),
    );
  });
});

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

// "No cap" for the per-reply call loop, in the same words the budget lifts
// use. The wire carries -1 — the engine reads negative as "lift this run's
// cap", with the token and cost budgets still guarding every call. A number
// typed and then capped away must not survive the checkbox.
describe("the launcher's calls/reply no-cap", () => {
  it("sends -1 when checked, whatever the box said before", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} onLaunch={onLaunch} />);
    fireEvent.change(screen.getByTestId("run-agent-turns"), { target: { value: "12" } });
    fireEvent.click(screen.getByTestId("run-turns-nocap"));
    expect((screen.getByTestId("run-agent-turns") as HTMLInputElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("run-start"));
    expect(onLaunch).toHaveBeenCalledWith(expect.objectContaining({ agentTurns: -1 }));
  });

  it("keeps a typed number when unchecked", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} onLaunch={onLaunch} />);
    fireEvent.change(screen.getByTestId("run-agent-turns"), { target: { value: "12" } });
    fireEvent.click(screen.getByTestId("run-start"));
    expect(onLaunch).toHaveBeenCalledWith(expect.objectContaining({ agentTurns: 12 }));
  });
});
