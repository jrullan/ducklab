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

const FILTERS = ["all", "waiting", "running", "done", "landed", "failed"] as const;
type Filter = (typeof FILTERS)[number];

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

  const ordered = useMemo(
    () => [...runs].sort((a, b) => (a.started_at < b.started_at ? 1 : -1)),
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
          case "done":
            return r.status === "done" && r.resolution !== "landed";
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

  if (runs.length === 0) {
    return <EmptyState message="No runs yet. Pick a task on the Board and press Run." />;
  }

  return (
    <div data-testid="runs-view">
      <div className="mb-3 flex items-center gap-2">
        {FILTERS.map((f) => (
          <button
            key={f}
            data-testid={`runs-filter-${f}`}
            aria-pressed={filter === f}
            onClick={() => setFilter(f)}
            className={
              "rounded border px-2 py-1 text-sm " +
              (filter === f ? "border-ink text-ink" : "border-hairline text-ink-muted")
            }
          >
            {f}
          </button>
        ))}
        <span className="text-sm text-ink-muted">
          {shown.length} of {runs.length}
        </span>
      </div>

      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="text-left text-ink-muted">
            <th className="border-b border-hairline py-1 pr-3 font-normal">status</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">what</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">mode</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">verdict</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">cost</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">took</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">turns</th>
            <th className="border-b border-hairline py-1 pr-3 font-normal">started</th>
            <th className="border-b border-hairline py-1 font-normal">run</th>
          </tr>
        </thead>
        <tbody>
          {shown.map((r) => (
            <tr key={r.id} data-testid="runs-row" data-run={r.id}>
              <td className="border-b border-hairline py-1 pr-3">
                <StatusChip role={r.resolution === "landed" ? "good" : runStatusRole(r.status)} label={r.resolution === "landed" ? "landed" : r.status} />
                {r.status === "queued" && r.queued_reason && (
                  <p className="mt-1 text-xs text-ink-secondary">{r.queued_reason}</p>
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
              <td className="border-b border-hairline py-1 pr-3 tabular-nums text-ink-secondary" data-testid="run-turns">
                {r.budget && r.budget.turns > 0 ? r.budget.turns : "—"}
              </td>
              <td className="border-b border-hairline py-1 pr-3 text-ink-muted">
                {r.started_at ? waitingFor(r.started_at) + " ago" : "—"}
              </td>
              <td className="border-b border-hairline py-1 font-mono text-xs text-ink-muted">
                {r.id}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
