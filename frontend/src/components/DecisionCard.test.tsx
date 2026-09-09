import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { DecisionCard } from "./DecisionCard";

describe("DecisionCard document gates", () => {
  it("makes request changes primary and names discard as the destructive exit", () => {
    render(
      <DecisionCard
        next={["accept", "request_changes", "reject"]}
        title="Proposal awaiting your decision"
        consequence="replaces the approved spec"
        documentGate
        onAccept={() => {}}
        onReject={() => {}}
        onRequestChanges={async () => {}}
      />,
    );

    expect(screen.getByTestId("request-changes-button")).toHaveTextContent("Request changes");
    expect(screen.getByTestId("request-changes-button").className).toContain("bg-good");
    expect(screen.getByTestId("reject-button")).toHaveTextContent("Discard draft");
  });
});

// B-261: one decision card per gate state. The reviewer's dissent, the
// competing "accept, then fix" and the findings filing are parts of the same
// decision and render inside it, each with its consequence stated. B-247: a
// task whose work already landed says so where the accept is offered.
describe("DecisionCard composes the whole gate decision", () => {
  const dissent = { verdict: "request-changes", findings: 2, notes: ["wrong week boundary (app.py:12) — fix: use ISO weeks", "missing null check"] };

  it("carries the dissent and states that accept-then-fix launches a run, with its cost", () => {
    const onClick = vi.fn();
    render(
      <DecisionCard
        next={["accept", "reject"]}
        title="Waiting for your decision"
        consequence="commits the diff locally without publishing it"
        onAccept={() => {}}
        onReject={() => {}}
        dissent={dissent}
        acceptAndFix={{ busy: false, mode: "pair", spent: "$0.31", onClick }}
      />,
    );
    const card = screen.getByTestId("decision-card");
    expect(within(card).getByTestId("decision-dissent").textContent).toContain("request-changes");
    expect(within(card).getByTestId("dissent-findings-list").textContent).toContain("ISO weeks");
    expect(within(card).getByTestId("accept-and-fix-launches").textContent).toBe("launches a new run");
    const consequence = within(card).getByTestId("accept-and-fix-consequence").textContent!;
    expect(consequence).toContain("commits the diff locally");
    expect(consequence).toContain("starts a new pair run");
    expect(consequence).toContain("2 findings");
    expect(consequence).toContain("$0.31 already spent stays on the record");
    fireEvent.click(within(card).getByTestId("accept-and-fix"));
    expect(onClick).toHaveBeenCalled();
    // The plain Accept is still there, and still says only what it does.
    expect(within(card).getByTestId("cycle-accept")).toBeTruthy();
    expect(within(card).getByTestId("decision-consequence").textContent).not.toContain("launches");
  });

  it("does not offer accept-then-fix when the engine offers no accept", () => {
    render(
      <DecisionCard next={["reject"]} title="t" consequence="nothing passed" onAccept={() => {}} onReject={() => {}}
        dissent={dissent} acceptAndFix={{ busy: false, mode: "pair", onClick: () => {} }} />,
    );
    expect(screen.queryByTestId("accept-and-fix")).toBeNull();
    expect(screen.getByTestId("decision-dissent")).toBeTruthy();
  });

  it("files the findings from inside the decision and shows where they went", () => {
    const onFile = vi.fn();
    const { rerender } = render(
      <DecisionCard next={["accept", "reject"]} title="t" consequence="commits the diff" onAccept={() => {}} onReject={() => {}}
        fileFindings={{ items: [{ severity: "major", issue: "wrong week boundary", file: "app.py", line: 12 }], filed: null, busy: false, onFile, boardHref: "#/board?tab=bugs" }} />,
    );
    const card = screen.getByTestId("decision-card");
    expect(within(card).getByTestId("file-findings-list").textContent).toContain("wrong week boundary");
    expect(within(card).getByTestId("file-findings").textContent).toContain("Filing does not decide this run");
    fireEvent.click(within(card).getByTestId("file-findings-button"));
    expect(onFile).toHaveBeenCalled();
    rerender(
      <DecisionCard next={["accept", "reject"]} title="t" consequence="commits the diff" onAccept={() => {}} onReject={() => {}}
        fileFindings={{ items: [{ issue: "wrong week boundary" }], filed: ["B-9"], busy: false, onFile, boardHref: "#/board?tab=bugs" }} />,
    );
    expect(screen.getByTestId("file-findings-done").textContent).toContain("B-9");
    expect(screen.queryByTestId("file-findings-button")).toBeNull();
  });

  it("says when the task already landed, and stays silent on a first run", () => {
    const { rerender } = render(
      <DecisionCard next={["accept", "reject"]} title="t" consequence="commits the diff" onAccept={() => {}} onReject={() => {}} landedAs="b0c33af9deadbeef" />,
    );
    expect(screen.getByTestId("landed-notice").textContent).toContain("already landed as b0c33af");
    expect(screen.getByTestId("landed-notice").textContent).toContain("second change on top");
    rerender(<DecisionCard next={["accept", "reject"]} title="t" consequence="commits the diff" onAccept={() => {}} onReject={() => {}} />);
    expect(screen.queryByTestId("landed-notice")).toBeNull();
  });
});
