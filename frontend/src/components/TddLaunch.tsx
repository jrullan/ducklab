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
  testRoster,
  buildRoster,
  onPhaseModeChange,
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
  /** Backward-compatible common roster for hosts that do not resolve per phase. */
  roster?: readonly RosterEntry[];
  testRoster?: readonly RosterEntry[];
  buildRoster?: readonly RosterEntry[];
  onPhaseModeChange?: (phase: "test" | "build", mode: string) => void;
}) {
  // Opening seats are empty: omitted ducklings leave the resolved roster in charge.
  const [testCfg, setTestCfg] = useState<PhaseConfig>(() => ({ mode: phaseDefaults.test, ducklings: [] }));
  // Leave an untouched build mode empty so RunStart resolves its setting.
  const [buildCfg, setBuildCfg] = useState<PhaseConfig>(() => ({ mode: "", ducklings: [] }));
  // Changing a mode re-resolves from the canonical roster. Saved Settings
  // line-ups are not launch defaults anymore; picks remain run-local.
  const reseat = (set: (c: PhaseConfig) => void) => (next: PhaseConfig, _prevMode: string) => {
    set(next);
  };
  const [tuning, setTuning] = useState(false);
  const resolvedTestRoster = Array.isArray(testRoster) ? testRoster : Array.isArray(roster) ? roster : undefined;
  const resolvedBuildRoster = Array.isArray(buildRoster) ? buildRoster : Array.isArray(roster) ? roster : undefined;
  // One line saying what a click does: modes and who is seated (the picked
  // duckling, else the roster's), and the build's measured cost when known.
  const seatFor = (cfg: PhaseConfig, role: string, index: number, roster?: readonly RosterEntry[]) =>
    cfg.ducklings[index] || roster?.find((r) => r.role === role)?.duckling || "roster";
  const buildDisplayMode = buildCfg.mode || phaseDefaults.build;
  const est = estimates?.[buildDisplayMode];
  const avg = est && est.runs > 0 ? est.usd / est.runs : undefined;
  const summary = `test: ${testCfg.mode} · ${seatFor(testCfg, "implementer", 0, resolvedTestRoster)} → build: ${buildDisplayMode} · ${seatFor(buildCfg, "implementer", 0, resolvedBuildRoster)}${buildDisplayMode === "pair" ? ` + ${seatFor(buildCfg, "reviewer", 2, resolvedBuildRoster)}` : ""}${avg !== undefined ? ` · ~$${avg.toFixed(2)}` : ""}`;
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
              onChange={(next) => {
                reseat(setTestCfg)(next, testCfg.mode);
                if (next.mode !== testCfg.mode) onPhaseModeChange?.("test", next.mode);
              }}
              modes={["solo", "pair"]}
              roster={resolvedTestRoster}
              defaultProvenance="roster"
            />
          </div>
          <div>
            <div className="text-xs font-medium text-ink-muted">2 · build until it passes</div>
            <LaunchConfig
              measured={measured}
              ducklings={ducklings}
              value={buildCfg}
              onChange={(next) => {
                reseat(setBuildCfg)(next, buildCfg.mode);
                if (next.mode !== buildCfg.mode) onPhaseModeChange?.("build", next.mode);
              }}
              estimates={estimates}
              showTokens
              roster={resolvedBuildRoster}
              defaultMode={phaseDefaults.build}
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
