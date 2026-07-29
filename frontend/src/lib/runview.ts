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
}

export interface TurnBlock {
  key: string;
  round: number;
  turn: number;
  role: string;
  duckling: string;
  /** Anonymous label used instead of `duckling` when the turn is hidden. */
  label?: string;
  toolCalls: ToolCall[];
  text: string;
  done: boolean;
  /** What this turn was working on: a split's subtask, a tournament's
   * contestant slot. Two lanes with the same role and the same duckling are
   * otherwise indistinguishable, which is exactly what a split produces. */
  subject?: string;
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
}

/** One thing a reviewer objected to. */
export interface Finding {
  severity: string;
  file?: string;
  line?: number;
  issue: string;
  fix?: string;
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

  const keyFor = (round: number, turn: number) => `${round}:${turn}`;

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
        if (b) b.done = true;
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
        if (b) {
          b.text = content;
          b.verdict = verdict;
          b.findings = findings;
        } else {
          // A message with no turn of its own still belongs in the lane rather
          // than being dropped on the floor.
          blocks.push({
            key: keyFor(Number(d.round ?? 1), Number(d.turn ?? 0)),
            round: Number(d.round ?? 1),
            turn: Number(d.turn ?? 0),
            role: String(d.role ?? ""),
            duckling: String(d.duckling ?? ""),
            toolCalls: [],
            text: content,
            done: true,
            verdict,
            findings,
          });
        }
        break;
      }
      case "tool_call": {
        blockFor(d)?.toolCalls.push({
          seq: e.seq ?? 0,
          tool: String(d.tool ?? "?"),
          ok: d.ok !== false,
          ms: typeof d.ms === "number" ? d.ms : undefined,
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
      });
    } else if (e.type === "policy_violation") {
      out.push({ seq: e.seq ?? 0, tool: String(d.tool ?? "?"), ok: false, violation: true });
    }
  }
  return out;
}

/** Groups tools into families so the timeline can colour them. */
export function toolFamily(tool: string): "read" | "write" | "exec" | "vcs" | "other" {
  if (tool.startsWith("fs_read") || tool === "fs_list" || tool === "fs_search") return "read";
  if (tool === "fs_write" || tool === "fs_patch" || tool === "fs_delete") return "write";
  if (tool === "shell" || tool === "verify_run") return "exec";
  if (tool.startsWith("git_")) return "vcs";
  return "other";
}

/**
 * Derives the gate card state.
 *
 * A `none` gate can never read as success (P3): nothing was executed, and the
 * label says so rather than showing a neutral tick a user would read as green.
 */
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

  if (gate === "none") {
    return {
      gate, exitCode: exit, cmd,
      role: "warning",
      label: "unverified — nothing executable to run",
      unverified: true,
    };
  }
  const green = exit === 0;
  return {
    gate, exitCode: exit, cmd,
    role: green ? "good" : "critical",
    label: green ? `${gate} passed` : `${gate} failed`,
    unverified: false,
  };
}

/** The pending human interaction, if the run is waiting on one. */
export function buildPending(events: readonly DucklabEvent[]): PendingHuman | null {
  let latest: DucklabEvent | null = null;
  for (const e of events) {
    if (e.type === "human_needed") latest = e;
    // A human action clears the wait.
    if (e.type === "human" || e.type === "run_end") latest = null;
  }
  if (!latest) return null;
  const d = latest.data ?? {};
  return {
    kind: String(d.kind ?? "gate"),
    question: d.question ? String(d.question) : undefined,
    questionId: d.question_id ? String(d.question_id) : undefined,
    verdict: d.verdict ? String(d.verdict) : undefined,
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
export function runLabel(run: { task_id?: string; stage?: string; id: string }): string {
  return run.task_id || run.stage || run.id;
}
