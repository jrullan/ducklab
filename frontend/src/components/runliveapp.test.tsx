import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RunLiveApp } from "./RunLiveApp";

const WT = "/home/u/.local/state/ducklab/worktrees/p/r-1";

function fakeClient(status: Record<string, unknown>, over: Record<string, unknown> = {}) {
  return {
    appStatus: vi.fn().mockResolvedValue({ smoke_expectation: "live", smoke_timeout_s: 3, ...status }),
    runAppStart: vi.fn().mockResolvedValue({ configured: true, running: true, run_id: "r-1" }),
    appStop: vi.fn().mockResolvedValue(undefined),
    ...over,
  };
}

describe("run live app (B-552)", () => {
  it("launches the run's worktree, not the project's app", async () => {
    const client = fakeClient({ configured: true, running: false, command: "python3 -m http.server 41666" });
    render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
    const launch = await screen.findByTestId("live-app-launch");
    expect(screen.getByTestId("live-app")).toHaveTextContent(WT);
    expect(screen.getByTestId("live-app")).toHaveTextContent("not main");
    fireEvent.click(launch);
    await waitFor(() => expect(client.runAppStart).toHaveBeenCalledWith("r-1"));
  });

  it("names whose app holds the slot when another tree is running", async () => {
    const client = fakeClient({ configured: true, running: true, pid: 9 });
    render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
    expect(await screen.findByTestId("live-app-occupied")).toHaveTextContent("The project's app (main) is running — not this change");
    expect(screen.queryByTestId("live-app-launch")).toBeNull();
    fireEvent.click(screen.getByTestId("live-app-stop-other"));
    await waitFor(() => expect(client.appStop).toHaveBeenCalledWith("p"));
  });

  it("shows this run's running app with its address and stop", async () => {
    const client = fakeClient({ configured: true, running: true, run_id: "r-1", pid: 7, url: "http://127.0.0.1:41666", log_tail: "serving" });
    render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
    expect(await screen.findByTestId("live-app-url")).toHaveAttribute("href", "http://127.0.0.1:41666");
    expect(screen.getByTestId("live-app")).toHaveTextContent("This run's app is running (pid 7)");
  });

  it("says why it cannot launch an unconfigured project and where the change is", async () => {
    const client = fakeClient({ configured: false, running: false });
    render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
    const note = await screen.findByTestId("live-app-unconfigured");
    expect(note).toHaveTextContent("no run command");
    expect(note).toHaveTextContent(WT);
  });

  it("surfaces a refused launch", async () => {
    const client = fakeClient({ configured: true, running: false, command: "x" }, {
      runAppStart: vi.fn().mockRejectedValue(new Error("not started — run r-1 is being accepted")),
    });
    render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
    fireEvent.click(await screen.findByTestId("live-app-launch"));
    expect(await screen.findByTestId("live-app-error")).toHaveTextContent("being accepted");
  });

  it("follows the app after launch: a crash shows without a click", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const client = fakeClient({ configured: true, running: true, run_id: "r-1", pid: 7 });
      render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
      expect(await screen.findByTestId("live-app-stop")).toBeInTheDocument();
      client.appStatus.mockResolvedValue({ configured: true, running: false, run_id: "r-1", exit_error: "exit status 1", smoke_expectation: "live", smoke_timeout_s: 3 });
      await vi.advanceTimersByTimeAsync(2500);
      expect(await screen.findByTestId("live-app-exit")).toHaveTextContent("exit status 1");
    } finally {
      vi.useRealTimers();
    }
  });

  it("says when the app's state cannot be read instead of vanishing", async () => {
    const client = fakeClient({}, { appStatus: vi.fn().mockRejectedValue(new Error("engine unreachable")) });
    render(<RunLiveApp client={client as never} runId="r-1" projectId="p" worktreePath={WT} />);
    expect(await screen.findByTestId("live-app-status-error")).toHaveTextContent("engine unreachable");
  });
});
