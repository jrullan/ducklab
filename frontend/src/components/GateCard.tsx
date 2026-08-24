import type { GateState } from "../lib/runview";
import { StatusChip } from "./StatusChip";

/** The gate result. A `none` gate reports that nothing was executable rather
 * than anything that could be read as success (P3).
 *
 * test-first inverts the reading and the card must say so: the run exists to
 * produce a test that FAILS against today's code, so red is the goal reached
 * — a person saw "tests failed" beside a PASSED verdict and reasonably read
 * it as a contradiction. Green after test-first is the actual bad news: a new
 * test that already passes asserts nothing. */
export function GateCard({ gate, stage }: { gate: GateState | null; stage?: string }) {
  const testFirst = stage === "test";
  if (!gate) {
    return (
      <div className="rounded-card border border-hairline p-3" data-testid="gate-card" data-gate="pending">
        <div className="text-sm text-ink-muted">gate</div>
        <div className="text-ink-secondary">no final gate yet</div>
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
      {/* Commands are long one-liners; unbroken they punched through the
          rail's edge and the tail of the command was simply gone. */}
      {gate.cmd && (
        <div className="break-all font-mono text-xs text-ink-secondary">{gate.cmd}</div>
      )}
      <div className="mt-1">
        {testFirst && gate.gate === "red" ? (
          <StatusChip role="good" label="red — the new test fails, as intended: it defines done" />
        ) : testFirst && gate.gate === "green" ? (
          <StatusChip role="critical" label="green — the new test already passes, so it asserts nothing" />
        ) : (
          <StatusChip role={gate.role} label={gate.label} />
        )}
      </div>
      {gate.exitCode !== undefined && <div className="mt-1 text-xs text-ink-secondary">exit code {gate.exitCode}{gate.durationS !== undefined ? ` · ${gate.durationS}s` : ""}</div>}
      {gate.output && <pre className="mt-1 max-h-24 overflow-auto whitespace-pre-wrap font-mono text-xs text-ink-secondary">{gate.output}</pre>}
    </div>
  );
}
