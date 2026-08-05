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
import { awards } from "../lib/leaderboard";
import { headToHead } from "../lib/compare";
import { Prose } from "../components/Prose";

const RANGES = [
  { label: "all time", value: "" },
  { label: "7 days", value: "7d" },
  { label: "30 days", value: "30d" },
  { label: "90 days", value: "90d" },
] as const;

export function Reports({ client, projectId }: { client: EngineClient; projectId: string }) {
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

  // Bench used to be a tab here, which left it living in two places once the
  // Records zone gave it a room of its own — the same data reachable two ways
  // is a question ("are these different?") nobody should have to ask. One
  // home, one pointer.
  const tabs = (
    <p className="mb-3 text-xs text-ink-muted">
      Cross-model comparisons live in{" "}
      <a href="#/bench" data-testid="reports-bench-link" className="text-ink underline">
        Bench
      </a>
      .
    </p>
  );

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
  // Summed over the mode rows, where each run is counted once — the duckling
  // rows split a run between its models. Follows the range filter, so "last
  // 30d" answers what the last month cost.
  const totalCost = rows.reduce((sum, r) => sum + r.cost_usd, 0);
  const totalRuns = rows.reduce((sum, r) => sum + r.runs, 0);
  const anyEstimated = rows.some((r) => r.estimated);

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

      {/* What the project has cost, in the selected window. Every other number
          on this page is relative — points against a baseline, rates, averages
          — and the one absolute a person budgeting needs was on none of it. */}
      <p className="text-sm text-ink-secondary" data-testid="project-cost">
        This project has cost{" "}
        <span className="text-ink" title={anyEstimated ? "includes estimated token counts" : undefined}>
          {anyEstimated ? "~" : ""}${totalCost.toFixed(2)}
        </span>{" "}
        across {totalRuns} finished {totalRuns === 1 ? "run" : "runs"}
        {since ? ` in the last ${since}` : ""}.
      </p>

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
        <Leaderboard rows={byDuckling} />
        <DucklingTable rows={byDuckling} />
      </section>

      {byDuckling.length >= 2 && <HeadToHeadSection rows={byDuckling} />}

      <DevReportSection client={client} projectId={projectId} />
    </div>
  );
}

/** Badges over the duckling table: who wins each question the table can
 * answer. Every claim carries its evidence — the value, and n — and the board
 * stays empty until two ducklings have enough runs to actually compete, since
 * a leaderboard with one contender is a mirror, not a measurement. */
function Leaderboard({ rows }: { rows: readonly ReportRow[] }) {
  const board = awards(rows);
  if (board.length === 0) return null;
  return (
    <div className="mb-3 flex flex-wrap gap-2" data-testid="leaderboard">
      {board.map((a) => (
        <div
          key={a.key}
          data-testid={`award-${a.key}`}
          className="rounded-card border border-hairline px-3 py-2"
        >
          <div className="text-xs text-ink-muted">{a.title}</div>
          <div className="text-sm text-ink">{a.winners.join(" and ")}</div>
          <div className="text-xs text-ink-secondary">
            {a.estimated && <span title="includes estimated token counts">~</span>}
            {a.value} · n={a.n}
          </div>
        </div>
      ))}
    </div>
  );
}

/** Pick two models, get the verdict: who is more effective, who is more
 * efficient, and the metric rows to check the claim against. Compared on each
 * model's own recorded history — for identical tasks, that is what Bench is
 * for, and the card says so. */
function HeadToHeadSection({ rows }: { rows: readonly ReportRow[] }) {
  const [aKey, setAKey] = useState("");
  const [bKey, setBKey] = useState("");
  const a = rows.find((r) => r.key === aKey);
  const b = rows.find((r) => r.key === bKey);
  const result = a && b && a.key !== b.key ? headToHead(a, b) : null;

  const picker = (value: string, set: (v: string) => void, testid: string, exclude: string) => (
    <select
      value={value}
      onChange={(e) => set(e.target.value)}
      data-testid={testid}
      className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
    >
      <option value="">pick a model…</option>
      {rows
        .filter((r) => r.key !== exclude)
        .map((r) => (
          <option key={r.key} value={r.key}>
            {r.key}
          </option>
        ))}
    </select>
  );

  return (
    <section className="rounded-card border border-hairline p-3" data-testid="head-to-head">
      <h3 className="mb-2 text-ink">Head to head</h3>
      <div className="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
        {picker(aKey, setAKey, "compare-a", bKey)}
        <span className="text-ink-muted">vs</span>
        {picker(bKey, setBKey, "compare-b", aKey)}
      </div>

      {result && a && b && (
        <div className="mt-3">
          <p className="text-sm text-ink" data-testid="compare-summary">
            {result.summary}
          </p>
          {result.thin && (
            <p className="mt-1 text-xs text-serious" data-testid="compare-thin">
              Thin evidence — one side has fewer than 3 finished runs. Read this as an
              anecdote, not a measurement.
            </p>
          )}
          <table className="mt-2 w-full text-sm tabular-nums" data-testid="compare-table">
            <thead className="text-ink-muted">
              <tr>
                <th className="text-left font-normal">metric</th>
                <th className="text-right font-normal">{a.key}</th>
                <th className="text-right font-normal">{b.key}</th>
              </tr>
            </thead>
            <tbody>
              {result.metrics.map((m) => (
                <tr key={m.key} data-testid={`compare-${m.key}`}>
                  <td className="text-ink-secondary">{m.label}</td>
                  <td className={"text-right " + (m.winner === "a" ? "text-good" : "text-ink-secondary")}>
                    {m.a}
                    {m.winner === "a" && " ✓"}
                  </td>
                  <td className={"text-right " + (m.winner === "b" ? "text-good" : "text-ink-secondary")}>
                    {m.b}
                    {m.winner === "b" && " ✓"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-2 text-xs text-ink-muted">
            Compared on each model's own recorded history, which may be different tasks in
            different modes. For the same tasks under the same conditions, run a{" "}
            <a href="#/bench" className="text-ink underline">
              bench
            </a>
            .
          </p>
        </div>
      )}
    </section>
  );
}

/** The development report, on demand: what the software is and the evidence
 * — requirement→spec→task with statuses, bug fixes, releases, spine health.
 * Deterministic and copyable as Markdown, because its natural destination is
 * an email, a wiki page, or a client's hands — not this window. */
function DevReportSection({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [rendered, setRendered] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function generate() {
    setBusy(true);
    setFailure(null);
    try {
      const r = await client.traceReport(projectId);
      setRendered(r.rendered);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="rounded-card border border-hairline p-3" data-testid="dev-report">
      <h3 className="text-ink">Development report</h3>
      <p className="mt-1 text-xs text-ink-muted">
        The software in the approved requirements&apos; own words, the requirement → spec → task
        matrix with statuses, bug fixes, releases, and the spine&apos;s breaks. Built from the
        record — no model writes it.
      </p>
      <div className="mt-2 flex items-center gap-2">
        <button
          type="button"
          onClick={() => void generate()}
          disabled={busy}
          data-testid="dev-report-generate"
          className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
        >
          {busy ? "Building…" : rendered ? "Rebuild" : "Build the report"}
        </button>
        {rendered && (
          <button
            type="button"
            data-testid="dev-report-copy"
            onClick={() => {
              void navigator.clipboard?.writeText(rendered).then(() => {
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
              });
            }}
            className="rounded border border-hairline px-3 py-1 text-sm"
          >
            {copied ? "Copied" : "Copy as Markdown"}
          </button>
        )}
        {failure && (
          <span className="text-xs text-critical" data-testid="dev-report-error">
            {failure}
          </span>
        )}
      </div>
      {rendered && (
        <div className="mt-3 max-h-[32rem] overflow-y-auto border-t border-hairline pt-3" data-testid="dev-report-body">
          <Prose body={rendered} />
        </div>
      )}
    </section>
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

/** One sortable value per column, so the header and the sort cannot disagree
 * about what a column means. */
const DUCKLING_COLUMNS = [
  { key: "duckling", label: "duckling", value: (r: ReportRow) => r.key },
  { key: "runs", label: "runs", value: (r: ReportRow) => r.runs },
  { key: "pass-rate", label: "pass rate", value: (r: ReportRow) => (r.runs ? r.passed / r.runs : 0) },
  { key: "avg-tokens", label: "avg tokens", value: (r: ReportRow) => (r.runs ? r.tokens / r.runs : 0) },
  { key: "avg-wall", label: "avg wall", value: (r: ReportRow) => (r.runs ? r.wallclock_ms / r.runs : 0) },
  { key: "avg-cost", label: "avg cost", value: (r: ReportRow) => (r.runs ? r.cost_usd / r.runs : 0) },
  { key: "total-cost", label: "total cost", value: (r: ReportRow) => r.cost_usd },
] as const;

function DucklingTable({ rows }: { rows: readonly ReportRow[] }) {
  // Numbers open descending — "sort by total cost" means "biggest spender
  // first" — and the name opens ascending, because Z-to-A is nobody's first
  // ask. A second click flips it.
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 } | null>(null);
  if (rows.length === 0) {
    return <p className="text-sm text-ink-muted">No runs recorded a duckling.</p>;
  }
  const sorted = [...rows];
  const col = DUCKLING_COLUMNS.find((c) => c.key === sort?.key);
  if (sort && col) {
    sorted.sort((a, b) => {
      const va = col.value(a);
      const vb = col.value(b);
      const cmp = typeof va === "string" ? va.localeCompare(String(vb)) : Number(va) - Number(vb);
      return cmp * sort.dir;
    });
  }
  return (
    <table className="w-full text-sm tabular-nums" data-testid="duckling-table">
      <thead className="text-ink-muted">
        <tr>
          {DUCKLING_COLUMNS.map((c) => (
            <th
              key={c.key}
              className={`font-normal ${c.key === "duckling" ? "text-left" : "text-right"}`}
              aria-sort={
                sort?.key !== c.key ? undefined : sort.dir === 1 ? "ascending" : "descending"
              }
            >
              <button
                type="button"
                data-testid={`duckling-sort-${c.key}`}
                className="cursor-pointer hover:text-ink"
                onClick={() =>
                  setSort((cur) =>
                    cur?.key === c.key
                      ? { key: c.key, dir: cur.dir === 1 ? -1 : 1 }
                      : { key: c.key, dir: c.key === "duckling" ? 1 : -1 },
                  )
                }
              >
                {c.label}
                {sort?.key === c.key && (sort.dir === 1 ? " ↑" : " ↓")}
              </button>
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {sorted.map((r) => (
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
            {/* The average says which model is expensive to run once; the
                total says where the project's money went. A cheap model
                called constantly can out-spend an expensive one used
                sparingly, and neither column reveals that alone. */}
            <td className="text-right" data-testid={`duckling-total-${r.key}`}>
              ${r.cost_usd.toFixed(4)}
            </td>
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
