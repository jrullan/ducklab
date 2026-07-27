import { useState } from "react";
import type { TurnBlock, ToolCall } from "../lib/runview";
import { ducklingColor } from "../lib/colors";
import { DuckAvatar } from "./DuckAvatar";

/**
 * One turn in the conversation.
 *
 * Tool calls render as a single collapsed line each; a run that made forty
 * fs_read calls must still be skimmable. Errors and policy violations are
 * expanded by default because they are never routine.
 *
 * When a turn is anonymised the block carries no duckling id at all
 * (see anonymiseTurns), so this component cannot reveal the mapping even by
 * accident — I7 is a property of the product, not only of the prompt.
 */
export function ConversationTurn({
  block,
  roster,
  streamed,
}: {
  block: TurnBlock;
  roster: readonly string[];
  streamed?: string;
}) {
  const anonymous = !!block.label;
  const who = anonymous ? block.label! : block.duckling;
  const tint = anonymous ? "var(--text-secondary)" : ducklingColor(block.duckling, roster);

  return (
    <article
      data-testid="conversation-turn"
      data-role={block.role}
      data-anonymous={anonymous ? "true" : "false"}
      className="border-l-2 py-2 pl-3"
      style={{ borderColor: tint }}
    >
      <header className="flex items-center gap-2 text-sm">
        {anonymous ? (
          <span
            aria-hidden="true"
            title="identities hidden — this reviewer must not know who wrote which candidate"
          >
            🔒
          </span>
        ) : (
          <DuckAvatar id={block.duckling} roster={roster} />
        )}
        <span style={{ color: tint }}>{who}</span>
        <span className="text-ink-muted">{block.role}</span>
        {!block.done && (
          <span className="text-ink-muted" data-testid="in-flight">
            thinking…
          </span>
        )}
      </header>

      {block.toolCalls.length > 0 && (
        <ul className="mt-1">
          {block.toolCalls.map((c) => (
            <ToolCallLine key={`${c.seq}-${c.tool}`} call={c} />
          ))}
        </ul>
      )}

      {/* Live tokens while a turn is in flight, the recorded message once it
          is not. Only `streamed` was rendered, and it comes from token_delta
          events that arrive solely during a live run — so a lane showed a
          participant, its tool calls, and no word of what was actually said. */}
      {(streamed || block.text) && (
        <pre
          data-testid="turn-text"
          className="mt-1 whitespace-pre-wrap font-mono text-sm text-ink-secondary"
        >
          {streamed || block.text}
        </pre>
      )}
    </article>
  );
}

function ToolCallLine({ call }: { call: ToolCall }) {
  // A violation starts expanded: it is the one thing a user must not scroll past.
  const [open, setOpen] = useState(!!call.violation);
  const failed = !call.ok;
  return (
    <li data-testid="tool-call" data-ok={String(call.ok)} data-violation={String(!!call.violation)}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="text-left text-sm"
        style={{ color: failed ? "var(--status-critical)" : "var(--text-muted)" }}
      >
        {failed ? "✕" : "·"} {call.tool}
        {call.ms !== undefined && ` · ${call.ms}ms`}
      </button>
      {open && call.detail && (
        <pre className="ml-4 whitespace-pre-wrap font-mono text-xs text-critical">{call.detail}</pre>
      )}
    </li>
  );
}
