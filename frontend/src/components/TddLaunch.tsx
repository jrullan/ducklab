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
  embedded = false,
  notePlaceholder = "Anything this run should know?",
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
  /** A host card already supplies the boundary; avoid a card inside a card. */
  embedded?: boolean;
  /** State-specific guidance. The no-change retry passes its own prompt. */
  notePlaceholder?: string;
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
  // A note for this run — the channel the no-changes brake demands, and the
  // gate review's "address the findings" rides too. Owned here, folded under
  // the summary; most launches carry nothing extra.
  const [note, setNote] = useState("");
  const resolvedTestRoster = Array.isArray(testRoster) ? testRoster : Array.isArray(roster) ? roster : undefined;
  const resolvedBuildRoster = Array.isArray(buildRoster) ? buildRoster : Array.isArray(roster) ? roster : undefined;
  // One line saying what a click does: modes and who is seated (the picked
  // duckling, else the roster's), and the build's measured cost when known.
  const seatFor = (cfg: PhaseConfig, role: string, index: number, roster?: readonly RosterEntry[]) =>
    cfg.ducklings[index] || roster?.find((r) => r.role === role)?.duckling || "roster";
  const buildDisplayMode = buildCfg.mode || phaseDefaults.build;
  const average = (mode: string) => {
    const estimate = estimates?.[mode];
    return estimate && estimate.runs > 0 ? estimate.usd / estimate.runs : undefined;
  };
  const testAvg = average(testCfg.mode);
  const buildAvg = average(buildDisplayMode);
  const estimate = testAvg !== undefined && buildAvg !== undefined
    ? `estimated chain ~$${(testAvg + buildAvg).toFixed(2)} (test ~$${testAvg.toFixed(2)} + build ~$${buildAvg.toFixed(2)})`
    : buildAvg !== undefined
      ? `build history ~$${buildAvg.toFixed(2)} · test has no history yet`
      : testAvg !== undefined
        ? `test history ~$${testAvg.toFixed(2)} · build has no history yet`
        : "no history yet for this chain";
  const summary = `test: ${testCfg.mode} · ${seatFor(testCfg, "implementer", 0, resolvedTestRoster)} → build: ${buildDisplayMode} · ${seatFor(buildCfg, "implementer", 0, resolvedBuildRoster)}${buildDisplayMode === "pair" ? ` + ${seatFor(buildCfg, "reviewer", 2, resolvedBuildRoster)}` : ""} · ${estimate}`;
  // The run's note rides the launch, not one phase: it answers the no-changes
  // brake's demand ("tell the next run what changed") and carries a gate
  // review's findings into the build. One note for one decision.
  const withNote = <C extends PhaseConfig>(cfg: C): C => ({ ...cfg, note: note.trim() || undefined });
  return (
    <div className={embedded ? "space-y-3" : "space-y-2 rounded border border-hairline p-2"} data-testid="tdd-block">
      {/* The common case is one click; the button leads. Seats and caps are
          the exception and fold beneath. */}
      <button
        type="button"
        onClick={() => onTdd(withNote(testCfg), withNote(buildCfg))}
        disabled={busy}
        data-testid="tdd-start"
        className="w-full rounded border border-good bg-good px-4 py-2 text-sm font-medium text-page disabled:opacity-40 sm:w-auto sm:min-w-64"
      >
        {busy ? "Starting…" : "Test first → Build"}
      </button>
      <div className="flex items-center gap-2 text-xs text-ink-muted">
        <span className="min-w-0 truncate" data-testid="tdd-summary" title={summary}>{summary}</span>
        <button type="button" data-testid="tdd-tune" aria-expanded={tuning} onClick={() => setTuning((v) => !v)} className="ml-auto shrink-0 underline hover:text-ink">{tuning ? "hide" : "adjust seats & caps"}</button>
      </div>
      <label className="block text-xs text-ink-muted">
        a note for this run
        <textarea
          aria-label="note"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          rows={2}
          placeholder={notePlaceholder}
          className="mt-1 w-full rounded border border-hairline bg-surface2 px-2 py-1.5 text-sm text-ink placeholder:text-ink-muted"
        />
      </label>
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
          onClick={() => onTestOnly(withNote(testCfg))}
          disabled={busy}
          data-testid="test-first-start"
          title="Write the failing test only; you accept it before any build"
          className="rounded border border-hairline px-2 py-1 text-ink-secondary hover:bg-surface2 disabled:opacity-40"
        >
          test only
        </button>
        <button
          type="button"
          onClick={() => onBuildOnly(withNote(buildCfg))}
          disabled={busy}
          data-testid="build-only"
          title="Build without a new test — the gate still judges the whole suite"
          className="rounded border border-hairline px-2 py-1 text-ink-secondary hover:bg-surface2 disabled:opacity-40"
        >
          build only
        </button>
      </div>
    </div>
  );
}
