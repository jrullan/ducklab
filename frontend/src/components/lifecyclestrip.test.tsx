import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { LifecycleStrip } from "./LifecycleStrip";
import type { ProjectLifecycle } from "../api/client";

const road = (over: Partial<ProjectLifecycle>): ProjectLifecycle => ({
  stages: [
    { id: "requirements", label: "Requirements", state: "done" },
    { id: "spec", label: "Spec", state: "decision" },
    { id: "plan", label: "Plan", state: "pending" },
    { id: "build", label: "Build", state: "pending" },
    { id: "release", label: "Release", state: "pending" },
  ],
  current: "spec", code_exists: false, tasks_accepted: 0, tasks_total: 0, unreleased_work: 0,
  next: "Review the specification draft: accept it, or send it back with a note.",
  ...over,
});

// B-461: where the project stands, what is next, and whether code exists.
describe("LifecycleStrip", () => {
  it("marks done, waiting and current stages, says no code exists yet, and states the next step", () => {
    render(<LifecycleStrip lifecycle={road({})} />);
    expect(screen.getByTestId("lifecycle-stage-requirements")).toHaveAttribute("data-state", "done");
    const spec = screen.getByTestId("lifecycle-stage-spec");
    expect(spec).toHaveAttribute("data-state", "decision");
    expect(spec).toHaveAttribute("aria-current", "step");
    expect(screen.getByTestId("lifecycle-no-code")).toHaveTextContent("no code yet");
    expect(screen.getByTestId("lifecycle-next")).toHaveTextContent("Review the specification draft");
  });

  it("counts built tasks on the build stage and drops the no-code note once code exists", () => {
    render(<LifecycleStrip lifecycle={road({ current: "build", code_exists: true, tasks_accepted: 3, tasks_total: 7,
      stages: road({}).stages.map((s) => s.id === "build" ? { ...s, state: "current" as const } : s.id === "release" ? s : { ...s, state: "done" as const }) })} />);
    expect(screen.getByTestId("lifecycle-stage-build")).toHaveTextContent("Build 3/7");
    expect(screen.queryByTestId("lifecycle-no-code")).toBeNull();
  });
});
