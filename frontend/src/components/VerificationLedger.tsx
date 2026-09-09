import { useState } from "react";
import type { Bug, EngineClient } from "../api/client";
import { SideDrawer } from "./SideDrawer";
import { Prose } from "./Prose";

/**
 * The verification queue, folded (B-281, absorbing B-216).
 *
 * N fixed bugs used to render as N identical ~180px cards, each saying "try
 * what the report describes" — a homogeneous work queue drawn as N unique
 * decisions, drowning everything else in Now. This is one card for the whole
 * queue; the drawer holds compact rows with the two verdict buttons inline,
 * and an expanded row tells the person HOW to verify: what the report
 * describes, and what the fix actually changed, read from the fixing task's
 * accepted commit. When a fix is internal, that commit and its test are the
 * evidence; the row says so instead of implying a manual repro.
 */
export function VerificationLedger({
  bugs,
  client,
  projectId,
  onMoved,
}: {
  bugs: Bug[];
  client: EngineClient;
  projectId: string;
  onMoved: (bugId: string, status: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  // What the fix changed, by task id, fetched once per expansion. null while
  // loading; a string once known; an Error-ish string when the read failed.
  const [changes, setChanges] = useState<Record<string, string | null>>({});

  if (bugs.length === 0) return null;
  const n = bugs.length;
  const headline = n === 1 ? "1 fixed bug awaits your verification" : `${n} fixed bugs await your verification`;

  const expand = (bug: Bug) => {
    const next = expanded === bug.id ? null : bug.id;
    setExpanded(next);
    if (!next || !bug.task_id || bug.task_id in changes || typeof client.review !== "function") return;
    const task = bug.task_id;
    setChanges((cur) => ({ ...cur, [task]: null }));
    client
      .review(projectId, task)
      .then((markdown) => setChanges((cur) => ({ ...cur, [task]: markdown || "" })))
      .catch(() => setChanges((cur) => ({ ...cur, [task]: "" })));
  };

  const move = (bug: Bug, status: string) =>
    void client
      .moveBug(projectId, bug.id, status)
      .then(() => onMoved(bug.id, status))
      .catch(() => {});

  return (
    <>
      <div data-testid="now-verify-ledger" className="rounded-card border border-hairline p-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <p className="text-sm text-ink">{headline}</p>
          <button
            type="button"
            data-testid="now-verify-open"
            onClick={() => setOpen(true)}
            className="rounded border border-hairline px-2 py-1 text-xs"
          >
            Review {n === 1 ? "it" : `all ${n}`}
          </button>
        </div>
        <p className="mt-1 text-xs text-ink-muted">
          Each row says how to verify: what the report describes and what the fix changed. The gate that passed may prove much less.
        </p>
        <p className="mt-1 truncate font-mono text-xs text-ink-muted" title={bugs.map((b) => b.id).join(", ")}>
          {bugs.slice(0, 6).map((b) => b.id).join(" · ")}
          {n > 6 ? ` · +${n - 6}` : ""}
        </p>
      </div>
      {open && (
        <SideDrawer
          title={headline}
          subtitle="Verified means the report is answered, not that a gate passed. Reopen anything that is still broken."
          onClose={() => setOpen(false)}
          testId="now-verify-drawer"
        >
          <ul className="space-y-2">
            {bugs.map((b) => {
              const isOpen = expanded === b.id;
              const changed = b.task_id ? changes[b.task_id] : undefined;
              return (
                <li key={b.id} data-testid="now-verify-row" className="rounded border border-hairline p-2">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="font-mono text-sm text-ink">{b.id}</span>
                    <span className="min-w-0 flex-1 truncate text-sm text-ink-secondary" title={b.title}>{b.title}</span>
                    {b.task_id && <span className="text-xs text-ink-muted">fixed by {b.task_id}</span>}
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-2">
                    {(b.next ?? []).includes("verified") && (
                      <button type="button" data-testid="now-verify-yes" onClick={() => move(b, "verified")} className="rounded border border-hairline px-2 py-0.5 text-xs">
                        Verified — it works
                      </button>
                    )}
                    {(b.next ?? []).includes("in_progress") && (
                      <button type="button" data-testid="now-verify-no" onClick={() => move(b, "in_progress")} className="rounded border border-hairline px-2 py-0.5 text-xs">
                        Still broken
                      </button>
                    )}
                    <button
                      type="button"
                      data-testid="now-verify-expand"
                      aria-expanded={isOpen}
                      onClick={() => expand(b)}
                      className="text-xs text-ink-muted underline"
                    >
                      {isOpen ? "hide" : "how to verify"}
                    </button>
                  </div>
                  {isOpen && (
                    <div data-testid="now-verify-guide" className="mt-2 space-y-3 border-t border-hairline pt-2">
                      <section>
                        <h4 className="text-xs font-medium text-ink">What the report describes</h4>
                        {b.body?.trim() ? (
                          <Prose body={b.body} className="mt-1 space-y-1 text-xs text-ink-secondary" />
                        ) : (
                          <p className="mt-1 text-xs text-ink-muted">The report has no body; its title is all there is to try.</p>
                        )}
                      </section>
                      <section>
                        <h4 className="text-xs font-medium text-ink">What the fix changed</h4>
                        {!b.task_id ? (
                          <p className="mt-1 text-xs text-ink-muted">No task is recorded for this fix. Verify from the report alone.</p>
                        ) : changed === undefined || changed === null ? (
                          <p className="mt-1 text-xs text-ink-muted">Reading {b.task_id}&apos;s accepted commit…</p>
                        ) : changed === "" ? (
                          <p className="mt-1 text-xs text-ink-muted">{b.task_id} has no readable accepted commit. Verify from the report.</p>
                        ) : (
                          <Prose body={changed} className="mt-1 space-y-1 text-xs text-ink-secondary" />
                        )}
                        {b.task_id && (
                          <p className="mt-1 text-xs text-ink-muted">
                            An internal fix is proven by its test, not by a manual repro: if the commit names one, run it and mark Verified on that evidence.
                          </p>
                        )}
                      </section>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </SideDrawer>
      )}
    </>
  );
}
