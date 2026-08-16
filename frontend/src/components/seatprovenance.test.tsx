import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TddLaunch } from "./TddLaunch";
import { SeatChips } from "./SeatChips";
import type { Duckling } from "../api/client";

const fleet = [
  { id: "luna", provider: "fake", model: "luna" },
  { id: "glm52", provider: "fake", model: "glm52" },
] as Duckling[];

describe("launcher seat resolution", () => {
  it("does not turn an untouched Settings lineup into an explicit TDD override", () => {
    const onTdd = vi.fn();
    const props = {
      ducklings: fleet,
      // This is stale Settings memory, not a per-run choice.
      preferred: { solo: ["glm52"] },
      phaseDefaults: { test: "solo", build: "solo" },
      roster: { implementer: "luna" },
      busy: false,
      onTdd,
      onTestOnly: () => {},
      onBuildOnly: () => {},
    } as never;
    render(<TddLaunch {...(props as any)} />);

    fireEvent.click(screen.getByTestId("tdd-start"));
    expect(onTdd).toHaveBeenCalledWith(
      expect.objectContaining({ ducklings: [] }),
      expect.objectContaining({ ducklings: [] }),
    );
  });

  it("labels roster, Settings, and picked-now seats so their precedence is visible", () => {
    render(
      <SeatChips
        fleet={fleet}
        entries={[
          { role: "implementer", duckling: "luna", provenance: "roster" },
          { role: "reviewer", duckling: "glm52", provenance: "Settings" },
          { role: "judge", duckling: "luna", provenance: "picked now" },
        ] as never}
      />,
    );

    const chips = screen.getAllByTestId("seat-chip");
    expect(chips[0]).toHaveTextContent("roster");
    expect(chips[1]).toHaveTextContent("Settings");
    expect(chips[2]).toHaveTextContent("picked now");
  });
});
