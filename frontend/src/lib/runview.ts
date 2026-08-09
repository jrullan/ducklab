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
          target: toolTarget(d),
          // A failed call's result says WHY — "search text not found",
          // "path is not a test file" — and it was recorded and never shown:
          // the ✕ expanded to nothing.
          detail: d.ok === false ? String(d.result ?? "") || undefined : undefined,
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

/** The reviewer's standing objection, when its LAST word was not approval.
 *
 * The gate decides the verdict (I2) and the reviewer advises — so a pair can
 * end green with the reviewer still requesting changes, rounds exhausted.
 * That is a legitimate state and exactly what the human gate is for; what it
 * must not be is silent. T-028 ended "tests passed" over three consecutive
 * request-changes verdicts, and the person learned it only by reading the
 * whole transcript. */
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

/** The pending human interaction, if the run is waiting on one. */
export function buildPending(events: readonly DucklabEvent[]): PendingHuman | null {
  let latest: DucklabEvent | null = null;
  for (const e of events) {
    if (e.type === "human_needed") latest = e;
    // A human action clears the wait — and so does a checkpoint: resume
    // appends one, and a budget pause that was lifted and resumed went on
    // saying "waiting for you" over a run that was already working again.
    if (e.type === "human" || e.type === "run_end" || e.type === "checkpoint") latest = null;
  }
  if (!latest) return null;
  const d = latest.data ?? {};
  return {
    kind: String(d.kind ?? "gate"),
    question: d.question ? String(d.question) : undefined,
    questionId: d.question_id ? String(d.question_id) : undefined,
    verdict: d.verdict ? String(d.verdict) : undefined,
    detail: d.detail ? String(d.detail) : undefined,
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
export function runLabel(run: { task_id?: string; stage?: string; id: string }): string {
  return run.task_id || run.stage || run.id;
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
