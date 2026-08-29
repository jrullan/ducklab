/**
 * The Runs list.
 *
 * This route rendered Overview. Two nav entries showed the same screen, so the
 * one place meant to answer "what has this project actually done" did not
 * exist — and the runs that pause at a human gate were reachable only through
 * a truncated recent-runs list on a dashboard.
 */

import { useMemo, useState } from "react";
import type { Run } from "../api/client";
import { routeHref } from "../app/routes";
import { EmptyState } from "../components/EmptyState";
import { StatusChip } from "../components/StatusChip";
import { runLabel } from "../lib/runview";
import { money } from "../lib/format";
import { runStatusRole, verdictStatus, verdictLabel, type Verdict } from "../lib/colors";
import { waitingFor } from "../lib/format";
import { CollectionToolbar, ContextStrip, InspectorPane, PageHeader } from "../components/PageShell";
import { SideDrawer } from "../components/SideDrawer";

const FILTERS = ["all", "waiting", "running", "landed", "failed"] as const;
type Filter = (typeof FILTERS)[number];

const PAGE_SIZE = 25;

/** A run's wall time, compact: the tracker's own wallclock when recorded,
 * else the started→ended span, else started→now for one still going. */
function verdictText(r: Run): string {
  if (r.resolution === "landed") return "landed";
  if (r.verdict && r.acceptance_gate?.green) return `${verdictLabel(r.verdict as Verdict)} · reproduced green at accept`;
  return r.verdict ? verdictLabel(r.verdict as Verdict) : "—";
}

function took(r: Run): string {
  let secs = r.budget?.wallclock_s ?? 0;
  if (secs <= 0 && r.started_at) {
    const end = r.ended_at ? Date.parse(r.ended_at) : Date.now();
    secs = Math.max(0, (end - Date.parse(r.started_at)) / 1000);
  }
  if (secs <= 0) return "—";
  if (secs < 60) return `${Math.round(secs)}s`;
  const m = Math.floor(secs / 60);
  if (m < 60) return `${m}m${Math.round(secs % 60)}s`;
  return `${Math.floor(m / 60)}h${m % 60}m`;
}

export function Runs({ runs }: { runs: Run[] }) {
  const [filter, setFilter] = useState<Filter>("all");
  const [page, setPage] = useState(1);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const ordered = useMemo(
    () =>
      [...runs].sort((a, b) => {
        const byTime = Date.parse(b.started_at) - Date.parse(a.started_at);
        return byTime || b.id.localeCompare(a.id);
      }),
    [runs],
  );

  const shown = useMemo(
    () =>
      ordered.filter((r) => {
        switch (filter) {
          case "waiting":
            return r.status === "paused";
          case "running":
            return r.status === "running" || r.status === "queued";
          case "landed":
            return r.resolution === "landed";
          case "failed":
            // By outcome, not by status field. A test-first that concluded
            // cleanly with verdict FAILED (its test never landed red) wears
            // status "done" — filtering on status alone hid exactly the runs
            // a person hunting T-110's failed tests was told to relaunch.
            return r.resolution !== "landed" && (r.status === "failed" || r.verdict === "FAILED" || r.verdict === "ABORTED");
          default:
            return true;
        }
      }),
    [ordered, filter],
  );

  const pageCount = Math.max(1, Math.ceil(shown.length / PAGE_SIZE));
  const currentPage = Math.min(page, pageCount);
  const paged = shown.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);
  const selectedRun = runs.find((run) => run.id === selectedId) ?? null;
  const inspector = selectedRun ? <div className="mt-4 space-y-4">
    <div><div className="font-mono text-xs text-ink-muted">{selectedRun.id}</div><div className="mt-1 text-base font-medium text-ink">{runLabel(selectedRun)}</div></div>
    <dl className="space-y-2 border-y border-hairline py-3 text-sm">
      <div className="flex justify-between gap-3"><dt className="text-ink-muted">Status</dt><dd><StatusChip role={selectedRun.resolution === "landed" ? "good" : runStatusRole(selectedRun.status)} label={selectedRun.resolution === "landed" ? "landed" : selectedRun.status} /></dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-muted">Mode</dt><dd className="text-ink">{selectedRun.mode}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-muted">Verdict</dt><dd className="text-right text-ink">{verdictText(selectedRun)}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-muted">Cost</dt><dd className="tabular-nums text-ink">{selectedRun.budget?.usd ? `${selectedRun.tokens_estimated ? "~" : ""}${money(selectedRun.budget.usd)}` : "—"}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-muted">Duration</dt><dd className="tabular-nums text-ink">{took(selectedRun)}</dd></div>
    </dl>
    <a href={routeHref({ name: "run", id: selectedRun.id })} className="block rounded bg-ink px-3 py-2 text-center text-sm text-page">Open full run</a>
  </div> : undefined;

  if (runs.length === 0) {
    return <div className="space-y-4"><PageHeader eyebrow="Records" title="Runs" subtitle="Follow every execution from launch through its final decision." /><EmptyState message="No runs yet. Choose a task in Work to start the first one." /></div>;
  }

  return (
    <div data-testid="runs-view" className="space-y-4">
      <PageHeader
        eyebrow="Records"
        title="Runs"
        subtitle="Follow every execution from launch through its final decision."
      />
      <ContextStrip>
        <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-ink-secondary">
          <span><strong className="font-medium text-ink">{runs.length}</strong> total</span>
          <span><strong className="font-medium text-ink">{runs.filter((r) => r.status === "paused").length}</strong> waiting</span>
          <span><strong className="font-medium text-ink">{runs.filter((r) => r.status === "running" || r.status === "queued").length}</strong> active</span>
          <span><strong className="font-medium text-ink">{runs.filter((r) => r.resolution === "landed").length}</strong> landed</span>
        </div>
      </ContextStrip>
      <CollectionToolbar>
        {FILTERS.map((f) => (
          <button
            key={f}
            data-testid={`runs-filter-${f}`}
            aria-pressed={filter === f}
            onClick={() => {
              setFilter(f);
              setPage(1);
            }}
            className={
              "rounded border px-2 py-1 text-sm " +
              (filter === f ? "border-accent bg-surface2 text-ink" : "border-hairline text-ink-muted")
            }
          >
            {f}
          </button>
        ))}
        <span className="text-sm text-ink-muted" data-testid="runs-count">
          showing {paged.length} of {shown.length}
        </span>
      </CollectionToolbar>
      {pageCount > 1 && (
        <nav className="mb-3 flex items-center gap-2" aria-label="Runs pagination">
          <button
            type="button"
            data-testid="runs-previous"
            disabled={currentPage === 1}
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            className="rounded border border-hairline px-2 py-1 text-sm text-ink disabled:opacity-40"
          >
            Previous
          </button>
          <span className="text-sm text-ink-muted" data-testid="runs-page">
            Page {currentPage} of {pageCount}
          </span>
          <button
            type="button"
            data-testid="runs-next"
            disabled={currentPage === pageCount}
            onClick={() => setPage((p) => Math.min(pageCount, p + 1))}
            className="rounded border border-hairline px-2 py-1 text-sm text-ink disabled:opacity-40"
          >
            Next
          </button>
        </nav>
      )}

      <div className="grid min-h-[30rem] grid-cols-1 overflow-hidden rounded-card border border-hairline bg-surface1 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <div className="overflow-x-auto">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="sticky top-0 bg-surface1 text-left text-ink-muted">
            <th className="border-b border-hairline py-1 pr-3 font-normal">status</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">what</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">mode</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">verdict</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">cost</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">took</th>
            <th className="hidden border-b border-hairline py-1 pr-3 font-normal 2xl:table-cell">turns</th>
            <th className="hidden border-b border-hairline py-1 pr-3 font-normal 2xl:table-cell">started</th>
            <th className="hidden border-b border-hairline py-1 font-normal 2xl:table-cell">run</th>
          </tr>
        </thead>
        <tbody>
          {paged.map((r) => (
            <tr
              key={r.id}
              data-testid="runs-row"
              data-run={r.id}
              aria-selected={selectedId === r.id}
              tabIndex={0}
              onClick={() => setSelectedId(r.id)}
              onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") setSelectedId(r.id); }}
              className={selectedId === r.id ? "bg-surface2 shadow-[inset_3px_0_0_var(--accent)]" : "hover:bg-surface2"}
            >
              <td className="border-b border-hairline py-1 pr-3">
                <StatusChip role={r.resolution === "landed" ? "good" : runStatusRole(r.status)} label={r.resolution === "landed" ? "landed" : r.status} />
                {r.status === "queued" && r.queued_reason && (
                  <p className="mt-1 text-xs text-ink-secondary">{r.queued_reason === "engine at max_concurrent_runs" ? <a href="#/settings?section=engine" className="underline">{r.queued_reason}</a> : r.queued_reason}</p>
                )}
              </td>
              <td className="border-b border-hairline py-1 pr-3">
                {/* Every row links, including the artifact stages. Labelling
                    rows by task_id alone left those anchors empty. */}
                <a href={routeHref({ name: "run", id: r.id })} className="text-ink underline">
                  {runLabel(r)}
                </a>
              </td>
              <td className="border-b border-hairline py-1 pr-3 text-ink-secondary">{r.mode}</td>
              <td className="border-b border-hairline py-1 pr-3">
                {/* A chip, not grey prose: done-with-FAILED-verdict rows read
                    as successes when the one word that says otherwise is the
                    quietest thing in the row. */}
                {r.verdict ? (
                  <StatusChip role={r.resolution === "landed" ? "good" : verdictStatus(r.verdict as Verdict)} label={verdictText(r)} />
                ) : (
                  <span className="text-ink-secondary">—</span>
                )}
              </td>
              {/* What the run spent — live for a running one, since the
                  record serves the tracker's numbers. ~ marks a cost built on
                  estimated token counts, never presented as measured. */}
              <td className="border-b border-hairline py-1 pr-3 tabular-nums text-ink-secondary" data-testid="run-cost">
                {r.budget && r.budget.usd > 0
                  ? `${r.tokens_estimated ? "~" : ""}${money(r.budget.usd)}`
                  : "—"}
              </td>
              <td className="border-b border-hairline py-1 pr-3 tabular-nums text-ink-secondary" data-testid="run-took">
                {took(r)}
              </td>
              <td className="hidden border-b border-hairline py-1 pr-3 tabular-nums text-ink-secondary 2xl:table-cell" data-testid="run-turns">
                {r.budget && r.budget.turns > 0 ? r.budget.turns : "—"}
              </td>
              <td className="hidden border-b border-hairline py-1 pr-3 text-ink-muted 2xl:table-cell">
                {r.started_at ? waitingFor(r.started_at) + " ago" : "—"}
              </td>
              <td className="hidden border-b border-hairline py-1 font-mono text-xs text-ink-muted 2xl:table-cell">
                {r.id}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      </div>
      <div className="hidden lg:block"><InspectorPane title="Run inspector" empty="Select a run to inspect its outcome and open its full record.">{inspector}</InspectorPane></div>
      </div>
      {selectedRun && <div className="lg:hidden"><SideDrawer title="Run inspector" subtitle={runLabel(selectedRun)} onClose={() => setSelectedId(null)}>{inspector}</SideDrawer></div>}
    </div>
  );
}
