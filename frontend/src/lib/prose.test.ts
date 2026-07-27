import { describe, it, expect } from "vitest";
import { parseProse, parseSpans } from "./prose";

describe("parseSpans", () => {
  it("pulls out bold and inline code, keeping the text around them", () => {
    expect(parseSpans("An **Assumption:** about `add.go` here")).toEqual([
      { kind: "text", text: "An " },
      { kind: "strong", text: "Assumption:" },
      { kind: "text", text: " about " },
      { kind: "code", text: "add.go" },
      { kind: "text", text: " here" },
    ]);
  });

  it("leaves a lone asterisk alone rather than eating the rest of the line", () => {
    expect(parseSpans("2 * 3 = 6")).toEqual([{ kind: "text", text: "2 * 3 = 6" }]);
  });
});

describe("parseProse", () => {
  it("turns a bullet run into one list, not one list per line", () => {
    const blocks = parseProse("- Admins can create clients.\n- Admins can edit projects.");
    expect(blocks).toHaveLength(1);
    expect(blocks[0]!.kind).toBe("list");
    expect(blocks[0]!.kind === "list" && blocks[0]!.items).toHaveLength(2);
  });

  it("separates a paragraph that follows a list", () => {
    // This is the real shape of a requirement body: bullets, blank line, then
    // an assumption paragraph.
    const blocks = parseProse("- One\n- Two\n\n**Assumption:** it holds.");
    expect(blocks.map((b) => b.kind)).toEqual(["list", "para"]);
  });

  it("joins a wrapped bullet into the item it continues", () => {
    const blocks = parseProse("- A long bullet that\n    wraps onto a second line");
    expect(blocks).toHaveLength(1);
    const items = blocks[0]!.kind === "list" ? blocks[0]!.items : [];
    expect(items).toHaveLength(1);
    expect(items[0]!.map((s) => s.text).join("")).toContain("wraps onto a second line");
  });

  it("keeps consecutive prose lines in one paragraph", () => {
    const blocks = parseProse("A responsive web app\nfor a consultancy.");
    expect(blocks).toHaveLength(1);
    expect(blocks[0]!.kind === "para" && blocks[0]!.spans[0]!.text).toBe(
      "A responsive web app for a consultancy.",
    );
  });
});

describe("parseProse, the constructs a spec actually contains", () => {
  it("renders a sub-heading as a heading, not as literal hashes", () => {
    const blocks = parseProse("### Clients\n\n- Admins can create clients.");
    expect(blocks[0]).toEqual({
      kind: "heading",
      level: 3,
      spans: [{ kind: "text", text: "Clients" }],
    });
  });

  it("parses a table instead of spilling its pipes into a paragraph", () => {
    const blocks = parseProse(
      "| Role | Permissions |\n|------|-------------|\n| **Empleado** | Log hours |\n| **Gerente** | Approve |",
    );
    expect(blocks).toHaveLength(1);
    const t = blocks[0]!;
    expect(t.kind).toBe("table");
    if (t.kind !== "table") return;
    expect(t.head.map((c) => c.map((s) => s.text).join(""))).toEqual(["Role", "Permissions"]);
    expect(t.rows).toHaveLength(2); // the |---| separator is not a row
    expect(t.rows[0]![0]![0]).toEqual({ kind: "strong", text: "Empleado" });
  });

  it("treats --- as a rule rather than printing it", () => {
    const blocks = parseProse("Some prose.\n\n---\n");
    expect(blocks.map((b) => b.kind)).toEqual(["para", "rule"]);
  });

  // The view renders the traceability edge itself, so leaving it in the prose
  // showed every spec section's Implements twice.
  it("drops field lines the view renders separately", () => {
    const blocks = parseProse("**Implements:** REQ-001, REQ-007\n\nA single-tenant app.");
    expect(blocks).toHaveLength(1);
    expect(blocks[0]!.kind === "para" && blocks[0]!.spans[0]!.text).toBe("A single-tenant app.");
  });

  it("keeps field lines the view does not render, like Assumption", () => {
    const blocks = parseProse("**Assumption:** single tenant.");
    expect(blocks).toHaveLength(1);
    expect(blocks[0]!.kind === "para" && blocks[0]!.spans[0]).toEqual({
      kind: "strong",
      text: "Assumption:",
    });
  });
});

// The text protocol carries tool calls inside ```ducklab fences. Reformatting
// their contents would rewrite what a duckling actually said.
describe("parseProse and fenced blocks", () => {
  it("keeps a fenced block verbatim, with its language", () => {
    const blocks = parseProse('Doing it.\n\n```ducklab\n{"tool": "fs_read"}\n```\n\nDone.');
    expect(blocks.map((b) => b.kind)).toEqual(["para", "code", "para"]);
    const code = blocks[1]!;
    expect(code.kind === "code" && code.lang).toBe("ducklab");
    expect(code.kind === "code" && code.text).toBe('{"tool": "fs_read"}');
  });

  it("does not treat markdown inside a fence as markdown", () => {
    const blocks = parseProse("```\n**not bold** and - not a bullet\n```");
    expect(blocks).toHaveLength(1);
    expect(blocks[0]!.kind === "code" && blocks[0]!.text).toBe("**not bold** and - not a bullet");
  });
});

// Two trailing spaces are markdown's hard line break. Models use it to
// separate labelled points, and joining them ran three statements together.
describe("parseProse and hard line breaks", () => {
  it("keeps lines the model broke on purpose apart", () => {
    const blocks = parseProse(
      "**Changed:** add.go  \n**Why:** it was wrong  \n**Did not do:** anything else",
    );
    expect(blocks).toHaveLength(3);
    expect(blocks.every((b) => b.kind === "para")).toBe(true);
  });

  it("still joins ordinary wrapped prose", () => {
    const blocks = parseProse("A responsive web app\nfor a consultancy.");
    expect(blocks).toHaveLength(1);
  });
});
