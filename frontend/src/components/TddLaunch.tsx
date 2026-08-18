import { useState } from "react";
import type { Duckling, RosterEntry } from "../api/client";
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
  preferred: _preferred,
  phaseDefaults,
  estimates,
  busy,
  onTdd,
  onTestOnly,
  onBuildOnly,
  measured,
  roster,
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
  roster?: readonly RosterEntry[];
}) {
  // Opening seats are empty: omitted ducklings leave the resolved roster in charge.
  const [testCfg, setTestCfg] = useState<PhaseConfig>(() => ({ mode: phaseDefaults.test, ducklings: [] }));
  const [buildCfg, setBuildCfg] = useState<PhaseConfig>(() => ({ mode: phaseDefaults.build, ducklings: [] }));
  // Changing a mode re-resolves from the canonical roster. Saved Settings
  // line-ups are not launch defaults anymore; picks remain run-local.
  const reseat = (set: (c: PhaseConfig) => void) => (next: PhaseConfig, _prevMode: string) => {
    set(next);
  };
  const [tuning, setTuning] = useState(false);
  // One line saying what a click does: modes and who is seated (the picked
  // duckling, else the roster's), and the build's measured cost when known.
  const seatFor = (cfg: PhaseConfig, role: string, index: number) =>
    cfg.ducklings[index] || (Array.isArray(roster) ? roster.find((r) => r.role === role)?.duckling : undefined) || "roster";
  const est = estimates?.[buildCfg.mode];
  const avg = est && est.runs > 0 ? est.usd / est.runs : undefined;
  const summary = `test: ${testCfg.mode} · ${seatFor(testCfg, "implementer", 0)} → build: ${buildCfg.mode} · ${seatFor(buildCfg, "implementer", 0)}${buildCfg.mode === "pair" ? ` + ${seatFor(buildCfg, "reviewer", 1)}` : ""}${avg !== undefined ? ` · ~$${avg.toFixed(2)}` : ""}`;
  return (
    <div className="space-y-2 rounded border border-hairline p-2" data-testid="tdd-block">
      {/* The common case is one click; the button leads. Seats and caps are
          the exception and fold beneath. */}
      <button
        type="button"
        onClick={() => onTdd(testCfg, buildCfg)}
        disabled={busy}
        data-testid="tdd-start"
        className="w-full rounded border border-good px-3 py-1.5 text-sm text-good disabled:opacity-40"
      >
        {busy ? "Starting…" : "Test first → Build"}
      </button>
      <div className="flex items-center gap-2 text-xs text-ink-muted">
        <span className="min-w-0 truncate" data-testid="tdd-summary" title={summary}>{summary}</span>
        <button type="button" data-testid="tdd-tune" aria-expanded={tuning} onClick={() => setTuning((v) => !v)} className="ml-auto shrink-0 underline hover:text-ink">{tuning ? "hide" : "adjust seats & caps"}</button>
      </div>
      {tuning && (
        <div className="space-y-2 border-t border-hairline pt-2" data-testid="tdd-tuning">
          <div>
            <div className="text-xs font-medium text-ink-muted">1 · write the failing test</div>
            <LaunchConfig
              measured={measured}
              ducklings={ducklings}
              value={testCfg}
              onChange={(next) => reseat(setTestCfg)(next, testCfg.mode)}
              modes={["solo", "pair"]}
              roster={roster}
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
              roster={roster}
              defaultProvenance="roster"
            />
          </div>
        </div>
      )}
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
