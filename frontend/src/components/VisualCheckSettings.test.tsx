import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { EngineClient, VisualCheckView } from "../api/client";
import { VisualCheckSettings } from "./VisualCheckSettings";

vi.mock("../lib/picker", () => ({
  canChooseFile: () => true,
  chooseFile: vi.fn(async () => "/home/ada/ti36x-front.png"),
  canChooseDirectory: () => false,
  chooseDirectory: vi.fn(async () => null),
}));

const fresh: VisualCheckView = {
  configured: false, command: "", effective_command: "", artifacts: "", enforcement: "diagnostic", compare: [],
  recent_captures: ["scene-01.png", "scene-02.png"], recent_run_id: "r-1",
  references: [{ id: "REF-IMG-1a2b3c4d", stored: ".ducklab/refs/images/1a2b3c4d5e6f.png", bytes: 10, width: 390, height: 844 }],
};

function fakeClient(view = fresh) {
  return {
    visualCheck: vi.fn(async () => view),
    visualCheckSet: vi.fn(async (_id: string, req: { command: string; enforcement: string; compare: unknown[] }) => ({
      ...view, configured: true, command: req.command, enforcement: req.enforcement, compare: req.compare,
    })),
    referenceImport: vi.fn(async () => ({ id: "REF-IMG-99887766", stored: ".ducklab/refs/images/998877665544.png", bytes: 5 })),
    referenceImageUrl: vi.fn(async (_p: string, ref: string) => `blob:${ref}`),
  };
}

// B-460 part 2: the person sets up the visual gate by choosing, not typing ids.
describe("VisualCheckSettings", () => {
  it("offers the last captures and the stored references, and saves what was chosen", async () => {
    const client = fakeClient();
    render(<VisualCheckSettings client={client as unknown as EngineClient} projectId="calc" />);
    await waitFor(() => expect(screen.getByTestId("visual-settings-summary")).toHaveTextContent("not set up"));
    fireEvent.click(screen.getByTestId("visual-settings-edit"));
    // Prefilled from the record: the first capture and the first reference.
    expect(screen.getByTestId("visual-capture-0")).toHaveValue("scene-01.png");
    expect(screen.getByTestId("visual-ref-0-REF-IMG-1a2b3c4d")).toHaveAttribute("aria-checked", "true");
    await waitFor(() => expect(screen.getByAltText("REF-IMG-1a2b3c4d")).toHaveAttribute("src", "blob:REF-IMG-1a2b3c4d"));
    // No command yet: a comparison cannot be saved, and the form says why.
    expect(screen.getByTestId("visual-save")).toBeDisabled();
    expect(screen.getByTestId("visual-editor")).toHaveTextContent("needs a capture command");
    fireEvent.change(screen.getByTestId("visual-command"), { target: { value: "node capture.mjs" } });
    fireEvent.click(screen.getByTestId("visual-enf-required"));
    fireEvent.change(screen.getByTestId("visual-tolerance-0"), { target: { value: "3.5" } });
    fireEvent.click(screen.getByTestId("visual-save"));
    await waitFor(() => expect(client.visualCheckSet).toHaveBeenCalled());
    expect(client.visualCheckSet).toHaveBeenCalledWith("calc", expect.objectContaining({
      command: "node capture.mjs", enforcement: "required",
      compare: [{ capture: "scene-01.png", reference: "REF-IMG-1a2b3c4d", tolerance: 0.035 }],
    }));
    await waitFor(() => expect(screen.getByTestId("visual-settings-summary")).toHaveTextContent("1 comparison · fails the run on a mismatch"));
  });

  it("imports an image chosen from disk as a reference and selects it", async () => {
    const client = fakeClient({ ...fresh, references: [] });
    render(<VisualCheckSettings client={client as unknown as EngineClient} projectId="calc" />);
    await waitFor(() => screen.getByTestId("visual-settings-edit"));
    fireEvent.click(screen.getByTestId("visual-settings-edit"));
    fireEvent.click(screen.getByTestId("visual-import-0"));
    await waitFor(() => expect(screen.getByTestId("visual-ref-0-REF-IMG-99887766")).toHaveAttribute("aria-checked", "true"));
    expect(client.referenceImport).toHaveBeenCalledWith("calc", "/home/ada/ti36x-front.png");
  });

  // Review of #124: editing an unrelated setting keeps a configured threshold.
  it("keeps each comparison's threshold when saving other changes", async () => {
    const client = fakeClient({ ...fresh, configured: true, command: "node capture.mjs", compare: [{ capture: "scene-01.png", reference: "REF-IMG-1a2b3c4d", tolerance: 0.02, threshold: 0.01 }] });
    render(<VisualCheckSettings client={client as unknown as EngineClient} projectId="calc" />);
    await waitFor(() => screen.getByTestId("visual-settings-edit"));
    fireEvent.click(screen.getByTestId("visual-settings-edit"));
    fireEvent.click(screen.getByTestId("visual-enf-required"));
    fireEvent.click(screen.getByTestId("visual-save"));
    await waitFor(() => expect(client.visualCheckSet).toHaveBeenCalled());
    expect(client.visualCheckSet.mock.calls[0]?.[1].compare).toEqual([{ capture: "scene-01.png", reference: "REF-IMG-1a2b3c4d", tolerance: 0.02, threshold: 0.01 }]);
  });

  it("shows the engine's refusal in place", async () => {
    const client = fakeClient();
    client.visualCheckSet.mockRejectedValueOnce(new Error("comparison 1: REF-IMG-1a2b3c4d is not stored in this project"));
    render(<VisualCheckSettings client={client as unknown as EngineClient} projectId="calc" />);
    await waitFor(() => screen.getByTestId("visual-settings-edit"));
    fireEvent.click(screen.getByTestId("visual-settings-edit"));
    fireEvent.change(screen.getByTestId("visual-command"), { target: { value: "node capture.mjs" } });
    fireEvent.click(screen.getByTestId("visual-save"));
    await waitFor(() => expect(screen.getByTestId("visual-failure")).toHaveTextContent("is not stored in this project"));
  });
});
