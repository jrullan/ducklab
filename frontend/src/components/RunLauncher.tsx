import { useState } from "react";
import type { Duckling } from "../api/client";
import { money } from "../lib/format";
import { fixedSeats, seatLabel } from "../lib/seats";

/** What a mode has cost here before: total dollars over how many runs, from
 * the project's own history. */
export type ModeEstimates = Record<string, { usd: number; runs: number }>;

/** What a launch asks for. Anything unset falls back to the engine's defaults. */
export type LaunchOpts = { mode: string; ducklings: string[]; maxTokens?: number; note?: string; agentTurns?: number };

export const MODES = ["solo", "pair", "tournament", "split"] as const;

/** One phase's launch configuration: mode, seats, optional token ceiling. */
export type PhaseConfig = { mode: string; ducklings: string[]; maxTokens?: number; agentTurns?: number };

/**
 * A controlled mode-and-seats configurator: one dropdown per seat, labelled
 * with the role its position carries. Owned state lives in the caller, so two
 * phases (a test and its build) can each carry their own without duplicating
 * the picker.
 */
export function LaunchConfig({
  ducklings,
  value,
  onChange,
  modes = [...MODES],
  estimates,
  showTokens = false,
}: {
  ducklings: readonly Duckling[];
  value: PhaseConfig;
  onChange: (next: PhaseConfig) => void;
  modes?: string[];
  estimates?: ModeEstimates;
  showTokens?: boolean;
}) {
  const [extraSeats, setExtraSeats] = useState(0);
  const seats = fixedSeats(value.mode);
  const cols = seats > 0 ? seats : Math.max(2, value.ducklings.length, extraSeats);
  const setSeat = (i: number, id: string) => {
    const next = [...value.ducklings];
    while (next.length <= i) next.push("");
    next[i] = id;
    onChange({ ...value, ducklings: next });
  };
  return (
    <div className="flex flex-wrap items-end gap-2" data-testid="launch-config">
      <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
        mode
        <select
          aria-label="mode"
          data-testid="cfg-mode"
          value={value.mode}
          onChange={(e) => onChange({ ...value, mode: e.target.value, ducklings: value.ducklings })}
          className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs text-ink-secondary"
        >
          {modes.map((m) => {
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
      </label>
      {ducklings.length > 0 &&
        Array.from({ length: cols }, (_, i) => (
          <label key={i} className="flex flex-col gap-0.5 text-xs text-ink-muted">
            {seatLabel(value.mode, i)}
            <select
              value={value.ducklings[i] ?? ""}
              onChange={(e) => setSeat(i, e.target.value)}
              data-testid={`cfg-seat-${i}`}
              className="rounded border border-hairline bg-surface2 px-1 py-1 text-xs text-ink-secondary"
            >
              <option value="">default</option>
              {ducklings
                .filter((d) => d.id === value.ducklings[i] || !value.ducklings.includes(d.id))
                .map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.id}
                  </option>
                ))}
            </select>
          </label>
        ))}
      {seats === 0 && (
        <button
          type="button"
          data-testid="cfg-seat-add"
          onClick={() => setExtraSeats(cols + 1)}
          className="rounded border border-hairline px-2 py-1 text-xs"
          title="add a seat"
        >
          +
        </button>
      )}
      {showTokens && (
        <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
          token ceiling
          <input
            aria-label="token budget"
            data-testid="cfg-max-tokens"
            placeholder="default"
            value={value.maxTokens ? String(value.maxTokens) : ""}
            onChange={(e) => onChange({ ...value, maxTokens: Number(e.target.value) || undefined })}
            className="w-24 rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
          />
        </label>
      )}
      {showTokens && (
        <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
          calls / reply
          <span className="flex items-center gap-1.5">
            <input
              aria-label="agent turns"
              data-testid="cfg-agent-turns"
              placeholder={value.agentTurns === -1 ? "no cap" : "default"}
              disabled={value.agentTurns === -1}
              value={value.agentTurns && value.agentTurns > 0 ? String(value.agentTurns) : ""}
              onChange={(e) => onChange({ ...value, agentTurns: Number(e.target.value) || undefined })}
              className="w-20 rounded border border-hairline bg-surface2 px-2 py-1 text-xs disabled:opacity-40"
            />
            {/* -1 on the wire: the engine reads negative as "lift this run's
                call cap"; the token and cost budgets still guard. */}
            <label className="flex items-center gap-1 text-xs text-ink-muted" title="no cap on model calls per reply — the run's token and cost budgets still guard">
              <input
                type="checkbox"
                data-testid="cfg-turns-nocap"
                checked={value.agentTurns === -1}
                onChange={(e) => onChange({ ...value, agentTurns: e.target.checked ? -1 : undefined })}
              />
              no cap
            </label>
          </span>
        </label>
      )}
    </div>
  );
}

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
  // The run-specific instruction — the consultant's "relaunch with a note"
  // had the channel (RunRequest.note) and no general doorway. Collapsed
  // until wanted: most launches carry nothing extra.
  const [noteOpen, setNoteOpen] = useState(false);
  const [note, setNote] = useState("");
  const [chosen, setChosen] = useState<string[]>([...initialDucklings]);
  const [maxTokens, setMaxTokens] = useState("");
  const [agentTurns, setAgentTurns] = useState("");
  // "no cap" for the per-reply call loop, same word as the budget lifts:
  // sent as -1, which the engine reads as "lift the cap for this run" — the
  // token and cost budgets still guard every call.
  const [turnsNoCap, setTurnsNoCap] = useState(false);
  const [extraSeats, setExtraSeats] = useState(0);
  const seats = fixedSeats(mode);
  const cols = seats > 0 ? seats : Math.max(2, chosen.length, extraSeats);
  const setSeat = (i: number, id: string) => {
    setChosen((cur) => {
      const next = [...cur];
      while (next.length <= i) next.push("");
      next[i] = id;
      onDucklingsChange?.(next.filter(Boolean));
      return next;
    });
  };

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
        {/* The loop cap inside one reply. It exists to stop circling models
            (I3) — and a hard task can need more looking than the default
            allows; the budget still bounds the spend either way. */}
        <input
          aria-label="agent turns"
          data-testid="run-agent-turns"
          placeholder={turnsNoCap ? "no cap" : "calls/reply (default)"}
          disabled={turnsNoCap}
          value={turnsNoCap ? "" : agentTurns}
          onChange={(e) => setAgentTurns(e.target.value)}
          className="w-32 rounded border border-hairline bg-surface2 px-2 py-1 text-xs disabled:opacity-40"
        />
        <label
          className="flex items-center gap-1 text-xs text-ink-muted"
          title="no cap on model calls per reply — the run's token and cost budgets still guard"
        >
          <input
            type="checkbox"
            data-testid="run-turns-nocap"
            checked={turnsNoCap}
            onChange={(e) => setTurnsNoCap(e.target.checked)}
          />
          no cap
        </label>
        {noteOpen ? (
          <textarea
            value={note}
            onChange={(e) => setNote(e.target.value)}
            rows={2}
            placeholder="a note for this run — what only you know now"
            data-testid="run-note"
            className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
          />
        ) : (
          <button
            type="button"
            data-testid="run-note-toggle"
            onClick={() => setNoteOpen(true)}
            className="text-xs text-ink-muted underline"
          >
            add a note
          </button>
        )}
        <button
          type="button"
          onClick={() =>
            onLaunch({ mode, ducklings: chosen.filter(Boolean), maxTokens: Number(maxTokens) || undefined, note: note.trim() || undefined, agentTurns: turnsNoCap ? -1 : Number(agentTurns) || undefined })
          }
          disabled={busy}
          data-testid="run-start"
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          {busy ? "Starting…" : label}
        </button>
      </div>

      {/* One dropdown per seat, labelled with the role its position carries —
          the same picker Settings uses, because two pickers that disagree
          about what a seat means would be worse than the checkbox wall this
          replaced. Ten ducklings configured never widens the row. */}
      {ducklings.length > 1 && (
        <div className="flex flex-wrap items-end gap-2 text-xs text-ink-secondary">
          {Array.from({ length: cols }, (_, i) => (
            <label key={i} className="flex flex-col gap-0.5 text-xs text-ink-muted">
              {seatLabel(mode, i)}
              <select
                value={chosen[i] ?? ""}
                onChange={(e) => setSeat(i, e.target.value)}
                data-testid={`run-seat-${i}`}
                className="rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs text-ink-secondary"
              >
                <option value="">default</option>
                {ducklings
                  .filter((d) => d.id === chosen[i] || !chosen.includes(d.id))
                  .map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.id}
                    </option>
                  ))}
              </select>
            </label>
          ))}
          {seats === 0 && (
            <button
              type="button"
              data-testid="run-seat-add"
              onClick={() => setExtraSeats(cols + 1)}
              className="rounded border border-hairline px-2 py-0.5 text-xs"
              title="add a seat"
            >
              +
            </button>
          )}
        </div>
      )}
    </div>
  );
}
