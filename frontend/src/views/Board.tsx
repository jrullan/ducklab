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
import type { Bug, Duckling, EngineClient, GateResult, Task } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusChip } from "../components/StatusChip";

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
  const [board, setBoard] = useState<"tasks" | "bugs">(tab === "bugs" ? "bugs" : "tasks");
  const [tasks, setTasks] = useState<Task[]>([]);
  // Needed to offer a choice of ducklings when starting a run. Failing to load
  // them is not worth blocking the board over: with none, the roster decides.
  const [ducklings, setDucklings] = useState<Duckling[]>([]);
  // Which gate the project has, so the rail offers only what can work here.
  const [gate, setGate] = useState("");
  const [gateCommand, setGateCommand] = useState("");
  const [bugs, setBugs] = useState<Bug[]>([]);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
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
  const current = isBugs
    ? (bugs.find((b) => b.id === selected) ?? null)
    : (tasks.find((t) => t.id === selected) ?? null);

  if (!loading && !failure && tasks.length === 0 && bugs.length === 0) {
    return (
      <EmptyState message="Nothing here yet — run `ducklab plan` for tasks, or `ducklab bug add` to file a report." />
    );
  }

  return (
    <div data-testid="board-view" className="flex gap-4">
      <div className="min-w-0 flex-1">
        {failure && (
          <div data-testid="board-error" className="mb-3 text-sm text-critical">
            {failure}
          </div>
        )}

        {/* Filters in one row above the board (08 §4.3). */}
        <div className="mb-3 flex items-center gap-2">
          {(["tasks", "bugs"] as const).map((b) => (
            <button
              key={b}
              data-testid={`board-toggle-${b}`}
              aria-pressed={board === b}
              onClick={() => {
                setBoard(b);
                // The selection belongs to the board that made it: keeping it
                // would leave the rail describing a task while the bugs are
                // on screen.
                setSelected(null);
              }}
              className={
                "rounded border px-2 py-1 text-sm " +
                (board === b ? "border-ink text-ink" : "border-hairline text-ink-muted")
              }
            >
              {b} <span className="text-ink-muted">{b === "bugs" ? bugs.length : tasks.length}</span>
            </button>
          ))}

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
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>
      </div>

      <aside data-testid="board-rail" className="w-72 shrink-0">
        {current === null ? (
          <p className="text-sm text-ink-muted">
            Select {isBugs ? "a bug" : "a task"} to see its record.
          </p>
        ) : isBugs ? (
          <BugRail bug={current as Bug} />
        ) : (
          <TaskRail
            task={current as Task}
            client={client}
            projectId={projectId}
            ducklings={ducklings}
            gate={gate}
            gateCommand={gateCommand}
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
  gate,
  gateCommand,
}: {
  task: Task;
  client: EngineClient;
  projectId: string;
  ducklings: readonly Duckling[];
  gate: string;
  gateCommand: string;
}) {
  return (
    <div className="space-y-2">
      <div className="font-mono text-xs text-ink-muted">{task.id}</div>
      <div className="text-sm font-medium text-ink">{task.title}</div>
      <dl className="space-y-1 text-xs text-ink-muted">
        <Row label="status" value={task.status.replace("_", " ")} />
        <Row label="milestone" value={task.milestone} />
        <Row label="implements" value={task.implements?.join(", ")} />
        <Row label="depends on" value={task.depends_on?.join(", ")} />
      </dl>
      {task.body && <p className="whitespace-pre-wrap text-sm text-ink-secondary">{task.body}</p>}
      <TaskRunner
        task={task}
        client={client}
        projectId={projectId}
        ducklings={ducklings}
        gate={gate}
        gateCommand={gateCommand}
      />
    </div>
  );
}

const MODES = ["solo", "pair", "tournament", "split"] as const;

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
  gate,
  gateCommand,
}: {
  task: Task;
  client: EngineClient;
  projectId: string;
  ducklings: readonly Duckling[];
  gate: string;
  gateCommand: string;
}) {
  const [mode, setMode] = useState<string>("solo");
  const [chosen, setChosen] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [started, setStarted] = useState<string | null>(null);
  // Accepted work is not waiting to be built. The controls follow the task's
  // state rather than being offered whatever it is.
  const accepted = task.status === "accepted";
  const [failure, setFailure] = useState<string | null>(null);

  const go = async (what: "run" | "test" | "review") => {
    setBusy(true);
    setFailure(null);
    try {
      const run =
        what === "run"
          ? await client.runStart(projectId, task.id, { mode, ducklings: chosen })
          : what === "test"
            ? await client.testStart(projectId, task.id, chosen[0] ?? "")
            : await client.reviewStart(projectId, task.id);
      setStarted(run.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-2 rounded border border-hairline p-2" data-testid="task-runner">
      <GateState client={client} projectId={projectId} gate={gate} command={gateCommand} />
      <div className="flex flex-wrap items-center gap-2">
        {/* A task that is already accepted is not waiting to be built, and its
            mode and duckling pickers are apparatus for a decision nobody is
            making. Building again is still possible — a result can be
            regretted — but it says what it is rather than sitting there as the
            obvious next step. */}
        {!accepted && (
          <select
            aria-label="mode"
            data-testid="run-mode"
            value={mode}
            onChange={(e) => setMode(e.target.value)}
            className="rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
          >
            {MODES.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        )}
        {!accepted && (
          <button
            type="button"
            onClick={() => void go("run")}
            disabled={busy}
            data-testid="run-start"
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            {busy ? "Starting…" : "Build it"}
          </button>
        )}
        {/* Only where a test would change something the gate can see. A
            compiler, a linter or a bespoke script gives a new test nothing to
            hook into, and the engine refuses — so the button is absent rather
            than present and failing. */}
        {gate === "tests" && !accepted && (
          <button
            type="button"
            onClick={() => void go("test")}
            disabled={busy}
            data-testid="test-first-start"
            title="Write the failing test first, by a model that will not implement it"
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            Test first
          </button>
        )}
        {/* Only on work that has been accepted: a review reads the commit, and
            there is no commit until then. The engine refuses with exactly
            that, and a button that only ever errors is worse than none. */}
        {accepted && (
          <button
            type="button"
            onClick={() => void go("review")}
            disabled={busy}
            data-testid="review-start"
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            Review
          </button>
        )}
        {accepted && (
          <button
            type="button"
            onClick={() => void go("run")}
            disabled={busy}
            data-testid="run-again"
            title="Starts another run against a task that is already done"
            className="text-xs text-ink-muted underline"
          >
            build again
          </button>
        )}
      </div>

      {accepted && (
        <p className="text-xs text-ink-muted" data-testid="accepted-note">
          Already accepted. Reviewing reads the commit; building again starts a new run against
          work that is already done.
        </p>
      )}

      {!accepted && ducklings.length > 1 && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-ink-secondary">
          {ducklings.map((d) => (
            <label key={d.id} className="flex items-center gap-1">
              <input
                type="checkbox"
                data-testid={`run-duckling-${d.id}`}
                checked={chosen.includes(d.id)}
                onChange={(e) =>
                  setChosen((cur) =>
                    e.target.checked ? [...cur, d.id] : cur.filter((x) => x !== d.id),
                  )
                }
              />
              {d.id}
            </label>
          ))}
          <span className="text-ink-muted">
            {chosen.length === 0 ? "none chosen — the roster decides" : "in this order"}
          </span>
        </div>
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

function BugRail({ bug }: { bug: Bug }) {
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
      {bug.body && <p className="whitespace-pre-wrap text-sm text-ink-secondary">{bug.body}</p>}
      {/* What to do next depends on where the bug is, and the loop's rules
          live in the engine. The command that fits is shown rather than a
          button that might be refused. */}
      <div className="rounded border border-hairline p-2 font-mono text-xs text-ink-secondary">
        {bug.status === "open"
          ? "ducklab bug triage"
          : bug.status === "triaged"
            ? `ducklab bug promote ${bug.id}`
            : bug.task_id
              ? `ducklab run ${bug.task_id} --mode pair`
              : `ducklab bug status ${bug.id} <status>`}
      </div>
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
      <span className="font-mono text-ink-secondary">{command || gate}</span>
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
