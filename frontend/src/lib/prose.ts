/**
 * The narrow slice of markdown that artifact bodies actually use.
 *
 * Artifacts are markdown documents, so rendering their bodies as plain text
 * puts literal `**Assumption:**` and `- ` on screen — the view's whole job is
 * to make a document readable, and that fails it.
 *
 * This is deliberately not a markdown parser. Models write bullets, bold and
 * inline code inside a section body; headings and links belong to the artifact
 * structure the engine already parsed. Pulling in a full parser to render
 * three constructs would add a dependency whose main new capability is
 * executing HTML we did not write.
 */

export type Block =
  | { kind: "para"; spans: Span[] }
  | { kind: "list"; items: Span[][] };

export type Span =
  | { kind: "text"; text: string }
  | { kind: "strong"; text: string }
  | { kind: "code"; text: string };

const BULLET = /^\s*[-*+]\s+/;

export function parseProse(body: string): Block[] {
  const blocks: Block[] = [];
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

  for (const line of body.split("\n")) {
    if (BULLET.test(line)) {
      flushPara();
      items ??= [];
      items.push(parseSpans(line.replace(BULLET, "")));
      continue;
    }
    if (line.trim() === "") {
      flushPara();
      flushList();
      continue;
    }
    // A non-bullet line right after a list is a continuation of the last item,
    // not a new paragraph — models wrap long bullets.
    if (items && /^\s+\S/.test(line)) {
      const last = items[items.length - 1];
      if (last) last.push({ kind: "text", text: " " + line.trim() });
      continue;
    }
    flushList();
    para.push(line.trim());
  }
  flushPara();
  flushList();
  return blocks;
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
