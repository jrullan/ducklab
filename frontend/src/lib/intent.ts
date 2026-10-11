/**
 * An intent entry read as a chain: the person's original brief and every
 * revision of the proposal it produced (B-521).
 *
 * The TI-36X journal recorded three revisions of one intake as INT-003..005,
 * each a reviewer-findings blob titled "- [major] REQ-008 invents…", and
 * closed the person's INT-002 as superseded with no requirements. The engine
 * now records a revision under the intent it revises; this module reads that
 * shape back for the Documents view. The headings are the engine's
 * (internal/artifact/intent.go) — keep them in step.
 */

export interface IntentRevision {
  number: number;
  run: string;
  submittedAt?: string;
  /** The person's own words; empty when they forwarded findings alone. */
  note: string;
  /** Reviewer findings carried over — never the person's words. */
  findings: string;
}

export interface IntentChain {
  run?: string;
  submittedAt?: string;
  outcome?: string;
  requirements: string[];
  /** The original brief. For an entry written before B-521, the whole body
   * after the header when it has no "Original brief" heading. */
  brief: string;
  /** True for an entry imported from a historical run record. */
  imported: boolean;
  revisions: IntentRevision[];
}

const REVISION = /^###\s+Revision\s+(\d+)\s+[—-]\s+(\S+)/;
const NOTE_HEADING = "#### The person's note";
const FINDINGS_HEADING = "#### Reviewer findings carried over";

function field(header: string, label: string): string | undefined {
  const match = header.match(new RegExp(`^\\s*\\*\\*${label}:\\*\\*\\s*(.*)$`, "im"));
  const value = match?.[1]?.trim();
  return value ? value : undefined;
}

export function parseIntentChain(body: string): IntentChain {
  const lines = body.split("\n");
  const head = { heading: "", lines: [] as string[] };
  const blocks: { heading: string; lines: string[] }[] = [head];
  let current = head;
  for (const line of lines) {
    if (/^###\s+/.test(line) && !/^####/.test(line)) {
      current = { heading: line.trim(), lines: [] };
      blocks.push(current);
    } else {
      current.lines.push(line);
    }
  }
  const header = head.lines.join("\n");
  const requirements = (field(header, "Requirements") ?? "")
    .split(",")
    .map((id) => id.trim())
    .filter((id) => id && id !== "-");
  const chain: IntentChain = {
    run: field(header, "Run"),
    submittedAt: field(header, "Submitted at"),
    outcome: field(header, "Outcome"),
    requirements,
    brief: "",
    imported: /Imported from the historical run record/.test(header),
    revisions: [],
  };
  let sawBrief = false;
  for (const block of blocks.slice(1)) {
    const revision = block.heading.match(REVISION);
    if (revision) {
      chain.revisions.push(parseRevision(Number(revision[1]), revision[2] ?? "", block.lines));
    } else if (/^###\s+Original brief/i.test(block.heading)) {
      chain.brief = block.lines.join("\n").trim();
      sawBrief = true;
    } else if (!sawBrief) {
      chain.brief = [chain.brief, block.heading, ...block.lines].join("\n").trim();
    }
  }
  if (!sawBrief && !chain.brief) {
    chain.brief = header
      .split("\n")
      .filter((line) => !/^\s*\*\*[^*]+:\*\*/.test(line) && !/Imported from the historical run record/.test(line))
      .join("\n")
      .trim();
  }
  return chain;
}

function parseRevision(number: number, run: string, lines: string[]): IntentRevision {
  const out: IntentRevision = { number, run, note: "", findings: "" };
  let into: "note" | "findings" | null = null;
  const note: string[] = [];
  const findings: string[] = [];
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed === NOTE_HEADING) {
      into = "note";
      continue;
    }
    if (trimmed === FINDINGS_HEADING) {
      into = "findings";
      continue;
    }
    if (into === null) {
      const submitted = trimmed.match(/^Submitted at (.+?)\.?$/);
      if (submitted) out.submittedAt = submitted[1];
      continue;
    }
    (into === "note" ? note : findings).push(line);
  }
  out.note = note.join("\n").trim();
  out.findings = findings.join("\n").trim();
  return out;
}
