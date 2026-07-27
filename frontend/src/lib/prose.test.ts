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
