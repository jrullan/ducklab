import type { ReportRow } from "../api/client";

/** One badge on the model leaderboard. */
export interface Award {
  key: string;
  title: string;
  /** Everyone tied for first. Breaking a tie alphabetically would award a
   * precision the data does not have. */
  winners: string[];
  value: string;
  n: number;
  estimated: boolean;
}

/** A duckling qualifies with this many finished runs. One lucky run is an
 * anecdote; the badge would change hands on every coin flip. */
export const MIN_RUNS = 3;

interface Contender {
  row: ReportRow;
  score: number;
}

/** Computes the leaderboard from the per-duckling report rows.
 *
 * Empty when fewer than two ducklings qualify: a leaderboard with one
 * contender is a mirror, not a measurement. Each badge draws only from rows
 * where its own metric means something — cost per pass needs a pass — so a
 * badge can have fewer contenders than the board, and says so via n.
 */
export function awards(rows: readonly ReportRow[]): Award[] {
  const qualified = rows.filter((r) => r.runs >= MIN_RUNS);
  if (qualified.length < 2) return [];

  const out: Award[] = [];
  const add = (
    key: string,
    title: string,
    contenders: Contender[],
    better: (a: number, b: number) => boolean,
    format: (v: number) => string,
  ) => {
    if (contenders.length < 2) return;
    let best = contenders[0]!;
    for (const c of contenders) {
      if (better(c.score, best.score)) best = c;
    }
    const winners = contenders.filter((c) => c.score === best.score);
    out.push({
      key,
      title,
      winners: winners.map((c) => c.row.key).sort(),
      value: format(best.score),
      // On a tie, the weakest evidence among the tied: the badge is only as
      // solid as its least-run winner.
      n: Math.min(...winners.map((c) => c.row.runs)),
      estimated: winners.some((c) => c.row.estimated),
    });
  };

  const lower = (a: number, b: number) => a < b;
  const higher = (a: number, b: number) => a > b;

  // Pass rate over runs that reached a verdict. A winner at 0% is not a
  // winner, so the badge needs somebody to have passed something.
  add(
    "performant",
    "Most performant",
    qualified.filter((r) => r.passed > 0).map((r) => ({ row: r, score: r.passed / r.runs })),
    higher,
    (v) => `${(v * 100).toFixed(0)}% passed`,
  );

  // Cost of a PASS, not of a run: a model that burns half the money but
  // passes twice as often is the economical one, and cost-per-run hides that.
  add(
    "economical",
    "Most economical",
    qualified.filter((r) => r.passed > 0).map((r) => ({ row: r, score: r.cost_usd / r.passed })),
    lower,
    (v) => `$${v.toFixed(4)} per pass`,
  );

  // Tokens per run: how much saying anything costs this model.
  add(
    "efficient",
    "Most efficient",
    qualified.filter((r) => r.tokens > 0).map((r) => ({ row: r, score: r.tokens / r.runs })),
    lower,
    (v) => `${Math.round(v / 1000)}k tokens per run`,
  );

  add(
    "fastest",
    "Fastest",
    qualified.filter((r) => r.wallclock_ms > 0).map((r) => ({ row: r, score: r.wallclock_ms / r.runs })),
    lower,
    (v) => {
      const s = Math.round(v / 1000);
      return s < 60 ? `${s}s per run` : `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s per run`;
    },
  );

  // Most runs: not a virtue, but the context every other badge is read in.
  add(
    "workhorse",
    "Workhorse",
    qualified.map((r) => ({ row: r, score: r.runs })),
    higher,
    (v) => `${v} runs`,
  );

  return out;
}
