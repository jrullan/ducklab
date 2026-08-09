import { useState } from "react";
import type { TurnBlock, ToolCall } from "../lib/runview";
import { ducklingColor } from "../lib/colors";
import { DuckAvatar } from "./DuckAvatar";
import { StatusChip } from "./StatusChip";
import { Prose } from "./Prose";

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
  reasoning,
  color,
}: {
  block: TurnBlock;
  roster: readonly string[];
  /** The duckling's fleet colour, stable across runs. Without it the tint came
   * from the duckling's index in this run's roster, so a model was blue as an
   * architect and orange as an implementer. */
  color?: string;
  streamed?: string;
  /** The model's thinking, when its endpoint separates it from the answer.
   * Collapsed by default: it is usually far longer than the reply, and a lane
   * that opens with a wall of deliberation buries what was actually decided. */
  reasoning?: string;
}) {
  const anonymous = !!block.label;
  const who = anonymous ? block.label! : block.duckling;
  const tint = anonymous ? "var(--text-secondary)" : (color ?? ducklingColor(block.duckling, roster));

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
          <DuckAvatar id={block.duckling} roster={roster} color={color} bobbing={!block.done} />
        )}
        <span style={{ color: tint }}>{who}</span>
        <span className="text-ink-muted">{block.role}</span>
        {block.subject && (
          <span className="text-ink-secondary" data-testid="turn-subject">
            {block.subject}
          </span>
        )}
        {/* Lanes are stacked, so without this concurrency reads as sequence —
            and "two models worked at once" is a different claim from "one
            model worked twice". */}
        {block.concurrent && (
          <span className="text-ink-muted" data-testid="turn-concurrent" title="ran at the same time as another turn">
            ∥ in parallel
          </span>
        )}
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

      {/* A reviewer's turn is already structured. Rendering its raw text put
          `{"verdict":"approve", "findings":[]}` on screen — the one turn whose
          content the engine has already parsed, shown as a blob. */}
      {!streamed && block.verdict && <VerdictBlock block={block} />}

      {/* Thinking arrives before the answer and is billed either way. The
          stream parser used to read only delta.content, so a reasoning model
          spent tokens no one could see and a run that looked idle for two
          minutes had nothing on screen to explain itself. */}
      {/* A turn that ran out of budget or lost its provider still did real
          work, and the record of it is now kept — but a partial record read as a
          complete one is worse than none. */}
      {block.incomplete && (
        <div className="mt-1" data-testid="turn-incomplete">
          <StatusChip role="warning" label="turn did not finish" />
        </div>
      )}

      {reasoning && <ReasoningBlock text={reasoning} live={!!streamed} />}

      {/* Live tokens while a turn is in flight, the recorded message once it
          is not. Only `streamed` was rendered, and it comes from token_delta
          events that arrive solely during a live run — so a lane showed a
          participant, its tool calls, and no word of what was actually said. */}
      {/* Raw while tokens are still arriving — a half-written fence or bold
          marker cannot be parsed without guessing at what comes next — and
          rendered once the turn has settled. */}
      {streamed ? (
        <pre
          data-testid="turn-text"
          className="mt-1 whitespace-pre-wrap font-mono text-sm text-ink-secondary"
        >
          {streamed}
        </pre>
      ) : (
        block.text &&
        !block.verdict && (
          <div data-testid="turn-text">
            <Prose body={block.text} suppress={[]} className="mt-1 space-y-2 text-sm text-ink-secondary" />
          </div>
        )
      )}
    </article>
  );
}

function VerdictBlock({ block }: { block: TurnBlock }) {
  const approved = block.verdict === "approve";
  const findings = block.findings ?? [];
  return (
    <div className="mt-1" data-testid="turn-verdict" data-verdict={block.verdict}>
      <StatusChip
        role={approved ? "good" : "serious"}
        label={approved ? "approve" : String(block.verdict)}
      />
      {findings.length === 0 ? (
        approved ? null : (
          <span className="ml-2 text-sm text-ink-muted">no findings given</span>
        )
      ) : (
        <ul className="mt-1 space-y-1">
          {findings.map((f, i) => (
            <li key={i} data-testid="finding" className="text-sm">
              <span className="text-ink-muted">{f.severity}</span>{" "}
              {f.file && (
                <span className="font-mono text-xs text-ink-muted">
                  {f.file}
                  {f.line ? `:${f.line}` : ""}
                </span>
              )}{" "}
              <span className="text-ink">{f.issue}</span>
              {f.fix && <span className="text-ink-secondary"> — {f.fix}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
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
        {call.target && (
          <span className="ml-1 opacity-75" data-testid="tool-target">
            {call.target}
          </span>
        )}
        {call.ms !== undefined && ` · ${call.ms}ms`}
      </button>
      {open && call.detail && (
        <pre className="ml-4 whitespace-pre-wrap font-mono text-xs text-critical">{call.detail}</pre>
      )}
    </li>
  );
}

/** Thinking, folded away.
 *
 * Open while it is the only thing arriving: that is exactly when a person is
 * deciding whether to abort a model going in circles.
 *
 * Its own state after that. `open` used to be derived on every render, so a
 * re-render on the next token would have reopened what the reader had just
 * closed — and a reader closes it precisely because it has grown to thousands
 * of lines.
 */
function ReasoningBlock({ text, live }: { text: string; live: boolean }) {
  const [open, setOpen] = useState(live);
  const lines = text.split("\n");
  // The newest thing it said, shown on the summary while folded. 3,914 lines
  // behind a disclosure triangle tells you it is busy but not what it is busy
  // with, and expanding to find out means scrolling to the bottom of a wall.
  const tail = lines.filter((l) => l.trim() !== "").at(-1) ?? "";
  return (
    <details
      data-testid="turn-reasoning"
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
      className="mt-1 rounded border border-hairline bg-surface2 px-2 py-1"
    >
      <summary className="flex cursor-pointer items-baseline gap-2 text-xs text-ink-muted">
        <span className="shrink-0">
          thinking · {lines.length} line{lines.length === 1 ? "" : "s"}
        </span>
        {!open && tail && (
          <span
            data-testid="turn-reasoning-tail"
            className="min-w-0 truncate font-mono text-ink-secondary"
            title={tail}
          >
            {tail.length > 120 ? tail.slice(0, 120) + "…" : tail}
          </span>
        )}
      </summary>
      <pre className="mt-1 whitespace-pre-wrap font-mono text-xs text-ink-muted">{text}</pre>
    </details>
  );
}
