import type { DucklabEvent } from "./events";

/**
 * Coalesces streamed deltas into one store update per animation frame.
 *
 * Without this, a model emitting 40 tokens a second causes 40 React renders a
 * second in a virtualised list — the store update is cheap, the re-render is
 * not. Persisted events are NOT batched: they are rare, they change run state,
 * and delaying them would make the UI lag the engine's own record.
 *
 * The frame scheduler is injected so tests can drive it deterministically
 * rather than waiting on a real rAF.
 */
export class DeltaBatcher {
  private pending: DucklabEvent[] = [];
  private scheduled = false;

  constructor(
    private readonly flush: (batch: DucklabEvent[]) => void,
    private readonly schedule: (fn: () => void) => void = (fn) =>
      typeof requestAnimationFrame === "function" ? requestAnimationFrame(() => fn()) : setTimeout(fn, 16),
  ) {}

  /** Queues a delta. Returns true if it was batched rather than passed through. */
  push(e: DucklabEvent): boolean {
    if (e.type !== "token_delta") return false;
    this.pending.push(e);
    if (!this.scheduled) {
      this.scheduled = true;
      this.schedule(() => this.drain());
    }
    return true;
  }

  /** Flushes whatever has accumulated. Safe to call with nothing pending. */
  drain(): void {
    this.scheduled = false;
    if (this.pending.length === 0) return;
    const batch = this.pending;
    this.pending = [];
    this.flush(batch);
  }

  get pendingCount(): number {
    return this.pending.length;
  }
}

/**
 * Merges a batch of deltas into one text fragment per (run, duckling).
 *
 * Concatenating here rather than in the store means the store performs one
 * assignment per duckling per frame instead of one per token.
 */
export function mergeDeltas(batch: readonly DucklabEvent[]): Map<string, Map<string, string>> {
  const byRun = new Map<string, Map<string, string>>();
  for (const e of batch) {
    const runId = e.run_id ?? "";
    if (!runId) continue;
    const duckling = String(e.data?.duckling ?? "unknown");
    const text = String(e.data?.text ?? "");
    let forRun = byRun.get(runId);
    if (!forRun) {
      forRun = new Map();
      byRun.set(runId, forRun);
    }
    forRun.set(duckling, (forRun.get(duckling) ?? "") + text);
  }
  return byRun;
}
