/** Formatting helpers. Money and durations appear in many views; one
 * implementation keeps them consistent and testable. */

/** 4 decimals under $1, 2 above (03-CLI.md §5). */
export function money(usd: number): string {
  if (!Number.isFinite(usd)) return "$0.00";
  return usd < 1 ? `$${usd.toFixed(4)}` : `$${usd.toFixed(2)}`;
}

/** 2 decimals for aggregate spend values. */
export function money2(usd: number): string {
  if (!Number.isFinite(usd)) return "$0.00";
  return `$${usd.toFixed(2)}`;
}

/** Money display for spend values, including an exact zero. */
export function moneyOrZero(usd: number): string {
  return usd === 0 ? "$0.00" : money(usd);
}

export function tokens(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "0";
  if (n < 1000) return String(Math.round(n));
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

export function duration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "0s";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h${String(m % 60).padStart(2, "0")}m`;
}

/**
 * A gate's recorded duration, for its exit-code line. Zero or absent says
 * nothing: before B-555 every gate that finished recorded 0 s, and a reused
 * result records 0 because nothing ran, so neither is shown as "0s".
 */
export function gateDuration(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds <= 0) return "";
  if (seconds < 1) return "<1s";
  return duration(seconds * 1000);
}

/** How long something has been waiting, for the human-gate inbox. */
export function waitingFor(since: string, now: Date = new Date()): string {
  const started = Date.parse(since);
  if (Number.isNaN(started)) return "just now";
  return duration(Math.max(0, now.getTime() - started));
}

export function shortSha(sha: string): string {
  return sha.slice(0, 8);
}
