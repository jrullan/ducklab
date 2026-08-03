import { describe, it, expect } from "vitest";
import { headToHead } from "./compare";
import type { ReportRow } from "../api/client";

const row = (over: Partial<ReportRow>): ReportRow =>
  ({
    key: "x", runs: 0, passed: 0, unverified: 0, failed: 0,
    tokens: 0, cost_usd: 0, wallclock_ms: 0, estimated: false,
    ...over,
  }) as ReportRow;

const luna = row({ key: "luna", runs: 10, passed: 9, failed: 1, tokens: 900_000, cost_usd: 0.05, wallclock_ms: 3_600_000 });
const sonnet = row({ key: "pato-sonnet", runs: 5, passed: 4, failed: 1, tokens: 300_000, cost_usd: 2.0, wallclock_ms: 600_000 });

describe("head to head", () => {
  it("names the trade when effectiveness and efficiency split", () => {
    // luna passes more (90% vs 80%) and costs less per pass ($0.0056 vs $0.50):
    // she wins both, and the summary says so in one claim, not two.
    expect(headToHead(luna, sonnet).summary).toBe(
      "luna is both more effective and more efficient on this history.",
    );
    // Make sonnet the better passer: now it is a genuine trade.
    const flakyLuna = { ...luna, passed: 3, failed: 7 };
    expect(headToHead(flakyLuna, sonnet).summary).toBe(
      "pato-sonnet is more effective; luna is more efficient — the trade is yours.",
    );
  });

  it("marks each metric's winner so the summary can be checked", () => {
    const h = headToHead(luna, sonnet);
    const m = Object.fromEntries(h.metrics.map((x) => [x.key, x.winner]));
    expect(m["pass-rate"]).toBe("a");
    expect(m["cost-per-pass"]).toBe("a");
    expect(m["tokens-per-run"]).toBe("b");
    expect(m["wall-per-run"]).toBe("b");
    // Context rows call no winner: spending more in total is not losing.
    expect(m["total-cost"]).toBe("none");
    expect(m["runs"]).toBe("none");
  });

  // Two local models are both free: cost per pass ties at $0, and tokens are
  // the resource that still differs.
  it("breaks a free-model cost tie on tokens", () => {
    const a = row({ key: "gemma", runs: 6, passed: 6, tokens: 1_200_000 });
    const b = row({ key: "qwen", runs: 6, passed: 6, tokens: 600_000 });
    expect(headToHead(a, b).summary).toBe("Equally effective; qwen is more efficient.");
  });

  it("withholds cost per pass when one side never passed", () => {
    const never = row({ key: "never", runs: 4, failed: 4, tokens: 100, cost_usd: 1 });
    const h = headToHead(luna, never);
    expect(h.metrics.find((x) => x.key === "cost-per-pass")!.winner).toBe("none");
  });

  it("flags thin evidence instead of refusing", () => {
    const fresh = row({ key: "fresh", runs: 2, passed: 2, tokens: 100 });
    expect(headToHead(luna, fresh).thin).toBe(true);
    expect(headToHead(luna, sonnet).thin).toBe(false);
  });

  it("admits when there is no daylight", () => {
    const a = row({ key: "a", runs: 4, passed: 4, tokens: 400 });
    const b = row({ key: "b", runs: 4, passed: 4, tokens: 400 });
    expect(headToHead(a, b).summary).toBe("No daylight between them on this history.");
  });
});
