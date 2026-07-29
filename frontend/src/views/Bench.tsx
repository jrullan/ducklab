/**
 * The Bench tab of Reports (08 §4.7).
 *
 * A bench answers the same question as a report, from the same tasks every
 * time. This shows past results; starting one is `ducklab bench`, because a
 * bench is minutes to hours and a desktop button that blocks for an afternoon
 * would be a worse thing than a command.
 */

import { useCallback, useEffect, useState } from "react";
import type { BenchCell, BenchResult, BenchSummary, EngineClient } from "../api/client";
import { BarChart, ChartFrame } from "../components/Chart";
import { EmptyState } from "../components/EmptyState";

export function Bench({ client }: { client: EngineClient }) {
  const [runs, setRuns] = useState<BenchSummary[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [result, setResult] = useState<BenchResult | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const list = await client.benchList();
      setRuns(list);
      if (list.length > 0 && selected === null) setSelected(list[0]!.stamp);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (selected === null) return;
    const run = runs.find((r) => r.stamp === selected);
    if (!run) return;
    let cancelled = false;
    client
      .benchGet(run.suite, run.stamp)
      .then((r) => !cancelled && setResult(r.result))
      .catch((err) => !cancelled && setFailure(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [client, selected, runs]);

  if (loading) return <p className="text-ink-muted">Loading…</p>;
  if (runs.length === 0) {
    return (
      <EmptyState message="No bench has run yet — `ducklab bench --ducklings a,b --modes solo,pair`." />
    );
  }

  return (
    <div data-testid="bench-view" className="flex gap-6">
      <nav className="w-56 shrink-0 space-y-1">
        {runs.map((r) => (
          <button
            key={r.stamp}
            type="button"
            data-testid="bench-item"
            aria-pressed={selected === r.stamp}
            onClick={() => setSelected(r.stamp)}
            className={
              "w-full rounded-card border p-2 text-left " +
              (selected === r.stamp ? "border-serious" : "border-hairline")
            }
          >
            <div className="font-mono text-xs text-ink">{r.stamp}</div>
            <div className="mt-1 text-xs text-ink-secondary">
              {r.suite} v{r.suite_version} · {r.passed}/{r.cells}
            </div>
            {r.errors > 0 && (
              <div className="text-xs text-critical">{r.errors} could not run</div>
            )}
          </button>
        ))}
      </nav>

      <div className="min-w-0 flex-1 space-y-4">
        {failure && <p className="text-critical">{failure}</p>}
        {result && <BenchDetail result={result} />}
      </div>
    </div>
  );
}

function BenchDetail({ result }: { result: BenchResult }) {
  const groups = groupCells(result.cells);
  const solo = groups.find((g) => g.key.endsWith(" / solo"));

  return (
    <>
      <p className="text-sm text-ink-secondary">
        suite {result.suite} v{result.suite_version} · {result.cells.length} cells ·{" "}
        {result.ducklings.join(", ")} · {result.modes.join(", ")}
      </p>

      {/* A suite everyone passes compares as little as one nobody passes. Said
          here, because a wall of 100% reads as a triumph rather than as a
          benchmark that has stopped discriminating. */}
      {groups.length > 1 && groups.every((g) => g.rate === 100) && (
        <p className="rounded-card border border-serious p-2 text-sm text-serious" data-testid="no-discrimination">
          Every cell passed. This suite is below the ceiling of these ducklings, so it does not
          tell them apart.
        </p>
      )}

      <ChartFrame title="Pass rate" note="by duckling and mode" table={<CellTable cells={result.cells} />}>
        <BarChart
          bars={groups.map((g) => ({ key: g.key, value: g.rate, n: g.cells }))}
          baseline={solo?.rate}
          baselineLabel={solo ? `${solo.key}, ${solo.rate.toFixed(1)}%` : undefined}
        />
      </ChartFrame>

      <section className="rounded-card border border-hairline p-3">
        <h3 className="mb-2 text-ink">Every cell</h3>
        <CellTable cells={result.cells} />
      </section>
    </>
  );
}

type Group = { key: string; cells: number; passed: number; rate: number };

function groupCells(cells: readonly BenchCell[]): Group[] {
  const by = new Map<string, Group>();
  for (const c of cells) {
    const key = `${c.duckling} / ${c.mode}`;
    const g = by.get(key) ?? { key, cells: 0, passed: 0, rate: 0 };
    g.cells++;
    if (c.verdict === "PASSED") g.passed++;
    by.set(key, g);
  }
  return [...by.values()]
    .map((g) => ({ ...g, rate: g.cells ? (g.passed / g.cells) * 100 : 0 }))
    .sort((a, b) => a.key.localeCompare(b.key));
}

function CellTable({ cells }: { cells: readonly BenchCell[] }) {
  return (
    <table className="w-full text-sm tabular-nums" data-testid="cell-table">
      <thead className="text-ink-muted">
        <tr>
          <th className="text-left font-normal">task</th>
          <th className="text-left font-normal">duckling</th>
          <th className="text-left font-normal">mode</th>
          <th className="text-left font-normal">outcome</th>
          <th className="text-right font-normal">tokens</th>
          <th className="text-right font-normal">wall</th>
        </tr>
      </thead>
      <tbody>
        {cells.map((c, i) => (
          <tr key={`${c.task}-${c.duckling}-${c.mode}-${i}`} data-testid="cell-row">
            <td>{c.task}</td>
            <td>{c.duckling}</td>
            <td>{c.mode}</td>
            <td className={c.error ? "text-critical" : c.verdict === "PASSED" ? "text-good" : "text-serious"}>
              {/* A harness failure is not a model failure, and reading them the
                  same way blames the model for our bug. */}
              {c.error ? "could not run" : c.verdict.toLowerCase()}
            </td>
            <td className="text-right">
              {c.estimated && <span title="estimated, not reported by the provider">~</span>}
              {c.tokens.toLocaleString()}
            </td>
            <td className="text-right">{Math.round(c.wallclock_ms / 1000)}s</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
