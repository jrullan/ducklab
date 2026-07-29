/**
 * The decision a person makes about a proposed document.
 *
 * One component, used by both the Cycle view and the run's own view, because
 * the same gate shows up in both and they had drifted: Cycle offered only
 * Accept with the note box at the bottom, the run offered Accept, Reject and
 * Abort with the note box at the top. Consistency by construction rather than
 * by remembering.
 *
 * Three answers, and only three:
 *
 *   Accept          — take it. The document is promoted and the run closes.
 *   Request changes — almost right. The draft goes back with a note, and what
 *                     comes back is a revision rather than a fresh draft.
 *   Reject          — do not take it. The run closes so it stops sitting in
 *                     the inbox claiming to wait for you; the draft stays on
 *                     disk, because it is the only record of what the
 *                     ducklings actually produced.
 *
 * There is no Abort here. Aborting stops work in progress, and a run at a gate
 * is not working — it is waiting for this. Offering both made Reject and Abort
 * look like two ways to say no, when only one of them records a decision.
 */

import { useState } from "react";

export function StageGate({
  title,
  subtitle,
  onAccept,
  onReject,
  onRequestChanges,
  accepting,
  extraAction,
  revisionRun,
}: {
  title: string;
  subtitle?: React.ReactNode;
  onAccept: () => void;
  onReject: () => void;
  onRequestChanges: (note: string) => Promise<void>;
  accepting?: boolean;
  /** A view-specific control, like the Cycle view's read/diff toggle. */
  extraAction?: React.ReactNode;
  /** The run started by the last Request changes, so it can be watched. */
  revisionRun?: string | null;
}) {
  const [note, setNote] = useState("");
  const [asking, setAsking] = useState(false);

  const ask = async () => {
    if (!note.trim()) return;
    setAsking(true);
    try {
      await onRequestChanges(note.trim());
      setNote("");
    } finally {
      setAsking(false);
    }
  };

  return (
    <div data-testid="stage-gate">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-sm font-medium text-ink">{title}</div>
          {subtitle && <div className="text-xs text-ink-muted">{subtitle}</div>}
        </div>
        <div className="flex items-center gap-2">
          {extraAction}
          <button
            type="button"
            onClick={onAccept}
            disabled={accepting}
            data-testid="cycle-accept"
            className="rounded border border-hairline px-3 py-1 text-sm text-ink disabled:opacity-50"
          >
            {accepting ? "Accepting…" : "Accept"}
          </button>
          <button
            type="button"
            onClick={onReject}
            data-testid="reject-button"
            className="rounded border border-hairline px-3 py-1 text-sm"
          >
            Reject
          </button>
        </div>
      </div>

      {/* Above the document, not below it: the decision is what this screen is
          for, and a control under a long draft is a control nobody scrolls to. */}
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
            className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
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
            The draft goes back with your note. Everything you did not mention is meant to come
            back unchanged — check what changed before accepting.
          </span>
        </div>
      </div>
    </div>
  );
}
