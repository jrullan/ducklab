import { meterRole, statusVar } from "../lib/colors";

/** A bounded track. Turns warning at 80% and critical at 100%.
 *
 * `lift`, when given, puts a checkbox beside the numbers: checking it removes
 * THIS cap from the live run — one-way, recorded by the engine — so a run
 * close to a ceiling can be given headroom without waiting for it to die.
 * A lifted (or never-set) cap renders as "no cap" with the box checked and
 * frozen: there is no un-lifting, only the other caps still standing guard. */
export function BudgetMeter({
  label, used, limit, format, lift, inFlight,
}: {
  label: string;
  used: number;
  limit: number;
  format: (n: number) => string;
  lift?: { onLift: () => void };
  /** Estimated spend inside the CURRENT streaming call — settled usage only
   * lands when a call completes, and a long stream read as frozen zeros. */
  inFlight?: number;
}) {
  const lifted = limit <= 0;
  const role = meterRole(used, limit);
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
  return (
    <div data-testid="budget-meter" data-role={role}>
      <div className="flex items-baseline justify-between gap-2 text-sm text-ink-secondary">
        <span>{label}</span>
        <span className="flex items-baseline gap-2">
          <span className="tabular-nums">
            {format(used)} / {lifted ? "no cap" : format(limit)}
          </span>
          {lift && (
            <label
              className="flex items-center gap-1 text-xs text-ink-muted"
              title={
                lifted
                  ? "no cap on this run — the others still guard"
                  : "remove this cap for the rest of the run (cannot be undone)"
              }
            >
              <input
                type="checkbox"
                data-testid={`lift-${label}`}
                checked={lifted}
                disabled={lifted}
                onChange={() => lift.onLift()}
              />
              no cap
            </label>
          )}
        </span>
      </div>
      {/* Supplementary, on its own line: riding the value row it broke the
          row's shape and the symmetry with the meters below — the main line
          stays identical to its siblings, the estimate whispers under it. */}
      {(inFlight ?? 0) > 0 && (
        <div className="flex justify-end">
          <span className="text-xs text-ink-muted" data-testid="meter-inflight" title="estimated from the text streamed so far; the settled number lands when the call completes">
            + ~{format(inFlight!)} streaming
          </span>
        </div>
      )}
      <div className="mt-1 h-1.5 w-full rounded bg-surface2">
        <div className="h-full rounded" style={{ width: `${pct}%`, background: statusVar(role) }} />
      </div>
    </div>
  );
}
