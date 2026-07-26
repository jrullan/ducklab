import type { ReactNode } from "react";

/** A value with a label and an optional sub-line. No plot inside a tile. */
export function StatTile({
  label, value, sub, children,
}: { label: string; value: string; sub?: string; children?: ReactNode }) {
  return (
    <div className="rounded-card border border-hairline bg-surface1 p-4" data-testid="stat-tile">
      <div className="text-sm text-ink-muted">{label}</div>
      <div className="mt-1 text-lg text-ink">{value}</div>
      {sub && <div className="mt-1 text-sm text-ink-secondary">{sub}</div>}
      {children}
    </div>
  );
}
