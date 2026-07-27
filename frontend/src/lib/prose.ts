/**
 * The slice of markdown that artifact bodies actually use.
 *
 * Artifacts are markdown documents, so rendering their bodies as plain text
 * puts literal `**Assumption:**`, `### Clients` and pipe-delimited table rows
 * on screen. The view's whole job is to make a document readable, and that
 * fails it.
 *
 * This is deliberately not a full markdown parser. It handles what the
 * architect writes inside a section — paragraphs, bullets, sub-headings,
 * tables, rules, bold and inline code — and nothing else. Pulling in a real
 * parser to cover those would add a dependency whose main new capability is
 * executing HTML we did not write.
 */

export type Block =
  | { kind: "para"; spans: Span[] }
  | { kind: "list"; items: Span[][] }
  | { kind: "heading"; level: number; spans: Span[] }
  | { kind: "table"; head: Span[][]; rows: Span[][][] }
  | { kind: "rule" };

export type Span =
  | { kind: "text"; text: string }
  | { kind: "strong"; text: string }
  | { kind: "code"; text: string };

const BULLET = /^\s*[-*+]\s+/;
const HEADING = /^\s*(#{1,6})\s+(.*)$/;
const RULE = /^\s*([-*_])\1{2,}\s*$/;
const TABLE_ROW = /^\s*\|.*\|\s*$/;
const TABLE_SEP = /^\s*\|[\s:|-]+\|\s*$/;

/**
 * parseProse turns a section body into blocks.
 *
 * suppress names field keys whose lines the caller renders itself. Without it
 * a spec section showed its Implements edge twice — once as the view's own
 * traceability line and again as raw prose two lines below.
 */
export function parseProse(body: string, suppress: string[] = ["implements", "depends on"]): Block[] {
  const blocks: Block[] = [];
  const lines = body.split("\n");
  let para: string[] = [];
  let items: Span[][] | null = null;

  const flushPara = () => {
    if (para.length) {
      blocks.push({ kind: "para", spans: parseSpans(para.join(" ")) });
      para = [];
    }
  };
  const flushList = () => {
    if (items) {
      blocks.push({ kind: "list", items });
      items = null;
    }
  };
  const flushAll = () => {
    flushPara();
    flushList();
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;

    if (isSuppressedField(line, suppress)) continue;

    const rule = RULE.test(line);
    if (rule) {
      flushAll();
      blocks.push({ kind: "rule" });
      continue;
    }

    const heading = HEADING.exec(line);
    if (heading) {
      flushAll();
      blocks.push({
        kind: "heading",
        level: heading[1]!.length,
        spans: parseSpans(heading[2]!),
      });
      continue;
    }

    if (TABLE_ROW.test(line)) {
      flushAll();
      const rows: string[] = [];
      while (i < lines.length && TABLE_ROW.test(lines[i]!)) rows.push(lines[i++]!);
      i--;
      blocks.push(parseTable(rows));
      continue;
    }

    if (BULLET.test(line)) {
      flushPara();
      items ??= [];
      items.push(parseSpans(line.replace(BULLET, "")));
      continue;
    }
    if (line.trim() === "") {
      flushAll();
      continue;
    }
    // A non-bullet line right after a list is a continuation of the last item,
    // not a new paragraph — models wrap long bullets.
    if (items && /^\s+\S/.test(line)) {
      items[items.length - 1]?.push({ kind: "text", text: " " + line.trim() });
      continue;
    }
    flushList();
    para.push(line.trim());
  }
  flushAll();
  return blocks;
}

function isSuppressedField(line: string, suppress: string[]): boolean {
  const m = /^\s*\*?\*?([A-Za-z][A-Za-z ]{0,23}):/.exec(line);
  if (!m) return false;
  return suppress.includes(m[1]!.trim().toLowerCase());
}

function parseTable(rows: string[]): Block {
  const cells = (row: string) =>
    row
      .trim()
      .replace(/^\|/, "")
      .replace(/\|$/, "")
      .split("|")
      .map((c) => parseSpans(c.trim()));

  const body = rows.filter((r) => !TABLE_SEP.test(r));
  const [head, ...rest] = body;
  return { kind: "table", head: head ? cells(head) : [], rows: rest.map(cells) };
}

const SPAN = /(\*\*[^*]+\*\*|`[^`]+`)/g;

export function parseSpans(text: string): Span[] {
  const out: Span[] = [];
  let last = 0;
  for (const m of text.matchAll(SPAN)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ kind: "text", text: text.slice(last, at) });
    const tok = m[0];
    if (tok.startsWith("**")) out.push({ kind: "strong", text: tok.slice(2, -2) });
    else out.push({ kind: "code", text: tok.slice(1, -1) });
    last = at + tok.length;
  }
  if (last < text.length) out.push({ kind: "text", text: text.slice(last) });
  return out;
}
