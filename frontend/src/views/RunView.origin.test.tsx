import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { RunView } from "./RunView";
import { useRuns } from "../store/runs";

function makeClient(trace: Record<string, unknown>, sections = [{ id: "REQ-1", title: "Requirement", body: "People can see why a run exists. More detail follows." }]) {
  return {
    run: vi.fn().mockResolvedValue({ run: {
      id: "run-1", project_id: "project-1", stage: "build", mode: "solo", task_id: "task-1",
      status: "done", verdict: "PASSED", started_at: "2026-01-01T00:00:00Z",
    }, events: [] }),
    ducklings: vi.fn().mockResolvedValue([]),
    modeDefaults: vi.fn().mockResolvedValue({ ducklings: {} }),
    report: vi.fn().mockResolvedValue({ rows: [] }),
    tasks: vi.fn().mockResolvedValue([]),
    traceShow: vi.fn((_: string, id: string) => Promise.resolve(trace[id] ?? { id, up: [] })),
    artifact: vi.fn().mockResolvedValue({ sections }),
    runDiff: vi.fn().mockResolvedValue({ diff: "", tests: "" }),
    runVerify: vi.fn().mockResolvedValue(""),
    runCandidates: vi.fn().mockResolvedValue([]),
    runLLM: vi.fn().mockResolvedValue([]),
  };
}

function seedRun(id = "run-1") {
  useRuns.setState({
    runs: { [id]: { id, project_id: "project-1", stage: "build", mode: "solo", task_id: "task-1", status: "done", verdict: "PASSED", started_at: "2026-01-01T00:00:00Z" } },
    events: { [id]: [] }, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open",
  });
}

describe("RunView publication consequences", () => {
  it("names the configured push consequence before accepting and preserves a failed publication for retry", async () => {
    useRuns.setState({
      runs: { "run-1": { id: "run-1", project_id: "project-1", stage: "build", mode: "solo", task_id: "task-1", status: "done", verdict: "PASSED", started_at: "2026-01-01T00:00:00Z", next: ["accept"] } },
      events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open",
    });
    const client = makeClient({}) as Record<string, ReturnType<typeof vi.fn>>;
    client.projectGet = vi.fn().mockResolvedValue({ config: { remote: { name: "origin", on_accept: "push" }, github: { pr_base: "main" } } });
    client.accept = vi.fn().mockResolvedValue({ commit_sha: "abc123456", warning: "committed as abc123456; push failed: permission denied" });
    client.projectPush = vi.fn().mockResolvedValue({ status: "pushed", branch: "main" });

    render(<RunView runId="run-1" client={client as never} />);
    expect(await screen.findByTestId("decision-consequence")).toHaveTextContent("commits the diff and pushes to origin/main");
    screen.getByTestId("cycle-accept").click();
    expect(await screen.findByTestId("publication-failure")).toHaveTextContent("committed locally as abc123456; push failed: permission denied");
    screen.getByTestId("retry-publication").click();
    expect(client.projectPush).toHaveBeenCalledWith("project-1");
  });

  it("does not mislabel an unrelated accept warning as a failed push", async () => {
    useRuns.setState({
      runs: { "run-1": { id: "run-1", project_id: "project-1", stage: "spec", mode: "council", task_id: "", status: "paused", verdict: "UNVERIFIED", started_at: "2026-01-01T00:00:00Z", next: ["accept"] } },
      events: {}, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open",
    });
    const client = makeClient({}) as Record<string, ReturnType<typeof vi.fn>>;
    client.projectGet = vi.fn().mockResolvedValue({ config: { remote: { name: "origin", on_accept: "nothing" } } });
    client.accept = vi.fn().mockResolvedValue({
      commit_sha: "be7087d9",
      warning: "beelink-local is on both sides of the pair: this measures self-consistency, not review.",
    });

    render(<RunView runId="run-1" client={client as never} />);
    screen.getByTestId("cycle-accept").click();

    expect(await screen.findByTestId("accept-committed")).toHaveTextContent("committed be7087d9");
    expect(screen.queryByTestId("publication-failure")).not.toBeInTheDocument();
  });
});

describe("RunView origin panel", () => {
  it("quotes the requirement and links every document in the breadcrumb", async () => {
    seedRun();
    const client = makeClient({
      "task-1": { id: "task-1", kind: "task", title: "Implement origin panel", up: ["spec-1"] },
      "spec-1": { id: "spec-1", kind: "spec", title: "Origin visibility", up: ["plan-1"] },
      "plan-1": { id: "plan-1", kind: "plan", title: "Build origin panel", up: ["REQ-1"] },
      "REQ-1": { id: "REQ-1", kind: "requirement", title: "Origin is visible", up: [] },
    });

    render(<RunView runId="run-1" client={client as never} />);

    expect(await screen.findByText("“People can see why a run exists.”")).toBeInTheDocument();
    const breadcrumb = await screen.findByTestId("run-origin-breadcrumb");
    expect(breadcrumb.querySelector('a[href="#/cycle/intake/REQ-1"]')).toHaveTextContent("Origin is visible");
    expect(breadcrumb.querySelector('a[href="#/cycle/plan/plan-1"]')).toHaveTextContent("Build origin panel");
    expect(breadcrumb.querySelector('a[href="#/cycle/spec/spec-1"]')).toHaveTextContent("Origin visibility");
    expect(breadcrumb.querySelector('a[href="#/cycle/plan/task-1"]')).toHaveTextContent("Implement origin panel");
  });

  it("returns a document chat to its exact section", async () => {
    const chat = { id: "run-1", project_id: "project-1", stage: "chat", mode: "solo", task_id: "", status: "paused", verdict: "", started_at: "2026-01-01T00:00:00Z", note: "chat about document REQ-005" };
    useRuns.setState({ runs: { "run-1": chat as never }, events: { "run-1": [] }, deltas: {}, reasoning: {}, spend: {}, acceptState: {}, needsResync: false, connection: "open" });
    const client = makeClient({}) as Record<string, ReturnType<typeof vi.fn>>;
    client.run = vi.fn().mockResolvedValue({ run: chat, events: [] });
    render(<RunView runId="run-1" client={client as never} />);
    expect(await screen.findByTestId("chat-document-return")).toHaveAttribute("href", "#/cycle/intake/REQ-005");
  });

  it("says plainly when the run has no document spine", async () => {
    seedRun();
    const client = makeClient({ "task-1": { id: "task-1", kind: "task", title: "Unlinked task", up: [] } });

    render(<RunView runId="run-1" client={client as never} />);

    await waitFor(() => expect(screen.getByTestId("run-origin-none")).toHaveTextContent("this run has no document behind it — worth knowing"));
    expect(screen.queryByTestId("run-origin-requirement")).not.toBeInTheDocument();
  });
});
