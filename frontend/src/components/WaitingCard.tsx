import { EngineClient, type Run } from "../api/client";
import { StatusChip } from "./StatusChip";
import { routeHref } from "../app/routes";
import { runLabel } from "../lib/runview";
import { waitingFor, moneyOrZero } from "../lib/format";
import { useState } from "react";
import { EvidenceDrawerHost } from "./EvidenceDrawer";
import { useRuns } from "../store/runs";

function waitingExplanation(run: Run): string {
  if (["intake", "spec", "plan"].includes(run.stage)) {
    const subject = run.stage === "intake" ? "requirements" : run.stage;
    return `The ${subject} proposal is ready — it is waiting for your decision.`;
  }
  if (run.pending_kind === "question") {
    return "This task paused to ask you a question — it is waiting for your answer.";
  }
  if (run.pending_kind === "dissent") {
    return "This task finished, but a reviewer disagreed — it is waiting for your decision.";
  }
  if (run.verdict === "UNVERIFIED") {
    return "This task finished without verified tests — it is waiting for your decision.";
  }
  return "This task finished and passed its tests — it is waiting for your decision.";
}

/** A run waiting at its gate, decidable in place: buttons from the engine's
 * next list, verdict and cost as the minimum evidence, and the run linked for
 * the scrutiny that needs the diff. Shared by Now's inbox and the board's
 * task rail, because a decision should meet the person wherever they already
 * are. */
export function WaitingCard({
  run,
  accepting,
  onAccept,
  onReject,
  onAbort,
  onRequestChanges,
  acceptError,
  client,
}: {
  client?: EngineClient;
  run: Run;
  accepting: boolean;
  onAccept: () => void;
  onReject: () => void;
  onAbort: () => void;
  onRequestChanges?: (note: string) => Promise<void>;
  acceptError?: string;
}) {
  const [evidenceOpen, setEvidenceOpen] = useState(false);
  const [changesOpen, setChangesOpen] = useState(false);
  const [changes, setChanges] = useState("");
  const [changesBusy, setChangesBusy] = useState(false);
  // From the engine's list, never this card's opinion of the state
  // (docs/ux-evaluation.md §5.4).
  const next = run.next ?? [];
  // B-247: the task's work already landed under another accepted run. The
  // T-181 phantom re-run was accepted from a card that looked like any other.
  const landedAs = useRuns((s) =>
    run.task_id
      ? Object.values(s.runs).find((r) => r.id !== run.id && r.task_id === run.task_id && r.accepted && r.commit_sha)?.commit_sha
      : undefined,
  );
  return (
    <li data-testid="now-waiting-card" className="rounded-card border border-serious p-3">
      <div className="flex flex-wrap items-baseline gap-2">
        <a href={routeHref({ name: "run", id: run.id })} className="text-ink underline">
          {runLabel(run)}
        </a>
        <span className="text-xs text-ink-secondary">{run.mode}</span>
        {run.verdict && (
          <>
            <StatusChip
              role={run.verdict === "UNVERIFIED" ? "warning" : "good"}
              label={run.verdict.toLowerCase()}
            />
            {run.warning && (
              <span
                className="text-xs"
                style={{ color: "var(--status-serious)" }}
                title={`passed with caveat: ${run.warning}`}
                aria-label={`passed with caveat: ${run.warning}`}
              >
                ⚠ passed with caveat
              </span>
            )}
          </>
        )}
        <span className="text-xs text-ink-muted">
          waiting {run.pending_since ? waitingFor(run.pending_since) : ""}
        </span>
        {run.budget && run.budget.usd > 0 && (
          <span className="ml-auto text-xs tabular-nums text-ink-secondary">
            {moneyOrZero(run.budget.usd)}
          </span>
        )}
      </div>
      <p className="mt-2 text-sm text-ink-secondary" data-testid="waiting-explanation">
        {waitingExplanation(run)}
      </p>
      {landedAs && (
        <p className="mt-1 rounded border border-warn px-2 py-1 text-xs text-ink" data-testid="landed-notice">
          This task already landed as <span className="font-mono">{landedAs.slice(0, 7)}</span> — accepting this re-run lands a second change on top of it. Only accept a deliberate redo.
        </p>
      )}
      {/* The reason the run stopped, where the decision is offered. A card
          saying "waiting — error" with the error a click away taught the
          person the card could not be trusted to say why. */}
      {run.failure && (
        <p className="mt-1 break-words text-xs text-critical" data-testid="waiting-reason">
          {run.failure}
        </p>
      )}
      {run.warning && (
        <p className="mt-1 break-words text-xs" style={{ color: "var(--status-serious)" }} data-testid="waiting-warning">
          {run.warning}
        </p>
      )}
      <div className="mt-2 flex items-center gap-2">
        {/* The evidence — diff, transcript, gate output — is one click away on
            the label. AC-34 holds: nothing optimistic, the commit shows only
            when the engine confirms, which the run view handles. */}
        {next.includes("accept") && (
          <button
            type="button"
            data-testid="now-accept"
            onClick={onAccept}
            disabled={accepting}
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            {accepting ? "Accepting…" : "Accept"}
          </button>
        )}
        {next.includes("abort") && (
          <button
            type="button"
            data-testid="now-abort"
            onClick={onAbort}
            className="rounded border border-hairline px-2 py-1 text-xs"
          >
            Abort
          </button>
        )}
        {next.includes("reject") && (
          <button
            type="button"
            data-testid="now-reject"
            onClick={onReject}
            className="rounded border border-hairline px-2 py-1 text-xs"
          >
            Reject
          </button>
        )}
        {next.includes("request_changes") && onRequestChanges && (
          <button
            type="button"
            data-testid="now-request-changes"
            onClick={() => setChangesOpen((open) => !open)}
            className="rounded border border-hairline px-2 py-1 text-xs"
          >
            Request changes
          </button>
        )}
        {next.includes("answer") && (
          <a
            href={routeHref({ name: "run", id: run.id })}
            data-testid="now-answer"
            className="text-xs text-ink underline"
          >
            a duckling asked a question — answer it
          </a>
        )}
        {(next.includes("accept") || next.includes("resume")) && (
          <button
            type="button"
            data-testid="review-evidence"
            onClick={() => setEvidenceOpen(true)}
            className="text-xs text-ink-muted underline"
          >
            review evidence <span className="text-ink-muted">— see the evidence</span>
          </button>
        )}
      </div>
      {changesOpen && (
        <form
          className="mt-2 flex gap-2"
          data-testid="now-request-changes-form"
          onSubmit={(event) => {
            event.preventDefault();
            if (!changes.trim() || !onRequestChanges) return;
            setChangesBusy(true);
            void onRequestChanges(changes.trim()).finally(() => setChangesBusy(false));
          }}
        >
          <input
            aria-label="requested changes"
            value={changes}
            onChange={(event) => setChanges(event.target.value)}
            placeholder="What must change?"
            className="min-w-0 flex-1 rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
          />
          <button type="submit" disabled={changesBusy || !changes.trim()} className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40">
            {changesBusy ? "Starting revision…" : "Start revision"}
          </button>
        </form>
      )}
      {acceptError && (
        <p className="mt-1 text-xs text-critical" data-testid="now-accept-error">
          accept failed: {acceptError}
        </p>
      )}
      <EvidenceDrawerHost run={run} captureClient={client} open={evidenceOpen} onClose={() => setEvidenceOpen(false)} />
    </li>
  );
}
