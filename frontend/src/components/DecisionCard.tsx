/**
 * The one shape every human decision takes: claim, evidence, consequence,
 * verdict (docs/ux-evaluation.md §5.3).
 *
 * Grown out of StageGate, which proved the idea for documents — the same gate
 * had appeared in two views with two layouts until one component made drift
 * impossible. This finishes the job for every gate kind, with one structural
 * change: the verdict buttons come from the engine's `next` list, never from
 * this component's opinion of the run's state. An action the engine did not
 * offer cannot render; one it offered cannot be missing.
 *
 * The consequence line is required. Three incidents in the first real project
 * were the person discovering after the click what the click did — or did not
 * do. What accepting does is part of the question being asked.
 */

import { useState } from "react";

export function DecisionCard({
  next,
  title,
  subtitle,
  consequence,
  cost,
  onAccept,
  onReject,
  onRequestChanges,
  onResume,
  onAbort,
  accepting,
  extraAction,
  revisionRun,
  redoNote,
  onRetry,
  documentGate,
  landedAs,
  dissent,
  acceptAndFix,
  fileFindings,
}: {
  /** The engine's list of legal actions. Buttons render from this and only
   * this. */
  next: readonly string[];
  title: string;
  subtitle?: React.ReactNode;
  /** What accepting does, stated before the click: "replaces the approved
   * spec", "applies 2 classifications", "commits the diff". */
  consequence: string;
  /** What the work cost, when known: tokens, dollars, time. */
  cost?: string;
  onAccept: () => void;
  onReject: () => void;
  onRequestChanges?: (note: string) => Promise<void>;
  onResume?: () => void;
  onAbort?: () => void;
  accepting?: boolean;
  /** A view-specific control, like the Cycle view's read/diff toggle. */
  extraAction?: React.ReactNode;
  /** The run started by the last Request changes, so it can be watched. */
  revisionRun?: string | null;
  /** An advisor recommendation shown as an editable retry draft. */
  redoNote?: { draft: string; advisor: string; editable: boolean };
  onRetry?: (note: string) => void;
  /** Document gates revise; their other exit is explicitly a discard. */
  documentGate?: boolean;
  /** B-247: the task's work already landed under this commit. Accepting
   * this run lands a second change on top, and the card must say so. */
  landedAs?: string;
  /** B-261: a green gate over an unconvinced reviewer belongs INSIDE the
   * decision, not beside it. */
  dissent?: { verdict: string; findings: number; notes: string[] } | null;
  /** The one-click "accept what passed, then a follow-up run that reads the
   * objections". It launches a run, and the card says so before the click. */
  acceptAndFix?: { busy: boolean; error?: string | null; mode: string; spent?: string; onClick: () => void };
  /** Filing the reviewer's findings as bugs: a secondary action of the same
   * decision, not a sibling card. */
  fileFindings?: {
    items: { severity?: string; issue: string; file?: string; line?: number; fix?: string }[];
    filed: string[] | null;
    busy: boolean;
    error?: string | null;
    onFile: () => void;
    boardHref: string;
  };
}) {
  const [note, setNote] = useState("");
  const [redoDraft, setRedoDraft] = useState(redoNote?.draft ?? "");
  const [asking, setAsking] = useState(false);

  const offers = (verb: string) => next.includes(verb);

  const ask = async () => {
    if (!note.trim() || !onRequestChanges) return;
    setAsking(true);
    try {
      await onRequestChanges(note.trim());
      setNote("");
    } finally {
      setAsking(false);
    }
  };

  return (
    <div data-testid="decision-card">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-sm font-medium text-ink">{title}</div>
          {subtitle && <div className="text-xs text-ink-muted">{subtitle}</div>}
        </div>
        <div className="flex items-center gap-2">
          {extraAction}
          {offers("abort") && onAbort && (
            <button
              type="button"
              onClick={onAbort}
              data-testid="abort-button"
              className="rounded border border-hairline px-3 py-1 text-sm"
            >
              Abort
            </button>
          )}
          {offers("resume") && onResume && (
            <button
              type="button"
              onClick={onResume}
              data-testid="resume-button"
              className="rounded border border-hairline px-3 py-1 text-sm text-ink"
            >
              Resume
            </button>
          )}
          {offers("accept") && (
            <button
              type="button"
              onClick={onAccept}
              disabled={accepting}
              data-testid="cycle-accept"
              className="rounded border border-hairline px-3 py-1 text-sm text-ink disabled:opacity-50"
            >
              {accepting ? "Accepting…" : "Accept"}
            </button>
          )}
          {offers("reject") && (
            <button
              type="button"
              onClick={onReject}
              data-testid="reject-button"
              className="rounded border border-hairline px-3 py-1 text-sm"
            >
              {documentGate ? "Discard draft" : "Reject"}
            </button>
          )}
        </div>
      </div>

      {landedAs && (
        <p className="mb-2 rounded border border-warn px-2 py-1 text-xs text-ink" data-testid="landed-notice">
          This task already landed as <span className="font-mono">{landedAs.slice(0, 7)}</span> — accepting this re-run lands a second change on top of it. Only accept a deliberate redo.
        </p>
      )}

      {dissent && (
        <div className="mb-2 rounded border border-serious p-2" data-testid="decision-dissent">
          <span className="text-xs font-medium text-serious">green gate, unconvinced reviewer</span>
          <p className="mt-1 text-sm text-ink">
            The tests pass, but the reviewer's last verdict was “{dissent.verdict}”
            {dissent.findings > 0 && ` with ${dissent.findings} finding${dissent.findings === 1 ? "" : "s"}`}
            {" "}and its rounds ran out. The gate decides the verdict; the reviewer only advises — read them before deciding:
          </p>
          <ul className="mt-1 space-y-1 text-sm" data-testid="dissent-findings-list">
            {dissent.notes.map((n, i) => (
              <li key={i} className="text-ink-secondary">{n}</li>
            ))}
          </ul>
        </div>
      )}

      {redoNote && redoNote.editable && (
        <div className="mb-3 border-b border-hairline pb-3" data-testid="redo-note">
          <div className="text-xs text-ink-muted">Retry with this note · advisor-drafted by {redoNote.advisor}</div>
          <textarea aria-label="retry note" rows={5} value={redoDraft} onChange={(e) => setRedoDraft(e.target.value)}
            className="mt-1 w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm" />
          <div className="mt-1 flex items-center gap-2 text-xs text-ink-muted">
            {onRetry && <button type="button" onClick={() => onRetry(redoDraft)} disabled={!redoDraft.trim()} className="rounded border border-hairline px-2 py-1 text-sm text-ink">Retry with this note</button>}
            <span>Edit before retrying; the advisor recommends, never decides.</span>
          </div>
        </div>
      )}

      {/* Stated before the click, in the frame, where every gate kind carries
          it the same way. */}
      <p className="mb-2 text-xs text-ink-secondary" data-testid="decision-consequence">
        {offers("accept") ? `Accepting ${consequence}` : consequence}
        {cost && <span className="text-ink-muted"> · {cost}</span>}
      </p>

      {/* The competing Accept, with its cost stated where the plain Accept's
          is: this one also LAUNCHES a run, and a label alone hid that. */}
      {offers("accept") && acceptAndFix && (
        <div className="mb-3 border-b border-hairline pb-3" data-testid="accept-and-fix-block">
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              data-testid="accept-and-fix"
              disabled={acceptAndFix.busy || accepting}
              onClick={acceptAndFix.onClick}
              className="rounded border border-hairline px-3 py-1 text-sm text-ink disabled:opacity-50"
            >
              {acceptAndFix.busy ? "Starting…" : "Accept, then fix the findings"}
            </button>
            <span className="rounded border border-warn px-1.5 py-0.5 text-xs text-ink" data-testid="accept-and-fix-launches">launches a new run</span>
          </div>
          <p className="mt-1 text-xs text-ink-secondary" data-testid="accept-and-fix-consequence">
            Accepting, then fixing {consequence}, exactly like Accept, and then starts a new {acceptAndFix.mode} run that reads
            {dissent && dissent.findings > 0 ? ` the ${dissent.findings} finding${dissent.findings === 1 ? "" : "s"}` : " the findings"} — a fresh roll of the dice
            {acceptAndFix.spent ? `; the ${acceptAndFix.spent} already spent stays on the record` : ""}.
          </p>
          {acceptAndFix.error && (
            <p className="mt-1 text-xs text-critical" data-testid="accept-and-fix-error">{acceptAndFix.error}</p>
          )}
        </div>
      )}

      {fileFindings && fileFindings.items.length > 0 && (
        <div className="mb-3 border-b border-hairline pb-3" data-testid="file-findings">
          <p className="text-xs text-ink-secondary">
            The reviewer's last verdict carries {fileFindings.items.length} finding{fileFindings.items.length === 1 ? "" : "s"}. Filed as bugs they enter the loop — triage, promote, fix — instead of waiting in this transcript. Filing does not decide this run.
          </p>
          <ul className="mt-1 space-y-1 text-sm" data-testid="file-findings-list">
            {fileFindings.items.map((f, i) => (
              <li key={i} className="text-ink-secondary">
                <div className="line-clamp-3" title={`${f.issue}${f.fix ? ` · fix: ${f.fix}` : ""}`}>
                  {f.severity && <span className="mr-1 text-xs uppercase text-ink-muted">[{f.severity}]</span>}
                  {f.issue}
                  {f.file && <span className="text-ink-muted"> — {f.file}{f.line ? `:${f.line}` : ""}</span>}
                  {f.fix && <span className="text-ink-muted"> · fix: {f.fix}</span>}
                </div>
              </li>
            ))}
          </ul>
          <div className="mt-1">
            {fileFindings.filed ? (
              <p className="text-sm text-good" data-testid="file-findings-done">
                filed as {fileFindings.filed.join(", ")} — <a href={fileFindings.boardHref} className="underline">see the bugs board</a>
              </p>
            ) : (
              <button
                type="button"
                data-testid="file-findings-button"
                disabled={fileFindings.busy}
                onClick={fileFindings.onFile}
                className="rounded border border-hairline px-2 py-1 text-sm"
              >
                {fileFindings.busy ? "Filing…" : `File ${fileFindings.items.length} finding${fileFindings.items.length === 1 ? "" : "s"} as bugs`}
              </button>
            )}
            {fileFindings.error && <p className="mt-1 text-xs text-critical" data-testid="file-findings-error">{fileFindings.error}</p>}
          </div>
        </div>
      )}

      {/* Above the document, not below it: the decision is what this screen is
          for, and a control under a long draft is a control nobody scrolls to. */}
      {offers("request_changes") && onRequestChanges && (
        <div className="mb-3 border-b border-hairline pb-3" data-testid="request-changes">
          <textarea
            aria-label="what to change"
            data-testid="change-note"
            rows={2}
            placeholder="Right except SPEC-004 — locking an angle should also stop the opposite vertex from being dragged."
            value={note}
            onChange={(e) => setNote(e.target.value)}
            className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
          />
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <button
              type="button"
              onClick={() => void ask()}
              disabled={asking || !note.trim()}
              data-testid="request-changes-button"
              className="rounded border border-good bg-good px-2 py-1 text-sm text-page disabled:opacity-40"
            >
              {asking ? "Asking…" : "Request changes"}
            </button>
            {revisionRun && (
              <a
                href={`#/runs/${revisionRun}`}
                data-testid="revision-run-link"
                className="text-sm text-ink underline"
              >
                watch the revision
              </a>
            )}
            <span className="text-xs text-ink-muted">
              The draft goes back with your note. Everything you did not mention is meant to
              come back unchanged — check what changed before accepting.
            </span>
          </div>
        </div>
      )}
    </div>
  );
}
