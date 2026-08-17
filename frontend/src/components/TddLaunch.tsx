import { useState } from "react";
import type { Duckling } from "../api/client";
import { LaunchConfig, type PhaseConfig, type ModeEstimates } from "./RunLauncher";
import type { MeasuredSpend } from "./SeatChips";

/** The TDD chain's launcher: two phases, each with its own mode and seats,
 * one click for the whole intent.
 *
 * Shared by the board's task rail and Now's "ready to start" card, because
 * the same task offered "Test first → Build" in one place and a bare Run in
 * the other — and a person who follows the suggestion should not get a worse
 * workflow than one who goes looking. Owns the two phase configs (opening on
 * the Settings defaults with each mode's saved line-up seated) so its hosts
 * only say what to do when a button is pressed. */
export function TddLaunch({
  ducklings,
  preferred,
  phaseDefaults,
  estimates,
  busy,
  onTdd,
  onTestOnly,
  onBuildOnly,
  measured,
}: {
  ducklings: readonly Duckling[];
  preferred: Record<string, string[]>;
  phaseDefaults: { build: string; test: string };
  estimates?: ModeEstimates;
  busy: boolean;
  onTdd: (test: PhaseConfig, build: PhaseConfig) => void;
  onTestOnly: (test: PhaseConfig) => void;
  onBuildOnly: (build: PhaseConfig) => void;
  measured?: MeasuredSpend;
}) {
  // Opening seats are empty: omitted ducklings leave the resolved roster in charge.
  const [testCfg, setTestCfg] = useState<PhaseConfig>(() => ({ mode: phaseDefaults.test, ducklings: [] }));
  const [buildCfg, setBuildCfg] = useState<PhaseConfig>(() => ({ mode: phaseDefaults.build, ducklings: [] }));
  // Changing a mode re-seats from that mode's saved line-up.
  const reseat = (set: (c: PhaseConfig) => void) => (next: PhaseConfig, prevMode: string) => {
    if (next.mode !== prevMode) {
      const saved = preferred[next.mode];
      set(saved && saved.length > 0
        ? { ...next, ducklings: [...saved], seatProvenance: saved.map(() => "Settings") }
        : next);
      return;
    }
    set(next);
  };
  return (
    <div className="space-y-2 rounded border border-hairline p-2" data-testid="tdd-block">
      <div>
        <div className="text-xs font-medium text-ink-muted">1 · write the failing test</div>
        <LaunchConfig
          measured={measured}
          ducklings={ducklings}
          value={testCfg}
          onChange={(next) => reseat(setTestCfg)(next, testCfg.mode)}
          modes={["solo", "pair"]}
          defaultProvenance="roster"
        />
      </div>
      <div>
        <div className="text-xs font-medium text-ink-muted">2 · build until it passes</div>
        <LaunchConfig
          measured={measured}
          ducklings={ducklings}
          value={buildCfg}
          onChange={(next) => reseat(setBuildCfg)(next, buildCfg.mode)}
          estimates={estimates}
          showTokens
          defaultProvenance="roster"
        />
      </div>
      <button
        type="button"
        onClick={() => onTdd(testCfg, buildCfg)}
        disabled={busy}
        data-testid="tdd-start"
        className="w-full rounded border border-good px-3 py-1.5 text-sm text-good disabled:opacity-40"
      >
        {busy ? "Starting…" : "Test first → Build"}
      </button>
      <div className="flex items-center gap-3 text-xs">
        <button
          type="button"
          onClick={() => onTestOnly(testCfg)}
          disabled={busy}
          data-testid="test-first-start"
          title="Write the failing test only; you accept it before any build"
          className="text-ink-muted underline disabled:opacity-40"
        >
          test only
        </button>
        <span className="text-ink-muted">·</span>
        <button
          type="button"
          onClick={() => onBuildOnly(buildCfg)}
          disabled={busy}
          data-testid="build-only"
          title="Build without a new test — the gate still judges the whole suite"
          className="text-ink-muted underline disabled:opacity-40"
        >
          build only
        </button>
      </div>
    </div>
  );
}
