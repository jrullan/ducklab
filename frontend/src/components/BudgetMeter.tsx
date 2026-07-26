import { meterRole, statusVar } from "../lib/colors";

/** A bounded track. Turns warning at 80% and critical at 100%. */
export function BudgetMeter({
  label, used, limit, format,
}: { label: string; used: number; limit: number; format: (n: number) => string }) {
  const role = meterRole(used, limit);
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
  return (
    <div data-testid="budget-meter" data-role={role}>
      <div className="flex justify-between text-sm text-ink-secondary">
        <span>{label}</span>
        <span className="tabular-nums">
          {format(used)} / {format(limit)}
        </span>
      </div>
      <div className="mt-1 h-1.5 w-full rounded bg-surface2">
        <div className="h-full rounded" style={{ width: `${pct}%`, background: statusVar(role) }} />
      </div>
    </div>
  );
}
