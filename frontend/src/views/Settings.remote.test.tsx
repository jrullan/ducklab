import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { Settings } from "./Settings";
import { EngineClient } from "../api/client";

describe("remote diagnostics", () => {
  it("is read-only and has no setter controls", () => {
    const client = new EngineClient({ baseUrl: "http://engine", token: "t", fetchFn: (async () => new Response(JSON.stringify({ max_usd: 1, max_tokens: 1, max_turns: 1, max_wallclock_s: 1, rounds: {}, agent_max_turns: 1 }), { status: 200 })) as never });
    render(<Settings theme="light" onTheme={() => {}} engineVersion="" connection="open" client={client} projectId="p" />);
    fireEvent.click(screen.getByTestId("settings-nav-remote"));
    const panel = screen.getByTestId("remote-diagnostics");
    expect(panel.querySelectorAll("input, textarea, select, button")).toHaveLength(0);
    expect(panel).toHaveTextContent("remote reachable");
  });

  it("prefills remote values and saves edited remote and list keys with attribution", async () => {
    const projectUpdate = vi.fn().mockResolvedValue({});
    const client = {
      configDoctor: vi.fn().mockResolvedValue([]),
      configDiagnostics: vi.fn().mockResolvedValue({ remote_reachable: "reachable", gh_auth: "authenticated", credential_helper: "configured" }),
      projectGet: vi.fn().mockResolvedValue({ config: { remote: { name: "origin", fetch_on_open: true, allow_mcp_verbs: ["read"] }, github: { pr_base: "main" }, shell: { allow_prefixes: ["git"] }, git: { protected_paths: [".env"] }, verify: { link_deps: ["frontend/node_modules"] } } }),
      projectUpdate,
      budgetDefaults: vi.fn().mockResolvedValue({ max_usd: 1, max_tokens: 1, max_turns: 1, max_wallclock_s: 1 }),
      modeDefaults: vi.fn().mockResolvedValue({ rounds: {}, script_rounds: {}, role_turns: {}, script_role_turns: {}, agent_max_turns: 1 }),
      projectAutonomy: vi.fn().mockResolvedValue({ autonomy: "" }),
      autopilotDefaults: vi.fn().mockResolvedValue({ max_tasks: 1, max_fails: 1, autonomy: "" }),
      projectGate: vi.fn().mockResolvedValue({}),
    };
    render(<Settings theme="light" onTheme={() => {}} engineVersion="" connection="open" client={client as never} projectId="p" />);
    fireEvent.click(screen.getByTestId("settings-nav-remote"));
    await screen.findByDisplayValue("origin");
    fireEvent.change(screen.getByTestId("remote-remote.name"), { target: { value: "upstream" } });
    fireEvent.change(screen.getByTestId("slice-remote.allow_mcp_verbs"), { target: { value: "read\nwrite" } });
    fireEvent.click(screen.getByTestId("save-remote-git-settings"));
    expect(projectUpdate).toHaveBeenCalledWith("p", expect.objectContaining({ "remote.name": "upstream", "remote.allow_mcp_verbs": "read,write", "shell.allow_prefixes": "git" }), "settings_remote_git");
  });
});
