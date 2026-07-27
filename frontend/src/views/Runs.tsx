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
import { runStatusRole } from "../lib/colors";
import { waitingFor } from "../lib/format";

const FILTERS = ["all", "waiting", "running", "done", "failed"] as const;
type Filter = (typeof FILTERS)[number];

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
            return r.status === "done";
          case "failed":
            return r.status === "failed";
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
            <th className="border-b border-hairline py-1 pr-3 font-normal">started</th>
            <th className="border-b border-hairline py-1 font-normal">run</th>
          </tr>
        </thead>
        <tbody>
          {shown.map((r) => (
            <tr key={r.id} data-testid="runs-row" data-run={r.id}>
              <td className="border-b border-hairline py-1 pr-3">
                <StatusChip role={runStatusRole(r.status)} label={r.status} />
              </td>
              <td className="border-b border-hairline py-1 pr-3">
                {/* Every row links, including the artifact stages. Labelling
                    rows by task_id alone left those anchors empty. */}
                <a href={routeHref({ name: "run", id: r.id })} className="text-ink underline">
                  {runLabel(r)}
                </a>
              </td>
              <td className="border-b border-hairline py-1 pr-3 text-ink-secondary">{r.mode}</td>
              <td className="border-b border-hairline py-1 pr-3 text-ink-secondary">
                {r.verdict || "—"}
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
