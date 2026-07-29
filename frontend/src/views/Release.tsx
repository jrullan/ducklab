/**
 * The Release view: what shipped, and what is waiting to (08 §4).
 *
 * A drafted release and a cut one are different claims. The list says which is
 * which, because a draft read as shipped is the expensive mistake here — it is
 * a statement about software someone may act on.
 */

import { useCallback, useEffect, useState } from "react";
import type { EngineClient, ReleaseSummary } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { Prose } from "../components/Prose";
import { StatusChip } from "../components/StatusChip";
import { stripFrontmatter } from "./Review";

export function Release({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [items, setItems] = useState<ReleaseSummary[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [markdown, setMarkdown] = useState("");
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);
    try {
      const list = await client.releases(projectId);
      setItems(list);
      if (list.length > 0 && selected === null) setSelected(list[0]!.version);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, projectId]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (selected === null) return;
    let cancelled = false;
    client
      .release(projectId, selected)
      .then((md) => !cancelled && setMarkdown(md))
      .catch((err) => !cancelled && setFailure(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [client, projectId, selected]);

  if (!loading && items.length === 0 && !failure) {
    return <EmptyState message="No releases yet — run `ducklab release plan` to draft one." />;
  }

  const current = items.find((r) => r.version === selected) ?? null;

  return (
    <div data-testid="release-view" className="flex gap-6">
      <nav className="w-64 shrink-0 space-y-1">
        {items.map((r) => (
          <button
            key={r.version}
            data-testid="release-item"
            data-version={r.version}
            aria-pressed={selected === r.version}
            onClick={() => setSelected(r.version)}
            className={
              "w-full rounded-card border p-2 text-left " +
              (selected === r.version ? "border-serious" : "border-hairline")
            }
          >
            <div className="font-mono text-sm text-ink">{r.version}</div>
            <div className="mt-1">
              {/* A draft is not a release. Saying "drafted" rather than
                  showing nothing keeps an unapproved set of notes from being
                  read as a statement about shipped software. */}
              <StatusChip
                role={r.drafted ? "warning" : r.tagged ? "good" : "serious"}
                label={r.drafted ? "drafted" : r.tagged ? "tagged" : "untagged"}
              />
            </div>
            <div className="mt-1 text-xs text-ink-muted">
              {r.tasks} task{r.tasks === 1 ? "" : "s"}
              {r.since ? ` since ${r.since}` : ""}
            </div>
          </button>
        ))}
      </nav>

      <div className="min-w-0 flex-1">
        {failure && (
          <div data-testid="release-error" className="mb-3 text-sm text-critical">
            {failure}
          </div>
        )}

        {current?.drafted && (
          <div
            data-testid="release-draft-notice"
            className="mb-3 rounded-card border border-serious p-3 text-sm"
          >
            <div className="text-ink">These notes are a draft.</div>
            <div className="mt-1 font-mono text-xs text-ink-secondary">
              ducklab release cut {current.version}
            </div>
          </div>
        )}

        {current && current.unverified ? (
          <p data-testid="release-unverified" className="mb-3 text-sm text-serious">
            {current.unverified} of these changes were accepted with no gate that could run.
          </p>
        ) : null}

        {markdown ? (
          <Prose body={stripFrontmatter(markdown)} suppress={[]} />
        ) : (
          <p className="text-sm text-ink-muted">Select a release.</p>
        )}
      </div>
    </div>
  );
}
