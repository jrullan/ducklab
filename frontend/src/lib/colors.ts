/**
 * The single place a series colour is chosen (08-DESKTOP-UI.md §9).
 *
 * No component may hardcode a hue. Centralising it is what makes the two rules
 * below enforceable rather than aspirational:
 *
 *  - a duckling's colour follows the ENTITY, not its position in the current
 *    view, so filtering a report never repaints the survivors;
 *  - slots are assigned 1→8 in fixed order and never cycled; a 9th series
 *    folds into "Other" rather than inventing a hue.
 */

export const SERIES_SLOTS = 8;

/** Status roles. Fixed, never themed, never reused as a series colour. */
export type StatusRole = "good" | "warning" | "serious" | "critical" | "muted";

export type Verdict =
  | "PASSED"
  | "UNVERIFIED"
  | "FAILED"
  | "BUDGET_EXCEEDED"
  | "ABORTED"
  | "";

export type RunStatus = "running" | "queued" | "paused" | "done" | "failed";

/**
 * Assigns a stable slot to a duckling from a roster order.
 * Returns a CSS variable reference, never a raw hex.
 */
export function seriesVar(index: number): string {
  if (index < 0) return "var(--text-muted)";
  if (index >= SERIES_SLOTS) return "var(--text-muted)"; // folded into "Other"
  return `var(--series-${index + 1})`;
}

/**
 * Resolves a duckling to its colour using a stable ordering.
 *
 * `order` is the roster order, which does not change when a view is filtered.
 * An unknown duckling gets muted rather than slot 1, so a stranger never
 * impersonates an established series.
 */
export function ducklingColor(id: string, order: readonly string[]): string {
  const index = order.indexOf(id);
  return seriesVar(index);
}

/** True when a set of series exceeds what the palette can distinguish. */
export function needsOtherBucket(count: number): boolean {
  return count > SERIES_SLOTS;
}

/**
 * All-pairs chart forms (scatter, bubble, small multiples) can only use the
 * first three slots: past three, the palette stops clearing the CVD floor
 * when every pair is on screen at once.
 */
export const ALL_PAIRS_SLOT_LIMIT = 3;

export function allPairsSafe(count: number): boolean {
  return count <= ALL_PAIRS_SLOT_LIMIT;
}

/** Maps a verdict to its status role. */
export function verdictStatus(verdict: Verdict): StatusRole {
  switch (verdict) {
    case "PASSED":
      return "good";
    case "UNVERIFIED":
      // Nothing was executed. It is not a success and must never render as one.
      return "warning";
    case "FAILED":
    case "BUDGET_EXCEEDED":
      return "critical";
    case "ABORTED":
      return "serious";
    default:
      return "muted";
  }
}

/** Maps a run status to its status role. */
export function runStatusRole(status: RunStatus): StatusRole {
  switch (status) {
    case "running":
      return "good";
    case "queued":
      return "muted";
    case "paused":
      // Waiting for a person is not an error; it is also not fine to ignore.
      return "serious";
    case "done":
      return "good";
    case "failed":
      return "critical";
    default:
      return "muted";
  }
}

export function statusVar(role: StatusRole): string {
  return role === "muted" ? "var(--text-muted)" : `var(--status-${role})`;
}

/**
 * Icon + label for a status. Status is NEVER colour alone: warning and serious
 * are below 3:1 on the light surface by design, and this pairing is the
 * mitigation.
 */
export function statusIcon(role: StatusRole): string {
  switch (role) {
    case "good":
      return "✓";
    case "warning":
      return "⚠";
    case "serious":
      return "⏸";
    case "critical":
      return "✕";
    default:
      return "·";
  }
}

/** Human label for a verdict, never blank. */
export function verdictLabel(verdict: Verdict): string {
  switch (verdict) {
    case "PASSED":
      return "passed";
    case "UNVERIFIED":
      return "unverified";
    case "FAILED":
      return "failed";
    case "BUDGET_EXCEEDED":
      return "budget exceeded";
    case "ABORTED":
      return "aborted";
    default:
      return "in progress";
  }
}

/** Budget meters turn warning at 80% and critical at 100%. */
export function meterRole(used: number, limit: number): StatusRole {
  if (limit <= 0) return "muted";
  const pct = (used / limit) * 100;
  if (pct >= 100) return "critical";
  if (pct >= 80) return "warning";
  return "good";
}
