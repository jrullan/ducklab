import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AgentAccess } from "./AgentAccess";
import type { AgentToken } from "../api/client";

const opencode: AgentToken = {
  id: "at_1",
  name: "OpenCode ti36",
  actor: "agent:opencode-ti36",
  hint: "dlk_ab12cd",
  created_at: "2026-10-29T00:00:00Z",
  grants: [{ project: "ti36", level: "full" }],
};

function fakeClient(over: Record<string, unknown> = {}) {
  return {
    agentTokens: vi.fn().mockResolvedValue({ items: [opencode], total: 1, legacy_enabled: true }),
    projects: vi.fn().mockResolvedValue([{ id: "ti36" }, { id: "ducklab" }]),
    agentTokenCreate: vi.fn().mockResolvedValue({ token: { ...opencode, id: "at_2", name: "Claude Code" }, secret: "dlk_secret" }),
    agentTokenSetGrants: vi.fn().mockResolvedValue(opencode),
    agentTokenRevoke: vi.fn().mockResolvedValue(opencode),
    agentTokenLegacySet: vi.fn().mockResolvedValue({ legacy_enabled: false }),
    ...over,
  };
}

describe("agent access", () => {
  it("lists tokens with their grants and warns while the shared token is on", async () => {
    const client = fakeClient();
    render(<AgentAccess client={client as never} />);
    const row = await screen.findByTestId("agent-token-at_1");
    expect(row).toHaveTextContent("OpenCode ti36");
    expect(row).toHaveTextContent("agent:opencode-ti36");
    expect(row).toHaveTextContent("never used");
    const grants = await screen.findByTestId("agent-token-grants-at_1");
    expect((grants.querySelector('select[aria-label="access to ti36"]') as HTMLSelectElement).value).toBe("full");
    expect((grants.querySelector('select[aria-label="access to ducklab"]') as HTMLSelectElement).value).toBe("none");
    expect(screen.getByTestId("agent-access-legacy")).toHaveTextContent("full access to every project");
  });

  it("changes one project's level and keeps the others", async () => {
    const client = fakeClient();
    render(<AgentAccess client={client as never} />);
    const grants = await screen.findByTestId("agent-token-grants-at_1");
    await waitFor(() => expect(grants.querySelector('select[aria-label="access to ducklab"]')).not.toBeNull());
    fireEvent.change(grants.querySelector('select[aria-label="access to ducklab"]')!, { target: { value: "read-only" } });
    expect(client.agentTokenSetGrants).toHaveBeenCalledWith("at_1", [
      { project: "ti36", level: "full" },
      { project: "ducklab", level: "read-only" },
    ]);
  });

  it("issues a token and shows its secret once", async () => {
    const client = fakeClient();
    render(<AgentAccess client={client as never} />);
    const draft = await screen.findByTestId("agent-access-new-grants");
    fireEvent.change(screen.getByTestId("agent-access-name"), { target: { value: "Claude Code" } });
    fireEvent.change(draft.querySelector('select[aria-label="access to ducklab"]')!, { target: { value: "full" } });
    fireEvent.click(screen.getByTestId("agent-access-create"));
    expect(client.agentTokenCreate).toHaveBeenCalledWith("Claude Code", [{ project: "ducklab", level: "full" }]);
    expect(await screen.findByTestId("agent-access-secret")).toHaveTextContent("DUCKLAB_TOKEN=dlk_secret");
    fireEvent.click(screen.getByTestId("agent-access-secret-done"));
    expect(screen.queryByTestId("agent-access-secret")).toBeNull();
  });

  it("revokes only after a second, explicit click", async () => {
    const client = fakeClient();
    render(<AgentAccess client={client as never} />);
    fireEvent.click(await screen.findByTestId("agent-token-revoke-at_1"));
    expect(client.agentTokenRevoke).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("agent-token-revoke-confirm-at_1"));
    expect(client.agentTokenRevoke).toHaveBeenCalledWith("at_1");
  });

  it("turns the shared token off", async () => {
    const client = fakeClient();
    render(<AgentAccess client={client as never} />);
    await screen.findByTestId("agent-token-at_1");
    fireEvent.click(screen.getByTestId("agent-access-legacy-toggle"));
    expect(client.agentTokenLegacySet).toHaveBeenCalledWith(false);
    await waitFor(() => expect(screen.getByTestId("agent-access-legacy")).toHaveTextContent("only agents holding their own token"));
  });

  it("says why when the engine refuses", async () => {
    const client = fakeClient({ agentTokens: vi.fn().mockRejectedValue(new Error("listing agent tokens is the person's decision")) });
    render(<AgentAccess client={client as never} />);
    expect(await screen.findByTestId("agent-access-error")).toHaveTextContent("person's decision");
  });
});
