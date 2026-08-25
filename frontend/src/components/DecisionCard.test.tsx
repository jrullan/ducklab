import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
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
