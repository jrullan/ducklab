/**
 * The Review view: what reviewers said about accepted work (08 §4).
 *
 * A review is a reading of a commit, so the list is of readings and the pane
 * is the reading itself. Nothing here is actionable — the review already
 * happened, and what to do about it is a decision made elsewhere.
 */

import { useCallback, useEffect, useState } from "react";
import type { EngineClient, ReviewSummary } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { Prose } from "../components/Prose";
import { StatusChip } from "../components/StatusChip";

export function Review({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [items, setItems] = useState<ReviewSummary[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [markdown, setMarkdown] = useState("");
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);
    try {
      const list = await client.reviews(projectId);
      setItems(list);
      // Open the newest by default. A list of reviews with nothing shown makes
      // the reader click before they learn anything.
      if (list.length > 0 && selected === null) setSelected(list[0]!.task_id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
    // selected is deliberately not a dependency: reloading must not fight the
    // reader's choice.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, projectId]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (selected === null) return;
    let cancelled = false;
    client
      .review(projectId, selected)
      .then((md) => !cancelled && setMarkdown(md))
      .catch((err) => !cancelled && setFailure(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [client, projectId, selected]);

  if (!loading && items.length === 0 && !failure) {
    return (
      <EmptyState message="No reviews yet — run `ducklab review <task>` on work that has been accepted." />
    );
  }

  return (
    <div data-testid="review-view" className="flex gap-6">
      <nav className="w-64 shrink-0 space-y-1">
        {items.map((r) => (
          <button
            key={r.task_id}
            data-testid="review-item"
            data-task={r.task_id}
            aria-pressed={selected === r.task_id}
            onClick={() => setSelected(r.task_id)}
            className={
              "w-full rounded-card border p-2 text-left " +
              (selected === r.task_id ? "border-serious" : "border-hairline")
            }
          >
            <div className="font-mono text-xs text-ink-muted">{r.task_id}</div>
            <div className="mt-1">
              <StatusChip
                role={r.verdict === "approve" ? "good" : "serious"}
                label={r.verdict || "unreviewed"}
              />
            </div>
            {r.findings > 0 && (
              <div className="mt-1 text-xs text-ink-muted">
                {r.findings} finding{r.findings === 1 ? "" : "s"}
              </div>
            )}
          </button>
        ))}
      </nav>

      <div className="min-w-0 flex-1">
        {failure && (
          <div data-testid="review-error" className="mb-3 text-sm text-critical">
            {failure}
          </div>
        )}
        {markdown ? (
          <Prose body={stripFrontmatter(markdown)} suppress={[]} />
        ) : (
          <p className="text-sm text-ink-muted">Select a review.</p>
        )}
      </div>
    </div>
  );
}

/** Drops the frontmatter a document carries for machines.
 *
 * The list beside it already shows the verdict and the commit; repeating them
 * as raw YAML at the top of the pane is noise the reader has to scroll past. */
export function stripFrontmatter(md: string): string {
  if (!md.startsWith("---\n")) return md;
  const end = md.indexOf("\n---\n", 4);
  if (end < 0) return md;
  return md.slice(end + 5);
}
