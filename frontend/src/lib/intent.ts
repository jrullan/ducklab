/**
 * An intent entry read as a chain: the person's original brief and every
 * revision of the proposal it produced (B-521).
 *
 * The TI-36X journal recorded three revisions of one intake as INT-003..005,
 * each a reviewer-findings blob titled "- [major] REQ-008 invents…", and
 * closed the person's INT-002 as superseded with no requirements. The engine
 * now records a revision under the intent it revises; this module reads that
 * shape back for the Documents view.
 *
 * Only engine-owned structure is parsed (review of #175). Every body a person
 * or reviewer wrote — the brief, a revision's note, the findings — sits
 * between `<!-- ducklab:text KIND -->` and `<!-- /ducklab:text -->`, which the
 * engine alone writes (a look-alike line inside user text arrives as
 * `&lt;!--`). Fenced lines are opaque: a brief containing `### Requirements`
 * is kept whole, and a line `### Revision 2 — r-x` inside a note is text,
 * not chain metadata. The fences and headings are the engine's
 * (internal/artifact/intent.go, intent_text.go) — keep them in step.
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
  /** The original brief. */
  brief: string;
  /** True for an entry imported from a historical run record. */
  imported: boolean;
  revisions: IntentRevision[];
}

const OPEN = /^<!-- ducklab:text (brief|note|findings) -->\s*$/;
const CLOSE = /^<!-- \/ducklab:text -->\s*$/;
const REVISION = /^###\s+Revision\s+(\d+)\s+[—-]\s+(\S+)\s*$/;
const FIELD = /^\s*\*\*([^*]+):\*\*\s*(.*)$/;

/** User text as written: the engine's neutralized look-alike fences shown
 * as the person typed them. */
function unescapeUserText(lines: string[]): string {
  return lines
    .map((line) => line.replace(/^(\s*)&lt;(!--\s*\/?\s*ducklab\s*:)/i, "$1<$2"))
    .join("\n")
    .trim();
}

export function parseIntentChain(body: string): IntentChain {
  const chain: IntentChain = { requirements: [], brief: "", imported: false, revisions: [] };
  const lines = body.split("\n");
  if (!lines.some((line) => OPEN.test(line))) return parseLegacy(lines, chain);

  let fenced: "brief" | "note" | "findings" | null = null;
  let text: string[] = [];
  let revision: IntentRevision | null = null;
  let inHeader = true;
  for (const line of lines) {
    if (fenced) {
      if (CLOSE.test(line)) {
        const value = unescapeUserText(text);
        if (fenced === "brief") chain.brief = value;
        else if (revision) revision[fenced] = value;
        fenced = null;
        text = [];
      } else {
        text.push(line);
      }
      continue;
    }
    const open = line.match(OPEN);
    if (open) {
      fenced = open[1] as "brief" | "note" | "findings";
      continue;
    }
    if (/^###\s/.test(line)) inHeader = false;
    const rev = line.match(REVISION);
    if (rev) {
      revision = { number: Number(rev[1]), run: rev[2] ?? "", note: "", findings: "" };
      chain.revisions.push(revision);
      continue;
    }
    if (inHeader) readHeaderLine(line, chain);
    else if (revision) {
      const submitted = line.trim().match(/^Submitted at (.+?)\.?$/);
      if (submitted) revision.submittedAt = submitted[1];
    }
  }
  return chain;
}

function readHeaderLine(line: string, chain: IntentChain) {
  if (/Imported from the historical run record/.test(line)) chain.imported = true;
  const field = line.match(FIELD);
  if (!field) return;
  const value = (field[2] ?? "").trim();
  switch ((field[1] ?? "").trim().toLowerCase()) {
    case "run":
      chain.run = value || undefined;
      break;
    case "submitted at":
      chain.submittedAt = value || undefined;
      break;
    case "outcome":
      chain.outcome = value || undefined;
      break;
    case "requirements":
      chain.requirements = value.split(",").map((id) => id.trim()).filter((id) => id && id !== "-");
      break;
  }
}

/** An entry written before the fences: the header, then everything after
 * "### Original brief" is the brief — whole, whatever headings it holds.
 * Such entries have no revisions. */
function parseLegacy(lines: string[], chain: IntentChain): IntentChain {
  const at = lines.findIndex((line) => /^###\s+Original brief\s*$/i.test(line));
  const header = at < 0 ? lines : lines.slice(0, at);
  for (const line of header) readHeaderLine(line, chain);
  chain.brief = at < 0
    ? header.filter((line) => !FIELD.test(line) && !/Imported from the historical run record/.test(line)).join("\n").trim()
    : lines.slice(at + 1).join("\n").trim();
  return chain;
}
