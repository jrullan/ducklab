/**
 * Bench: the controlled answer (08 §4.7).
 *
 * A bench answers the same question as a report, from the same tasks every
 * time — which is how "I have a new model, how do I best use it?" actually
 * gets answered. For a long time this view only showed past results, on the
 * reasoning that a bench is minutes to hours and a blocking desktop button
 * would be worse than a command. The conclusion drawn from that true premise
 * was wrong: the fix was to start one WITHOUT blocking, not to have no way to
 * start one. Its own empty state pointed at the CLI — the anti-pattern this
 * codebase keeps paying to remove.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import type { BenchCell, BenchResult, BenchSummary, Duckling, EngineClient, Run } from "../api/client";
import { BarChart, ChartFrame } from "../components/Chart";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageShell";
import { selectableSurface } from "../lib/ui";

export function Bench({ client }: { client: EngineClient }) {
  const [runs, setRuns] = useState<BenchSummary[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [result, setResult] = useState<BenchResult | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [fleet, setFleet] = useState<Duckling[]>([]);
  const [picked, setPicked] = useState<string[]>([]);
  const [modes, setModes] = useState<string[]>(["solo"]);
  const [inFlight, setInFlight] = useState<{ cells: number } | null>(null);
  const [cellRuns, setCellRuns] = useState<Run[]>([]);
  const [startError, setStartError] = useState<string | null>(null);
  const knownStamps = useRef<Set<string>>(new Set());

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
    client.ducklings().then(setFleet).catch(() => setFleet([]));
  }, [load, client]);

  // A bench someone started earlier is rediscovered on mount. The in-flight
  // state lived only in this component, so leaving for another view and
  // coming back showed a launcher at rest while nine cells burned tokens in
  // the background — the run was fine, the view had amnesia. The planned cell
  // count went with it, so a rediscovered bench reports progress without a
  // total (cells: 0 means "unknown").
  useEffect(() => {
    if (inFlight) return;
    let cancelled = false;
    void client
      .runs()
      .then((all) => {
        const cells = all.filter((r) => r.project_id.startsWith("ducklab-bench-"));
        const alive = cells.some((r) => r.status === "running" || r.status === "queued" || r.status === "paused");
        if (!cancelled && alive) {
          setCellRuns(cells);
          setInFlight({ cells: 0 });
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  // While a bench is in flight, its progress lives HERE. Each cell runs in a
  // throwaway project (ducklab-bench-*), so the desktop's project-scoped runs
  // list never shows one — a lesson bought by writing "watchable in Records ▸
  // Runs" on this very screen and being wrong. The unfiltered runs list is
  // polled for the cells, and the bench list for the finished result; a bench
  // belongs to no project, so the event stream never mentions either.
  useEffect(() => {
    if (!inFlight) return;
    knownStamps.current = new Set(runs.map((r) => r.stamp));
    const poll = () => {
      void client
        .runs()
        .then((all) => setCellRuns(all.filter((r) => r.project_id.startsWith("ducklab-bench-"))))
        .catch(() => {});
      void client
        .benchList()
        .then((list) => {
          const fresh = list.find((r) => !knownStamps.current.has(r.stamp));
          if (fresh) {
            setInFlight(null);
            setCellRuns([]);
            setRuns(list);
            setSelected(fresh.stamp);
          }
        })
        .catch(() => {});
    };
    poll();
    const timer = setInterval(poll, 5000);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inFlight, client]);

  const start = async () => {
    setStartError(null);
    try {
      const out = await client.benchStart({ ducklings: picked, modes });
      setInFlight({ cells: out.cells });
    } catch (e) {
      // The engine validates before anything runs: a misspelled duckling is
      // refused here, not discovered in a log twenty minutes later.
      setStartError(e instanceof Error ? e.message : String(e));
    }
  };

  const launcher = (
    <section className="mb-4 rounded-card border border-hairline p-3" data-testid="bench-launcher">
      <h2 className="text-sm font-medium text-ink">Test models against the standard tasks</h2>
      <p className="mt-1 text-xs text-ink-muted">
        Nine fixed tasks, each proved to start red and be solvable. The same tasks every
        time is what makes two models comparable — project runs never are.
      </p>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-ink-secondary">
        {fleet.map((d) => (
          <label key={d.id} className="flex items-center gap-1">
            <input
              type="checkbox"
              data-testid={`bench-duckling-${d.id}`}
              checked={picked.includes(d.id)}
              onChange={(e) =>
                setPicked((cur) => (e.target.checked ? [...cur, d.id] : cur.filter((x) => x !== d.id)))
              }
            />
            {d.id}
          </label>
        ))}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-ink-secondary">
        {["solo", "pair", "tournament", "split"].map((m) => (
          <label key={m} className="flex items-center gap-1">
            <input
              type="checkbox"
              data-testid={`bench-mode-${m}`}
              checked={modes.includes(m)}
              onChange={(e) =>
                setModes((cur) => (e.target.checked ? [...cur, m] : cur.filter((x) => x !== m)))
              }
            />
            {m}
          </label>
        ))}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <button
          type="button"
          data-testid="bench-start"
          disabled={picked.length === 0 || modes.length === 0 || inFlight !== null}
          onClick={() => void start()}
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          Run bench
        </button>
        {/* The size of what is about to be bought, before the click. */}
        {picked.length > 0 && modes.length > 0 && !inFlight && (
          <span className="text-xs text-ink-muted" data-testid="bench-cells">
            {picked.length * modes.length * 9} cells ({picked.length} duckling
            {picked.length === 1 ? "" : "s"} × {modes.length} mode{modes.length === 1 ? "" : "s"} × 9
            tasks)
          </span>
        )}
        {inFlight && (
          <span className="text-xs text-ink-secondary" data-testid="bench-running">
            {inFlight.cells > 0 ? `running ${inFlight.cells} cells` : "a bench is running"} — progress
            below; the result appears here when it finishes
          </span>
        )}
        {startError && (
          <span className="text-xs text-critical" data-testid="bench-start-error">
            {startError}
          </span>
        )}
      </div>
      {inFlight && cellRuns.length > 0 && (
        <div className="mt-2 border-t border-hairline pt-2" data-testid="bench-progress">
          <span className="text-xs text-ink-muted" data-testid="bench-progress-count">
            {cellRuns.filter((r) => r.status === "done" || r.status === "failed").length}
            {inFlight.cells > 0 ? ` of ${inFlight.cells}` : ""} cells done
          </span>
          <ul className="mt-1 space-y-0.5 text-xs">
            {cellRuns.map((r) => (
              <li key={r.id} data-testid="bench-cell-run" className="flex gap-2 tabular-nums">
                <span className="font-mono text-ink">{r.task_id}</span>
                <span className="text-ink-secondary">{r.mode}</span>
                <span
                  className={
                    r.status === "failed" || r.verdict === "FAILED"
                      ? "text-critical"
                      : r.status === "done"
                        ? "text-good"
                        : "text-ink-muted"
                  }
                >
                  {r.status === "done" || r.status === "failed" ? r.verdict.toLowerCase() : r.status}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );

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
    // The empty state IS the launcher: a person with no bench yet is exactly
    // the person trying to start their first.
    return (
      <div data-testid="bench-view" className="space-y-4">
        <PageHeader eyebrow="Records" title="Bench" subtitle="Compare ducklings against the same controlled tasks and inspect the evidence behind each result." />
        {launcher}
        <EmptyState message="No bench has run yet. Pick ducklings and modes above." />
      </div>
    );
  }

  return (
    <div data-testid="bench-view" className="space-y-4">
      <PageHeader eyebrow="Records" title="Bench" subtitle="Compare ducklings against the same controlled tasks and inspect the evidence behind each result." />
      {launcher}
      <div className="grid min-h-[32rem] grid-cols-[14rem_minmax(0,1fr)] overflow-hidden rounded-card border border-hairline bg-surface1">
      <nav className="space-y-1 border-r border-hairline p-3">
        <div className="mb-3 flex items-baseline justify-between"><h2 className="text-sm font-medium text-ink">Bench history</h2><span className="text-xs text-ink-muted">{runs.length}</span></div>
        {runs.map((r) => (
          <button
            key={r.stamp}
            type="button"
            data-testid="bench-item"
            aria-pressed={selected === r.stamp}
            onClick={() => setSelected(r.stamp)}
            className={
              "w-full rounded-card border p-2 text-left transition-colors " + selectableSurface(selected === r.stamp)
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

      <div className="min-w-0 space-y-4 p-5">
        {failure && <p className="text-critical">{failure}</p>}
        {result && <BenchDetail result={result} />}
      </div>
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
          benchmark that has stopped discriminating.
          
          It points at the effort chart rather than stopping at the bad news:
          when every model solves every task, what they spent doing it is the
          only thing left that differs — and on the run that prompted this, one
          duckling used twice the tokens of the other on the same task. */}
      {groups.length > 1 && groups.every((g) => g.rate === 100) && (
        <p className="rounded-card border border-serious p-2 text-sm text-serious" data-testid="no-discrimination">
          Every cell passed, so this suite does not tell these ducklings apart on correctness.
          What they spent is below.
        </p>
      )}

      <ChartFrame title="Pass rate" note="by duckling and mode" table={<CellTable cells={result.cells} />}>
        <BarChart
          bars={groups.map((g) => ({ key: g.key, value: g.rate, n: g.cells }))}
          baseline={solo?.rate}
          baselineLabel={solo ? `${solo.key}, ${solo.rate.toFixed(1)}%` : undefined}
        />
      </ChartFrame>

      <ChartFrame
        title="Tokens spent"
        note="two models can both be right and not cost the same"
        table={<CellTable cells={result.cells} />}
      >
        <BarChart
          bars={groups.map((g) => ({ key: g.key, value: g.tokens, n: g.cells }))}
          unit=""
        />
      </ChartFrame>

      <section className="rounded-card border border-hairline p-3">
        <h3 className="mb-2 text-ink">Every cell</h3>
        <CellTable cells={result.cells} />
      </section>
    </>
  );
}

type Group = { key: string; cells: number; passed: number; rate: number; tokens: number };

function groupCells(cells: readonly BenchCell[]): Group[] {
  const by = new Map<string, Group>();
  for (const c of cells) {
    const key = `${c.duckling} / ${c.mode}`;
    const g = by.get(key) ?? { key, cells: 0, passed: 0, rate: 0, tokens: 0 };
    g.cells++;
    g.tokens += c.tokens;
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
