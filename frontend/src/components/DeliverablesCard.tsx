import type { DeliverablesState, DeliverableLine } from "../lib/runview";
import type { StatusRole } from "../lib/colors";
import { statusVar } from "../lib/colors";

/** The implementer's deliverables report, as a checklist.
 *
 * The task's numbered bullets are the implementer's work contract; at the end
 * of its turn it reports each by number. This card is that report, rendered
 * for a person at a glance: what got done, what the implementer itself says
 * it did not, and — when a reviewer approved over an undelivered item — the
 * contradiction the record flagged. Notes are the implementer's own words and
 * are shown here (the person may read them; the reviewer may not). */
export function DeliverablesCard({ report }: { report: DeliverablesState | null }) {
  if (!report) return null;
  return (
    <div
      className="rounded-card border border-hairline p-3"
      data-testid="deliverables-card"
      data-done={report.done}
      data-total={report.total}
      data-gap={String(report.gap)}
    >
      <div className="flex items-baseline justify-between">
        <div className="text-sm text-ink-muted">deliverables</div>
        <div className="tabular-nums text-sm text-ink-secondary" data-testid="deliverables-count">
          {report.done}/{report.total}
          {report.retry ? <span className="ml-1 text-ink-muted">· retry {report.retry}</span> : null}
        </div>
      </div>
      {report.unreported ? (
        <div className="mt-1 text-sm text-ink-secondary" data-testid="deliverables-unreported">
          the implementer filed no report on these
        </div>
      ) : null}
      <ul className="mt-2 space-y-1">
        {report.lines.map((l) => (
          <DeliverableRow key={l.id} line={l} />
        ))}
      </ul>
      {report.gap ? (
        <div className="mt-2 text-sm" data-testid="deliverables-gap">
          <span className="rounded border px-1.5 py-0.5" style={{ color: statusVar("critical"), borderColor: statusVar("critical") }}>
            ⚠ reviewer approved over undelivered items
          </span>
        </div>
      ) : null}
    </div>
  );
}

const MARK: Record<DeliverableLine["status"], { glyph: string; title: string; role: StatusRole }> = {
  done: { glyph: "☑", title: "done", role: "good" },
  partial: { glyph: "◐", title: "partial", role: "warning" },
  not_done: { glyph: "☐", title: "not done", role: "critical" },
  blocked: { glyph: "⊘", title: "blocked", role: "critical" },
  unreported: { glyph: "☐", title: "not reported", role: "muted" },
};

function DeliverableRow({ line }: { line: DeliverableLine }) {
  const m = MARK[line.status];
  return (
    <li className="flex gap-2 text-sm" data-testid="deliverable" data-id={line.id} data-status={line.status}>
      <span className="shrink-0 font-mono" style={{ color: statusVar(m.role) }} title={m.title} aria-label={m.title}>
        {m.glyph}
      </span>
      <span className="min-w-0">
        <span className="text-ink-muted tabular-nums">{line.id}.</span>{" "}
        <span className={line.status === "done" ? "text-ink-secondary" : "text-ink"}>{line.text}</span>
        {line.note ? <div className="text-xs text-ink-muted break-words">— {line.note}</div> : null}
      </span>
    </li>
  );
}


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
