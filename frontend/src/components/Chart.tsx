/**
 * The two chart forms Reports needs, and the table that replaces either one.
 *
 * No chart library: a strict CSP and a bundled desktop make one more trouble
 * than a hundred lines of SVG. Every chart here has a Table toggle rendering
 * the same numbers, because a chart is a summary and someone always needs the
 * value (08 §4.7).
 */

import { useState } from "react";

/** A chart and its table, switched by a toggle the keyboard can reach. */
export function ChartFrame({
  title,
  note,
  table,
  children,
}: {
  title: string;
  note?: string;
  table: React.ReactNode;
  children: React.ReactNode;
}) {
  const [asTable, setAsTable] = useState(false);
  return (
    <section className="rounded-card border border-hairline p-3" data-testid={`chart-${title}`}>
      <header className="mb-2 flex items-baseline gap-2">
        <h3 className="text-ink">{title}</h3>
        {note && <span className="text-xs text-ink-muted">{note}</span>}
        <button
          type="button"
          onClick={() => setAsTable((v) => !v)}
          aria-pressed={asTable}
          data-testid="table-toggle"
          className="ml-auto rounded border border-hairline px-2 py-0.5 text-xs text-ink-secondary"
        >
          {asTable ? "Chart" : "Table"}
        </button>
      </header>
      {asTable ? table : children}
    </section>
  );
}

/** Outcome states, in a fixed order, with the status palette.
 *
 * These are states, not series, so they use the status colours — and never
 * colour alone: every segment is labelled in the legend with its own swatch,
 * and the table toggle carries the numbers. */
const OUTCOMES = [
  { key: "passed", label: "passed", color: "var(--status-good)" },
  { key: "unverified", label: "unverified", color: "var(--status-warning)" },
  { key: "failed", label: "failed", color: "var(--status-critical)" },
] as const;

export type OutcomeRow = { key: string; passed: number; unverified: number; failed: number };

/** Horizontal stacked bar per group (08 §4.7). */
export function OutcomeMix({ rows }: { rows: readonly OutcomeRow[] }) {
  return (
    <div data-testid="outcome-mix">
      <div className="mb-2 flex gap-3 text-xs text-ink-secondary">
        {OUTCOMES.map((o) => (
          <span key={o.key} className="flex items-center gap-1">
            <span className="inline-block h-2 w-2 rounded-sm" style={{ background: o.color }} />
            {o.label}
          </span>
        ))}
      </div>
      {rows.map((r) => {
        const total = r.passed + r.unverified + r.failed;
        return (
          <div key={r.key} className="mb-1 flex items-center gap-2">
            <span className="w-24 shrink-0 text-sm text-ink-secondary">{r.key}</span>
            <span className="flex h-4 flex-1 gap-[2px]" role="img" aria-label={
              `${r.key}: ${r.passed} passed, ${r.unverified} unverified, ${r.failed} failed`
            }>
              {OUTCOMES.map((o) => {
                const n = r[o.key];
                if (!n) return null;
                return (
                  <span
                    key={o.key}
                    data-testid={`segment-${r.key}-${o.key}`}
                    style={{ width: `${(n / Math.max(total, 1)) * 100}%`, background: o.color }}
                  />
                );
              })}
            </span>
            <span className="w-10 shrink-0 text-right text-xs text-ink-muted">{total}</span>
          </div>
        );
      })}
    </div>
  );
}

export type Bar = { key: string; value: number; n: number };

/**
 * Horizontal bars with a labelled baseline (08 §4.7).
 *
 * One series, so the value is written on each bar and there is no legend. The
 * baseline is drawn because a pass rate on its own says nothing — the whole
 * question is whether it beat solo (05 §4.1).
 */
export function BarChart({
  bars,
  baseline,
  baselineLabel,
  unit = "%",
}: {
  bars: readonly Bar[];
  baseline?: number;
  baselineLabel?: string;
  unit?: string;
}) {
  // 100 is the floor for a percentage axis, and wrong for anything else: a
  // token count of 30_000 against a floor of 100 draws every bar full width.
  const max = unit === "%" ? Math.max(100, ...bars.map((b) => b.value)) : Math.max(1, ...bars.map((b) => b.value));
  const sorted = [...bars].sort((a, b) => b.value - a.value);
  return (
    <div data-testid="bar-chart" className="relative">
      {baseline !== undefined && (
        <div
          data-testid="baseline"
          className="pointer-events-none absolute inset-y-0 border-l border-dashed border-ink-muted"
          style={{ left: `calc(6rem + ${(baseline / max) * 100}% * 0.75)` }}
          title={baselineLabel}
        />
      )}
      {sorted.map((b) => (
        <div key={b.key} className="mb-1 flex items-center gap-2">
          <span className="w-24 shrink-0 text-sm text-ink-secondary">{b.key}</span>
          <span className="flex h-5 flex-1 items-center">
            <span
              data-testid={`bar-${b.key}`}
              className="h-4 rounded-sm"
              style={{ width: `${(b.value / max) * 100}%`, background: "var(--series-1)" }}
            />
            <span className="ml-2 text-xs text-ink">
              {unit === "%" ? b.value.toFixed(1) : b.value.toLocaleString()}
              {unit}
            </span>
            <span className="ml-2 text-xs text-ink-muted">n={b.n}</span>
          </span>
        </div>
      ))}
      {baselineLabel && (
        <p className="mt-1 text-xs text-ink-muted">dashed line: {baselineLabel}</p>
      )}
    </div>
  );
}
