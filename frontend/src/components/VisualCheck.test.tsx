import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Run } from "../api/client";
import { VisualCheck } from "./VisualCheck";
import { EvidenceDrawer } from "./EvidenceDrawer";

const base: Run = {
  id: "r-v", project_id: "calc", stage: "build", mode: "solo", task_id: "T-003",
  status: "paused", verdict: "FAILED", started_at: "2026-10-01T00:00:00Z",
  captures: ["scene-01.png"],
};

const failing: Run = {
  ...base,
  visual: {
    enforcement: "required", passed: false,
    results: [{
      capture: "scene-01.png", reference: "REF-IMG-1a2b3c4d",
      reference_capture: "visual-ref-scene-01.png", diff_capture: "visual-diff-scene-01.png",
      mismatch: 0.083, tolerance: 0.02, threshold: 0.1, width: 1440, height: 900, scaled_from: "390x844", passed: false,
    }],
  },
};

// B-460: the person deciding sees what the gate saw, side by side, in words.
describe("VisualCheck", () => {
  it("shows reference, capture and difference with the measure and the allowance", async () => {
    const captureClient = { runCaptureUrl: vi.fn(async (_id: string, name: string) => `blob:${name}`) };
    render(<VisualCheck run={failing} captureClient={captureClient} />);
    expect(screen.getByTestId("visual-check")).toHaveTextContent("the run fails");
    expect(screen.getByTestId("visual-measure")).toHaveTextContent("8.3% of the pixels differ (allowed 2.0%)");
    expect(screen.getByTestId("visual-check")).toHaveTextContent("390x844 px and was scaled to the capture's 1440x900");
    await vi.waitFor(() => expect(screen.getByTestId("visual-diff")).toHaveAttribute("src", "blob:visual-diff-scene-01.png"));
    expect(screen.getByTestId("visual-reference")).toHaveAttribute("src", "blob:visual-ref-scene-01.png");
    expect(screen.getByTestId("visual-capture")).toHaveAttribute("src", "blob:scene-01.png");
    // A run view column is narrow: the comparison opens large on request.
    fireEvent.click(screen.getByTestId("visual-view-large"));
    expect(screen.getByTestId("visual-large")).toHaveTextContent("8.3% of the pixels differ");
    expect(screen.getByTestId("visual-large").querySelectorAll("img")).toHaveLength(3);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("visual-large")).toBeNull();
  });

  it("says a diagnostic mismatch does not fail the run, and names a comparison that could not run", () => {
    render(<VisualCheck run={{ ...failing, visual: { enforcement: "diagnostic", passed: false, results: [{ capture: "scene-02.png", reference: "ref.png", mismatch: 0, tolerance: 0.02, threshold: 0.1, passed: false, error: "the capture command produced no scene-02.png" }] } }} captureClient={{ runCaptureUrl: vi.fn(async () => "blob:x") }} />);
    expect(screen.getByTestId("visual-check")).toHaveTextContent("(caveat)");
    expect(screen.getByTestId("visual-check")).toHaveTextContent("does not fail the run");
    expect(screen.getByTestId("visual-row-scene-02.png")).toHaveTextContent("not compared: the capture command produced no scene-02.png");
  });

  it("renders nothing without a visual gate, and appears in the evidence drawer with one", () => {
    const { container } = render(<VisualCheck run={base} />);
    expect(container).toBeEmptyDOMElement();
    render(<EvidenceDrawer run={failing} captureClient={{ runCaptureUrl: vi.fn(async (_i: string, n: string) => `blob:${n}`) }} onClose={() => {}} />);
    expect(screen.getByRole("region", { name: "Visual check" })).toBeInTheDocument();
  });
});
