import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StartProject, slugPreview } from "./StartProject";
import { ApiError, type EngineClient } from "../api/client";

const started = { project: { id: "ti-36x", name: "TI-36X", path: "/home/x/Ducklab/ti-36x" }, run_id: "r-1" };

// B-456: from an idea to a drafting run in one form.
describe("StartProject", () => {
  it("needs only a name, defaults the folder, and sends the brief and references", async () => {
    const projectStart = vi.fn(() => Promise.resolve(started));
    const onStarted = vi.fn();
    render(<StartProject client={{ projectStart } as unknown as EngineClient} onStarted={onStarted} />);
    expect(screen.getByTestId("start-submit")).toBeDisabled();
    fireEvent.change(screen.getByTestId("start-brief"), { target: { value: "A pixel perfect TI-36X Pro." } });
    fireEvent.change(screen.getByTestId("start-name"), { target: { value: "TI-36X" } });
    expect(screen.getByTestId("start-path")).toHaveAttribute("placeholder", "~/Ducklab/ti-36x");
    fireEvent.change(screen.getByTestId("start-refs"), { target: { value: "/pics/front.png\n/notes/brief.md" } });
    expect(screen.getByTestId("start-project")).toHaveTextContent("1 image: shown to a model that can see");
    fireEvent.click(screen.getByTestId("start-submit"));
    await waitFor(() => expect(onStarted).toHaveBeenCalledWith(started));
    expect(projectStart).toHaveBeenCalledWith({
      name: "TI-36X", brief: "A pixel perfect TI-36X Pro.", refs: ["/pics/front.png", "/notes/brief.md"],
    });
  });

  // B-463: asked once, in plain words, and sent with the retry.
  it("asks for a git identity when the machine has none, then retries with it", async () => {
    const projectStart = vi.fn()
      .mockRejectedValueOnce(new ApiError("git needs a name and email", 400, "git_identity_required"))
      .mockResolvedValueOnce(started);
    const onStarted = vi.fn();
    render(<StartProject client={{ projectStart } as unknown as EngineClient} onStarted={onStarted} />);
    fireEvent.change(screen.getByTestId("start-name"), { target: { value: "TI-36X" } });
    fireEvent.click(screen.getByTestId("start-submit"));
    expect(await screen.findByTestId("start-git-identity")).toHaveTextContent("saved for this project only");
    expect(screen.queryByTestId("start-failure")).toBeNull();
    expect(screen.getByTestId("start-submit")).toBeDisabled();
    fireEvent.change(screen.getByTestId("start-git-name"), { target: { value: "Ada" } });
    fireEvent.change(screen.getByTestId("start-git-email"), { target: { value: "ada@example.com" } });
    fireEvent.click(screen.getByTestId("start-submit"));
    await waitFor(() => expect(onStarted).toHaveBeenCalled());
    expect(projectStart).toHaveBeenLastCalledWith(expect.objectContaining({ git_name: "Ada", git_email: "ada@example.com" }));
  });

  it("refuses a relative or ~ folder before sending, with the fix", () => {
    render(<StartProject client={{ projectStart: vi.fn() } as unknown as EngineClient} onStarted={vi.fn()} />);
    fireEvent.change(screen.getByTestId("start-name"), { target: { value: "calc" } });
    fireEvent.change(screen.getByTestId("start-path"), { target: { value: "~/code/calc" } });
    expect(screen.getByTestId("start-path-problem")).toHaveTextContent("full path starting with /");
    expect(screen.getByTestId("start-submit")).toBeDisabled();
  });

  it("previews the folder slug the engine will use", () => {
    expect(slugPreview("TI-36X Pro")).toBe("ti-36x-pro");
  });
});
