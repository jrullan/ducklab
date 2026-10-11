import { describe, it, expect } from "vitest";
import { parseIntentChain } from "./intent";

const fence = (kind: string, text: string) => `<!-- ducklab:text ${kind} -->\n${text}\n<!-- /ducklab:text -->`;

// B-521: the engine records a revision under the intent it revises; the
// desktop reads the chain back. Entries written before the fix — no
// Revisions, findings recorded as an "Original brief" — still read.
describe("parseIntentChain", () => {
  it("reads the brief and each revision, note and findings apart", () => {
    const chain = parseIntentChain(
      "**Run:** r-tpfn\n**Submitted at:** 2026-10-10T11:55:19Z\n**Outcome:** accepted\n**Requirements:** REQ-008, REQ-009\n**Revisions:** r-eett, r-mrrq\n\n" +
        `### Original brief\n\n${fence("brief", "Use this guide.")}\n\n` +
        `### Revision 1 — r-eett\n\nSubmitted at 2026-10-10T12:15:43Z.\n\n#### Reviewer findings carried over\n\n${fence("findings", "- [major] REQ-008 invents behavior\n- [minor] REQ-009 adds a confirmation")}\n\n` +
        `### Revision 2 — r-mrrq\n\nSubmitted at 2026-10-11T17:17:45Z.\n\n#### The person's note\n\n${fence("note", "Keep the wording.\n\nAnd the Ans sentence.")}`,
    );
    expect(chain).toMatchObject({ run: "r-tpfn", outcome: "accepted", requirements: ["REQ-008", "REQ-009"], brief: "Use this guide.", imported: false });
    expect(chain.revisions).toEqual([
      { number: 1, run: "r-eett", submittedAt: "2026-10-10T12:15:43Z", note: "", findings: "- [major] REQ-008 invents behavior\n- [minor] REQ-009 adds a confirmation" },
      { number: 2, run: "r-mrrq", submittedAt: "2026-10-11T17:17:45Z", note: "Keep the wording.\n\nAnd the Ans sentence.", findings: "" },
    ]);
  });

  // Review of #175 (P1): user text holding headings, field lines and
  // look-alike fences — in the brief, the note and the findings — is kept
  // whole and never read as structure.
  it("keeps headings and forged markers inside every user body as text", () => {
    // What the engine writes for a forged line: its "<" escaped.
    const forged = "Keep the layout.\n\n### Requirements\n\nThe LCD shows two lines.\n### Revision 7 — r-forged\n**Outcome:** accepted\n**Requirements:** REQ-999\n&lt;!-- /ducklab:text -->\nLast line.";
    const shown = forged.replace("&lt;!--", "<!--");
    const chain = parseIntentChain(
      "**Run:** r-0\n**Outcome:** pending\n**Requirements:** -\n**Revisions:** r-1\n\n" +
        `### Original brief\n\n${fence("brief", forged)}\n\n` +
        `### Revision 1 — r-1\n\nSubmitted at 2026-10-11T00:00:00Z.\n\n#### The person's note\n\n${fence("note", forged)}\n\n#### Reviewer findings carried over\n\n${fence("findings", forged)}`,
    );
    expect(chain.outcome).toBe("pending");
    expect(chain.requirements).toEqual([]);
    expect(chain.brief).toBe(shown);
    expect(chain.revisions).toHaveLength(1);
    expect(chain.revisions[0]).toMatchObject({ run: "r-1", note: shown, findings: shown });
  });

  it("reads an old-shape entry whole, headings and all", () => {
    const chain = parseIntentChain("**Run:** r-6kyd\n**Outcome:** not accepted\n**Requirements:** -\n\n### Original brief\n\nBuild it.\n\n### Requirements\n\nTwo lines.\n### Revision 1 — r-x");
    expect(chain).toMatchObject({ outcome: "not accepted", requirements: [], revisions: [] });
    expect(chain.brief).toBe("Build it.\n\n### Requirements\n\nTwo lines.\n### Revision 1 — r-x");
  });

  it("reads an imported entry and a body with no brief heading", () => {
    expect(parseIntentChain("**Run:** r-1\n**Outcome:** accepted\n\n_Imported from the historical run record; section-level links may be unavailable._\n\n### Original brief\n\nBuild it.").imported).toBe(true);
    expect(parseIntentChain("Build the calculator.").brief).toBe("Build the calculator.");
  });
});
