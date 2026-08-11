import { useState } from "react";
import type { ToolCall } from "../lib/runview";
import { toolFamily } from "../lib/runview";

const COLLAPSE_KEY = "ducklab.timeline";

const FAMILY_COLOR: Record<string, string> = {
  read: "var(--series-1)",
  write: "var(--series-2)",
  exec: "var(--series-3)",
  vcs: "var(--series-7)",
  other: "var(--text-muted)",
};

const FAMILY_LABEL: Record<string, string> = {
  read: "read",
  write: "write",
  exec: "run",
  vcs: "git",
  other: "other",
};

/** A tick per tool call, in order. This is how "it read the same file nine
 * times" is visible at a glance instead of buried in a transcript.
 *
 * It carries a caption and a legend. Without them it was a row of coloured
 * squares with no stated meaning: a reader could not tell whether the colours
 * were categories, severity, or decoration, and a hover tooltip is not an
 * explanation for someone who does not know there is anything to hover.
 *
 * Collapsible, remembered across runs: pinned to the bottom edge it earns
 * its place on a busy run, but a person reading a long transcript may want
 * the pixels back. The caption stays either way — count and failures are the
 * one-line summary — and clicking it folds or unfolds the ticks.
 */
export function ToolTimeline({ calls }: { calls: readonly ToolCall[] }) {
  const [collapsed, setCollapsed] = useState(
    () => localStorage.getItem(COLLAPSE_KEY) === "1",
  );
  if (calls.length === 0) return null;
  const families = [...new Set(calls.map((c) => toolFamily(c.tool)))];
  const failed = calls.filter((c) => !c.ok).length;
  const toggle = () => {
    setCollapsed((v) => {
      localStorage.setItem(COLLAPSE_KEY, v ? "0" : "1");
      return !v;
    });
  };
  return (
    <div data-testid="tool-timeline" data-collapsed={String(collapsed)}>
      <div className="mb-1 flex items-center gap-3 text-xs text-ink-muted">
        <button
          type="button"
          data-testid="timeline-toggle"
          onClick={toggle}
          className="flex items-center gap-1 text-ink-muted"
          title={collapsed ? "show the tick bar" : "hide the tick bar"}
        >
          <span aria-hidden="true">{collapsed ? "›" : "⌄"}</span>
          {calls.length} tool call{calls.length === 1 ? "" : "s"}
          {collapsed ? "" : ", in order"}
        </button>
        {families.map((f) => (
          <span key={f} className="flex items-center gap-1">
            <span
              className="inline-block h-2 w-2 rounded-sm"
              style={{ background: FAMILY_COLOR[f] }}
            />
            {FAMILY_LABEL[f] ?? f}
          </span>
        ))}
        {failed > 0 && (
          <span className="flex items-center gap-1 text-critical" data-testid="timeline-failed">
            <span
              className="inline-block h-2 w-2 rounded-sm"
              style={{ background: "var(--status-critical)" }}
            />
            {failed} failed
          </span>
        )}
      </div>
      {!collapsed && (
      <div className="flex flex-wrap gap-0.5">
        {calls.map((c) => (
        <span
          key={`${c.seq}-${c.tool}`}
          data-testid="timeline-tick"
          data-tool={c.tool}
          title={`${c.tool}${c.target ? ` ${c.target}` : ""}${c.ms !== undefined ? ` · ${c.ms}ms` : ""}${c.ok ? "" : " · failed"}`}
          className="inline-block h-3 w-1.5 rounded-sm"
          style={{
            background: c.ok ? FAMILY_COLOR[toolFamily(c.tool)] : "var(--status-critical)",
          }}
          />
        ))}
      </div>
      )}
    </div>
  );
}
