/**
 * Reports — where the thesis gets measured (08 §4.7).
 *
 * The headline is a number, not a chart: did the combination beat the single
 * model, and by how much. Everything below it exists to say whether that
 * number should be believed — how many runs, what they cost, and whether the
 * counts were measured or estimated.
 */

import { useCallback, useEffect, useState } from "react";
import type { EngineClient, ReportDelta, ReportRow } from "../api/client";
import { BarChart, ChartFrame, OutcomeMix } from "../components/Chart";
import { EmptyState } from "../components/EmptyState";
import { Bench } from "./Bench";

const RANGES = [
  { label: "all time", value: "" },
  { label: "7 days", value: "7d" },
  { label: "30 days", value: "30d" },
  { label: "90 days", value: "90d" },
] as const;

export function Reports({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [tab, setTab] = useState<"runs" | "bench">("runs");
  const [since, setSince] = useState("");
  const [byMode, setByMode] = useState<{ rows: ReportRow[]; deltas: ReportDelta[] } | null>(null);
  const [byDuckling, setByDuckling] = useState<ReportRow[]>([]);
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);
    try {
      // Two questions, two shapes. Asking by mode and by duckling separately
      // is what lets the page say "pair beat solo" and "on which model".
      const [modes, ducklings] = await Promise.all([
        client.report(projectId, "mode", since),
        client.report(projectId, "duckling", since),
      ]);
      setByMode({ rows: modes.rows, deltas: modes.deltas });
      setByDuckling(ducklings.rows);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [client, projectId, since]);

  useEffect(() => {
    void load();
  }, [load]);

  // The tabs live above everything, including the loading and empty states: a
  // project with no runs still has benches worth looking at, and hiding the
  // tab behind "nothing to measure" would make them unreachable.
  const tabs = (
    <div className="mb-3 flex gap-2 border-b border-hairline">
      {(["runs", "bench"] as const).map((t) => (
        <button
          key={t}
          type="button"
          onClick={() => setTab(t)}
          data-testid={`reports-tab-${t}`}
          className={`px-2 py-1 text-sm ${tab === t ? "text-ink" : "text-ink-muted"}`}
        >
          {t === "runs" ? "This project" : "Bench"}
        </button>
      ))}
    </div>
  );

  if (tab === "bench") {
    return (
      <div data-testid="reports-view">
        {tabs}
        <Bench client={client} />
      </div>
    );
  }

  if (loading && !byMode) return <p className="text-ink-muted">Loading…</p>;
  if (failure) {
    return (
      <p className="text-critical" data-testid="reports-error">
        {failure}
      </p>
    );
  }

  const rows = byMode?.rows ?? [];
  const solo = rows.find((r) => r.key === "solo");
  const best = (byMode?.deltas ?? []).slice().sort((a, b) => b.points_vs_baseline - a.points_vs_baseline)[0];

  if (rows.length === 0) {
    return (
      <div data-testid="reports-view">
        {tabs}
        <EmptyState message="No finished runs yet — there is nothing to measure." />
      </div>
    );
  }

  return (
    <div data-testid="reports-view" className="space-y-4">
      {tabs}
      <div className="flex items-center gap-2">
        {RANGES.map((r) => (
          <button
            key={r.value}
            type="button"
            onClick={() => setSince(r.value)}
            aria-pressed={since === r.value}
            data-testid={`range-${r.value || "all"}`}
            className={
              "rounded border px-2 py-0.5 text-xs " +
              (since === r.value ? "border-serious text-ink" : "border-hairline text-ink-muted")
            }
          >
            {r.label}
          </button>
        ))}
      </div>

      {/* The hero. Without a solo baseline there is no comparison to make, and
          saying so is more useful than a number with nothing behind it. */}
      <section className="rounded-card border border-hairline p-4" data-testid="hero">
        {!solo ? (
          <p className="text-ink-secondary">
            No solo runs yet. Solo is the baseline every other mode is measured against — run one
            to give the rest of this page a meaning.
          </p>
        ) : !best ? (
          <p className="text-ink-secondary">
            Only solo has run, at {solo.passed}/{solo.runs} passed. Run the same task in another
            mode to compare.
          </p>
        ) : (
          <>
            <div
              className="text-lg"
              data-testid="hero-number"
              style={{ color: best.points_vs_baseline >= 0 ? "var(--status-good)" : "var(--status-critical)" }}
            >
              {best.points_vs_baseline >= 0 ? "+" : ""}
              {best.points_vs_baseline.toFixed(1)} pts
            </div>
            <div className="text-ink-secondary">
              {best.key} vs solo baseline
              <span className="ml-3 text-ink-muted">
                n = {best.n} runs{since ? ` · last ${since}` : ""}
              </span>
            </div>
          </>
        )}
      </section>

      <ChartFrame
        title="Outcome mix by mode"
        table={<OutcomeTable rows={rows} />}
      >
        <OutcomeMix rows={rows} />
      </ChartFrame>

      <ChartFrame
        title="Pass rate by mode"
        note="unverified does not count as a pass"
        table={<PassRateTable rows={rows} />}
      >
        <BarChart
          bars={rows.map((r) => ({
            key: r.key,
            value: r.runs ? (r.passed / r.runs) * 100 : 0,
            n: r.runs,
          }))}
          baseline={solo && solo.runs ? (solo.passed / solo.runs) * 100 : undefined}
          baselineLabel={solo ? `solo baseline, ${((solo.passed / solo.runs) * 100).toFixed(1)}%` : undefined}
        />
      </ChartFrame>

      <section className="rounded-card border border-hairline p-3" data-testid="per-duckling">
        <h3 className="mb-2 text-ink">Per duckling</h3>
        <DucklingTable rows={byDuckling} />
      </section>
    </div>
  );
}

function OutcomeTable({ rows }: { rows: readonly ReportRow[] }) {
  return (
    <table className="w-full text-sm tabular-nums">
      <thead className="text-ink-muted">
        <tr>
          <th className="text-left font-normal">mode</th>
          <th className="text-right font-normal">passed</th>
          <th className="text-right font-normal">unverified</th>
          <th className="text-right font-normal">failed</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.key}>
            <td>{r.key}</td>
            <td className="text-right">{r.passed}</td>
            <td className="text-right">{r.unverified}</td>
            <td className="text-right">{r.failed}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function PassRateTable({ rows }: { rows: readonly ReportRow[] }) {
  return (
    <table className="w-full text-sm tabular-nums">
      <thead className="text-ink-muted">
        <tr>
          <th className="text-left font-normal">mode</th>
          <th className="text-right font-normal">pass rate</th>
          <th className="text-right font-normal">runs</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.key}>
            <td>{r.key}</td>
            <td className="text-right">{r.runs ? ((r.passed / r.runs) * 100).toFixed(1) : "0.0"}%</td>
            <td className="text-right">{r.runs}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** Per-duckling numbers.
 *
 * Estimated token counts carry a `~` and are never presented as measured
 * (04 §7). The marker is on the row that is estimated, not on a total, because
 * a total of measured and estimated numbers is not a number anyone can use. */
function DucklingTable({ rows }: { rows: readonly ReportRow[] }) {
  if (rows.length === 0) {
    return <p className="text-sm text-ink-muted">No runs recorded a duckling.</p>;
  }
  return (
    <table className="w-full text-sm tabular-nums" data-testid="duckling-table">
      <thead className="text-ink-muted">
        <tr>
          <th className="text-left font-normal">duckling</th>
          <th className="text-right font-normal">runs</th>
          <th className="text-right font-normal">pass rate</th>
          <th className="text-right font-normal">avg tokens</th>
          <th className="text-right font-normal">avg wall</th>
          <th className="text-right font-normal">avg cost</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.key} data-testid={`duckling-row-${r.key}`}>
            <td>{r.key}</td>
            <td className="text-right">{r.runs}</td>
            <td className="text-right">{r.runs ? ((r.passed / r.runs) * 100).toFixed(1) : "0.0"}%</td>
            <td className="text-right">
              {r.estimated && <span title="estimated, not reported by the provider">~</span>}
              {r.runs ? Math.round(r.tokens / r.runs).toLocaleString() : 0}
            </td>
            <td className="text-right">{formatMs(r.runs ? r.wallclock_ms / r.runs : 0)}</td>
            <td className="text-right">${(r.runs ? r.cost_usd / r.runs : 0).toFixed(4)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function formatMs(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`;
}
