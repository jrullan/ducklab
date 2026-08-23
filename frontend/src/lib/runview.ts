/**
 * Pure derivations for the Run view.
 *
 * All the reasoning lives here rather than in components: an event stream
 * turned into lanes, a timeline and a gate verdict is real logic, and logic
 * inside JSX can only be tested through a DOM. Components below this file are
 * meant to be thin enough to be obviously correct.
 */

import type { DucklabEvent } from "../api/events";
import type { StatusRole } from "./colors";

export interface ToolCall {
  seq: number;
  tool: string;
  ok: boolean;
  ms?: number;
  detail?: string;
  /** Policy violations are always expanded; they are never routine. */
  violation?: boolean;
  /** An ask_advisor consult's answer: the rubber duck spoke inline, in the
   * middle of the turn. Rendered open — a consult is never routine, and the
   * advice the implementer acted on next must not be buried in the calls. */
  advice?: string;
  /** The call's salient argument — the path it read, the pattern it searched,
   * the command it ran. A live list of bare "fs_read fs_read fs_read" says a
   * model is busy without saying WITH WHAT. */
  target?: string;
}

/** Distils a tool call's args to the one argument a watcher wants: what it
 * touched. */
export function toolTarget(d: Record<string, unknown>): string | undefined {
  let a: Record<string, unknown> | null = null;
  try {
    a = JSON.parse(String(d.args ?? "")) as Record<string, unknown>;
  } catch {
    return undefined;
  }
  if (!a || typeof a !== "object") return undefined;
  let s: string;
  if (typeof a.path === "string" && a.path) {
    s = a.path;
    if (typeof a.start === "number" && typeof a.end === "number") {
      s += `:${a.start}–${a.end}`;
    }
  } else {
    const pick = a.pattern ?? a.cmd ?? a.command ?? a.glob ?? a.id ?? a.kind ?? a.question ?? a.name;
    if (pick === undefined || pick === null) return undefined;
    s = String(pick);
  }
  // The tail is the identity of a path; the head is the identity of a command.
  if (s.length > 64) {
    s = typeof a.path === "string" ? "…" + s.slice(-63) : s.slice(0, 63) + "…";
  }
  return s || undefined;
}

export interface TurnBlock {
  key: string;
  round: number;
  turn: number;
  role: string;
  duckling: string;
  /** A bare message with no agent turn behind it — a chat's human messages,
   * mostly. Its key is synthetic, so it must never be looked up in the
   * delta/reasoning stores: those are keyed round:turn, and a human bubble
   * wearing the consultant's coordinates rendered the consultant's thinking
   * twice. */
  messageOnly?: boolean;
  /** Anonymous label used instead of `duckling` when the turn is hidden. */
  label?: string;
  toolCalls: ToolCall[];
  text: string;
  done: boolean;
  /** The turn ended without finishing — its budget ran out, its provider failed.
   * What it said and did is real but partial, and a reader must not take it for
   * a complete record. */
  incomplete?: boolean;
  /** What this turn was working on: a split's subtask, a tournament's
   * contestant slot. Two lanes with the same role and the same duckling are
   * otherwise indistinguishable, which is exactly what a split produces. */
  subject?: string;
  /** The tool that has STARTED and not yet completed — a gate command can
   * legally run for fifteen minutes, and without this the lane showed that
   * as unexplained silence. Cleared by the completion event. */
  pendingTool?: { tool: string; target?: string };
  /** A harness gate rendered as its own turn: "running" while the suite
   * executes, then the round's outcome. Not a model's turn — the reviewer's
   * approve above it and the silence below it are otherwise indistinguishable
   * from a hang. */
  gate?: "running" | "green" | "red" | string;
  /** The lifecycle phase that opened a harness gate. */
  gatePhase?: string;
  /** The turn's recorded thinking, consolidated at turn end. The live deltas
   * are display state and die with the window; this is what a relaunched
   * desktop reads instead of showing the thinking gone. */
  reasoning?: string;
  /** True when another turn was open at the same time.
   *
   * Lanes are stacked, so concurrency reads as sequence: a reviewer of a split
   * cannot tell whether two pieces ran together or one after the other, and
   * those are different claims about what the models were given. */
  concurrent?: boolean;
  /** A reviewer's turn is a verdict, not prose. Present only when the engine
   * parsed one, so the lane can render findings instead of a JSON blob. */
  verdict?: string;
  findings?: Finding[];
  /** Deliverable ids the implementer reported undelivered when this
   * reviewer approved anyway — the contradiction the record flagged. */
  deliverablesGap?: number[];
  /** A harness pause/resume rendered as its own divider in the lane. A run
   * that paused on its wallclock budget and was resumed replays the strategy
   * from round 1; without a marker the lane read imp → rev → imp → rev and
   * looked broken. */
  pause?: { reason: string; resumed: boolean };
}

/** One thing a reviewer objected to. */
export interface Finding {
  severity: string;
  file?: string;
  line?: number;
  issue: string;
  fix?: string;
}

/** The implementer's deliverables report — the work contract, as filed. */
export interface DeliverableLine {
  id: number;
  text: string;
  status: "done" | "partial" | "not_done" | "blocked" | "unreported";
  note?: string;
}
export interface DeliverablesState {
  round: number;
  retry?: number;
  unreported: boolean;
  lines: DeliverableLine[];
  done: number;
  total: number;
  /** True when a reviewer approved over items the implementer reported undelivered. */
  gap: boolean;
}

/** The LATEST deliverables_report wins: a retried implementer turn re-files. */
export function buildDeliverables(events: DucklabEvent[]): DeliverablesState | null {
  let report: DucklabEvent | null = null;
  let gap = false;
  for (const e of events) {
    if (e.type === "deliverables_report") {
      report = e;
      gap = false;
    } else if (e.type === "deliverables_gap") {
      gap = true;
    }
  }
  if (!report) return null;
  const d = report.data ?? {};
  const texts = Array.isArray(d.deliverables) ? d.deliverables.map(String) : [];
  const total = Number(d.total ?? texts.length) || texts.length;
  const byId = new Map<number, { status: string; note?: string }>();
  if (Array.isArray(d.items)) {
    for (const it of d.items) {
      const id = Number(it?.id);
      if (id >= 1) byId.set(id, { status: String(it.status ?? ""), note: it.note ? String(it.note) : undefined });
    }
  }
  const lines: DeliverableLine[] = [];
  for (let id = 1; id <= Math.max(total, texts.length); id++) {
    const r = byId.get(id);
    const status = (r && ["done", "partial", "not_done", "blocked"].includes(r.status)
      ? r.status
      : "unreported") as DeliverableLine["status"];
    lines.push({ id, text: texts[id - 1] ?? `deliverable ${id}`, status, note: r?.note });
  }
  return {
    round: Number(d.round ?? 0),
    retry: d.retry != null ? Number(d.retry) : undefined,
    unreported: Boolean(d.unreported),
    lines,
    done: lines.filter((l) => l.status === "done").length,
    total: lines.length,
    gap,
  };
}

/** Splits an implementer's reply into its prose and the deliverables report
 * object it ends with, so the lane can render the report as a checklist
 * instead of a JSON blob. Same tolerant walk as the engine's parser: the last
 * object containing "deliverables", brace-matched. */
export function splitDeliverablesReport(text: string): {
  prose: string;
  items: { id: number; status: string; note?: string }[];
} | null {
  const idx = text.lastIndexOf('"deliverables"');
  if (idx < 0) return null;
  const start = text.lastIndexOf("{", idx);
  if (start < 0) return null;
  let depth = 0;
  let end = -1;
  for (let i = start; i < text.length; i++) {
    const c = text[i];
    if (c === "{") depth++;
    else if (c === "}") {
      depth--;
      if (depth === 0) {
        end = i + 1;
        break;
      }
    }
  }
  if (end < 0) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(text.slice(start, end));
  } catch {
    return null;
  }
  const raw = (parsed as { deliverables?: unknown })?.deliverables;
  if (!Array.isArray(raw)) return null;
  const items = raw
    .map((it) => ({
      id: Number((it as { id?: unknown })?.id),
      status: String((it as { status?: unknown })?.status ?? "").toLowerCase().replace(/[-\s]/g, "_"),
      note: (it as { note?: unknown })?.note ? String((it as { note?: unknown }).note) : undefined,
    }))
    .filter((it) => it.id >= 1 && ["done", "partial", "not_done", "blocked"].includes(it.status));
  if (items.length === 0) return null;
  // Drop the object and any code fence that wrapped it.
  let prose = text.slice(0, start) + text.slice(end);
  prose = prose.replace(/```(?:json)?\s*```/g, "").replace(/\n{3,}/g, "\n\n").trim();
  return { prose, items };
}

export interface GateState {
  gate: string;
  exitCode?: number;
  cmd?: string;
  role: StatusRole;
  label: string;
  /** True when nothing executable existed, so this can never read as success. */
  unverified: boolean;
}

export interface PendingHuman {
  kind: string;
  question?: string;
  questionId?: string;
  verdict?: string;
  /** The advisor's drafted answer, when one has landed — the human's role
   * becomes choosing, not researching. */
  advice?: string;
  advisor?: string;
  /** Advisor seat while its recommendation is still in flight. */
  advisorPending?: string;
  /** Why the run stopped, for pauses that carry a reason (budget, provider,
   * error). "waiting for you — error" without the error sent the person to
   * the record to learn what the event already said. */
  detail?: string;
}

/**
 * Groups a run's events into per-turn blocks.
 *
 * Tool calls collapse into their turn: a run that made forty fs_read calls
 * must still be skimmable, so the lane shows one line each rather than forty
 * message bubbles.
 */
export function buildTurns(events: readonly DucklabEvent[]): TurnBlock[] {
  const blocks: TurnBlock[] = [];
  const byKey = new Map<string, TurnBlock>();
  // The turn most recently opened, used only for events that do not say which
  // turn they belong to.
  let last: TurnBlock | null = null;
  // Turns that have started and not yet ended.
  const open = new Set<TurnBlock>();
  // The harness gate currently executing, opened by gate_started. Kept out of
  // `last`: coordinate-less events must never land inside a gate turn.
  let openGate: TurnBlock | null = null;

  const keyFor = (round: number, turn: number) => `${round}:${turn}`;

  const isConfirmedCommitHandoff = (gate: TurnBlock, detail: Record<string, unknown>): boolean => {
    if (gate.gatePhase !== "commit") return false;
    for (const key of ["sha", "commit_sha", "committed_sha"]) {
      if (typeof detail[key] === "string" && detail[key]) return true;
    }
    // Some handoff events carry the sha only in their human-readable detail.
    return typeof detail.detail === "string" && /\b[0-9a-f]{7,40}\b/i.test(detail.detail);
  };

  /**
   * The turn an event belongs to.
   *
   * This used to be a single `current` pointer advanced by turn_start and
   * cleared by turn_end, which assumes turns happen one after another. A
   * tournament runs its contestants in parallel, so their events interleave:
   * the second turn_start replaced the first, everything after it landed in
   * the wrong lane, and the first contestant was left thinking forever above
   * an orphaned block holding its own words.
   *
   * Events that carry round and turn are routed by them. Older events that do
   * not fall back to the last opened turn, which is correct for every
   * sequential mode.
   */
  const blockFor = (d: Record<string, unknown>): TurnBlock | null => {
    if (d.round === undefined && d.turn === undefined) return last;
    return byKey.get(keyFor(Number(d.round ?? 1), Number(d.turn ?? 0))) ?? last;
  };

  for (const e of events) {
    const d = e.data ?? {};
    switch (e.type) {
      case "turn_start": {
        const round = Number(d.round ?? 1);
        const turn = Number(d.turn ?? 0);
        // The round boundary, said out loud. A council whose reviewer asked
        // for changes opens round 2 with the architect again — two adjacent
        // entries by the same actor that read as a duplicated turn until
        // the divider names the loop.
        const lastReal = [...blocks].reverse().find((b) => b.role !== "pause" && !b.key.startsWith("round:"));
        // Not in chats: there a "round" is just one exchange's coordinates,
        // and a divider between every reply would slice the conversation.
        if (round > 1 && String(d.role ?? "") !== "consultant" && lastReal && lastReal.round < round) {
          blocks.push({
            key: `round:${round}:${e.seq ?? blocks.length}`,
            round, turn: -1, role: "round", duckling: "",
            toolCalls: [], text: `round ${round}`, done: true, messageOnly: true,
          });
        }
        const key = keyFor(round, turn);
        const block: TurnBlock = {
          key,
          round,
          turn,
          role: String(d.role ?? ""),
          duckling: String(d.duckling ?? ""),
          subject: subjectOf(d),
          toolCalls: [],
          text: "",
          done: false,
        };
        byKey.set(key, block);
        last = block;
        blocks.push(block);
        // Anything already open overlaps this one, and this one overlaps them.
        if (open.size > 0) {
          block.concurrent = true;
          for (const o of open) o.concurrent = true;
        }
        open.add(block);
        break;
      }
      case "turn_end": {
        const b = blockFor(d);
        if (b) {
          b.done = true;
          if (d.incomplete === true) b.incomplete = true;
        }
        open.delete(b ?? ({} as TurnBlock));
        break;
      }
      case "message": {
        // What the model said. Nothing filled `text` before, so every lane
        // rendered a participant header above an empty bubble even when the
        // engine had the content.
        const content = String(d.content ?? "");
        const verdict = d.verdict ? String(d.verdict) : undefined;
        const findings = Array.isArray(d.findings) ? (d.findings as Finding[]) : undefined;
        const b = blockFor(d);
        // Only a block of the SAME role may absorb the message. A chat's
        // human messages carry no coordinates, so the fallback handed them
        // to the last open turn — and the human's next question OVERWROTE
        // the consultant's recorded reply in the lane. A role mismatch means
        // this message is its own bubble, in event order.
        const role = String(d.role ?? "");
        if (b && (role === "" || b.role === role)) {
          b.text = content;
          b.verdict = verdict;
          b.findings = findings;
          if (typeof d.reasoning === "string" && d.reasoning) b.reasoning = d.reasoning;
        } else {
          // A message with no turn of its own still belongs in the lane
          // rather than being dropped on the floor. Its key is synthetic and
          // unique: reusing round:turn coordinates collided with a real
          // turn's key, and the delta stores served that turn's thinking to
          // both blocks.
          blocks.push({
            key: `msg:${e.seq ?? blocks.length}`,
            round: Number(d.round ?? 1),
            turn: Number(d.turn ?? 0),
            role,
            duckling: String(d.duckling ?? ""),
            toolCalls: [],
            text: content,
            done: true,
            messageOnly: true,
            verdict,
            findings,
          });
        }
        break;
      }
      case "deliverables_gap": {
        // Belongs to the reviewer's verdict of that round, not to a rail
        // card: an unreviewed progress report must not read as a result.
        const round = Number(d.round ?? 0);
        const ids = Array.isArray(d.undelivered) ? d.undelivered.map(Number) : [];
        for (let i = blocks.length - 1; i >= 0; i--) {
          const rb = blocks[i]!;
          if (rb.role === "reviewer" && (round === 0 || rb.round === round)) {
            rb.deliverablesGap = ids;
            break;
          }
        }
        break;
      }
      case "tool_call_started": {
        const b = blockFor(d);
        if (b) b.pendingTool = { tool: String(d.tool ?? "?"), target: toolTarget(d) };
        break;
      }
      case "tool_call": {
        const b0 = blockFor(d);
        if (b0) b0.pendingTool = undefined;
        blockFor(d)?.toolCalls.push({
          seq: e.seq ?? 0,
          tool: String(d.tool ?? "?"),
          ok: d.ok !== false,
          ms: typeof d.ms === "number" ? d.ms : undefined,
          target: toolTarget(d),
          // A failed call's result says WHY — "search text not found",
          // "path is not a test file" — and it was recorded and never shown:
          // the ✕ expanded to nothing.
          detail: d.ok === false ? String(d.result ?? "") || undefined : undefined,
          advice:
            d.tool === "ask_advisor" && d.ok !== false
              ? String(d.result ?? "").replace(/^advisor:\s*/, "") || undefined
              : undefined,
        });
        break;
      }
      case "human_needed": {
        // Only harness pauses that break the conversation's flow get a
        // divider; a question pauses in place and renders as its own input.
        const kind = String(d.kind ?? "");
        if (kind === "budget" || kind === "error" || kind === "provider" || kind === "engine_restart") {
          blocks.push({
            key: `pause:${e.seq ?? blocks.length}`,
            round: Number(d.round ?? 0),
            turn: -1,
            role: "pause",
            duckling: "",
            toolCalls: [],
            text: "",
            done: true,
            messageOnly: true,
            pause: { reason: String(d.detail ?? kind), resumed: false },
          });
        }
        break;
      }
      case "checkpoint": {
        if (String(d.reason ?? "") === "resume") {
          for (let i = blocks.length - 1; i >= 0; i--) {
            const pb = blocks[i]!;
            if (pb.role === "pause") {
              if (pb.pause) pb.pause.resumed = true;
              break;
            }
            if (pb.role !== "pause" && !pb.messageOnly) break;
          }
        }
        break;
      }
      case "references_loaded": {
        const files = Array.isArray(d.files) ? d.files : [];
        const total = files.reduce((n: number, f) => n + Number((f as Record<string, unknown>).chars ?? 0), 0);
        const mode = String(d.mode ?? "inline");
        blocks.push({
          key: `refs:${e.seq ?? blocks.length}`,
          round: 0,
          turn: -1,
          role: "refs",
          duckling: "",
          toolCalls: [],
          text:
            `${files.length} reference document${files.length === 1 ? "" : "s"} · ${total.toLocaleString()} chars — ` +
            (mode === "digest"
              ? "digest mode: summaries in the prompt, full text via ref_read"
              : "full text in the prompt"),
          done: true,
          messageOnly: true,
        });
        break;
      }
      case "reference_digested": {
        const path = String(d.path ?? "");
        const base = path.split("/").pop() ?? path;
        blocks.push({
          key: `refdigest:${e.seq ?? blocks.length}`,
          round: 0,
          turn: -1,
          role: "refs",
          duckling: "",
          toolCalls: [],
          text: `digested ${base} (${Number(d.chars ?? 0).toLocaleString()} → ${Number(d.digest_chars ?? 0).toLocaleString()} chars${d.cached ? ", cached" : ""})`,
          done: true,
          messageOnly: true,
        });
        break;
      }
      case "gate_started": {
        // A new gate phase supersedes an earlier open gate block. Superseding
        // is not itself a successful verdict: in particular, committing may
        // have aborted after its announcement. Only a handoff that identifies
        // the committed object earns a green check for that phase.
        if (openGate && !openGate.done) {
          openGate.done = true;
          openGate.gate = isConfirmedCommitHandoff(openGate, d) ? "green" : "done";
        }
        const round = Number(d.round ?? 1);
        const block: TurnBlock = {
          key: `gate:${round}:${e.seq ?? blocks.length}`,
          round,
          turn: -1,
          role: "gate",
          duckling: "gate",
          toolCalls: [],
          // A test-first's baseline suite takes minutes before any model
          // speaks; the announcement's own words are what stands between
          // "working" and "hung".
          text: d.detail ? String(d.detail) : "",
          // The phase named by the moment it measures, not by harness
          // vocabulary: "accept" says who ordered the suite, "after commit ·
          // clean checkout" says what world it ran in.
          subject: d.phase ? gatePhaseLabel(String(d.phase)) : undefined,
          gatePhase: d.phase ? String(d.phase) : undefined,
          done: false,
          messageOnly: true,
          gate: "running",
        };
        blocks.push(block);
        openGate = block;
        break;
      }
      case "gate": {
        // Test-first's settled gate (phase before/after) closes the turn its
        // announcement opened; build rounds close via round_gate instead.
        if (openGate && !openGate.done && d.phase) {
          openGate.done = true;
          openGate.gate = (typeof d.exit === "number" ? d.exit : 1) === 0 ? "green" : "red";
          openGate = null;
        }
        break;
      }
      case "gate_reproduced": {
        // A clean-checkout reproduction is the terminal verdict for every
        // accept step it follows: commit and reproduction are separately
        // announced, but neither must remain live in a replayed transcript.
        for (const gate of blocks) {
          if (gate.role === "gate" && gate.gatePhase === "accept" && !gate.done) {
            gate.done = true;
            gate.gate = d.green === false ? "red" : "green";
          }
          // Reproduction is the later confirmation that the preceding commit
          // really landed and was testable. It may upgrade the commit's
          // previously neutral superseded state, but not before that evidence.
          if (gate.role === "gate" && gate.gatePhase === "commit" && gate.done && gate.gate === "done" && d.green !== false) {
            gate.gate = "green";
          }
        }
        openGate = null;
        break;
      }
      case "round_gate": {
        // Closes the announced gate turn; runs recorded before gate_started
        // existed still get their gate in the lane, already settled.
        const result = String(d.result ?? "");
        if (openGate && !openGate.done) {
          openGate.done = true;
          openGate.gate = result;
        } else {
          blocks.push({
            key: `gate:${Number(d.round ?? 1)}:${e.seq ?? blocks.length}`,
            round: Number(d.round ?? 1),
            turn: -1,
            role: "gate",
            duckling: "gate",
            toolCalls: [],
            text: "",
            done: true,
            messageOnly: true,
            gate: result,
          });
        }
        openGate = null;
        break;
      }
      case "provider_retry": {
        // Provider weather, in the lane where the silence was: "retrying
        // (2): provider sent nothing for 2m0s" is what stops a person from
        // aborting healthy work. Rendered as a failed tool line — it is an
        // action the turn took, with a reason worth expanding.
        blockFor(d)?.toolCalls.push({
          seq: e.seq ?? 0,
          tool: `provider retry (${Number(d.attempt ?? 1)})`,
          ok: false,
          detail: String(d.error ?? ""),
        });
        break;
      }
      case "policy_violation": {
        blockFor(d)?.toolCalls.push({
          seq: e.seq ?? 0,
          tool: String(d.tool ?? "?"),
          ok: false,
          detail: String(d.detail ?? ""),
          violation: true,
        });
        break;
      }
    }
  }
  return blocks;
}

/** What a turn was working on, if the event says.
 *
 * A split runs its subtasks concurrently on the same duckling, so its lanes
 * read "pato-atom implementer" twice with nothing to tell them apart — which
 * is the question the screen should answer without anyone opening the run log.
 */
function subjectOf(d: Record<string, unknown>): string | undefined {
  if (typeof d.subtask === "string" && d.subtask) return d.subtask;
  if (typeof d.bug === "string" && d.bug) return d.bug;
  if (typeof d.contestant === "number") return `candidate ${d.contestant + 1}`;
  return undefined;
}

/**
 * Applies anonymisation to turn blocks for display.
 *
 * The UI must not reveal a mapping it happens to hold: I7 is a property of the
 * product, not only of the prompt. When a run is anonymised, the author's
 * duckling is replaced by a stable letter and the identity is dropped from the
 * block entirely, so a component cannot render it by accident.
 */
export function anonymiseTurns(blocks: readonly TurnBlock[], anonymise: boolean): TurnBlock[] {
  if (!anonymise) return blocks.map((b) => ({ ...b }));

  const labels = new Map<string, string>();
  return blocks.map((b) => {
    // Dividers and harness blocks are not contestants; handing them a
    // letter shifted everyone after them down the alphabet.
    if (!b.duckling) return { ...b };
    let label = labels.get(b.duckling);
    if (!label) {
      label = String.fromCharCode(65 + labels.size);
      labels.set(b.duckling, label);
    }
    return { ...b, duckling: "", label };
  });
}

/** One tick per tool call, in order — how a user spots "it read the same file
 * nine times" without reading the transcript. */
export function buildTimeline(events: readonly DucklabEvent[]): ToolCall[] {
  const out: ToolCall[] = [];
  for (const e of events) {
    const d = e.data ?? {};
    if (e.type === "tool_call") {
      out.push({
        seq: e.seq ?? 0,
        tool: String(d.tool ?? "?"),
        ok: d.ok !== false,
        ms: typeof d.ms === "number" ? d.ms : undefined,
        target: toolTarget(d),
      });
    } else if (e.type === "policy_violation") {
      out.push({ seq: e.seq ?? 0, tool: String(d.tool ?? "?"), ok: false, violation: true });
    }
  }
  return out;
}

/** Groups tools into families so the timeline can colour them. */
export function toolFamily(tool: string): "read" | "write" | "exec" | "vcs" | "other" {
  if (tool.startsWith("git_")) return "vcs";
  // Suffix rules, not name lists: artifact_read, bug_read, run_list and
  // whatever read surface arrives next classify themselves.
  if (tool.endsWith("_read") || tool.endsWith("_list") || tool === "fs_search") return "read";
  if (tool.startsWith("fs_write") || tool === "fs_patch" || tool === "fs_delete") return "write";
  if (tool === "shell" || tool === "verify_run" || tool === "skill_run") return "exec";
  return "other";
}

/**
 * Derives the gate card state.
 *
 * A `none` gate can never read as success (P3): nothing was executed, and the
 * label says so rather than showing a neutral tick a user would read as green.
 */
/** The words a gate phase wears in the lane. */
export function gatePhaseLabel(phase: string): string {
  switch (phase) {
    case "before":
      return "baseline";
    case "after":
      return "over the new test";
    case "final":
      return "the full gate — verdict";
    case "commit":
      return "committing accepted work";
    case "accept":
      return "after commit · clean checkout";
  }
  return `gate ${phase}`;
}

export function buildGate(events: readonly DucklabEvent[]): GateState | null {
  let latest: DucklabEvent | null = null;
  for (const e of events) {
    if (e.type === "gate") latest = e;
  }
  if (!latest) return null;

  const d = latest.data ?? {};
  const gate = String(d.gate ?? "none");
  const exit = typeof d.exit === "number" ? d.exit : undefined;
  const cmd = d.cmd ? String(d.cmd) : undefined;
  const phase = d.phase ? String(d.phase) : "";

  if (gate === "none") {
    return {
      gate, exitCode: exit, cmd,
      role: "warning",
      label: "unverified — nothing executable to run",
      unverified: true,
    };
  }
  const green = exit === 0;
  // The baseline is not a verdict: "✓ tests passed" on a test-first's rail
  // read as a judgment of work that had not happened yet.
  const phaseWord = phase === "before" ? "baseline " : "";
  return {
    gate, exitCode: exit, cmd,
    role: green ? "good" : "critical",
    label: green ? `${phaseWord}${gate} passed` : `${phaseWord}${gate} failed`,
    unverified: false,
  };
}

/** The reviewer's standing objection, when its LAST word was not approval.
 *
 * The gate decides the verdict (I2) and the reviewer advises — so a pair can
 * end green with the reviewer still requesting changes, rounds exhausted.
 * That is a legitimate state and exactly what the human gate is for; what it
 * must not be is silent. T-028 ended "tests passed" over three consecutive
 * request-changes verdicts, and the person learned it only by reading the
 * whole transcript. */
/** The reviewer's last word, approval or not, with its findings. */
export function finalVerdict(
  turns: readonly TurnBlock[],
): { verdict: string; findings: Finding[] } | null {
  let last: TurnBlock | null = null;
  for (const t of turns) {
    if (t.verdict) last = t;
  }
  if (!last) return null;
  return { verdict: last.verdict!, findings: last.findings ?? [] };
}

export function reviewerDissent(
  turns: readonly TurnBlock[],
): { verdict: string; findings: number; notes: string[] } | null {
  let last: TurnBlock | null = null;
  for (const t of turns) {
    if (t.verdict) last = t;
  }
  if (!last) return null;
  const v = last.verdict!.toLowerCase().replace(/_/g, "-");
  if (v === "approve" || v === "approved") return null;
  // The findings as sentences, ready to ride a follow-up run's note.
  const notes = (last.findings ?? []).map((f) => {
    let s = f.issue;
    if (f.file) s += ` (${f.file}${f.line ? `:${f.line}` : ""})`;
    if (f.fix) s += ` — fix: ${f.fix}`;
    return s;
  });
  return { verdict: last.verdict!, findings: notes.length, notes };
}

/** The bug ids this run's findings were already filed under, from the
 * record. The button knew only about clicks made in this mount, so a filed
 * run re-visited offered to file again — and the engine's refusal, not the
 * record, was what saved the person from believing it. */
export function findingsFiled(events: readonly DucklabEvent[]): string[] | null {
  for (const e of events) {
    if (e.type === "findings_filed") {
      const bugs = e.data?.bugs;
      if (Array.isArray(bugs)) return bugs.map((b) => String(b));
      return [];
    }
  }
  return null;
}

/** The chained build this test run started, from the record. */
export function chainedBuildId(events: readonly DucklabEvent[]): string {
  for (const e of events) {
    if (e.type === "tdd_build_started" && e.data?.run) return String(e.data.run);
  }
  return "";
}

/** The pending human interaction, if the run is waiting on one. */
export function buildPending(events: readonly DucklabEvent[]): PendingHuman | null {
  let latest: DucklabEvent | null = null;
  let advice: DucklabEvent | null = null;
  for (const e of events) {
    if (e.type === "human_needed") {
      latest = e;
      advice = null;
    }
    if (e.type === "advice" || e.type === "advice_failed") advice = e;
    // A human action clears the wait — and so does a checkpoint: resume
    // appends one, and a budget pause that was lifted and resumed went on
    // saying "waiting for you" over a run that was already working again.
    if (e.type === "human" || e.type === "run_end" || e.type === "checkpoint") {
      latest = null;
      advice = null;
    }
  }
  if (!latest) return null;
  const d = latest.data ?? {};
  const a = advice?.data ?? {};
  const adviceMatches = advice && String(a.question_id ?? "") === String(d.question_id ?? "");
  const question = String(d.kind ?? "") === "question";
  const failed = adviceMatches && advice?.type === "advice_failed";
  return {
    kind: String(d.kind ?? "gate"),
    question: d.question ? String(d.question) : undefined,
    questionId: d.question_id ? String(d.question_id) : undefined,
    verdict: d.verdict ? String(d.verdict) : undefined,
    detail: failed && (a.cause || a.error) ? String(a.cause ?? a.error) : d.detail ? String(d.detail) : undefined,
    advice: adviceMatches && !failed && a.answer ? String(a.answer) : undefined,
    advisor: adviceMatches && a.advisor ? String(a.advisor) : d.advisor ? String(d.advisor) : undefined,
    advisorPending: question && !adviceMatches && d.advisor ? String(d.advisor) : undefined,
  };
}

export interface DiffFile {
  path: string;
  hunks: string[];
  /** True for files a project would recognise as tests. */
  isTest: boolean;
}

const TEST_PATTERNS = [/_test\.go$/, /^test_.*\.py$/, /\.test\.(ts|tsx|js|jsx)$/, /(^|\/)tests?\//];

export function isTestPath(path: string): boolean {
  return TEST_PATTERNS.some((re) => re.test(path));
}

/**
 * Splits a unified diff into files.
 *
 * Test files are flagged so the Diff tab can pin them to the top under the
 * "this change edits tests" banner (05 §5.3) — a change to the thing that
 * decides pass/fail must never be buried halfway down a diff.
 */
export function parseDiff(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  let current: DiffFile | null = null;
  let hunk: string[] = [];

  const flushHunk = () => {
    if (current && hunk.length) {
      current.hunks.push(hunk.join("\n"));
      hunk = [];
    }
  };

  for (const line of diff.split("\n")) {
    if (line.startsWith("+++ ")) {
      flushHunk();
      const path = line.slice(4).replace(/^b\//, "").trim();
      current = { path, hunks: [], isTest: isTestPath(path) };
      files.push(current);
    } else if (line.startsWith("@@")) {
      flushHunk();
      if (!current) {
        // A diff of two documents rather than two trees — what a stage's
        // proposal carries — has no file header at all. Without this the
        // hunks belonged to no file, parseDiff returned nothing, and the
        // Cycle view reported "No changes yet." over a whole draft.
        current = { path: "", hunks: [], isTest: false };
        files.push(current);
      }
      hunk.push(line);
    } else if (current && (hunk.length || line.startsWith("---"))) {
      if (!line.startsWith("---")) hunk.push(line);
    }
  }
  flushHunk();
  return files;
}

/** Test files first, so an edit to the gate is the first thing seen. */
export function orderDiffFiles(files: readonly DiffFile[]): DiffFile[] {
  return [...files].sort((a, b) => Number(b.isTest) - Number(a.isTest));
}

export function touchesTests(files: readonly DiffFile[]): boolean {
  return files.some((f) => f.isTest);
}

/** What to call a run in a list.
 *
 * Rows used to be labelled with task_id alone, which is empty for the artifact
 * stages — intake, spec and plan carry no task. Those rows rendered an anchor
 * with no text: invisible, unclickable, and the only runs that ever pause at a
 * human gate. */
export function runLabel(run: {
  task_id?: string;
  stage?: string;
  subject?: string;
  id: string;
}): string {
  // The stage ALWAYS shows. A test run and a build run of the same task both
  // read "T-083", and the person who launched a TDD chain could not tell
  // which phase they were looking at — nor notice when a relaunch quietly
  // became build-only.
  const stage = run.stage || "";
  if (run.task_id) return stage ? `${stage} · ${run.task_id}` : run.task_id;
  // A taskless run may still have a subject — the bug a triage read. Every
  // triage row said "triage" and telling two apart meant opening both.
  if (run.subject) return stage ? `${stage} · ${run.subject}` : run.subject;
  return stage || run.id;
}

/** A report the triager could not classify, and why. */
export type TriageFailure = { bug: string; error: string };

/**
 * Reports the triage could not classify.
 *
 * One bad bug does not poison the others — the batch carries on and the failure
 * is written down. But nothing rendered it, so a run that triaged two of three
 * looked exactly like one that triaged two, and the third stayed open with no
 * explanation anywhere a person would look.
 */
export function buildTriageFailures(events: readonly DucklabEvent[]): TriageFailure[] {
  const byBug = new Map<string, TriageFailure>();
  for (const e of events) {
    if (!e || e.type !== "triage_failed") continue;
    const d = (e.data ?? {}) as Record<string, unknown>;
    const bug = String(d.bug ?? "");
    if (!bug) continue;
    byBug.set(bug, { bug, error: String(d.error ?? "no reason recorded") });
  }
  return [...byBug.values()];
}

/** One bug's proposed classification, as the triager returned it. */
export type TriageProposal = {
  bug: string;
  severity?: string;
  component?: string;
  reason?: string;
  task_title?: string;
  suspected_files?: string[];
  duplicate_of?: string;
  reproducible?: boolean;
};

/**
 * What a triage run is asking to be accepted.
 *
 * The proposals were written to the event stream in full — severity, reason,
 * suspected files — and nothing rendered them. The run paused at its human gate
 * offering Accept and Reject with the thing being decided nowhere on screen.
 *
 * The last proposal for a bug wins: a re-run triages the same bug again, and
 * the newer answer is the one on the table.
 */
export function buildTriage(events: readonly DucklabEvent[]): TriageProposal[] {
  const byBug = new Map<string, TriageProposal>();
  for (const e of events) {
    if (!e || e.type !== "triage") continue;
    const d = (e.data ?? {}) as Record<string, unknown>;
    const bug = String(d.bug ?? "");
    if (!bug) continue;
    byBug.set(bug, { ...(d as unknown as TriageProposal), bug });
  }
  return [...byBug.values()];
}

/** A contract turn's answer, said the way a person would say it.
 *
 * The advisor, the judge and the triager answer in JSON — that is the
 * contract — and the lane rendered the blob: a folded advisor turn read
 * `{"action":"note","note":"Stop assuming…"}` where a human would write
 * "note — Stop assuming…". The engine has already parsed these; the view
 * re-parses only to display, and anything that does not parse stays as the
 * text it was. */
export type HumanisedContract = {
  /** One or two words for the header chip: "note", "stop", "chose A". */
  label: string;
  /** The chip's tone. */
  tone: "good" | "warning" | "serious" | "muted";
  /** The prose a person actually wants to read. */
  body: string;
};

function contractObject(text: string): Record<string, unknown> | null {
  const trimmed = text.trim();
  const start = trimmed.indexOf("{");
  const end = trimmed.lastIndexOf("}");
  if (start < 0 || end <= start) return null;
  try {
    const parsed: unknown = JSON.parse(trimmed.slice(start, end + 1));
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) return parsed as Record<string, unknown>;
  } catch {
    return null;
  }
  return null;
}

export function humaniseContract(role: string, text: string | undefined): HumanisedContract | null {
  if (!text) return null;
  const o = contractObject(text);
  if (!o) return null;
  if (role === "advisor" && typeof o.action === "string") {
    const note = typeof o.note === "string" ? o.note : "";
    if (o.action === "none") return { label: "no concerns", tone: "muted", body: note };
    if (o.action === "note") return { label: "note — back to work", tone: "warning", body: note };
    if (o.action === "stop") return { label: "stop", tone: "serious", body: note };
    return null;
  }
  if (role === "judge" && typeof o.choice === "string") {
    return { label: `chose ${o.choice}`, tone: "good", body: typeof o.reason === "string" ? o.reason : "" };
  }
  if (role === "triager" && typeof o.severity === "string") {
    const parts = [o.severity as string];
    parts.push(o.duplicate_of ? `duplicate of ${String(o.duplicate_of)}` : "not a duplicate");
    if (typeof o.component === "string" && o.component) parts.push(o.component);
    return { label: parts[0]!, tone: "muted", body: `${parts.slice(1).join(" · ")}${typeof o.reason === "string" && o.reason ? ` — ${o.reason}` : ""}` };
  }
  return null;
}
