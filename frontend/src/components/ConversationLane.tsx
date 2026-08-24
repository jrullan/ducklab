import { useState } from "react";
import type { TurnBlock, ToolCall } from "../lib/runview";
import { ducklingColor } from "../lib/colors";
import { DuckAvatar } from "./DuckAvatar";
import { StatusChip } from "./StatusChip";
import { Prose } from "./Prose";
import { statusVar } from "../lib/colors";
import { DeliverablesInline } from "./DeliverablesCard";
import { humaniseContract, splitDeliverablesReport } from "../lib/runview";

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
  collapsed = false,
  onToggle,
  deliverableTexts,
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
  /** Finished turns fold to a one-line summary so a forty-turn run is a page,
   * not a scroll marathon. What must never go dark survives the fold: the
   * verdict, the tool count, the failures. The header is the toggle. */
  collapsed?: boolean;
  onToggle?: () => void;
  /** The task's deliverable texts, when the run's report event carried them,
   * so the implementer's closing report renders as a readable checklist. */
  deliverableTexts?: string[];
}) {
  const anonymous = !!block.label;
  const isGate = block.role === "gate";
  const isHuman = block.role === "human";
  if (block.role === "round") {
    return (
      <div data-testid="round-divider" className="my-2 flex items-center gap-2 text-xs text-ink-muted" role="separator">
        <span className="h-px flex-1 bg-hairline" />
        <span>{block.text}</span>
        <span className="h-px flex-1 bg-hairline" />
      </div>
    );
  }
  if (block.role === "refs") {
    return (
      <div data-testid="refs-divider" className="my-2 flex items-center gap-2 text-xs text-ink-muted" role="separator">
        <span className="h-px flex-1 bg-hairline" />
        <span>📚 {block.text}</span>
        <span className="h-px flex-1 bg-hairline" />
      </div>
    );
  }
  if (block.role === "pause" && block.pause) {
    return (
      <div
        data-testid="pause-divider"
        className="my-2 flex items-center gap-2 text-xs text-ink-muted"
        role="separator"
      >
        <span className="h-px flex-1 bg-hairline" />
        <span>
          ⏸ paused — {block.pause.reason}
          {block.pause.resumed ? " · resumed: the strategy replays from round 1 over the work already in the tree" : ""}
        </span>
        <span className="h-px flex-1 bg-hairline" />
      </div>
    );
  }
  const who = anonymous ? block.label! : block.duckling;
  const tint = anonymous || isGate ? "var(--text-secondary)" : (color ?? ducklingColor(block.duckling, roster));
  const failedTools = block.toolCalls.filter((c) => !c.ok).length;
  // The implementer's closing report is a contract, not prose: split it out
  // once so both the folded and the open turn can show it as a checklist.
  const report = block.done && block.role === "implementer" && block.text ? splitDeliverablesReport(block.text) : null;
  // A contract turn's JSON, said as a person would say it — the folded
  // advisor line read {"action":"note","note":"…"} where it should read
  // note — the note itself.
  const human = block.done ? humaniseContract(block.role, block.text) : null;
  const preview = human
    ? [human.label, human.body.split("\n").find((l) => l.trim() !== "") ?? ""].filter(Boolean).join(" — ")
    : (((report ? report.prose : block.text) ?? "").split("\n").find((l) => l.trim() !== "") ?? "");
  const reportDone = report ? report.items.filter((i) => i.status === "done").length : 0;

  return (
    <article
      data-testid="conversation-turn"
      data-role={block.role}
      data-anonymous={anonymous ? "true" : "false"}
      data-collapsed={String(collapsed)}
      className="border-l-2 py-2 pl-3"
      style={{ borderColor: tint }}
    >
      <header
        className={`flex items-center gap-2 text-sm ${onToggle ? "cursor-pointer select-none" : ""}`}
        data-testid={onToggle ? "turn-toggle" : undefined}
        onClick={onToggle}
      >
        {onToggle && (
          <span aria-hidden="true" className="text-xs text-ink-muted">
            {collapsed ? "›" : "⌄"}
          </span>
        )}
        {block.author ? (
          <span aria-label="advisor avatar" role="img" title={block.author}>🦆</span>
        ) : isGate ? (
          /* Turning while the suite runs, for the same reason the ducks bob:
             movement is how this UI says "working", and a still cog under a
             minutes-long gate read as a hang. */
          <span aria-hidden="true" title="the harness's deterministic gate — no model decides this" className={block.gate === "running" ? "cog-turn" : undefined} data-testid="gate-cog" data-turning={block.gate === "running" ? "true" : "false"}>
            ⚙
          </span>
        ) : anonymous ? (
          <span
            aria-hidden="true"
            title="identities hidden — this reviewer must not know who wrote which candidate"
          >
            🔒
          </span>
        ) : isHuman ? (
          <span aria-label="human avatar" role="img" title="human">
            🧑
          </span>
        ) : (
          <DuckAvatar id={block.duckling} roster={roster} color={color} bobbing={!block.done} />
        )}
        <span style={{ color: tint }}>{block.author ? block.author.replace(/ \(yolo\)$/, "") : isGate ? "gate" : who}</span>
        <span className="text-ink-muted">{block.author ? "answered under yolo" : isGate ? "verify" : block.role}</span>
        {/* The gate's own state, right in the header: running with a pulse,
            then the round's outcome. This is the moment between "approve"
            and the verdict that used to read as a hang. */}
        {isGate && block.gate === "running" && (
          <span className="text-ink-muted" data-testid="gate-running">
            <span className="animate-pulse">▸</span> running the suite…
          </span>
        )}
        {isGate && block.done && block.gate && block.gate !== "running" && (
          <StatusChip
            role={block.gate === "green" ? "good" : "serious"}
            label={block.gate}
          />
        )}
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
        {!block.done && !isGate && (
          <span className="text-ink-muted" data-testid="in-flight">
            thinking…
          </span>
        )}
        {/* The fold must not hide a judgement or a failure. */}
        {collapsed && block.verdict && (
          <StatusChip
            role={block.verdict === "approve" ? "good" : "serious"}
            label={String(block.verdict)}
          />
        )}
        {collapsed && failedTools > 0 && (
          <span className="text-xs text-critical">✕ {failedTools} failed</span>
        )}
      </header>

      {collapsed && (
        <p className="mt-0.5 truncate text-xs text-ink-muted" data-testid="turn-summary" title={preview}>
          {block.toolCalls.length > 0 && `${block.toolCalls.length} tool call${block.toolCalls.length === 1 ? "" : "s"}`}
          {block.toolCalls.length > 0 && preview && " · "}
          {preview}
        </p>
      )}
      {/* Nor the answer to "did it finish?": the report survives the fold as
          its own compact toggle — n/m at a glance, the checklist on click,
          without unfolding forty tool calls. */}
      {collapsed && report && (
        <FoldedReport items={report.items} done={reportDone} texts={deliverableTexts} />
      )}

      {isGate && block.done && block.gateExitCode !== undefined && (
        <div className="mt-1 text-sm text-ink-secondary" data-testid="gate-result">
          exit code {block.gateExitCode}{block.gateDurationS !== undefined ? ` · ${block.gateDurationS}s` : ""}
          {block.gateCommand && <div className="break-all font-mono text-xs">{block.gateCommand}</div>}
          {block.gateOutput && <pre className="mt-1 max-h-24 overflow-auto whitespace-pre-wrap font-mono text-xs">{block.gateOutput}</pre>}
        </div>
      )}
      {!collapsed && block.toolCalls.length > 0 && (
        <ul className="mt-1">
          {block.toolCalls.map((c) => (
            <ToolCallLine key={`${c.seq}-${c.tool}`} call={c} />
          ))}
        </ul>
      )}
      {/* The tool that is running RIGHT NOW. A verify_run can legally take
          its whole ceiling; unnamed, those minutes read as a hang and taught
          the person to abort healthy work. */}
      {!collapsed && !block.done && block.pendingTool && (
        <p className="mt-1 text-xs text-ink-muted" data-testid="tool-in-flight">
          <span className="animate-pulse">▸</span> {block.pendingTool.tool}
          {block.pendingTool.target ? ` ${block.pendingTool.target}` : ""} — running…
        </p>
      )}

      {/* A reviewer's turn is already structured. Rendering its raw text put
          `{"verdict":"approve", "findings":[]}` on screen — the one turn whose
          content the engine has already parsed, shown as a blob. */}
      {!collapsed && (block.done || !streamed) && block.verdict && <VerdictBlock block={block} />}

      {/* Thinking arrives before the answer and is billed either way. The
          stream parser used to read only delta.content, so a reasoning model
          spent tokens no one could see and a run that looked idle for two
          minutes had nothing on screen to explain itself. */}
      {/* A turn that ran out of budget or lost its provider still did real
          work, and the record of it is now kept — but a partial record read as a
          complete one is worse than none. */}
      {!collapsed && block.incomplete && (
        <div className="mt-1" data-testid="turn-incomplete">
          <StatusChip role="warning" label="turn did not finish" />
        </div>
      )}

      {!collapsed && reasoning && <ReasoningBlock text={reasoning} live={!!streamed && !block.done} />}

      {/* Live tokens while a turn is in flight, the RECORD once it is done.
          The streamed buffer holds whatever happened to stream — a chat
          consultant's thinking-aloud between its tool calls — while the
          message event holds what was actually said. The out-of-calls
          conclude reply (tools withheld) does not stream at all, so a capped
          turn's real answer existed only on the record, and this lane showed
          the scratch work over it: T-064's chat looked like it never
          answered while a full reply sat in events.jsonl. */}
      {/* Raw while tokens are still arriving — a half-written fence or bold
          marker cannot be parsed without guessing at what comes next — and
          rendered once the turn has settled. */}
      {!collapsed && block.author && (
        <p className="mt-1 text-xs text-ink-muted" data-testid="advisor-yolo-answer">
          answered by {block.author.replace(/^advisor:/, "").replace(" (yolo)", "")} under yolo
        </p>
      )}
      {!collapsed && (block.done && block.text && !block.verdict ? (
        <div data-testid="turn-text">
          {human ? (
            /* The engine already parsed this contract; the view re-parses
               only to display. The chip carries the decision, the prose the
               why — never the braces. */
            <div className="mt-1" data-testid="contract-answer">
              <StatusChip role={human.tone} label={human.label} />
              {human.body && <Prose body={human.body} suppress={[]} className="mt-1 space-y-2 text-sm text-ink-secondary" />}
            </div>
          ) : !report ? (
            <Prose body={block.text} suppress={[]} className="mt-1 space-y-2 text-sm text-ink-secondary" />
          ) : (
            <>
              {report.prose && <Prose body={report.prose} suppress={[]} className="mt-1 space-y-2 text-sm text-ink-secondary" />}
              <DeliverablesInline items={report.items} texts={deliverableTexts} />
            </>
          )}
        </div>
      ) : streamed ? (
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
      ))}
    </article>
  );
}

/** The report on a folded turn: a count that opens the checklist by itself. */
function FoldedReport({
  items,
  done,
  texts,
}: {
  items: { id: number; status: string; note?: string }[];
  done: number;
  texts?: string[];
}) {
  const [open, setOpen] = useState(false);
  const complete = done === items.length;
  return (
    <div className="mt-1" data-testid="deliverables-fold">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="text-sm tabular-nums"
        data-testid="deliverables-fold-count"
        aria-expanded={open}
        title={open ? "hide the checklist" : "show the checklist"}
        style={{ color: statusVar(complete ? "good" : "warning") }}
      >
        {complete ? "☑" : "◐"} {done}/{items.length} <span className="text-ink-muted">{open ? "⌄" : "›"}</span>
      </button>
      {open && <DeliverablesInline items={items} texts={texts} />}
    </div>
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
      {block.deliverablesGap && block.deliverablesGap.length > 0 && (
        <span className="ml-2 text-sm" data-testid="deliverables-gap" style={{ color: statusVar("critical") }}>
          ⚠ approved over deliverables the implementer reported undelivered: {block.deliverablesGap.join(", ")}
        </span>
      )}
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
  // A violation starts expanded: it is the one thing a user must not scroll
  // past. So does a consult: the duck spoke, and what it said is the reason
  // for whatever the implementer does next.
  const [open, setOpen] = useState(!!call.violation || !!call.advice);
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
      {open && call.advice && (
        <blockquote
          data-testid="tool-advice"
          className="ml-4 mt-0.5 border-l-2 border-hairline pl-2 text-sm text-ink-secondary"
        >
          <span className="text-xs text-ink-muted">advisor · </span>
          {call.advice}
        </blockquote>
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
