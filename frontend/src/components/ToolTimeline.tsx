import type { ToolCall } from "../lib/runview";
import { toolFamily } from "../lib/runview";

const FAMILY_COLOR: Record<string, string> = {
  read: "var(--series-1)",
  write: "var(--series-2)",
  exec: "var(--series-3)",
  vcs: "var(--series-7)",
  other: "var(--text-muted)",
};

/** A tick per tool call, in order. This is how "it read the same file nine
 * times" is visible at a glance instead of buried in a transcript. */
export function ToolTimeline({ calls }: { calls: readonly ToolCall[] }) {
  if (calls.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-0.5" data-testid="tool-timeline">
      {calls.map((c) => (
        <span
          key={`${c.seq}-${c.tool}`}
          data-testid="timeline-tick"
          data-tool={c.tool}
          title={`${c.tool}${c.ms !== undefined ? ` · ${c.ms}ms` : ""}${c.ok ? "" : " · failed"}`}
          className="inline-block h-3 w-1.5 rounded-sm"
          style={{
            background: c.ok ? FAMILY_COLOR[toolFamily(c.tool)] : "var(--status-critical)",
          }}
        />
      ))}
    </div>
  );
}
