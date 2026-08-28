import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Settings } from "./Settings";

describe("Roster information architecture", () => {
  it("does not frame the operational roster inside Settings", () => {
    render(<Settings theme="light" onTheme={() => {}} engineVersion="" connection="open" />);
    expect(screen.queryByTestId("settings-nav-roster")).not.toBeInTheDocument();
    expect(screen.queryByTestId("settings-room-roster")).not.toBeInTheDocument();
  });
});
