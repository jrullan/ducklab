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
  const [busy, setBusy] = useState(false);
  const [planned, setPlanned] = useState<string | null>(null);
  const [bump, setBump] = useState<"patch" | "minor" | "major">("minor");

  // Drafting starts a release RUN — a scribe writes the prose over the
  // deterministically collected changelog — so the affordance here is
  // "start it and point at the run", not a spinner that hides one.
  const draft = async (bump: string) => {
    setBusy(true);
    setFailure(null);
    try {
      const run = await client.releasePlan(projectId, bump);
      setPlanned(run.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const cut = async (version: string) => {
    setBusy(true);
    setFailure(null);
    try {
      await client.releaseCut(projectId, version);
      await load();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

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
    // The view used to point at the CLI from inside the desktop — the one
    // place a desktop user cannot go. The affordance lives here now.
    return (
      <div className="p-4">
        <EmptyState message="No releases yet." />
        {planned ? (
          <p className="mt-2 text-sm text-ink-secondary" data-testid="release-planned">
            Drafting — watch the release run in Now, then come back to cut it.
          </p>
        ) : (
          <div className="mt-3 flex items-center gap-2">
            <button
              type="button"
              data-testid="release-draft"
              disabled={busy}
              onClick={() => void draft("minor")}
              className="rounded border border-hairline px-3 py-1 text-sm text-ink"
            >
              Draft a release
            </button>
            <span className="text-xs text-ink-muted">
              collects accepted work since the last tag; a scribe writes the notes
            </span>
          </div>
        )}
        {failure && (
          <p className="mt-2 text-sm text-critical" data-testid="release-error">
            {failure}
          </p>
        )}
      </div>
    );
  }

  const current = items.find((r) => r.version === selected) ?? null;
  const pendingDraft = items.find((r) => r.drafted);

  return (
    <div data-testid="release-view" className="flex gap-6">
      <nav className="w-64 shrink-0 space-y-1">
        {/* The door to the NEXT release lives with the list, not only in the
            empty state: with one release on file there was no way to draft
            the second from the desktop. One draft at a time — while one
            waits, the door says so instead of opening a second. */}
        {!loading && <div className="mb-2 rounded-card border border-hairline p-2" data-testid="release-next">
          {planned ? (
            <p className="text-xs text-ink-secondary" data-testid="release-planned">Drafting — watch the release run in Now, then come back to cut it.</p>
          ) : pendingDraft ? (
            <p className="text-xs text-ink-muted">a draft is waiting: cut or revise {pendingDraft.version} before drafting another</p>
          ) : (
            <div className="flex items-center gap-2">
              <button type="button" data-testid="release-draft" disabled={busy} onClick={() => void draft(bump)} className="rounded border border-hairline px-2 py-1 text-sm text-ink">Draft next release</button>
              <select aria-label="version bump" data-testid="release-bump" value={bump} onChange={(e) => setBump(e.target.value as typeof bump)} className="rounded border border-hairline bg-surface px-1 py-1 text-xs text-ink">
                <option value="patch">patch</option><option value="minor">minor</option><option value="major">major</option>
              </select>
            </div>
          )}
        </div>}
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
            className="mb-3 flex items-center justify-between rounded-card border border-serious p-3 text-sm"
          >
            <div className="text-ink">
              These notes are a draft. Cutting tags {current.version} and makes them the record.
            </div>
            <button
              type="button"
              data-testid="release-cut"
              disabled={busy}
              onClick={() => void cut(current.version)}
              className="rounded border border-serious px-3 py-1 text-ink"
            >
              Cut {current.version}
            </button>
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
