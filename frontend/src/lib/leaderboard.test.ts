import { describe, it, expect } from "vitest";
import { awards, MIN_RUNS } from "./leaderboard";
import type { ReportRow } from "../api/client";

const row = (over: Partial<ReportRow>): ReportRow =>
  ({
    key: "x", runs: 0, passed: 0, unverified: 0, failed: 0,
    tokens: 0, cost_usd: 0, wallclock_ms: 0, estimated: false,
    ...over,
  }) as ReportRow;

// The cheap local model that always passes, the expensive hosted one that
// works fast, and a flaky one — the comparison the board exists for.
const luna = row({ key: "luna", runs: 10, passed: 9, failed: 1, tokens: 900_000, cost_usd: 0.05, wallclock_ms: 3_600_000 });
const sonnet = row({ key: "pato-sonnet", runs: 5, passed: 4, failed: 1, tokens: 300_000, cost_usd: 2.0, wallclock_ms: 600_000 });
const flaky = row({ key: "k3", runs: 4, passed: 1, failed: 3, tokens: 800_000, cost_usd: 0.4, wallclock_ms: 2_000_000 });

describe("the model leaderboard", () => {
  it("hands each badge to the right duckling, with the evidence", () => {
    const board = awards([luna, sonnet, flaky]);
    const byKey = Object.fromEntries(board.map((a) => [a.key, a]));

    expect(byKey["performant"]!.winners).toEqual(["luna"]); // 90%
    expect(byKey["performant"]!.value).toBe("90% passed");
    expect(byKey["economical"]!.winners).toEqual(["luna"]); // $0.0056/pass beats $0.50
    expect(byKey["efficient"]!.winners).toEqual(["pato-sonnet"]); // 60k/run
    expect(byKey["fastest"]!.winners).toEqual(["pato-sonnet"]); // 2m/run
    expect(byKey["workhorse"]!.winners).toEqual(["luna"]);
    expect(byKey["workhorse"]!.n).toBe(10);
  });

  // One lucky run is an anecdote; the badge would change hands on a coin flip.
  it("does not let a duckling below the minimum runs compete", () => {
    const lucky = row({ key: "lucky", runs: MIN_RUNS - 1, passed: 2, cost_usd: 0.0001, tokens: 100 });
    const board = awards([luna, sonnet, lucky]);
    for (const a of board) {
      expect(a.winners).not.toContain("lucky");
    }
  });

  // A leaderboard with one contender is a mirror, not a measurement.
  it("stays empty until two ducklings qualify", () => {
    expect(awards([luna])).toEqual([]);
    expect(awards([luna, row({ key: "new", runs: 1, passed: 1 })])).toEqual([]);
  });

  // Breaking a tie alphabetically would award a precision the data lacks.
  it("names everyone tied for first", () => {
    const a = row({ key: "a", runs: 4, passed: 4, tokens: 400, cost_usd: 0.4, wallclock_ms: 4000 });
    const b = row({ key: "b", runs: 8, passed: 8, tokens: 800, cost_usd: 0.8, wallclock_ms: 8000 });
    const board = awards([a, b]);
    const perf = board.find((x) => x.key === "performant")!;
    expect(perf.winners).toEqual(["a", "b"]); // both 100%
    // The badge is only as solid as its least-run winner.
    expect(perf.n).toBe(4);
  });

  // Nobody has passed anything: "most performant, 0%" is not a thing to say,
  // and cost per pass divides by zero.
  it("withholds performance and economy badges when nothing has passed", () => {
    const a = row({ key: "a", runs: 4, failed: 4, tokens: 400, wallclock_ms: 4000 });
    const b = row({ key: "b", runs: 4, failed: 4, tokens: 800, wallclock_ms: 8000 });
    const keys = awards([a, b]).map((x) => x.key);
    expect(keys).not.toContain("performant");
    expect(keys).not.toContain("economical");
    expect(keys).toContain("efficient");
  });

  // Estimated counts are never presented as measured (04 §7).
  it("marks a badge won on estimated numbers", () => {
    const est = row({ key: "est", runs: 4, passed: 4, tokens: 100, cost_usd: 0.01, estimated: true });
    const board = awards([est, luna]);
    expect(board.find((x) => x.key === "efficient")!.estimated).toBe(true);
  });
});
