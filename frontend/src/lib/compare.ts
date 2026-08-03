import type { ReportRow } from "../api/client";
import { MIN_RUNS } from "./leaderboard";

/** One row of the head-to-head: a metric, both values, and who wins it. */
export interface CompareMetric {
  key: string;
  label: string;
  a: string;
  b: string;
  /** "none" marks context rows — total spent, run counts — where calling a
   * winner would be nonsense. */
  winner: "a" | "b" | "tie" | "none";
}

export interface HeadToHead {
  metrics: CompareMetric[];
  /** The verdict, in a sentence: who is more effective, who is more
   * efficient, or that the history shows no daylight. */
  summary: string;
  /** True when either side has too few runs for the comparison to be more
   * than an anecdote. */
  thin: boolean;
}

const pct = (r: ReportRow) => (r.runs ? (r.passed / r.runs) * 100 : 0);
const perRun = (v: number, r: ReportRow) => (r.runs ? v / r.runs : 0);
const wall = (ms: number) => {
  const s = Math.round(ms / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`;
};

/** Compares two ducklings on their recorded history.
 *
 * Effectiveness is the pass rate. Efficiency is the cost of a pass — and when
 * both models are free (two local models both at $0), tokens per run break
 * the tie, because that is the resource that still differs. Each metric row
 * names its own winner, so the summary can be checked against the evidence
 * right below it.
 */
export function headToHead(a: ReportRow, b: ReportRow): HeadToHead {
  const metrics: CompareMetric[] = [];
  const num = (
    key: string,
    label: string,
    va: number,
    vb: number,
    format: (v: number) => string,
    lowerWins: boolean,
    comparable = true,
  ) => {
    let winner: CompareMetric["winner"] = "none";
    if (comparable) {
      if (va === vb) winner = "tie";
      else winner = (lowerWins ? va < vb : va > vb) ? "a" : "b";
    }
    metrics.push({ key, label, a: format(va), b: format(vb), winner });
  };

  num("pass-rate", "pass rate", pct(a), pct(b), (v) => `${v.toFixed(0)}%`, false);

  // Cost per pass needs a pass on both sides; with one side never passing,
  // the effectiveness row already says everything money could.
  const costComparable = a.passed > 0 && b.passed > 0;
  num(
    "cost-per-pass",
    "cost per pass",
    a.passed ? a.cost_usd / a.passed : 0,
    b.passed ? b.cost_usd / b.passed : 0,
    (v) => `$${v.toFixed(4)}`,
    true,
    costComparable,
  );

  num("tokens-per-run", "tokens per run", perRun(a.tokens, a), perRun(b.tokens, b),
    (v) => `${Math.round(v / 1000)}k`, true);
  num("wall-per-run", "time per run", perRun(a.wallclock_ms, a), perRun(b.wallclock_ms, b), wall, true);
  num("total-cost", "total spent", a.cost_usd, b.cost_usd, (v) => `$${v.toFixed(2)}`, true, false);
  num("runs", "finished runs", a.runs, b.runs, (v) => String(v), false, false);

  const effective = metrics.find((m) => m.key === "pass-rate")!.winner;
  let efficient = metrics.find((m) => m.key === "cost-per-pass")!.winner;
  if (efficient === "tie" || efficient === "none") {
    // Two free local models both cost $0 a pass; tokens are the resource
    // that still differs.
    efficient = metrics.find((m) => m.key === "tokens-per-run")!.winner;
  }

  const name = (w: "a" | "b") => (w === "a" ? a.key : b.key);
  let summary: string;
  if (effective === "tie" && efficient === "tie") {
    summary = "No daylight between them on this history.";
  } else if (effective === "tie") {
    summary = `Equally effective; ${name(efficient as "a" | "b")} is more efficient.`;
  } else if (efficient === "tie") {
    summary = `${name(effective as "a" | "b")} is more effective; they are equally efficient.`;
  } else if (efficient === effective) {
    summary = `${name(effective as "a" | "b")} is both more effective and more efficient on this history.`;
  } else {
    summary = `${name(effective as "a" | "b")} is more effective; ${name(efficient as "a" | "b")} is more efficient — the trade is yours.`;
  }

  return { metrics, summary, thin: a.runs < MIN_RUNS || b.runs < MIN_RUNS };
}
