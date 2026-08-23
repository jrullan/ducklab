import { useState } from "react";
import type { Duckling } from "../api/client";
import { assignDucklingColors } from "../lib/colors";
import { tokens } from "../lib/format";
import { useChipFacts } from "../lib/chipfacts";

/** One seat for the chips row: the role's name and who sits in it. */
export type SeatEntry = {
  role: string;
  duckling: string;
  /** Why this seat has its current value (roster, Settings, or picked now). */
  provenance?: string;
};

/** Measured spend per duckling — this project's own report, not a price
 * list: cost_usd and runs from report(project, "duckling"). */
export type MeasuredSpend = Record<string, { usd: number; runs: number }>;

/** A seat, said with everything that matters at a glance: the duckling in
 * its own colour and the facts the person chose to care about (appearance
 * settings) — context window, vision, declared price, measured cost per run,
 * tools, JSON. [glm52 384.0k 👁️] answers "who will do this and what can
 * they take" without a trip to Settings. Every launch surface that names a
 * duckling wears these.
 *
 * With onPick, a chip is a DOOR: clicking it opens the pick for that seat,
 * this run only. Without it, the chips are the honest label beside whatever
 * picker the surface already has. */
export function SeatChips({
  entries,
  fleet,
  measured,
  onPick,
  optionsFor,
  allowDefault = false,
  stack = false,
  activeDuckling,
}: {
  entries: SeatEntry[];
  fleet: Duckling[];
  measured?: MeasuredSpend;
  onPick?: (index: number, duckling: string) => void;
  /** Narrow the picker per seat (e.g. exclude ducklings already seated). */
  optionsFor?: (index: number) => Duckling[];
  /** Offer "default" (empty) — the roster decides that seat. */
  allowDefault?: boolean;
  /** One chip per row: in a narrow rail a chip that carries role, duckling,
   *  provenance and facts overflows and wraps mid-word. Stacked, each reads
   *  as a line of a seating list. */
  stack?: boolean;
  activeDuckling?: string;
}) {
  const colors = assignDucklingColors(fleet);
  const [open, setOpen] = useState<number | null>(null);
  // The person's own pick of facts — reactive: a change in Settings lands
  // here without a remount or a save button.
  const facts = useChipFacts();
  return (
    <div className={stack ? "flex flex-col items-start gap-1" : "flex flex-wrap items-center gap-2"} data-testid="seat-chips">
      {entries.map((e, i) => {
        const d = fleet.find((x) => x.id === e.duckling);
        const m = measured?.[e.duckling];
        if (open === i && onPick) {
          return (
            <label key={e.role + i} className="flex items-center gap-1 text-xs text-ink-muted">
              {e.role}
              <select
                autoFocus
                data-testid={`seat-pick-${i}`}
                value={e.duckling}
                onChange={(ev) => {
                  onPick(i, ev.target.value);
                  setOpen(null);
                }}
                onBlur={() => setOpen(null)}
                className="rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
              >
                {allowDefault && <option value="">default</option>}
                {(optionsFor ? optionsFor(i) : fleet).map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.id}
                    {f.caps?.vision ? " 👁" : ""}
                  </option>
                ))}
              </select>
            </label>
          );
        }
        return (
          <button
            key={e.role + i}
            type="button"
            disabled={!onPick}
            onClick={() => onPick && setOpen(i)}
            className={
              "flex items-center gap-1 rounded-full border border-hairline px-2 py-0.5 text-xs" +
              (onPick ? " cursor-pointer hover:border-ink" : "") +
              (activeDuckling && activeDuckling === e.duckling ? " ring-2 ring-serious" : "")
            }
            data-testid="seat-chip"
            title={onPick ? "click to pick a different duckling for this run only" : undefined}
          >
            <span className="text-ink-muted">{e.role}</span>
            {e.duckling ? (
              <span style={{ color: colors[e.duckling] }} className="font-medium">
                {e.duckling}
              </span>
            ) : (
              <span className="text-ink-muted">default</span>
            )}
            {e.provenance && <span className="text-ink-muted">{e.provenance}</span>}
            {facts.includes("context") && d?.caps?.context_tokens ? (
              <span className="text-ink-muted" title="context window">
                {tokens(d.caps.context_tokens)}
              </span>
            ) : null}
            {facts.includes("vision") && d?.caps?.vision && (
              <span title="vision — can be shown images">👁️</span>
            )}
            {facts.includes("price") && d?.cost && (d.cost.input_per_mtok || d.cost.output_per_mtok) ? (
              <span
                className="text-ink-muted"
                title="average of declared input/output cost per Mtok"
                data-testid="chip-price"
              >
                ${(((d.cost.input_per_mtok ?? 0) + (d.cost.output_per_mtok ?? 0)) / 2).toFixed(2)}/M
              </span>
            ) : null}
            {facts.includes("mprice") && m && m.runs > 0 ? (
              <span
                className="text-ink-muted"
                title={`measured in this project: ${m.runs} run(s) it took part in`}
                data-testid="chip-mprice"
              >
                ~${(m.usd / m.runs).toFixed(3)}/run
              </span>
            ) : null}
            {facts.includes("tools") && d?.caps?.native_tools && (
              <span title="calls tools natively">🔧</span>
            )}
            {facts.includes("json") && d?.caps?.json_mode && (
              <span className="text-ink-muted" title="has a JSON mode">{"{}"}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}
