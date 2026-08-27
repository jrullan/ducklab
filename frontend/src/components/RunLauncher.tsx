import { useEffect, useRef, useState } from "react";
import type { Duckling, RosterEntry } from "../api/client";
import { fixedSeats, seatLabel } from "../lib/seats";
import { SeatChips, type MeasuredSpend } from "./SeatChips";

/** What a mode has cost here before: total dollars over how many runs, from
 * the project's own history. */
export type ModeEstimates = Record<string, { usd: number; runs: number }>;

/** What a launch asks for. Anything unset falls back to the engine's defaults. */
export type LaunchOpts = { mode: string; ducklings: string[]; seats?: Record<string, string>; maxTokens?: number; note?: string; agentTurns?: number; yes?: boolean };

export const MODES = ["solo", "pair", "tournament", "split"] as const;

const MODE_WHEN: Record<string, string> = {
  solo: "Use one voice when the task is clear and quick.",
  pair: "Use two voices when you want an independent review.",
  tournament: "Use competing voices when you want several approaches.",
  split: "Use this when the task can be worked on in parallel.",
};

function modeCost(mode: string, estimates?: ModeEstimates): string {
  const estimate = estimates?.[mode];
  if (!estimate || estimate.runs <= 0) return "no history yet for this shape";
  const average = estimate.usd / estimate.runs;
  return `estimated $${(average * 0.8).toFixed(2)}–$${(average * 1.2).toFixed(2)} per run`;
}

/** Project the canonical roster into the positional seats sent to the engine.
 * Roster responses are role-ordered, not seat-ordered. */
function rosterSeats(mode: string, roster: readonly RosterEntry[]): string[] {
  const count = fixedSeats(mode);
  if (count > 0) {
    return Array.from({ length: count }, (_, i) =>
      roster.find((entry) => entry.role === seatLabel(mode, i))?.duckling ?? "",
    );
  }
  return roster.map((entry) => entry.duckling);
}

export function roleSeats(mode: string, ducklings: readonly string[]): Record<string, string> {
  return Object.fromEntries(ducklings.map((id, i) => [seatLabel(mode, i), id]));
}

/** Only a person’s current pick is a request override; roster resolutions stay display-only. */
export function pickedSeats(mode: string, value: PhaseConfig): Record<string, string> {
  return Object.fromEntries(
    value.ducklings.flatMap((id, i) => value.seatProvenance?.[i] === "picked now" && id ? [[seatLabel(mode, i), id]] : []),
  );
}

/** One phase's launch configuration: mode, seats, optional token ceiling. */
export type PhaseConfig = { mode: string; ducklings: string[]; seatProvenance?: string[]; maxTokens?: number; agentTurns?: number };

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
  measured,
  defaultProvenance,
  roster,
  defaultMode,
}: {
  ducklings: readonly Duckling[];
  value: PhaseConfig;
  onChange: (next: PhaseConfig) => void;
  modes?: string[];
  estimates?: ModeEstimates;
  showTokens?: boolean;
  measured?: MeasuredSpend;
  roster?: readonly RosterEntry[];
  /** Mode shown while an empty mode remains omitted from the request. */
  defaultMode?: string;
  /** Provenance for untouched empty seats; a pick is always per-run. */
  defaultProvenance?: string;
}) {
  const [extraSeats, setExtraSeats] = useState(0);
  const displayMode = value.mode || defaultMode || modes[0] || "solo";
  // Re-seed when this phase's resolution changes, rather than treating any
  // nonempty seat array as permanent. Per-run picks survive a re-resolution.
  const seededRoster = useRef<readonly RosterEntry[] | undefined>();
  const seededMode = useRef("");
  useEffect(() => {
    if (!roster?.length || (seededRoster.current === roster && seededMode.current === displayMode)) return;
    seededRoster.current = roster;
    seededMode.current = displayMode;
    const resolved = rosterSeats(displayMode, roster);
    onChange({
      ...value,
      ducklings: resolved.map((id, i) => value.seatProvenance?.[i] === "picked now" ? value.ducklings[i] ?? "" : id),
      seatProvenance: resolved.map((_, i) => {
        if (value.seatProvenance?.[i] === "picked now") return "picked now";
        const source = roster.find((entry) => entry.role === seatLabel(displayMode, i))?.source;
        return source?.startsWith("project") ? "project" : "global";
      }),
    });
  }, [roster, displayMode]);
  const seats = fixedSeats(displayMode);
  const cols = seats > 0 ? seats : Math.max(2, value.ducklings.length, extraSeats);
  const setSeat = (i: number, id: string) => {
    const next = [...value.ducklings];
    while (next.length <= i) next.push("");
    next[i] = id;
    const provenance = [...(value.seatProvenance ?? [])];
    provenance[i] = id ? "picked now" : defaultProvenance ?? "roster";
    onChange({ ...value, ducklings: next, seatProvenance: provenance });
  };
  return (
    <div className="flex flex-wrap items-end gap-2" data-testid="launch-config">
      <label className="flex flex-col gap-0.5 text-xs text-ink-muted">
        mode
        <select
          aria-label="mode"
          data-testid="cfg-mode"
          value={displayMode}
          onChange={(e) => onChange({ ...value, mode: e.target.value, ducklings: value.ducklings })}
          className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs text-ink-secondary"
        >
          {modes.map((m) => {
            const e = estimates?.[m];
            const avg = e && e.runs > 0 ? e.usd / e.runs : undefined;
            return (
              <option key={m} value={m}>
                {m}
                {avg !== undefined ? ` · ~$${avg.toFixed(2)}` : ""}
              </option>
            );
          })}
        </select>
      </label>
      {/* The chips ARE the picker here too — one seat UI on every surface. */}
      {ducklings.length > 0 && (
        <SeatChips
          entries={Array.from({ length: cols }, (_, i) => ({
            role: seatLabel(displayMode, i),
            duckling: value.seatProvenance?.[i] === "picked now"
              ? value.ducklings[i] ?? ""
              : roster?.find((entry) => entry.role === seatLabel(displayMode, i))?.duckling ?? "",
            provenance: value.seatProvenance?.[i] === "picked now"
              ? "picked now"
              : (() => {
                  const source = roster?.find((entry) => entry.role === seatLabel(displayMode, i))?.source;
                  return source?.startsWith("project") ? "project" : source ? "global" : defaultProvenance;
                })(),
          }))}
          fleet={[...ducklings]}
          measured={measured}
          allowDefault
          stack
          optionsFor={(i) =>
            ducklings.filter((d) => d.id === value.ducklings[i] || !value.ducklings.includes(d.id))
          }
          onPick={(i, id) => setSeat(i, id)}
        />
      )}
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
  onModeChange,
  measured,
  roster,
  initiallyOpen = true,
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
  onModeChange?: (mode: string) => void;
  measured?: MeasuredSpend;
  roster?: readonly RosterEntry[];
  initiallyOpen?: boolean;
}) {
  const resolved = roster ?? [];
  const [open, setOpen] = useState(initiallyOpen);
  const [mode, setMode] = useState(initialMode);
  // The run-specific instruction — the consultant's "relaunch with a note"
  // had the channel (RunRequest.note) and no general doorway. Collapsed
  // until wanted: most launches carry nothing extra.
  const [noteOpen, setNoteOpen] = useState(false);
  const [note, setNote] = useState("");
  const changed = useRef(initialDucklings.length > 0 && resolved.length === 0);
  const [chosen, setChosen] = useState<string[]>(() => resolved.length ? [] : initialDucklings.length ? [...initialDucklings] : []);
  const [seatProvenance, setSeatProvenance] = useState<string[]>(() => resolved.length ? [] : initialDucklings.map(() => "picked now"));
  const [maxTokens, setMaxTokens] = useState("");
  const [yolo, setYolo] = useState(false);
  const [agentTurns, setAgentTurns] = useState("");
  // "no cap" for the per-reply call loop, same word as the budget lifts:
  // sent as -1, which the engine reads as "lift the cap for this run" — the
  // token and cost budgets still guard every call.
  const [turnsNoCap, setTurnsNoCap] = useState(false);
  const [extraSeats, setExtraSeats] = useState(0);
  const seats = fixedSeats(mode);
  const cols = seats > 0 ? seats : Math.max(2, chosen.length, extraSeats);
  useEffect(() => {
    if (!changed.current && resolved.length) {
      const seatsForMode = rosterSeats(mode, resolved);
      setChosen(seatsForMode);
      setSeatProvenance(seatsForMode.map((_, i) => {
        const entry = resolved.find((e) => e.role === seatLabel(mode, i));
        return entry?.source === "project pin" ? "project" : "global";
      }));
    }
  }, [roster, mode]);
  const setSeat = (i: number, id: string) => {
    changed.current = true;
    setSeatProvenance((cur) => { const next = [...cur]; next[i] = "picked now"; return next; });
    setChosen((cur) => {
      const next = [...cur];
      while (next.length <= i) next.push("");
      next[i] = id;
      onDucklingsChange?.(next);
      return next;
    });
  };

  if (!open) return (
    <div>
      <button
        type="button"
        data-testid="launch-modal-trigger"
        onClick={() => setOpen(true)}
        className="rounded border border-hairline px-3 py-1.5 text-sm"
      >
        {label}
      </button>
    </div>
  );
  return (
    <div className="space-y-2" data-testid="run-launcher">
      <div
        role="dialog"
        aria-label="Launch work"
        data-testid="launch-modal"
        className="space-y-3 rounded-card border border-hairline bg-surface p-3"
      >
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-base font-medium text-ink">Launch work</h2>
            <p className="text-xs text-ink-muted">
              The task is already selected. Choose a shape and start.
            </p>
          </div>
          <button
            type="button"
            aria-label="Close launch"
            onClick={() => setOpen(false)}
            className="text-xs text-ink-muted"
          >
            close
          </button>
        </div>
        <fieldset>
          <legend className="text-xs font-medium text-ink-muted">how to work</legend>
          <div
            className="mt-1 grid gap-2 sm:grid-cols-2"
            data-testid="mode-cards"
          >
            {MODES.map((m) => (
              <button
                key={m}
                type="button"
                data-testid={`mode-card-${m}`}
                aria-pressed={mode === m}
                onClick={() => {
                  setMode(m);
                  onModeChange?.(m);
                  if (resolved.length) {
                    changed.current = false;
                    setChosen([]);
                    setSeatProvenance([]);
                  } else {
                    const saved = preferred?.[m];
                    if (saved?.length) {
                      setChosen([...saved]);
                      onDucklingsChange?.([...saved]);
                    }
                  }
                }}
                className={`rounded border p-2 text-left text-xs ${
                  mode === m ? "border-ink" : "border-hairline"
                }`}
              >
                <span className="block font-medium text-ink">{m}</span>
                <span className="mt-1 block text-ink-secondary">
                  {MODE_WHEN[m] ?? "Use this shape when it fits the work."}
                </span>
                <span className="mt-1 block text-ink-muted">
                  {modeCost(m, estimates)}
                </span>
              </button>
            ))}
          </div>
        </fieldset>
        <p className="text-xs text-ink-muted">
          Seats are filled from the roster. You never need to type a model name.
        </p>
        <button
          type="button"
          className="text-xs text-ink-muted underline"
          onClick={() => {
            changed.current = false;
            setChosen(resolved.length ? rosterSeats(mode, resolved) : preferred?.[mode] ?? []);
            setSeatProvenance([]);
          }}
        >
          use defaults
        </button>
        <div className="flex flex-wrap items-center gap-2">
        <select
          aria-label="mode"
          data-testid="run-mode"
          value={mode}
          onChange={(e) => {
            const next = e.target.value;
            setMode(next);
            onModeChange?.(next);
            if (resolved.length) {
              // A mode change re-resolves from the canonical roster; saved
              // Settings line-ups must not clobber the resolver.
              changed.current = false;
              setChosen([]);
              setSeatProvenance([]);
            } else {
              const saved = preferred?.[next];
              if (saved?.length) {
                setChosen([...saved]);
                onDucklingsChange?.([...saved]);
              }
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
                {avg !== undefined ? ` · ~$${avg.toFixed(2)}` : ""}
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
        {/* Per-run yolo: green gates accept themselves (dissent and
            UNVERIFIED still stop), ask_human answers "no human available".
            The budget caps are untouched — autonomy and money are separate
            axes. */}
        <label
          className="flex items-center gap-1 text-xs text-ink-muted"
          title="unattended: a green gate accepts itself; reviewer dissent and UNVERIFIED still wait for you. ask_human questions are auto-answered by the question advisor and recorded as advisor answers, not yours"
        >
          <input
            type="checkbox"
            data-testid="run-yolo"
            checked={yolo}
            onChange={(e) => setYolo(e.target.checked)}
          />
          unattended
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
            onLaunch({
              mode,
              // The roster is the source of truth for untouched defaults. Keep
              // the visible pins in the launcher, but leave them out of the
              // request so the engine can resolve the canonical roster.
              ducklings: resolved.length && !changed.current ? [] : chosen,
              ...(Number(maxTokens) ? { maxTokens: Number(maxTokens) } : {}),
              ...(note.trim() ? { note: note.trim() } : {}),
              ...(turnsNoCap || Number(agentTurns) ? { agentTurns: turnsNoCap ? -1 : Number(agentTurns) } : {}),
              ...(yolo ? { yes: true } : {}),
            })
          }
          disabled={busy}
          data-testid="run-start"
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          {busy ? "Starting…" : label}
        </button>
      </div>

      {/* The chips ARE the picker, as on every stage form: click a seat to
          reseat it, "default" leaves it to the roster. One seat UI, not a
          glance row above a dropdown row saying the same thing twice. */}
      {ducklings.length > 1 && (
        <div className="mb-1 flex flex-wrap items-center gap-2">
          <SeatChips
            entries={Array.from({ length: cols }, (_, i) => ({
              role: seatLabel(mode, i),
              duckling: chosen[i] ?? "",
              provenance: seatProvenance[i],
            }))}
            fleet={[...ducklings]}
            measured={measured}
            allowDefault
            optionsFor={(i) => ducklings.filter((d) => d.id === chosen[i] || !chosen.includes(d.id))}
            onPick={(i, id) => setSeat(i, id)}
          />
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
      </div>
  );
}
