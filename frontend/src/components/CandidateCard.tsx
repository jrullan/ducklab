import type { Candidate } from "../api/client";
import { StatusChip } from "./StatusChip";

/**
 * A tournament candidate.
 *
 * The Candidate type has no author field, so this component cannot render one
 * even by mistake. The applied winner is marked "applied verbatim", which
 * surfaces I8 to the user: the judge chose, it did not rewrite.
 */
export function CandidateCard({
  candidate, applied,
}: { candidate: Candidate; applied?: boolean }) {
  const role = candidate.gate === "green" ? "good" : candidate.gate === "red" ? "critical" : "warning";
  const label =
    candidate.gate === "green" ? "verification passed"
      : candidate.gate === "red" ? "verification failed"
        : "not verified";
  return (
    <div className="rounded-card border border-hairline p-3" data-testid="candidate-card">
      <header className="flex items-center gap-2">
        <span className="text-md">Candidate {candidate.label}</span>
        <StatusChip role={role} label={label} />
        {applied && (
          <span className="text-sm text-ink-muted" data-testid="applied-verbatim">
            applied verbatim
          </span>
        )}
      </header>
      <pre className="mt-2 max-h-64 overflow-auto whitespace-pre font-mono text-xs text-ink-secondary">
        {candidate.diff}
      </pre>
    </div>
  );
}
