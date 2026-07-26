import type { GateState } from "../lib/runview";
import { StatusChip } from "./StatusChip";

/** The gate result. A `none` gate reports that nothing was executable rather
 * than anything that could be read as success (P3). */
export function GateCard({ gate }: { gate: GateState | null }) {
  if (!gate) {
    return (
      <div className="rounded-card border border-hairline p-3" data-testid="gate-card" data-gate="pending">
        <div className="text-sm text-ink-muted">gate</div>
        <div className="text-ink-secondary">not run yet</div>
      </div>
    );
  }
  return (
    <div
      className="rounded-card border border-hairline p-3"
      data-testid="gate-card"
      data-gate={gate.gate}
      data-unverified={String(gate.unverified)}
    >
      <div className="text-sm text-ink-muted">gate</div>
      {gate.cmd && <div className="font-mono text-sm text-ink-secondary">{gate.cmd}</div>}
      <div className="mt-1">
        <StatusChip role={gate.role} label={gate.label} />
      </div>
    </div>
  );
}
