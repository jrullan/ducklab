import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ConfigFailureCard } from "./ConfigFailureCard";

describe("ConfigFailureCard", () => {
  it("pre-seeds the consultant chat with the configuration finding", () => {
    render(<ConfigFailureCard client={{} as never} projectId="p" ducklings={[{ id: "consultant", provider: "openai", model: "test", caps: { native_tools: false, context_tokens: 1 } }]} finding={{ key: "verify.link_deps", proposed: "frontend/node_modules", reason: "dependencies are unavailable" }} />);
    fireEvent.click(screen.getByTestId("chat-about"));
    expect(String((screen.getByTestId("chat-message") as HTMLTextAreaElement).value).includes("verify.link_deps → frontend/node_modules")).toBe(true);
  });
});
