import { useState } from "react";
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

/** What the run is actually doing about this card. */
export type EscalationState = {
  kind: "paused" | "pausing" | "continuing" | "ended";
  text: string;
};

type RunFacts = { status?: string; pending_kind?: string };

/**
 * The card's first sentence, from the run's real state and the record after
 * the card — never "The run paused" over a run that is still working.
 *
 * B-522: a triage's card said the run had paused; the pause was requested,
 * nothing in the triage loop ever landed it, and the next bug's turn started a
 * second later. The engine derives the same state for run_get.
 */
export function escalationState(event: DucklabEvent, events: readonly DucklabEvent[], run: RunFacts): EscalationState {
  const data = (event.data ?? {}) as EscalationData;
  const seq = event.seq ?? 0;
  let requested = "";
  let deferred = "";
  for (const e of events) {
    if ((e.seq ?? 0) < seq) continue;
    const detail = typeof e.data?.detail === "string" ? e.data.detail : "";
    if (e.type === "pause_requested") requested = detail;
    if (e.type === "pause_deferred") deferred = detail;
  }
  if (!deferred && data.pause === "deferred" && typeof data.pause_detail === "string") deferred = data.pause_detail;
  const live = run.status === "running" || run.status === "queued";
  if (run.status === "paused" && run.pending_kind === "history_duration") {
    return { kind: "paused", text: "The run paused at a safe point with evidence that continuing may not be the best recovery." };
  }
  if (live && deferred) {
    return { kind: "continuing", text: `The run did not pause — ${deferred}. This is advice; it keeps working.` };
  }
  if (live && requested) {
    return { kind: "pausing", text: `The run is still working — ${requested}.` };
  }
  if (live) {
    return { kind: "continuing", text: "The run is still working; this is advice recorded along the way." };
  }
  if (run.status === "paused") {
    return { kind: "paused", text: "The run paused with evidence that continuing may not be the best recovery." };
  }
  return { kind: "ended", text: `The run has ended (${run.status ?? "unknown"}); this evidence is for the next attempt.` };
}

/** Evidence recorded when a run suggests a different recovery. */
export function EscalationSuggestionCard({
  event,
  state,
  hasTask = true,
  stage = "",
  seatChoices = [],
  onRelaunch,
  onOpenTask,
  onContinue,
  onAbort,
  secondary = false,
}: {
  event: DucklabEvent;
  /** The run's real state; without it the card says nothing about pausing. */
  state?: EscalationState;
  /** A task-less run (triage, release, a document stage) has no brief to open. */
  hasTask?: boolean;
  stage?: string;
  /** Ducklings the person may seat in the role when no candidate has evidence. */
  seatChoices?: readonly string[];
  onRelaunch?: (seat: string) => void;
  onOpenTask: () => void;
  /** Resume a paused run, or dismiss the advice on one that keeps working. */
  onContinue: () => void;
  onAbort?: () => void;
  /** A stopped run already has an authoritative decision. Fold this advisory
   * recovery so it cannot compete with that decision for attention. */
  secondary?: boolean;
}) {
  const data = (event.data ?? {}) as EscalationData;
  const thresholds = words(data.thresholds_fired);
  const diagnoses = data.diagnoses && typeof data.diagnoses === "object" ? { ...(data.diagnoses as Record<string, unknown>) } : {};
  // Records written before B-522 stamped no role and could diagnose a task
  // brief on a run that had none. A task-less run never shows that line.
  if (!hasTask) delete diagnoses.task_brief_quality;
  const candidate = data.candidate && typeof data.candidate === "object" ? data.candidate as Record<string, unknown> : null;
  const candidateID = typeof candidate?.id === "string" ? candidate.id : "";
  const floor = typeof candidate?.wilson_floor === "number" ? candidate.wilson_floor : undefined;
  const role = typeof data.role === "string" && data.role ? data.role : hasTask ? "implementer" : "seat";
  const seat = typeof data.seat === "string" ? data.seat : "";
  // The engine's actions, filtered by what this run has; older records carry
  // none, and get the old three only when there is a task to act on.
  const recorded = words(data.actions);
  const actions = (recorded.length ? recorded : hasTask
    ? ["relaunch_with_stronger_seat", "improve_task_body", "continue_as-is"]
    : ["continue_as-is"])
    .map((action) => action === "continue_as_is" ? "continue_as-is" : action)
    .filter((action) => action !== "improve_task_body" || hasTask);
  const ended = state?.kind === "ended";
  const canRelaunch = !!onRelaunch && actions.some((action) => action.startsWith("relaunch_"));
  const choices = seatChoices.filter((id) => id && id !== seat);
  const [picked, setPicked] = useState("");
  const what = stage && !hasTask ? `${stage} ` : "";

  const relaunch = !canRelaunch ? null : candidateID ? (
    <div>
      <button type="button" data-testid="escalation-relaunch" onClick={() => onRelaunch!(candidateID)} className="rounded border border-hairline px-2 py-1 text-sm">
        {`Relaunch ${what}with stronger ${role}`}
      </button>
      <p className="mt-1 text-xs text-ink-muted">Relaunch re-rolls the dice; money already spent stays on the record.</p>
    </div>
  ) : (
    <div data-testid="escalation-relaunch-pick">
      {/* No candidate is not "no relaunch": the person may know a better
          seat the scorecards have not measured. Said, not just disabled. */}
      <p className="text-xs text-ink-muted" data-testid="escalation-no-candidate">
        {`No stronger ${role} with evidence for this role.`}{choices.length ? ` Pick another ${role} yourself:` : ""}
      </p>
      {choices.length > 0 && (
        <select
          aria-label={`Another ${role}`}
          data-testid="escalation-seat-pick"
          value={picked}
          onChange={(e) => setPicked(e.target.value)}
          className="mt-1 rounded border border-hairline bg-surface1 px-2 py-1 text-sm"
        >
          <option value="">choose a {role}…</option>
          {choices.map((id) => <option key={id} value={id}>{id}</option>)}
        </select>
      )}
      <button
        type="button"
        data-testid="escalation-relaunch"
        disabled={!picked}
        onClick={() => onRelaunch!(picked)}
        className="ml-2 mt-1 rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
      >
        {`Relaunch ${what}with another ${role}`}
      </button>
      <p className="mt-1 text-xs text-ink-muted">Relaunch re-rolls the dice; money already spent stays on the record.</p>
    </div>
  );

  const content = <>
      {state && <p className="mt-1 text-sm text-ink-secondary" data-testid="escalation-state" data-state={state.kind}>{state.text}</p>}
      {thresholds.length > 0 && <p className="mt-2 text-sm"><span className="text-ink-muted">thresholds fired · </span>{thresholds.map(label).join(" · ")}</p>}
      {typeof data.current_stage === "string" && <p className="mt-2 text-sm" data-testid="escalation-current-stage"><span className="text-ink-muted">current stage · </span>{data.current_stage}</p>}
      {seat && <p className="mt-2 text-sm" data-testid="escalation-seat"><span className="text-ink-muted">{role} · </span>{seat}</p>}
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
          <span className="text-ink-muted">stronger {role} candidate · </span>{candidateID || "unnamed candidate"}
          {floor !== undefined && ` · Wilson floor ${floor}%`}
        </p>
      )}
      <div className="mt-3 flex flex-wrap gap-3">
        {relaunch}
        {actions.includes("improve_task_body") && (
          <div>
            <button type="button" data-testid="escalation-task-body" onClick={onOpenTask} className="rounded border border-hairline px-2 py-1 text-sm">Open task body</button>
            <p className="mt-1 text-xs text-ink-muted">Editing creates a plan amendment before any relaunch; fixed lanes stay fixed.</p>
          </div>
        )}
        {actions.includes("continue_as-is") && !ended && (
          <div>
            <button type="button" data-testid="escalation-continue" onClick={onContinue} className="rounded border border-hairline px-2 py-1 text-sm">
              {state && state.kind !== "paused" ? "Keep it running" : "Continue as is"}
            </button>
            <p className="mt-1 text-xs text-ink-muted">Continue keeps this scope and this run&apos;s dice in play.</p>
          </div>
        )}
        {actions.includes("abort") && !ended && onAbort && (
          <div>
            <button type="button" data-testid="escalation-abort" onClick={onAbort} className="rounded border border-hairline px-2 py-1 text-sm">Abort</button>
            <p className="mt-1 text-xs text-ink-muted">Ends the run; work already on the record stays there.</p>
          </div>
        )}
      </div>
    </>;

  if (secondary) {
    return (
      <details className="m-2 rounded-card border border-hairline p-3" data-testid="escalation-suggestion" data-secondary="true">
        <summary className="cursor-pointer text-sm text-ink-secondary">
          Secondary recovery suggestion{candidateID ? ` · consider ${candidateID}` : ""}
        </summary>
        {content}
      </details>
    );
  }

  return (
    <section className="m-2 rounded-card border border-warning p-3" data-testid="escalation-suggestion">
      <h2 className="text-sm font-medium text-ink">escalation suggestion</h2>
      {content}
    </section>
  );
}
