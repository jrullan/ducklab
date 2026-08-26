import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RunLauncher, LaunchConfig, type PhaseConfig } from "./RunLauncher";
import { TddLaunch } from "./TddLaunch";
import type { Duckling, RosterEntry } from "../api/client";

// B-129: T-113's runs seated implementer/reviewer with ducklings from OTHER
// roles. The launcher treats the roster entries array as if it were already
// in mode-seat order, twice over:
//
//   1. Prefill maps POSITIONALLY from role-alphabetical entries. The engine
//      serves GET /v1/projects/{id}/roster?mode=pair sorted by role
//      (internal/service/roster.go), so entry[0] is the ADVISOR and entry[1]
//      the ARCHITECT — and for pair, seat 0 is the implementer and seat 1
//      the reviewer (frontend/src/lib/seats.ts seatLabel). Seeding chips by
//      array position seated the advisor as implementer and the architect as
//      reviewer: runs 536j/lxa7.
//   2. chosen.filter(Boolean) on launch compacts defaulted seats away, so
//      every later entry slides forward: consultant and implementer landed in
//      seats 0 and 1: runs eopp/j7xi. The engine's contract is positional
//      with "" meaning "resolve this seat from the roster"
//      (internal/service/testfirst.go:417-422), so the launcher must send ""
//      for a defaulted seat, not drop it.
//
// These tests pin the behaviour: seed by ROLE (each entry into the seat its
// role names), and preserve seat positions on the wire.

const fleet = [
  { id: "qwen38-max", provider: "fake", model: "q" },
  { id: "k3", provider: "fake", model: "k" },
  { id: "luna", provider: "fake", model: "l" },
  { id: "terra", provider: "fake", model: "t" },
  { id: "j9", provider: "fake", model: "j" },
  { id: "glm52", provider: "fake", model: "g" },
  { id: "s1", provider: "fake", model: "s" },
  { id: "t7", provider: "fake", model: "t7" },
] as Duckling[];

// The full 8-role roster, in the role-alphabetical order the engine serves
// it. terra is the implementer (a project mode seat pin), glm52 the reviewer
// (the global mode seat): the pair the launcher must seat.
const pairRoster: RosterEntry[] = [
  { role: "advisor", duckling: "qwen38-max", source: "global mode seat" },
  { role: "architect", duckling: "k3", source: "global mode seat" },
  { role: "consultant", duckling: "luna", source: "global mode seat" },
  { role: "implementer", duckling: "terra", source: "project mode seat" },
  { role: "judge", duckling: "j9", source: "global mode seat" },
  { role: "reviewer", duckling: "glm52", source: "global mode seat" },
  { role: "scribe", duckling: "s1", source: "global mode seat" },
  { role: "triager", duckling: "t7", source: "global mode seat" },
];

// The same roster, but with no duckling resolved for the pair seats: both
// must come up "default" and stay empty on the wire.
const unseatedPairRoster: RosterEntry[] = pairRoster.map((e) =>
  e.role === "implementer" || e.role === "reviewer" ? { ...e, duckling: "" } : e,
);

function launchCall(onLaunch: ReturnType<typeof vi.fn>) {
  expect(onLaunch).toHaveBeenCalledTimes(1);
  return onLaunch.mock.calls[0]![0] as { mode: string; ducklings: string[]; seats?: Record<string, string> };
}

describe("the run launcher seating from the canonical roster", () => {
  it("seats pair from the implementer/reviewer entries, not the first two entries", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        initialMode="pair"
        roster={pairRoster}
        onLaunch={onLaunch}
      />,
    );
    fireEvent.click(screen.getByTestId("run-start"));

    // Seat 0 is the implementer, seat 1 the reviewer. Positional seeding
    // would send the advisor and the architect — entries[0] and entries[1].
    expect(launchCall(onLaunch)).toEqual(
      expect.objectContaining({ mode: "pair", ducklings: [] }),
    );
  });

  it("seats solo from the implementer entry, not the advisor", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        initialMode="solo"
        roster={pairRoster}
        onLaunch={onLaunch}
      />,
    );
    fireEvent.click(screen.getByTestId("run-start"));

    expect(launchCall(onLaunch)).toEqual(
      expect.objectContaining({ mode: "solo", ducklings: [] }),
    );
  });

  it("re-seats by role when the mode changes and the roster re-resolves", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        initialMode="solo"
        roster={pairRoster}
        onLaunch={onLaunch}
      />,
    );
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "pair" } });
    fireEvent.click(screen.getByTestId("run-start"));

    expect(launchCall(onLaunch)).toEqual(
      expect.objectContaining({ mode: "pair", ducklings: [] }),
    );
  });

  it("sends an empty seat for a defaulted seat instead of compacting the list", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        initialMode="pair"
        roster={pairRoster}
        onLaunch={onLaunch}
      />,
    );
    // Leave the implementer on "default"; pick the reviewer by hand. The
    // engine resolves an empty position from the roster, so the request must
    // carry ["", "luna"] — filtering the empty seat out would slide luna
    // into the implementer's seat.
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "" } });
    fireEvent.click(screen.getAllByTestId("seat-chip")[2]!);
    fireEvent.change(screen.getByTestId("seat-pick-2"), { target: { value: "luna" } });
    fireEvent.click(screen.getByTestId("run-start"));

    expect(launchCall(onLaunch)).toEqual(
      expect.objectContaining({ mode: "pair", ducklings: ["", "qwen38-max", "luna"] }),
    );
  });

  it("keeps all seats empty when left on default", () => {
    const onLaunch = vi.fn();
    render(
      <RunLauncher
        ducklings={fleet}
        initialMode="pair"
        roster={unseatedPairRoster}
        onLaunch={onLaunch}
      />,
    );
    fireEvent.click(screen.getByTestId("run-start"));

    // Nothing picked: the whole line-up defers to the engine's roster.
    // The advisor is now a real pair seat and is represented as default too.
    const launch = launchCall(onLaunch);
    expect(launch.ducklings).toEqual([]);
  });
});

describe("the chained launcher's per-phase seating", () => {
  it("seats each phase from its role's roster entry, not by array position", () => {
    const onTdd = vi.fn();
    render(
      <TddLaunch
        ducklings={fleet}
        preferred={{}}
        phaseDefaults={{ test: "solo", build: "pair" }}
        busy={false}
        roster={pairRoster}
        onTdd={onTdd}
        onTestOnly={() => {}}
        onBuildOnly={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("tdd-tune"));
    fireEvent.click(screen.getByTestId("tdd-start"));

    expect(onTdd).toHaveBeenCalledTimes(1);
    const [testCfg, buildCfg] = onTdd.mock.calls[0]! as [PhaseConfig, PhaseConfig];
    expect(testCfg.ducklings[0]).toBe("terra");
    expect(testCfg.ducklings[1]).toBe("qwen38-max");
    expect(buildCfg.ducklings[0]).toBe("terra");
    expect(buildCfg.ducklings[1]).toBe("qwen38-max");
    expect(buildCfg.ducklings[2]).toBe("glm52");
  });
});

describe("the seat configurator's roster prefill", () => {
  it("seeds each seat from the entry whose role the seat carries", () => {
    const onChange = vi.fn();
    const value: PhaseConfig = { mode: "pair", ducklings: [] };
    render(
      <LaunchConfig
        ducklings={fleet}
        value={value}
        onChange={onChange}
        roster={pairRoster}
      />,
    );

    // The prefill effect reports the seeded seats upward. Positional seeding
    // would report the first two entries — the advisor and the architect.
    expect(onChange).toHaveBeenCalled();
    const seeded = onChange.mock.calls.map((c) => (c[0] as PhaseConfig).ducklings);
    expect(seeded[seeded.length - 1]).toEqual(["terra", "qwen38-max", "glm52"]);
  });
});
