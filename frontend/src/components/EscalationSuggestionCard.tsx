import type { DucklabEvent } from "../api/events";

type EscalationData = Record<string, unknown>;

function words(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function label(value: string): string {
  return value.replace(/_/g, " ");
}

function evidenceText(value: unknown): string {
  if (typeof value === "string" || typeof value === "number") return String(value);
  if (!value || typeof value !== "object" || Array.isArray(value)) return "";
  return Object.entries(value as Record<string, unknown>)
    .filter(([, item]) => typeof item === "string" || typeof item === "number")
    .map(([key, item]) => `${label(key)} ${item}`)
    .join(" · ");
}

/** Evidence recorded when the runner pauses to suggest a different recovery. */
export function EscalationSuggestionCard({
  event,
  onRelaunch,
  onOpenTask,
  onContinue,
}: {
  event: DucklabEvent;
  onRelaunch: (candidate: string) => void;
  onOpenTask: () => void;
  onContinue: () => void;
}) {
  const data = (event.data ?? {}) as EscalationData;
  const thresholds = words(data.thresholds_fired);
  const diagnoses = data.diagnoses && typeof data.diagnoses === "object" ? data.diagnoses as Record<string, unknown> : {};
  const candidate = data.candidate && typeof data.candidate === "object" ? data.candidate as Record<string, unknown> : null;
  const candidateID = typeof candidate?.id === "string" ? candidate.id : "";
  const floor = typeof candidate?.wilson_floor === "number" ? candidate.wilson_floor : undefined;

  return (
    <section className="m-2 rounded-card border border-warn p-3" data-testid="escalation-suggestion">
      <h2 className="text-sm font-medium text-ink">escalation suggestion</h2>
      <p className="mt-1 text-sm text-ink-secondary">The run paused with evidence that continuing may not be the best recovery.</p>
      {thresholds.length > 0 && <p className="mt-2 text-sm"><span className="text-ink-muted">thresholds fired · </span>{thresholds.map(label).join(" · ")}</p>}
      {typeof data.current_stage === "string" && <p className="mt-2 text-sm" data-testid="escalation-current-stage"><span className="text-ink-muted">current stage · </span>{data.current_stage}</p>}
      {Object.keys(diagnoses).length > 0 && (
        <div className="mt-2 text-sm" data-testid="escalation-diagnoses">
          <span className="text-ink-muted">diagnoses</span>
          <ul className="mt-1 list-disc pl-5 text-ink-secondary">
            {Object.entries(diagnoses).map(([cause, evidence]) => {
              const detail = evidenceText(evidence);
              return <li key={cause}><span className="font-medium text-ink">{label(cause)}</span>{detail && ` — ${detail}`}</li>;
            })}
          </ul>
        </div>
      )}
      {candidate && (
        <p className="mt-2 text-sm" data-testid="escalation-candidate">
          <span className="text-ink-muted">stronger seat candidate · </span>{candidateID || "unnamed candidate"}
          {floor !== undefined && ` · Wilson floor ${floor}%`}
        </p>
      )}
      <div className="mt-3 flex flex-wrap gap-3">
        <div>
          <button type="button" data-testid="escalation-relaunch" disabled={!candidateID} onClick={() => onRelaunch(candidateID)} className="rounded border border-hairline px-2 py-1 text-sm">Relaunch with stronger seat</button>
          <p className="mt-1 text-xs text-ink-muted">Relaunch re-rolls the dice; money already spent stays on the record.</p>
        </div>
        <div>
          <button type="button" data-testid="escalation-task-body" onClick={onOpenTask} className="rounded border border-hairline px-2 py-1 text-sm">Open task body</button>
          <p className="mt-1 text-xs text-ink-muted">Editing creates a plan amendment before any relaunch; fixed lanes stay fixed.</p>
        </div>
        <div>
          <button type="button" data-testid="escalation-continue" onClick={onContinue} className="rounded border border-hairline px-2 py-1 text-sm">Continue as is</button>
          <p className="mt-1 text-xs text-ink-muted">Continue keeps this scope and this run's dice in play.</p>
        </div>
      </div>
    </section>
  );
}
