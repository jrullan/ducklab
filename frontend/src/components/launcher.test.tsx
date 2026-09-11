import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RunLauncher } from "./RunLauncher";
import { TddLaunch } from "./TddLaunch";
import type { Duckling } from "../api/client";

const fleet = [
  { id: "pato-atom", provider: "aitopatom", model: "q" },
  { id: "pato-sonnet", provider: "openrouter", model: "s" },
] as Duckling[];

// Saved Settings line-ups are a preference echo, not launch defaults: seeding
// one as a pick sent it as an explicit request that overrode the project's
// roster (B-394). Seats come from the resolved roster; untouched seats travel
// empty so the engine decides; only a hand-made pick is a request.
describe("the run launcher's saved line-ups", () => {
  it("does not fill the boxes from a saved line-up when a mode is picked", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        preferred={{ pair: ["pato-sonnet", "pato-atom"] }}
        onLaunch={onLaunch}
      />,
    );
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "pair" } });
    const chips = screen.getAllByTestId("seat-chip").map((c) => c.textContent).join(" | ");
    expect(chips).not.toContain("pato-sonnet");
    expect(chips).not.toContain("picked now");
    fireEvent.click(screen.getByTestId("run-start"));

    expect(onLaunch).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "pair", ducklings: [] }),
    );
  });

  // Clearing them for a mode with no roster would throw away a selection the
  // person had just made by hand.
  it("leaves a hand-made selection alone when the mode changes without a roster", () => {
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

  it("tells its caller as the boxes change by hand, not only on launch", () => {
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
    expect(onDucklingsChange).not.toHaveBeenCalledWith(["pato-atom"]);
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "pato-atom" } });
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

describe("notes on shared launch controls", () => {
  it("forwards a note entered in the plain launcher", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} onLaunch={onLaunch} />);

    fireEvent.click(screen.getByTestId("run-note-toggle"));
    fireEvent.change(screen.getByTestId("run-note"), { target: { value: "  the tree changed after the last run  " } });
    fireEvent.click(screen.getByTestId("run-start"));

    expect(onLaunch).toHaveBeenCalledWith(expect.objectContaining({ note: "the tree changed after the last run" }));
  });

  it("forwards the TDD note into the chained build request", () => {
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

    fireEvent.change(screen.getByLabelText("note"), { target: { value: "the tree changed after the no-change answer" } });
    fireEvent.click(screen.getByTestId("tdd-start"));

    expect(onTdd).toHaveBeenCalledWith(
      expect.any(Object),
      expect.objectContaining({ note: "the tree changed after the no-change answer" }),
    );
  });

});

describe("the launch modal", () => {
  it("opens from the launch trigger and preselects the requested mode", () => {
    render(<RunLauncher ducklings={fleet} initialMode="pair" initiallyOpen={false} onLaunch={() => {}} />);
    expect(screen.queryByTestId("launch-modal")).toBeNull();
    fireEvent.click(screen.getByTestId("launch-modal-trigger"));
    expect(screen.getByTestId("launch-modal")).toBeTruthy();
    expect(screen.getByTestId("mode-card-pair")).toHaveAttribute("aria-pressed", "true");
  });

  it("shows history estimates and an honest fallback on mode cards", () => {
    render(<RunLauncher ducklings={fleet} estimates={{ pair: { usd: 0.87, runs: 3 } }} onLaunch={() => {}} />);
    expect(screen.getByTestId("mode-card-pair").textContent).toContain("estimated $0.23–$0.35 per run");
    expect(screen.getByTestId("mode-card-split").textContent).toContain("no history yet for this shape");
  });

  it("displays seats prefilled from the roster without asking for a model name", () => {
    const roster = [
      { role: "implementer", duckling: "pato-atom", source: "project mode seat" },
      { role: "reviewer", duckling: "pato-sonnet", source: "global mode seat" },
    ];
    render(<RunLauncher ducklings={fleet} initialMode="pair" roster={roster} onLaunch={() => {}} />);
    expect(screen.getByText("pato-atom")).toBeTruthy();
    expect(screen.getByText("pato-sonnet")).toBeTruthy();
    expect(screen.getByText(/never need to type a model name/)).toBeTruthy();
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
describe("the unattended consent", () => {
  it("discloses advisor auto-answers in the unattended tooltip", () => {
    render(<RunLauncher ducklings={fleet} onLaunch={() => {}} />);
    expect(screen.getByTestId("run-yolo").closest("label")).toHaveAttribute("title", expect.stringContaining("ask_human questions are auto-answered by the question advisor"));
  });
});

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
