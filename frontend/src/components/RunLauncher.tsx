import { useState } from "react";
import type { Duckling } from "../api/client";
import { money } from "../lib/format";

/** What a mode has cost here before: total dollars over how many runs, from
 * the project's own history. */
export type ModeEstimates = Record<string, { usd: number; runs: number }>;

/** What a launch asks for. Anything unset falls back to the engine's defaults. */
export type LaunchOpts = { mode: string; ducklings: string[]; maxTokens?: number };

export const MODES = ["solo", "pair", "tournament", "split"] as const;

/**
 * The controls for starting a run: which mode, which ducklings, how many tokens.
 *
 * Shared because these belong in two places. Starting work happens on the
 * board, but the moment you most want to change one of these and go again is
 * when you are looking at a run that just failed — and that meant leaving for
 * the board and finding the task by hand, which is enough friction that people
 * re-run with the same settings instead of the ones they meant.
 */
export function RunLauncher({
  ducklings,
  initialMode = "solo",
  initialDucklings = [],
  preferred,
  estimates,
  label = "Build it",
  busy = false,
  onLaunch,
  onDucklingsChange,
}: {
  ducklings: readonly Duckling[];
  initialMode?: string;
  initialDucklings?: readonly string[];
  /** The saved line-up per mode. Picking a mode fills the boxes with it: a
   * combination that works is a finding, and re-ticking the same boxes on every
   * run is how a finding gets lost. */
  preferred?: Record<string, string[]>;
  /** Average cost per mode from this project's runs. Shown beside each mode:
   * the person deciding how to run something is deciding what to spend, and
   * that number used to live in Reports, consulted after the money was gone
   * (docs/ux-evaluation.md F8). */
  estimates?: ModeEstimates;
  label?: string;
  busy?: boolean;
  onLaunch: (opts: LaunchOpts) => void;
  /** Reported as it changes, not only on launch: a caller may have its own
   * buttons that act on the selection — writing the test first, for one. */
  onDucklingsChange?: (ids: string[]) => void;
}) {
  const [mode, setMode] = useState(initialMode);
  const [chosen, setChosen] = useState<string[]>([...initialDucklings]);
  const [maxTokens, setMaxTokens] = useState("");

  return (
    <div className="space-y-2" data-testid="run-launcher">
      <div className="flex flex-wrap items-center gap-2">
        <select
          aria-label="mode"
          data-testid="run-mode"
          value={mode}
          onChange={(e) => {
            const next = e.target.value;
            setMode(next);
            // Only when that mode has a saved line-up. Clearing the boxes for a
            // mode with none would throw away a selection the person just made.
            const saved = preferred?.[next];
            if (saved && saved.length > 0) {
              setChosen([...saved]);
              onDucklingsChange?.([...saved]);
            }
          }}
          className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
        >
          {MODES.map((m) => {
            const e = estimates?.[m];
            const avg = e && e.runs > 0 ? e.usd / e.runs : undefined;
            return (
              <option key={m} value={m}>
                {m}
                {avg !== undefined ? ` · ~${money(avg)}` : ""}
              </option>
            );
          })}
        </select>
        {/* A run that hits the token ceiling fails with a number the person
            starting it could not see or change. Raised for this run only; the
            default lives in Settings. */}
        <input
          aria-label="token budget"
          data-testid="run-max-tokens"
          placeholder="tokens (default)"
          value={maxTokens}
          onChange={(e) => setMaxTokens(e.target.value)}
          className="w-32 rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
        />
        <button
          type="button"
          onClick={() =>
            onLaunch({ mode, ducklings: chosen, maxTokens: Number(maxTokens) || undefined })
          }
          disabled={busy}
          data-testid="run-start"
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          {busy ? "Starting…" : label}
        </button>
      </div>

      {/* Order matters: tournament and split assign them positionally, so the
          order the boxes were ticked is the order they are sent. */}
      {ducklings.length > 1 && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-ink-secondary">
          {ducklings.map((d) => (
            <label key={d.id} className="flex items-center gap-1">
              <input
                type="checkbox"
                data-testid={`run-duckling-${d.id}`}
                checked={chosen.includes(d.id)}
                onChange={(e) =>
                  setChosen((cur) => {
                    const next = e.target.checked
                      ? [...cur, d.id]
                      : cur.filter((x) => x !== d.id);
                    onDucklingsChange?.(next);
                    return next;
                  })
                }
              />
              {d.id}
            </label>
          ))}
        </div>
      )}
    </div>
  );
}
