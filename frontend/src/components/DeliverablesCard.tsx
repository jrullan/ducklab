import type { DeliverableLine } from "../lib/runview";

// The implementer's deliverables report, rendered where a person reads it: at
// the end of the implementer's own turn. Deliberately NOT a rail card — an
// unreviewed progress report beside the gate would read as a result, and it
// is subject to the run's later rounds.
import type { StatusRole } from "../lib/colors";
import { statusVar } from "../lib/colors";

const MARK: Record<DeliverableLine["status"], { glyph: string; title: string; role: StatusRole }> = {
  done: { glyph: "☑", title: "done", role: "good" },
  partial: { glyph: "◐", title: "partial", role: "warning" },
  not_done: { glyph: "☐", title: "not done", role: "critical" },
  blocked: { glyph: "⊘", title: "blocked", role: "critical" },
  unreported: { glyph: "☐", title: "not reported", role: "muted" },
};

/** The report as it appears at the end of the implementer's own turn: the
 * same marks, one row per reported item, texts when the run's report event
 * carried them and bare numbers when it did not. */
export function DeliverablesInline({
  items,
  texts,
}: {
  items: { id: number; status: string; note?: string }[];
  texts?: string[];
}) {
  const done = items.filter((i) => i.status === "done").length;
  return (
    <div className="mt-2 rounded-card border border-hairline p-2" data-testid="deliverables-inline">
      <div className="flex items-baseline justify-between text-xs text-ink-muted">
        <span>deliverables reported</span>
        <span className="tabular-nums">{done}/{items.length} done</span>
      </div>
      <ul className="mt-1 space-y-0.5">
        {items.map((it) => {
          const m = MARK[(it.status as DeliverableLine["status"]) in MARK ? (it.status as DeliverableLine["status"]) : "unreported"];
          const text = texts?.[it.id - 1];
          return (
            <li key={it.id} className="flex gap-2 text-sm" data-testid="deliverable-inline" data-id={it.id} data-status={it.status}>
              <span className="shrink-0 font-mono" style={{ color: statusVar(m.role) }} title={m.title} aria-label={m.title}>
                {m.glyph}
              </span>
              <span className="min-w-0">
                <span className="text-ink-muted tabular-nums">{it.id}.</span>{" "}
                {text ? <span className={it.status === "done" ? "text-ink-secondary" : "text-ink"}>{text}</span> : <span className="text-ink-muted">{m.title}</span>}
                {it.note ? <div className="text-xs text-ink-muted break-words">— {it.note}</div> : null}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
