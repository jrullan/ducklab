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

  it("sends only the hand-made pick, keyed by role, and never the visible roster seats", () => {
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
    // engine's positional list reads pair as [implementer, reviewer] while
    // the launcher shows [implementer, advisor, reviewer], so a positional
    // echo of the visible seats seated the advisor as reviewer (B-394). Role
    // modes send the picks keyed by role and nothing else.
    fireEvent.click(screen.getAllByTestId("seat-chip")[0]!);
    fireEvent.change(screen.getByTestId("seat-pick-0"), { target: { value: "" } });
    fireEvent.click(screen.getAllByTestId("seat-chip")[2]!);
    fireEvent.change(screen.getByTestId("seat-pick-2"), { target: { value: "luna" } });
    fireEvent.click(screen.getByTestId("run-start"));

    expect(launchCall(onLaunch)).toEqual(
      expect.objectContaining({ mode: "pair", ducklings: [], seats: { reviewer: "luna" } }),
    );
  });

  it("changing only the advisor sends only the advisor", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} initialMode="pair" roster={pairRoster} onLaunch={onLaunch} />);
    fireEvent.click(screen.getAllByTestId("seat-chip")[1]!);
    fireEvent.change(screen.getByTestId("seat-pick-1"), { target: { value: "k3" } });
    fireEvent.click(screen.getByTestId("run-start"));
    expect(launchCall(onLaunch)).toEqual(
      expect.objectContaining({ mode: "pair", ducklings: [], seats: { advisor: "k3" } }),
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

// B-394 (review): tournament's contestants and split's workers are the
// implementer seat's plural line-up. The roster response names every role, and
// projecting all eight entries made the advisor, architect, judge, scribe and
// triager contestants — then one pick sent them all as explicit participants.
describe("participant modes project the implementer line-up, not every role", () => {
  const tournamentRoster: RosterEntry[] = [
    { role: "advisor", duckling: "qwen38-max", ducklings: ["qwen38-max"], source: "global mode seat" },
    { role: "architect", duckling: "k3", ducklings: ["k3"], source: "global mode seat" },
    { role: "consultant", duckling: "luna", ducklings: ["luna"], source: "global mode seat" },
    { role: "implementer", duckling: "terra", ducklings: ["terra", "glm52"], source: "project mode seat" },
    { role: "judge", duckling: "j9", ducklings: ["j9"], source: "global mode seat" },
    { role: "reviewer", duckling: "s1", ducklings: ["s1"], source: "global mode seat" },
    { role: "scribe", duckling: "s1", ducklings: ["s1"], source: "global mode seat" },
    { role: "triager", duckling: "t7", ducklings: ["t7"], source: "global mode seat" },
  ];

  it("seats a tournament with exactly the configured contestants and sends nothing untouched", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} initialMode="tournament" roster={tournamentRoster} onLaunch={onLaunch} />);
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips).toHaveLength(2);
    expect(chips[0]!.textContent).toContain("terra");
    expect(chips[1]!.textContent).toContain("glm52");
    for (const stranger of ["qwen38-max", "k3", "j9", "t7", "luna"]) {
      expect(chips.map((c) => c.textContent).join(" | ")).not.toContain(stranger);
    }
    fireEvent.click(screen.getByTestId("run-start"));
    expect(launchCall(onLaunch)).toEqual(expect.objectContaining({ mode: "tournament", ducklings: [] }));
  });

  it("sends the whole participant list, and only participants, after one pick", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} initialMode="tournament" roster={tournamentRoster} onLaunch={onLaunch} />);
    fireEvent.click(screen.getAllByTestId("seat-chip")[1]!);
    fireEvent.change(screen.getByTestId("seat-pick-1"), { target: { value: "j9" } });
    fireEvent.click(screen.getByTestId("run-start"));
    const call = launchCall(onLaunch) as { mode: string; ducklings: string[]; seats?: unknown };
    expect(call.mode).toBe("tournament");
    expect(call.ducklings).toEqual(["terra", "j9"]);
    expect(call.seats).toBeUndefined();
  });

  it("seats split's workers from the same plural line-up", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} initialMode="split" roster={tournamentRoster} onLaunch={onLaunch} />);
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips).toHaveLength(2);
    expect(chips.map((c) => c.textContent).join(" | ")).toContain("terra");
    expect(chips.map((c) => c.textContent).join(" | ")).toContain("glm52");
    expect(chips.map((c) => c.textContent).join(" | ")).not.toContain("k3");
  });

  it("falls back to the single implementer when no plural line-up is configured", () => {
    const onLaunch = vi.fn();
    const single = tournamentRoster.map((e) => e.role === "implementer" ? { ...e, ducklings: [] } : e);
    render(<RunLauncher ducklings={fleet} initialMode="tournament" roster={single} onLaunch={onLaunch} />);
    const chips = screen.getAllByTestId("seat-chip");
    // A tournament opens two seats; the second stays default until picked.
    expect(chips).toHaveLength(2);
    expect(chips[0]!.textContent).toContain("terra");
    expect(chips[1]!.textContent).toContain("default");
    expect(chips.map((c) => c.textContent).join(" | ")).not.toContain("k3");
  });

  it("re-seats participants from the plural line-up when the mode changes from pair to tournament", () => {
    const onLaunch = vi.fn();
    render(<RunLauncher ducklings={fleet} initialMode="pair" roster={tournamentRoster} onLaunch={onLaunch} />);
    expect(screen.getAllByTestId("seat-chip")).toHaveLength(3);
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "tournament" } });
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips).toHaveLength(2);
    expect(chips[0]!.textContent).toContain("terra");
    expect(chips[1]!.textContent).toContain("glm52");
    fireEvent.click(screen.getByTestId("run-start"));
    expect(launchCall(onLaunch)).toEqual(expect.objectContaining({ mode: "tournament", ducklings: [] }));
  });

  // Provenance for a contestant/worker comes from the implementer entry that
  // seated it; "contestant N" is a label, not a roster role, and looking it
  // up by label made every participant read as "global".
  it("labels every contestant with the implementer entry's project provenance", () => {
    render(<RunLauncher ducklings={fleet} initialMode="tournament" roster={tournamentRoster} onLaunch={vi.fn()} />);
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip.textContent).toMatch(/project/i);
      expect(chip.textContent).not.toMatch(/global/i);
    }
  });

  it("labels every worker with the implementer entry's global provenance", () => {
    const global = tournamentRoster.map((e) => e.role === "implementer" ? { ...e, source: "global mode seat" } : e);
    render(<RunLauncher ducklings={fleet} initialMode="split" roster={global} onLaunch={vi.fn()} />);
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip.textContent).toMatch(/global/i);
      expect(chip.textContent).not.toMatch(/project/i);
    }
  });

  it("keeps the participant provenance after the mode changes from pair to tournament", () => {
    render(<RunLauncher ducklings={fleet} initialMode="pair" roster={tournamentRoster} onLaunch={vi.fn()} />);
    fireEvent.change(screen.getByTestId("run-mode"), { target: { value: "tournament" } });
    const chips = screen.getAllByTestId("seat-chip");
    expect(chips).toHaveLength(2);
    expect(chips[1]!.textContent).toMatch(/project/i);
  });

  function tddTuning(roster: RosterEntry[], build: string) {
    render(
      <TddLaunch
        ducklings={fleet}
        preferred={{}}
        phaseDefaults={{ test: "solo", build }}
        busy={false}
        onTdd={() => {}}
        onTestOnly={() => {}}
        onBuildOnly={() => {}}
        testRoster={roster}
        buildRoster={roster}
      />,
    );
    fireEvent.click(screen.getByTestId("tdd-tune"));
    // The build block's seats are the contestants; the test block (solo) is
    // the two role seats before them.
    return screen.getAllByTestId("seat-chip").filter((c) => /contestant|worker/.test(c.textContent ?? ""));
  }

  it("labels the TDD block's contestants with the implementer entry's project provenance", () => {
    const chips = tddTuning(tournamentRoster, "tournament");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip.textContent).toMatch(/project/i);
      expect(chip.textContent).not.toMatch(/global/i);
    }
  });

  it("labels the TDD block's workers with the implementer entry's global provenance", () => {
    const global = tournamentRoster.map((e) => e.role === "implementer" ? { ...e, source: "global mode seat" } : e);
    const chips = tddTuning(global, "split");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip.textContent).toMatch(/global/i);
      expect(chip.textContent).not.toMatch(/project/i);
    }
  });

  it("shows the projected contestants in the TDD block's tuning, not default", () => {
    render(
      <TddLaunch
        ducklings={fleet}
        preferred={{}}
        phaseDefaults={{ test: "solo", build: "tournament" }}
        busy={false}
        onTdd={() => {}}
        onTestOnly={() => {}}
        onBuildOnly={() => {}}
        testRoster={tournamentRoster}
        buildRoster={tournamentRoster}
      />,
    );
    fireEvent.click(screen.getByTestId("tdd-tune"));
    const text = screen.getAllByTestId("seat-chip").map((c) => c.textContent).join(" | ");
    expect(text).toContain("contestant 1terra");
    expect(text).toContain("contestant 2glm52");
    expect(text).not.toContain("k3");
  });
});
