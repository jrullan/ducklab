/**
 * The Board: tasks and bugs in columns, with a rail on selection (08 §4.3).
 *
 * There is no drag-to-move. A task's status is derived from its run records,
 * not stored on the task, so there is nothing for a drop to write — and that
 * derivation is deliberate: status kept in the plan document would let a model
 * rewriting the plan mark its own work accepted (I2). Moving a card means
 * running the task, so `Run` is the action the rail offers.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRuns } from "../store/runs";
import type { Bug, Duckling, EngineClient, GateResult, Task } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusChip } from "../components/StatusChip";
import { WaitingCard } from "../components/WaitingCard";
import { RunLauncher, LaunchConfig, type LaunchOpts, type ModeEstimates, type PhaseConfig } from "../components/RunLauncher";

const COLUMNS = [
  { key: "todo", label: "Todo" },
  { key: "in_progress", label: "In progress" },
  { key: "blocked", label: "Blocked" },
  { key: "review", label: "Review" },
  { key: "accepted", label: "Accepted" },
] as const;

// The bug loop's columns (08 §4.3). duplicate, wontfix and closed are decided
// outcomes rather than places work sits, so they are not columns; a board that
// showed them would be mostly archive.
const BUG_COLUMNS = [
  { key: "open", label: "Open" },
  { key: "triaged", label: "Triaged" },
  { key: "in_progress", label: "In progress" },
  { key: "fixed", label: "Fixed" },
  { key: "verified", label: "Verified" },
] as const;

/** Severity as a status role (08 §4.3). */
function severityRole(sev: string): "critical" | "serious" | "warning" | "good" {
  switch (sev) {
    case "critical":
      return "critical";
    case "high":
      return "serious";
    case "low":
      return "good";
    default:
      return "warning";
  }
}

export function Board({
  client,
  projectId,
  tab,
}: {
  client: EngineClient;
  projectId: string;
  tab?: string;
}) {
  // Derived from the route, never held beside it. This was useState seeded
  // from the prop, which reads it exactly once — so the Work subnav's Tasks
  // and Bugs links changed the hash, the prop arrived, and the mounted board
  // ignored it: two links, one unchanging screen. The toggle below navigates,
  // and the route is the only opinion about which board is showing.
  const board: "tasks" | "bugs" = tab === "bugs" ? "bugs" : "tasks";

  const [tasks, setTasks] = useState<Task[]>([]);
  // Needed to offer a choice of ducklings when starting a run. Failing to load
  // them is not worth blocking the board over: with none, the roster decides.
  const [ducklings, setDucklings] = useState<Duckling[]>([]);
  // The saved line-up per mode, so picking a mode fills the boxes with the
  // combination that was found to work.
  const [preferred, setPreferred] = useState<Record<string, string[]>>({});
  // The modes launchers open on, from Settings: the person who always builds
  // in pair and tests in solo should not re-pick both on every task.
  const [phaseDefaults, setPhaseDefaults] = useState<{ build: string; test: string }>({
    build: "solo",
    test: "solo",
  });
  // What each mode has cost in THIS project, for the launcher's mode picker.
  const [estimates, setEstimates] = useState<ModeEstimates>({});
  // Filing a report was reachable only from the CLI: the engine has had
  // POST /bugs since the operate loop was built, and the board's own empty
  // state told you to go and run `ducklab bug add`. On a desktop-only setup the
  // whole loop was unreachable.
  const [filing, setFiling] = useState(false);
  const [bugTitle, setBugTitle] = useState("");
  const [bugBody, setBugBody] = useState("");
  const [bugSeverity, setBugSeverity] = useState("normal");
  const [bugError, setBugError] = useState<string | null>(null);
  const [triageRun, setTriageRun] = useState<string | null>(null);
  // What to start. Derived by the engine — dependencies accepted, nothing
  // blocked, nothing already running — because the board showed every task's
  // state and never answered the question a person arrives with.
  const [next, setNext] = useState<Task | null>(null);
  // Which gate the project has, so the rail offers only what can work here.
  const [gate, setGate] = useState("");
  const [gateCommand, setGateCommand] = useState("");
  const [bugs, setBugs] = useState<Bug[]>([]);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  // The selection belongs to the board that made it: keeping it across a
  // switch would leave the rail describing a task while the bugs are on
  // screen. On board change — from the toggle or the Work subnav alike.
  useEffect(() => {
    setSelected(null);
  }, [board]);
  const [milestone, setMilestone] = useState("");
  const [severity, setSeverity] = useState("");
  const [query, setQuery] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);

    // Fetched together so switching boards is instant and the counts on the
    // toggle are true before you click — but settled independently. One board
    // failing must not blank the other: a project whose bugs cannot be read
    // still has tasks worth looking at, and losing both to one error tells the
    // reader less than showing what survived.
    client.ducklings().then(setDucklings).catch(() => {});
    client.taskNext(projectId).then(setNext).catch(() => setNext(null));
    client
      .modeDefaults()
      .then((d) => {
        setPreferred(d.ducklings ?? {});
        setPhaseDefaults({ build: d.build_mode || "solo", test: d.test_mode || "solo" });
      })
      .catch(() => setPreferred({}));
    void (async () => {
      try {
        const rep = await client.report(projectId, "mode");
        const est: ModeEstimates = {};
        for (const row of rep.rows) {
          est[row.key] = { usd: row.cost_usd, runs: row.runs };
        }
        setEstimates(est);
      } catch {
        setEstimates({});
      }
    })();
    client
      .projectGate(projectId)
      .then((g) => {
        setGate(g.mode);
        setGateCommand(g.command);
      })
      .catch(() => {});
    const [t, b] = await Promise.allSettled([client.tasks(projectId), client.bugs(projectId)]);
    const problems: string[] = [];
    if (t.status === "fulfilled") setTasks(t.value);
    else problems.push(`tasks: ${String(t.reason?.message ?? t.reason)}`);
    if (b.status === "fulfilled") setBugs(b.value);
    else problems.push(`bugs: ${String(b.reason?.message ?? b.reason)}`);

    setFailure(problems.length ? problems.join(" · ") : null);
    setLoading(false);
  }, [client, projectId]);

  useEffect(() => {
    void load();
  }, [load]);

  // Runs started or finished ANYWHERE — the CLI, an MCP operator, another
  // window — move tasks between columns. The store already hears run_start
  // and run_end on the stream; the board refetches when this project's set
  // of active runs changes, instead of waiting for someone to change views.
  const activeRunKey = useRuns((s) =>
    Object.values(s.runs)
      .filter((r) => r.project_id === projectId && (r.status === "running" || r.status === "queued"))
      .map((r) => r.id)
      .sort()
      .join(","),
  );
  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeRunKey]);

  const milestones = useMemo(
    () => [...new Set(tasks.map((t) => t.milestone).filter(Boolean))].sort(),
    [tasks],
  );

  const q = query.trim().toLowerCase();
  const shownTasks = useMemo(
    () =>
      tasks.filter(
        (t) =>
          (!milestone || t.milestone === milestone) &&
          (!q || t.id.toLowerCase().includes(q) || t.title.toLowerCase().includes(q)),
      ),
    [tasks, milestone, q],
  );
  const shownBugs = useMemo(
    () =>
      bugs.filter(
        (b) =>
          (!severity || b.severity === severity) &&
          (!q || b.id.toLowerCase().includes(q) || b.title.toLowerCase().includes(q)),
      ),
    [bugs, severity, q],
  );

  const isBugs = board === "bugs";
  const total = isBugs ? bugs.length : tasks.length;
  const shownCount = isBugs ? shownBugs.length : shownTasks.length;
  const decidedStatuses = ["closed", "duplicate", "wontfix"];
  const decided = bugs.filter((b) => decidedStatuses.includes(b.status));
  const current = isBugs
    ? (bugs.find((b) => b.id === selected) ?? null)
    : (tasks.find((t) => t.id === selected) ?? null);

  // An empty project used to render ONLY this message, and the message named
  // two CLI commands. So the one state where you most need to file something —
  // a project with nothing in it yet — was the state with no controls at all,
  // and the advice was to go and use a different program.
  const empty = !loading && !failure && tasks.length === 0 && bugs.length === 0;

  return (
    <div data-testid="board-view" className="flex gap-4">
      <div className="min-w-0 flex-1">
        {failure && (
          <div data-testid="board-error" className="mb-3 text-sm text-critical">
            {failure}
          </div>
        )}

        {/* One line, above the columns: the answer, not a nudge. It disappears
            when there is nothing ready, which is itself the answer — everything
            is done, running, or waiting on something. */}
        {!isBugs && next && (
          <p className="mb-3 text-sm text-ink-secondary" data-testid="task-next">
            Ready to start:{" "}
            <button
              type="button"
              onClick={() => setSelected(next.id)}
              data-testid="task-next-select"
              className="text-ink underline"
            >
              {next.id} — {next.title}
            </button>
          </p>
        )}

        {empty && (
          <div className="mb-3">
            <EmptyState message="Nothing here yet. Plan the work from Cycle, or file a report with the button below." />
          </div>
        )}

        {/* Filters in one row above the board (08 §4.3). The tasks/bugs
            toggle that lived here is gone: the Work subnav switches boards,
            and two controls for one choice on one screen was the duplication
            it looked like. */}
        <div className="mb-3 flex items-center gap-2">
          {isBugs ? (
            <select
              data-testid="board-severity"
              className="rounded border border-hairline bg-page px-2 py-1 text-sm text-ink"
              value={severity}
              onChange={(e) => setSeverity(e.target.value)}
            >
              <option value="">All severities</option>
              {["critical", "high", "normal", "low"].map((sv) => (
                <option key={sv} value={sv}>
                  {sv}
                </option>
              ))}
            </select>
          ) : (
            <select
              data-testid="board-milestone"
              className="rounded border border-hairline bg-page px-2 py-1 text-sm text-ink"
              value={milestone}
              onChange={(e) => setMilestone(e.target.value)}
            >
              <option value="">All milestones</option>
              {milestones.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          )}

          {isBugs && (
            <button
              type="button"
              data-testid="bug-file"
              onClick={() => setFiling((v) => !v)}
              className="rounded border border-hairline px-2 py-1 text-sm"
            >
              {filing ? "Cancel" : "File a bug"}
            </button>
          )}
          {/* Triage is the step that turns a report into something a run can be
              pointed at: severity, suspected files, whether it duplicates
              another. It reads every open bug, so it is one action, not one per
              report. */}
          {isBugs && bugs.some((b) => b.status === "open") && (
            <button
              type="button"
              data-testid="bug-triage"
              onClick={() =>
                void client
                  .triageBugs(projectId)
                  .then((r) => setTriageRun(r.id))
                  .catch((e) => setBugError(e instanceof Error ? e.message : String(e)))
              }
              className="rounded border border-hairline px-2 py-1 text-sm"
            >
              Triage open
            </button>
          )}

          <input
            data-testid="board-search"
            className="rounded border border-hairline bg-page px-2 py-1 text-sm text-ink"
            placeholder={isBugs ? "Filter bugs" : "Filter tasks"}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <span className="text-sm text-ink-muted">
            {shownCount} of {total}
          </span>
        </div>

        {filing && (
          <div className="mb-3 space-y-2 rounded-card border border-hairline p-3" data-testid="bug-form">
            <input
              aria-label="bug title"
              data-testid="bug-title"
              placeholder="What is wrong, in one line"
              value={bugTitle}
              onChange={(e) => setBugTitle(e.target.value)}
              className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
            />
            <textarea
              aria-label="bug body"
              data-testid="bug-body"
              placeholder="What you did, what happened, what you expected"
              value={bugBody}
              onChange={(e) => setBugBody(e.target.value)}
              rows={4}
              className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
            />
            <div className="flex flex-wrap items-center gap-2">
              {/* Taken as given. A reporter saying "critical" may be wrong, but a
                  tool that quietly downgrades what it was told is a tool nobody
                  reports to twice — triage is where that judgement belongs. */}
              <select
                aria-label="severity"
                data-testid="bug-severity"
                value={bugSeverity}
                onChange={(e) => setBugSeverity(e.target.value)}
                className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
              >
                {["critical", "high", "normal", "low"].map((sv) => (
                  <option key={sv} value={sv}>
                    {sv}
                  </option>
                ))}
              </select>
              <button
                type="button"
                data-testid="bug-submit"
                disabled={!bugTitle.trim()}
                onClick={() => {
                  setBugError(null);
                  void client
                    .bugAdd(projectId, {
                      title: bugTitle.trim(),
                      body: bugBody.trim(),
                      severity: bugSeverity,
                    })
                    .then(() => {
                      setBugTitle("");
                      setBugBody("");
                      setFiling(false);
                      return load();
                    })
                    .catch((e) => setBugError(e instanceof Error ? e.message : String(e)));
                }}
                className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
              >
                File it
              </button>
            </div>
          </div>
        )}

        {bugError && (
          <p className="mb-2 text-sm text-critical" data-testid="bug-error">
            {bugError}
          </p>
        )}
        {triageRun && (
          <p className="mb-2 text-sm" data-testid="triage-run">
            <a href={`#/runs/${triageRun}`} className="text-ink underline">
              triage started — watch it
            </a>
          </p>
        )}

        <div className="grid grid-cols-5 gap-2">
          {(isBugs ? BUG_COLUMNS : COLUMNS).map((col) => {
            const items = isBugs
              ? shownBugs.filter((b) => b.status === col.key)
              : shownTasks.filter((t) => t.status === col.key);
            return (
              <section key={col.key} data-testid={`board-col-${col.key}`}>
                <h2 className="mb-2 text-sm text-ink-muted">
                  {col.label}
                  <span className="ml-1 text-ink-muted">{items.length}</span>
                </h2>
                <ul className="space-y-2">
                  {items.map((it) => (
                    <li key={it.id}>
                      <button
                        data-testid="board-card"
                        data-task={it.id}
                        onClick={() => setSelected(it.id)}
                        aria-pressed={selected === it.id}
                        className={
                          "w-full rounded-card border p-2 text-left " +
                          (selected === it.id ? "border-serious" : "border-hairline")
                        }
                      >
                        <div className="font-mono text-xs text-ink-muted">{it.id}</div>
                        <div className="text-sm text-ink">{it.title}</div>
                        {isBugs ? (
                          <div className="mt-1">
                            <StatusChip
                              role={severityRole((it as Bug).severity)}
                              label={(it as Bug).severity}
                            />
                          </div>
                        ) : (
                          (it as Task).complexity && (
                            <div className="mt-1 text-xs text-ink-muted">
                              {(it as Task).complexity}
                            </div>
                          )
                        )}
                        {/* The committed failing test, said on the card: an
                            accepted test-first used to land the task in
                            Accepted, where every offered action implied the
                            work was done — and it had never been built once. */}
                        {!isBugs && (it as Task).test_ready && (
                          <div data-testid="test-ready" className="mt-1 text-xs text-good">
                            failing test committed — build it to make it pass
                          </div>
                        )}
                        {/* A card in Blocked without the reason on it sends you
                            reading run logs to learn what stopped it. */}
                        {!isBugs && (it as Task).blocked && (
                          <div
                            data-testid="blocked-reason"
                            className="mt-1 text-xs text-serious"
                          >
                            {(it as Task).blocked}
                          </div>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>

        {/* Decided outcomes — closed, duplicate, wontfix — are rightly not
            columns: a board that showed them would be mostly archive. But they
            used to render NOWHERE, which is a different claim than "not a
            column": the record existed and no surface owned it. Folded, below
            the work, selectable like anything else. */}
        {isBugs && decided.length > 0 && (
          <details className="mt-3" data-testid="bugs-decided">
            <summary className="cursor-pointer text-xs text-ink-muted">
              {decided.length} decided — closed, duplicate or wontfix
            </summary>
            <ul className="mt-1 space-y-1">
              {decided.map((b) => (
                <li key={b.id}>
                  <button
                    type="button"
                    data-testid="decided-bug"
                    onClick={() => setSelected(b.id)}
                    className="text-left text-sm text-ink-muted underline"
                  >
                    <span className="font-mono">{b.id}</span> {b.title}
                    <span className="ml-1 text-xs">({b.status})</span>
                  </button>
                </li>
              ))}
            </ul>
          </details>
        )}
      </div>

      <aside data-testid="board-rail" className="w-72 shrink-0">
        {current === null ? (
          <p className="text-sm text-ink-muted">
            Select {isBugs ? "a bug" : "a task"} to see its record.
          </p>
        ) : isBugs ? (
          <BugRail bug={current as Bug} client={client} projectId={projectId} onDone={() => void load()} />
        ) : (
          <TaskRail
            task={current as Task}
            client={client}
            projectId={projectId}
            ducklings={ducklings}
            preferred={preferred}
            phaseDefaults={phaseDefaults}
            estimates={estimates}
            gate={gate}
            gateCommand={gateCommand}
            onDone={() => void load()}
          />
        )}
      </aside>
    </div>
  );
}

function TaskRail({
  task,
  client,
  projectId,
  ducklings,
  preferred,
  phaseDefaults,
  estimates,
  gate,
  gateCommand,
  onDone,
}: {
  task: Task;
  client: EngineClient;
  projectId: string;
  ducklings: readonly Duckling[];
  preferred: Record<string, string[]>;
  phaseDefaults: { build: string; test: string };
  estimates: ModeEstimates;
  gate: string;
  gateCommand: string;
  onDone: () => void;
}) {
  return (
    <div className="space-y-2">
      <div className="font-mono text-xs text-ink-muted">{task.id}</div>
      <div className="text-sm font-medium text-ink">{task.title}</div>
      {/* Actions before prose. The person clicked this card to DO something,
          and a long task body — a promoted bug carries its whole report and
          triage — pushed the controls below the fold, where reaching them
          meant scrolling past text already read. Not a modal on purpose: an
          overlay would hide the description and the gate exactly when
          deciding how to run needs them; instead the actions sit at the top
          and the body scrolls in its own pane beneath. */}
      <TaskRunner
        task={task}
        client={client}
        projectId={projectId}
        ducklings={ducklings}
        preferred={preferred}
        phaseDefaults={phaseDefaults}
        estimates={estimates}
        gate={gate}
        gateCommand={gateCommand}
        onDone={onDone}
      />
      <dl className="space-y-1 text-xs text-ink-muted">
        <Row label="status" value={task.status.replace("_", " ")} />
        <Row label="milestone" value={task.milestone} />
        <Row label="implements" value={task.implements?.join(", ")} />
        <Row label="depends on" value={task.depends_on?.join(", ")} />
      </dl>
      {task.body && (
        <div className="max-h-72 overflow-y-auto border-t border-hairline pt-2" data-testid="task-body">
          <p className="whitespace-pre-wrap text-sm text-ink-secondary">{task.body}</p>
        </div>
      )}
    </div>
  );
}

/** Starting the work, from the place the work is listed.
 *
 * Every mode needs a different number of ducklings, so the picker is a list
 * rather than a single choice: `tournament` and `split` assign them
 * positionally, and `solo` uses the first. Choosing none means the project's
 * roster decides, which is the normal case.
 */
function TaskRunner({
  task,
  client,
  projectId,
  ducklings,
  preferred,
  phaseDefaults,
  estimates,
  gate,
  gateCommand,
  onDone,
}: {
  task: Task;
  client: EngineClient;
  projectId: string;
  ducklings: readonly Duckling[];
  preferred: Record<string, string[]>;
  phaseDefaults: { build: string; test: string };
  estimates: ModeEstimates;
  gate: string;
  gateCommand: string;
  onDone: () => void;
}) {
  const [chosen, setChosen] = useState<string[]>([]);
  // The two phases of the chain, opening on the Settings defaults with the
  // saved line-up for each mode already seated — the habitual launch is zero
  // touches. Changing a mode re-seats from that mode's saved line-up.
  const [testCfg, setTestCfg] = useState<PhaseConfig>(() => ({
    mode: phaseDefaults.test,
    ducklings: [...(preferred[phaseDefaults.test] ?? [])],
  }));
  const [buildCfg, setBuildCfg] = useState<PhaseConfig>(() => ({
    mode: phaseDefaults.build,
    ducklings: [...(preferred[phaseDefaults.build] ?? [])],
  }));
  const reseat = (set: (c: PhaseConfig) => void) => (next: PhaseConfig, prevMode: string) => {
    if (next.mode !== prevMode) {
      const saved = preferred[next.mode];
      set({ ...next, ducklings: saved && saved.length > 0 ? [...saved] : next.ducklings });
      return;
    }
    set(next);
  };
  const [busy, setBusy] = useState(false);
  const [started, setStarted] = useState<string | null>(null);
  // Accepted work is not waiting to be built. The controls follow the task's
  // state rather than being offered whatever it is.
  const accepted = task.status === "accepted";
  // What may be started from here, stated by the engine. The conditionals
  // below render from this list — the client stopped encoding "todo means
  // Build it" the day those rules were wrong for the fourth time
  // (docs/ux-evaluation.md §5.4).
  const next = task.next ?? [];
  // And neither is work a run is already doing. Two runs against one task edit
  // the same tree at the same time, and the second one's diff contains the
  // first one's changes — which is not a result anybody can judge.
  const running = task.status === "in_progress";
  const [failure, setFailure] = useState<string | null>(null);
  // The run doing the work, from the store the stream feeds — so the link is
  // there whether this window started the run or an operator did, and
  // survives leaving the view and coming back. `started` only remembered a
  // launch made in this mount.
  // The run waiting at its gate for THIS task, decidable here: the person
  // launched from this rail and the decision should come back to it — the
  // trip to the run view is for reading the diff, not for pressing Accept.
  const pausedRun = useRuns((s) => {
    return (
      Object.values(s.runs).find(
        (r) =>
          r.project_id === projectId &&
          r.task_id === task.id &&
          r.status === "paused" &&
          r.pending_kind === "gate",
      ) ?? null
    );
  });
  const acceptSt = useRuns((s) => (pausedRun ? s.acceptState[pausedRun.id] : undefined));
  const activeRunId = useRuns((s) => {
    const active = Object.values(s.runs).find(
      (r) =>
        r.project_id === projectId &&
        r.task_id === task.id &&
        (r.status === "running" || r.status === "queued"),
    );
    return active?.id ?? null;
  });

  const go = async (what: "run" | "test" | "tdd" | "review", opts?: LaunchOpts) => {
    setBusy(true);
    setFailure(null);
    try {
      const run =
        what === "run"
          ? await client.runStart(projectId, task.id, opts ?? { mode: "solo", ducklings: chosen })
          : what === "tdd"
            ? await client.testStart(projectId, task.id, "", {
                thenBuild: true,
                testMode: testCfg.mode,
                testDucklings: testCfg.ducklings.filter(Boolean),
                mode: buildCfg.mode,
                ducklings: buildCfg.ducklings.filter(Boolean),
                maxTokens: buildCfg.maxTokens,
              })
            : what === "test"
              ? await client.testStart(projectId, task.id, chosen[0] ?? "")
              : await client.reviewStart(projectId, task.id);
      setStarted(run.id);
      // The card moves NOW: starting a run puts the task in_progress on the
      // engine, and a board that waits for a view change to notice is a board
      // reporting the past. onDone reloads tasks and bugs.
      onDone();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // The rail renders the engine's actions IN THE ORDER STATED — the order IS
  // the workflow. On a fresh tests-gated task the engine puts test_first
  // before run: the definition of done comes before the work. The first
  // action reads as the step to take; the rest follow. Before this, the
  // controls sat in a fixed layout — gate first, launcher, then Test first at
  // the bottom — which read as "build now, test-first is an afterthought",
  // the exact inversion of the flow the person wanted.
  const ordered = next.filter((a) => a !== "remove");
  const tddPrimary = ordered[0] === "test_first";
  const renderAction = (action: string, primary: boolean) => {
    switch (action) {
      case "run":
        // Under a primary TDD block the build's controls and the build-only
        // link already live inside it; rendering them again would be the
        // same decision twice.
        if (tddPrimary && !accepted) {
          return null;
        }
        if (accepted) {
          return (
            <button
              key={action}
              type="button"
              onClick={() => void go("run")}
              disabled={busy}
              data-testid="run-again"
              title="Starts another run against a task that is already done"
              className="text-xs text-ink-muted underline"
            >
              build again
            </button>
          );
        }
        return (
          <div key={action} className={primary ? "" : "opacity-80"}>
            {/* Opens on the Settings build default with its saved seats — the
                TDD block learned this and the plain launcher was left on
                solo, so a person's pair habit held in one rendering of the
                rail and not the other. Keyed so a default arriving after
                mount still lands (read-a-prop-once, again). */}
            <RunLauncher
              key={phaseDefaults.build}
              ducklings={ducklings}
              initialMode={phaseDefaults.build}
              initialDucklings={preferred[phaseDefaults.build] ?? []}
              preferred={preferred}
              estimates={estimates}
              busy={busy}
              onDucklingsChange={setChosen}
              onLaunch={(opts) => void go("run", opts)}
            />
          </div>
        );
      case "test_first":
        // As the primary act, TDD is ONE click for the whole intent — and
        // each phase carries its OWN mode and seats, because a person who
        // pairs the build does not owe the test a pair too: the test phase
        // defaults to solo (cheap), the build to whatever is worth its cost.
        if (primary) {
          return (
            <div key={action} className="space-y-2 rounded border border-hairline p-2" data-testid="tdd-block">
              <div>
                <div className="text-xs font-medium text-ink-muted">1 · write the failing test</div>
                <LaunchConfig
                  ducklings={ducklings}
                  value={testCfg}
                  onChange={(next) => reseat(setTestCfg)(next, testCfg.mode)}
                  modes={["solo", "pair"]}
                />
              </div>
              <div>
                <div className="text-xs font-medium text-ink-muted">2 · build until it passes</div>
                <LaunchConfig
                  ducklings={ducklings}
                  value={buildCfg}
                  onChange={(next) => reseat(setBuildCfg)(next, buildCfg.mode)}
                  estimates={estimates}
                  showTokens
                />
              </div>
              <button
                type="button"
                onClick={() => void go("tdd")}
                disabled={busy}
                data-testid="tdd-start"
                className="w-full rounded border border-good px-3 py-1.5 text-sm text-good disabled:opacity-40"
              >
                {busy ? "Starting…" : "Test first → Build"}
              </button>
              <div className="flex items-center gap-3 text-xs">
                <button
                  type="button"
                  onClick={() => void go("test")}
                  disabled={busy}
                  data-testid="test-first-start"
                  title="Write the failing test only; you accept it before any build"
                  className="text-ink-muted underline disabled:opacity-40"
                >
                  test only
                </button>
                <span className="text-ink-muted">·</span>
                <button
                  type="button"
                  onClick={() => void go("run", { mode: buildCfg.mode, ducklings: buildCfg.ducklings.filter(Boolean), maxTokens: buildCfg.maxTokens })}
                  disabled={busy}
                  data-testid="build-only"
                  title="Build without a new test — the gate still judges the whole suite"
                  className="text-ink-muted underline disabled:opacity-40"
                >
                  build only
                </button>
              </div>
            </div>
          );
        }
        return (
          <button
            key={action}
            type="button"
            onClick={() => void go("test")}
            disabled={busy}
            data-testid="test-first-start"
            title="Write the failing test first, by a model that will not implement it"
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            Test first
          </button>
        );
      case "review":
        return (
          <button
            key={action}
            type="button"
            onClick={() => void go("review")}
            disabled={busy}
            data-testid="review-start"
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            Review
          </button>
        );
      default:
        return null;
    }
  };

  return (
    <div className="space-y-2 rounded border border-hairline p-2" data-testid="task-runner">
      {pausedRun && (
        <ul className="list-none">
          <WaitingCard
            run={pausedRun}
            accepting={acceptSt?.kind === "pending"}
            onAccept={() => {
              const store = useRuns.getState();
              store.beginAccept(pausedRun.id);
              client
                .accept(pausedRun.id)
                .then((res) => {
                  store.confirmAccept(pausedRun.id, res.commit_sha);
                  onDone();
                })
                .catch((e) =>
                  store.failAccept(pausedRun.id, e instanceof Error ? e.message : String(e)),
                );
            }}
            onReject={() =>
              void client
                .reject(pausedRun.id)
                .then(() => onDone())
                .catch(() => {})
            }
            acceptError={acceptSt?.kind === "error" ? acceptSt.message : undefined}
          />
        </ul>
      )}
      <div className="space-y-2" data-testid="task-actions">
        {ordered.map((a, i) => renderAction(a, i === 0))}
      </div>

      {running && (
        <p className="text-xs text-ink-muted" data-testid="running-note">
          A run is working on this task right now.{" "}
          {activeRunId ? (
            <>
              <a
                href={`#/runs/${activeRunId}`}
                data-testid="running-link"
                className="text-ink underline"
              >
                Watch {activeRunId}
              </a>
              {" · "}
              <button
                type="button"
                data-testid="abort-run"
                onClick={() =>
                  void client
                    .abort(activeRunId)
                    .then(() => onDone())
                    .catch(() => {})
                }
                className="underline"
              >
                abort
              </button>
            </>
          ) : (
            "Watch it"
          )}{" "}
          before starting another.
        </p>
      )}

      {/* The gate is context, not an action: what will judge the work, and a
          button to run it now. It opened the rail once, which made "check
          now" read as the first step of the workflow. */}
      <GateState client={client} projectId={projectId} gate={gate} command={gateCommand} />

      {/* Only while nothing has run it. The engine refuses afterwards — the
          runs, the reports and the spine all name the task — and a button that
          only ever errors is worse than none. */}
      {next.includes("remove") && (
        <RemoveTask task={task} client={client} projectId={projectId} onDone={onDone} />
      )}

      {accepted && (
        <p className="text-xs text-ink-muted" data-testid="accepted-note">
          Already accepted. Reviewing reads the commit; building again starts a new run against
          work that is already done.
        </p>
      )}
      {!accepted && task.test_ready && (
        <p className="text-xs text-good" data-testid="test-ready-note">
          The failing test is committed: it defines done for this task. Build it to make the
          test pass.
        </p>
      )}


      {failure && (
        <p className="text-xs text-critical" data-testid="run-error">
          {failure}
        </p>
      )}
      {started && (
        <a href={`#/runs/${started}`} data-testid="run-link" className="text-xs text-ink underline">
          watch run {started}
        </a>
      )}
    </div>
  );
}

function BugRail({
  bug,
  client,
  projectId,
  onDone,
}: {
  bug: Bug;
  client: EngineClient;
  projectId: string;
  onDone: () => void;
}) {
  return (
    <div className="space-y-2" data-testid="bug-rail">
      <div className="font-mono text-xs text-ink-muted">{bug.id}</div>
      <div className="text-sm font-medium text-ink">{bug.title}</div>
      <StatusChip role={severityRole(bug.severity)} label={bug.severity} />
      <dl className="space-y-1 text-xs text-ink-muted">
        <Row label="status" value={bug.status.replace("_", " ")} />
        <Row label="reported by" value={bug.reporter} />
        <Row label="source" value={bug.source} />
        <Row label="duplicate of" value={bug.duplicate_of} />
        <Row label="task" value={bug.task_id} />
      </dl>
      <BugBody bug={bug} client={client} projectId={projectId} onDone={onDone} />
      {/* What to do next depends on where the bug is, and the loop's rules live
          in the engine. This used to print the CLI command that fits — honest,
          but it made the operate loop the one loop a desktop-only user could not
          run. The engine refuses a transition it does not allow, so the button
          acts and the refusal is what gets shown. */}
      <BugNext bug={bug} client={client} projectId={projectId} onDone={onDone} />
    </div>
  );
}

function Row({ label, value }: { label: string; value?: string }) {
  if (!value) return null;
  return (
    <div className="flex gap-2">
      <dt className="w-24 shrink-0">{label}</dt>
      <dd className="text-ink-secondary">{value}</dd>
    </div>
  );
}

/** What will decide this run, and whether it passes right now.
 *
 * The state is not fetched on load. A gate is a whole test suite on a real
 * project, and a panel that ran one every time someone clicked a task would
 * make looking expensive — which is how people stop looking. So the command is
 * always shown and the answer is one click away.
 *
 * Knowing the gate is red before starting is what makes a green afterwards
 * mean anything: red to green is the run doing something, green to green is a
 * run that may have done nothing at all.
 */
function GateState({
  client,
  projectId,
  gate,
  command,
}: {
  client: EngineClient;
  projectId: string;
  gate: string;
  command: string;
}) {
  const [result, setResult] = useState<GateResult | null>(null);
  const [running, setRunning] = useState(false);

  if (!gate || gate === "none") {
    return (
      <div className="text-xs text-serious" data-testid="gate-state">
        No gate — this run can only reach UNVERIFIED.
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs" data-testid="gate-state">
      <span className="text-ink-muted">gate</span>
      <span className="min-w-0 break-all font-mono text-ink-secondary">{command || gate}</span>
      {result ? (
        <StatusChip
          role={result.green ? "good" : "critical"}
          label={result.green ? "green now" : "red now"}
        />
      ) : (
        <button
          type="button"
          onClick={() => {
            setRunning(true);
            client
              .gateRun(projectId)
              .then(setResult)
              .catch(() => setResult(null))
              .finally(() => setRunning(false));
          }}
          disabled={running}
          data-testid="gate-check"
          className="rounded border border-hairline px-2 py-0.5 disabled:opacity-40"
        >
          {running ? "running…" : "check now"}
        </button>
      )}
      {result && !result.green && (
        <span className="text-ink-muted">red before the run — a green afterwards means something</span>
      )}
    </div>
  );
}

/** The one action a bug's state allows, as a button rather than a command to
 * copy. */
function BugNext({
  bug,
  client,
  projectId,
  onDone,
}: {
  bug: Bug;
  client: EngineClient;
  projectId: string;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [startedRun, setStartedRun] = useState<string | null>(null);

  const act = (fn: () => Promise<unknown>) => {
    setBusy(true);
    setFailure(null);
    void fn()
      .then((out) => {
        const id = (out as { id?: string; task?: string } | undefined)?.id;
        if (id) setStartedRun(id);
        onDone();
      })
      .catch((e) => setFailure(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <div className="space-y-2" data-testid="bug-next">
      {bug.status === "open" && (
        <button
          type="button"
          data-testid="bug-next-triage"
          disabled={busy}
          onClick={() => act(() => client.triageBugs(projectId))}
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          Triage
        </button>
      )}
      {bug.status === "triaged" && (
        <button
          type="button"
          data-testid="bug-next-promote"
          disabled={busy}
          onClick={() => act(() => client.promoteBug(projectId, bug.id))}
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          Make it a task
        </button>
      )}
      {bug.task_id && (
        <p className="text-xs text-ink-muted">
          Tracked as{" "}
          <a href="#/board" className="text-ink underline">
            {bug.task_id}
          </a>
          . Run it from the tasks board.
        </p>
      )}
      {/* Every legal move, from the engine's own table. Without these a report
          could reach a state the rail had no case for — a fixed bug sat at
          in_progress with nothing to click and no way to move it by hand. */}
      {(bug.next ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-1" data-testid="bug-moves">
          <span className="text-xs text-ink-muted">move to</span>
          {(bug.next ?? []).map((to) => (
            <button
              key={to}
              type="button"
              data-testid={`bug-move-${to}`}
              disabled={busy}
              onClick={() => act(() => client.moveBug(projectId, bug.id, to))}
              className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
            >
              {to.replace("_", " ")}
            </button>
          ))}
        </div>
      )}
      {startedRun && (
        <p className="text-xs">
          <a href={`#/runs/${startedRun}`} data-testid="bug-next-run" className="text-ink underline">
            watch the run
          </a>
        </p>
      )}
      {failure && (
        <p className="text-xs text-critical" data-testid="bug-next-error">
          {failure}
        </p>
      )}
    </div>
  );
}

/** A report's words, and a way to correct them.
 *
 * A report is written by a person in a hurry, from memory, often before they
 * have looked. It could be moved, triaged and promoted but never edited — so a
 * typo or a missing detail lived as long as the bug did, and the triager, and
 * then the implementer, worked from it. */
function BugBody({
  bug,
  client,
  projectId,
  onDone,
}: {
  bug: Bug;
  client: EngineClient;
  projectId: string;
  onDone: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(bug.title);
  const [body, setBody] = useState(bug.body ?? "");
  const [severity, setSeverity] = useState(bug.severity);
  const [failure, setFailure] = useState<string | null>(null);

  if (!editing) {
    return (
      <div className="space-y-1">
        {bug.body && <p className="whitespace-pre-wrap text-sm text-ink-secondary">{bug.body}</p>}
        <button
          type="button"
          data-testid="bug-edit"
          onClick={() => {
            setTitle(bug.title);
            setBody(bug.body ?? "");
            setSeverity(bug.severity);
            setEditing(true);
          }}
          className="text-xs text-ink-muted underline"
        >
          edit report
        </button>
      </div>
    );
  }

  return (
    <div className="space-y-2" data-testid="bug-edit-form">
      <input
        aria-label="bug title"
        data-testid="bug-edit-title"
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
      />
      <textarea
        aria-label="bug body"
        data-testid="bug-edit-body"
        rows={4}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        className="w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
      />
      <div className="flex flex-wrap items-center gap-2">
        <select
          aria-label="severity"
          data-testid="bug-edit-severity"
          value={severity}
          onChange={(e) => setSeverity(e.target.value)}
          className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
        >
          {["critical", "high", "normal", "low"].map((sv) => (
            <option key={sv} value={sv}>
              {sv}
            </option>
          ))}
        </select>
        <button
          type="button"
          data-testid="bug-edit-save"
          disabled={!title.trim()}
          onClick={() => {
            setFailure(null);
            void client
              .bugEdit(projectId, bug.id, { title: title.trim(), body: body.trim(), severity })
              .then(() => {
                setEditing(false);
                onDone();
              })
              .catch((e) => setFailure(e instanceof Error ? e.message : String(e)));
          }}
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
        >
          Save
        </button>
        <button
          type="button"
          onClick={() => setEditing(false)}
          className="text-xs text-ink-muted underline"
        >
          cancel
        </button>
      </div>
      {failure && (
        <p className="text-xs text-critical" data-testid="bug-edit-error">
          {failure}
        </p>
      )}
    </div>
  );
}

/** Taking a task back out of the plan.
 *
 * What it is for is undoing a promotion — a bug turned into work before its
 * triage had run, so the task carries the reporter's prose and none of what was
 * worked out. Removing it puts the report back where it was, ready to be
 * promoted again with everything since. */
function RemoveTask({
  task,
  client,
  projectId,
  onDone,
}: {
  task: Task;
  client: EngineClient;
  projectId: string;
  onDone: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  if (!confirming) {
    return (
      <button
        type="button"
        data-testid="task-remove"
        onClick={() => setConfirming(true)}
        className="text-xs text-ink-muted underline"
      >
        remove from plan
      </button>
    );
  }
  return (
    <div className="space-y-1" data-testid="task-remove-confirm">
      <p className="text-xs text-ink-secondary">
        Remove {task.id} from the plan? If it came from a report, the report goes
        back to triaged.
      </p>
      <div className="flex items-center gap-2">
        <button
          type="button"
          data-testid="task-remove-yes"
          onClick={() => {
            setFailure(null);
            void client
              .taskRemove(projectId, task.id)
              .then(() => onDone())
              .catch((e) => setFailure(e instanceof Error ? e.message : String(e)));
          }}
          className="rounded border border-critical px-2 py-1 text-xs text-critical"
        >
          Remove
        </button>
        <button
          type="button"
          onClick={() => setConfirming(false)}
          className="text-xs text-ink-muted underline"
        >
          keep it
        </button>
      </div>
      {failure && (
        <p className="text-xs text-critical" data-testid="task-remove-error">
          {failure}
        </p>
      )}
    </div>
  );
}
