/**
 * The Board: tasks and bugs in columns, with a rail on selection (08 §4.3).
 *
 * There is no drag-to-move. A task's status is derived from its run records,
 * not stored on the task, so there is nothing for a drop to write — and that
 * derivation is deliberate: status kept in the plan document would let a model
 * rewriting the plan mark its own work accepted (I2). Moving a card means
 * running the task, so `Run` is the action the rail offers.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRuns } from "../store/runs";
import type { Bug, Duckling, EngineClient, GateResult, RosterEntry, Task } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { Prose } from "../components/Prose";
import { StatusChip } from "../components/StatusChip";
import { WaitingCard } from "../components/WaitingCard";
import { RunLauncher, type LaunchOpts, type ModeEstimates, type PhaseConfig } from "../components/RunLauncher";
import type { MeasuredSpend } from "../components/SeatChips";
import { TddLaunch } from "../components/TddLaunch";
import { ChatAbout } from "../components/ChatAbout";
import { RemoveTask } from "../components/RemoveTask";

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
const ARCHIVE_FOLD_AT = 6;

const BUG_COLUMNS = [
  { key: "open", label: "Open" },
  { key: "triaged", label: "Triaged" },
  { key: "in_progress", label: "In progress" },
  { key: "fixed", label: "Fixed" },
  { key: "verified", label: "Verified" },
] as const;

/** Severity as a status role (08 §4.3). */
/** Severity at a glance, one glyph each — the app-wide status icons (✕ ▪ ⚠)
 *  say "state", not "how bad"; a bug card is scanned for how bad. */
function SeverityChip({ severity }: { severity: string }) {
  const glyph = severity === "critical" ? "▲" : severity === "high" ? "●" : severity === "low" ? "·" : "○";
  return (
    <span data-testid="severity-chip" data-severity={severity} className="inline-flex items-center gap-1 text-xs" style={{ color: `var(--status-${severityRole(severity)})` }}>
      <span aria-hidden="true">{glyph}</span>
      <span>{severity}</span>
    </span>
  );
}

/** "3d", "5h", "just now": how long a report has sat where it is. */
export function ageOf(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 3600) return s < 120 ? "just now" : `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

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
  // The launchers open on the configured mode defaults.
  const [phaseDefaults, setPhaseDefaults] = useState<{ build: string; test: string }>({
    build: "solo",
    test: "solo",
  });
  // What each mode has cost in THIS project, for the launcher's mode picker.
  const [estimates, setEstimates] = useState<ModeEstimates>({});
  // Measured spend per duckling, for the seat chips on every launcher.
  const [measured, setMeasured] = useState<MeasuredSpend>({});
  // Filing a report was reachable only from the CLI: the engine has had
  // POST /bugs since the operate loop was built, and the board's own empty
  // state told you to go and run `ducklab bug add`. On a desktop-only setup the
  // whole loop was unreachable.
  const [filing, setFiling] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [bugTitle, setBugTitle] = useState("");
  const [bugBody, setBugBody] = useState("");
  const [bugSeverity, setBugSeverity] = useState("normal");
  // Screenshots to ride the report: read as base64 and attached right after
  // the bug is filed. Visual evidence for the human — and for a triager with
  // vision, which is shown the images themselves.
  const [bugFiles, setBugFiles] = useState<File[]>([]);
  const [bugError, setBugError] = useState<string | null>(null);
  const [triageRun, setTriageRun] = useState<string | null>(null);
  // The banner follows the run it announced: "started — watch it" is a lie
  // once the triage is done and the verdicts are already on the board.
  const triageStatus = useRuns((s) => (triageRun ? s.runs[triageRun]?.status : undefined));
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

  const load = useCallback(async (summary = false) => {
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
      try {
        const rep = await client.report(projectId, "duckling");
        const m: MeasuredSpend = {};
        for (const row of rep.rows) m[row.key] = { usd: row.cost_usd, runs: row.runs };
        setMeasured(m);
      } catch {
        setMeasured({});
      }
    })();
    client
      .projectGate(projectId)
      .then((g) => {
        setGate(g.mode);
        setGateCommand(g.command);
      })
      .catch(() => {});
    const [t, b] = await Promise.allSettled([client.tasks(projectId, summary), client.bugs(projectId, false, summary)]);
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

  // Runs started, paused or finished ANYWHERE — the CLI, an MCP operator,
  // another window — move tasks between columns and bugs between statuses.
  // The key carries each run's status, not just its id: keyed on the set of
  // ACTIVE runs, a paused triage being accepted (paused → done, the moment
  // ApplyTriage rewrites every bug) changed nothing the key could see, and
  // the Bugs board kept its pre-triage columns. Terminal statuses are
  // stable, so a finished run stops moving the key after its one last change.
  const activeRunKey = useRuns((s) =>
    Object.values(s.runs)
      .filter((r) => r.project_id === projectId)
      .map((r) => `${r.id}:${r.status}`)
      .sort()
      .join(","),
  );
  // Chained runs can emit several transitions together. Board lists are only
  // snapshots, so one trailing summary refresh is enough for the whole burst.
  const transitionRefresh = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (transitionRefresh.current) clearTimeout(transitionRefresh.current);
    transitionRefresh.current = setTimeout(() => {
      transitionRefresh.current = null;
      void load(true);
    }, 250);
    return () => {
      if (transitionRefresh.current) clearTimeout(transitionRefresh.current);
    };
  }, [activeRunKey, load]);

  // Summary lists deliberately omit bulky report bodies and audit trails. Read
  // the selected bug in full; the card list never needs that payload.
  useEffect(() => {
    if (!selected || board !== "bugs" || typeof client.bug !== "function") return;
    void client.bug(projectId, selected).then((detail) => {
      setBugs((current) => current.map((bug) => (bug.id === detail.id ? detail : bug)));
    }).catch(() => {});
  }, [board, client, projectId, selected]);

  // The same courtesy for tasks. A summary reload after a run transition
  // empties every task body, and the detail rail reads the body from that
  // list — a task selected right after a plan-amend showed a title and no
  // words. There is no single-task endpoint, so the full list is fetched
  // once, only when the selected task's body is missing.
  // Keyed on "the selected task has no body", not on the selection alone:
  // with a run in flight, every event refreshes the summary list and empties
  // the bodies again, and a refill that only fired on selection left the
  // panel wordless until the person clicked another card.
  const selectedBodyMissing = board === "tasks" && !!selected && !(tasks.find((t) => t.id === selected)?.body);
  useEffect(() => {
    if (!selected || board !== "tasks" || !selectedBodyMissing) return;
    void client.tasks(projectId, false).then((full) => {
      setTasks((current) => current.map((t) => full.find((f) => f.id === t.id) ?? t));
    }).catch(() => {});
    // tasks is read, not depended on: refilling must not re-trigger itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [board, client, projectId, selected, selectedBodyMissing]);

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
  const specDebtCount = tasks.filter((t) => t.status === "accepted" && t.spec_debt).length;
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
    <div data-testid="board-view" className="flex items-start gap-4">
      <div className="min-w-0 flex-1">
        {failure && (
          <div data-testid="board-error" className="mb-3 text-sm text-critical">
            {failure}
          </div>
        )}

        {/* One line, above the columns: the answer, not a nudge. It disappears
            when there is nothing ready, which is itself the answer — everything
            is done, running, or waiting on something. */}
        {!isBugs && next && selected !== next.id && (
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

        {/* Said once, not sixty-seven times: a label of exception on every
            accepted card stops meaning anything. The count and the door to
            settle it, above the board. */}
        {!isBugs && specDebtCount > 0 && (
          <p className="mb-3 text-xs text-warn" data-testid="spec-debt-banner">
            {specDebtCount} accepted task{specDebtCount === 1 ? "" : "s"} carr{specDebtCount === 1 ? "ies" : "y"} spec-debt — no spec section covers {specDebtCount === 1 ? "it" : "them"} yet;{" "}
            <a href="#/cycle/spec" className="underline">settle the spec</a> to teach it what was built.
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
        <div className="mb-3 flex flex-wrap items-center gap-2">
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
          {/* Filters read left, actions sit right; the count belongs to the
              filter it describes. */}
          <span className={`text-xs ${shownCount !== total ? "font-medium text-ink" : "text-ink-muted"}`}>
            {shownCount === total ? `all ${total}` : `${shownCount} of ${total}`}
          </span>
          {isBugs && (
            <span className="ml-auto flex items-center gap-2">
              <button
                type="button"
                data-testid="bug-file"
                onClick={() => setFiling((v) => !v)}
                className="rounded border border-hairline px-2 py-1 text-sm text-ink-muted hover:text-ink"
              >
                {filing ? "Cancel" : "File a bug"}
              </button>
              {/* Triage is the step that turns a report into something a run can be
                  pointed at: severity, suspected files, whether it duplicates
                  another. It reads every open bug, so it is one action, not one per
                  report — and it is THE action of this board, so it reads as one. */}
              {bugs.some((b) => b.status === "open") && (
                <button
                  type="button"
                  data-testid="bug-triage"
                  onClick={() =>
                    void client
                      .triageBugs(projectId)
                      .then((r) => {
                        setTriageRun(r.id);
                        // Seed the store with the engine's own record instead of
                        // waiting for the stream's run_start to race back: the
                        // refetch key sees the run (and later its ending) even if
                        // this tab's stream reconnects mid-triage.
                        useRuns.getState().setRun(r);
                      })
                      .catch((e) => setBugError(e instanceof Error ? e.message : String(e)))
                  }
                  className="rounded border border-ink px-3 py-1 text-sm font-medium text-ink"
                >
                  Triage all open ({bugs.filter((b) => b.status === "open").length})
                </button>
              )}
            </span>
          )}
        </div>

        {filing && (
          <div
            className="mb-3 space-y-2 rounded-card border border-hairline p-3"
            data-testid="bug-form"
            // A screenshot DROPPED on the form did nothing, silently — the
            // reporter walked away sure it was attached, and the bug went to
            // triage imageless (B-036). The whole form is a drop target now.
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              const dropped = Array.from(e.dataTransfer?.files ?? []).filter((f) =>
                f.type.startsWith("image/"),
              );
              if (dropped.length > 0) setBugFiles((prev) => [...prev, ...dropped]);
            }}
          >
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
              <label className="flex items-center gap-1 text-xs text-ink-muted">
                <input
                  type="file"
                  accept="image/*"
                  multiple
                  data-testid="bug-attachments"
                  onChange={(e) => setBugFiles(Array.from(e.target.files ?? []))}
                  className="text-xs"
                />
              </label>
              {/* Named, not counted: "1 image" reads the same whether it is
                  the right screenshot or a stale pick. */}
              {bugFiles.map((f, i) => (
                <span
                  key={`${f.name}-${i}`}
                  data-testid="bug-file-chip"
                  className="flex items-center gap-1 rounded-full border border-hairline px-2 py-0.5 text-xs text-ink-secondary"
                >
                  {f.name}
                  <button
                    type="button"
                    aria-label={`remove ${f.name}`}
                    onClick={() => setBugFiles(bugFiles.filter((_, j) => j !== i))}
                    className="text-ink-muted"
                  >
                    ✕
                  </button>
                </span>
              ))}
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
                    .then(async (b) => {
                      for (const f of bugFiles) {
                        const dataUrl = await new Promise<string>((res, rej) => {
                          const r = new FileReader();
                          r.onload = () => res(String(r.result));
                          r.onerror = () => rej(new Error(`could not read ${f.name}`));
                          r.readAsDataURL(f);
                        });
                        await client.bugAttach(projectId, b.id, f.name, dataUrl.split(",", 2)[1] ?? "").catch((err) => {
                          // The bug exists; only the evidence failed to ride.
                          // A generic error over an open form hid that split
                          // state completely.
                          throw new Error(
                            `${b.id} was filed, but ${f.name} failed to attach (${err instanceof Error ? err.message : String(err)}) — add it from the bug's gallery`,
                          );
                        });
                      }
                      setBugTitle("");
                      setBugBody("");
                      setBugFiles([]);
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
              {triageStatus === "done"
                ? "triage finished — the reports below carry its verdicts"
                : triageStatus === "paused"
                  ? "triage awaiting your decision — open it"
                  : triageStatus === "failed"
                    ? "triage failed — see why"
                    : "triage started — watch it"}
            </a>
          </p>
        )}

        <div className="flex items-start gap-2">
          {(isBugs ? BUG_COLUMNS : COLUMNS).map((col) => {
            const items = isBugs
              ? shownBugs.filter((b) => b.status === col.key)
              : shownTasks.filter((t) => t.status === col.key);
            // Verified (bugs) and Accepted (tasks) are the archive, not the
            // work: 57 or 67 cards there are permanent noise. Folded to the
            // count by default; empty columns shrink to a strip; the width
            // goes to columns with work.
            const archive = col.key === (isBugs ? "verified" : "accepted");
            // A handful of finished cards is context; dozens are noise. Fold
            // past a handful.
            const folded = archive && !archiveOpen && items.length > ARCHIVE_FOLD_AT;
            const strip = items.length === 0 || folded;
            if (folded) {
              return (
                <section key={col.key} data-testid={`board-col-${col.key}`} className="w-28 shrink-0">
                  <button type="button" data-testid="board-archive-toggle" onClick={() => setArchiveOpen(true)} className="w-full rounded border border-dashed border-hairline px-2 py-2 text-left text-sm text-ink-muted hover:text-ink" title={`show the ${col.label.toLowerCase()} archive`}>
                    {col.label} · {items.length} ›
                  </button>
                </section>
              );
            }
            return (
              <section key={col.key} data-testid={`board-col-${col.key}`} className={strip ? "w-28 shrink-0" : "min-w-0 flex-1"}>
                <h2 className="mb-2 flex items-baseline text-sm text-ink-muted">
                  {col.label}
                  <span className="ml-1 text-ink-muted">{items.length}</span>
                  {archive && archiveOpen && <button type="button" className="ml-auto text-xs underline" data-testid="board-archive-toggle" onClick={() => setArchiveOpen(false)}>fold</button>}
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
                        <div className="flex items-baseline gap-2">
                          <span className="font-mono text-xs text-ink-muted">{it.id}</span>
                          {isBugs && <span className="ml-auto"><SeverityChip severity={(it as Bug).severity} /></span>}
                        </div>
                        {/* Scanned, not read: three lines at most, the whole title
                            on hover and in the detail. */}
                        <div className={"text-sm text-ink" + (isBugs ? " line-clamp-3" : "")} title={isBugs ? it.title : undefined}>{it.title}</div>
                        {isBugs ? (
                          <div className="mt-1 text-[11px] text-ink-muted" data-testid="bug-card-meta" title={`reported ${(it as Bug).created_at ?? ""} by ${(it as Bug).reporter ?? "?"}`}>
                            {[ageOf((it as Bug).created_at), (it as Bug).reporter?.split(":")[0]].filter(Boolean).join(" · ")}
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
                        {/* The mirror of the debt chip: silence needed an
                            explanation. "Why no settle options?" — because
                            the amendment self-wired; now the card says so. */}
                        {!isBugs && !(it as Task).spec_debt && ((it as Task).implements?.length ?? 0) > 0 && (
                          <div
                            data-testid="task-coverage"
                            className="mt-1 text-xs text-ink-muted"
                            title="the spec sections this task implements — its coverage is already wired, nothing to settle"
                          >
                            covered by {(it as Task).implements!.join(", ")}
                          </div>
                        )}
                        {!isBugs && (it as Task).spec_debt && (it as Task).status !== "accepted" && (
                          <div
                            data-testid="spec-debt"
                            className="mt-1 text-xs text-warn"
                            title="no spec section covers this task — the plan amendment's toll; the scribe settles it by teaching the spec what was built"
                          >
                            spec-debt
                          </div>
                        )}
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
                        {/* An in-progress card whose run waits on a hand says
                            so, like Blocked says why. */}
                        {!isBugs && (it as Task).waiting && (
                          <div data-testid="waiting-reason" className="mt-1 text-xs text-serious">
                            {(it as Task).waiting}
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

      {/* Sticky, scrolling on its own: clicking a task at the bottom of a
          long column used to mean scrolling back to the top to find the rail
          that describes it. The kanban and the rail are different documents;
          they scroll like it. */}
      <aside data-testid="board-rail" className="sticky top-2 max-h-[calc(100vh-8rem)] w-72 shrink-0 self-start overflow-y-auto overscroll-contain">
        {current === null ? (
          <p className="text-sm text-ink-muted">
            Select {isBugs ? "a bug" : "a task"} to see its record.
          </p>
        ) : isBugs ? (
          <BugRail ducklings={ducklings} bug={current as Bug} client={client} projectId={projectId} onDone={() => void load()} />
        ) : (
          <TaskRail
            task={current as Task}
            client={client}
            projectId={projectId}
            ducklings={ducklings}
            preferred={preferred}
            phaseDefaults={phaseDefaults}
            estimates={estimates}
            measured={measured}
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
  measured,
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
  measured?: MeasuredSpend;
}) {
  const fixes = /\bFixes\s+(B-\d+)/.exec(task.body ?? "")?.[1];
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
        measured={measured}
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
        {fixes && (
          <div className="flex gap-2"><dt className="w-24 shrink-0">fixes</dt><dd><a href="#/board/bugs" data-testid="task-fixes" className="text-ink underline">{fixes}</a></dd></div>
        )}
      </dl>
      {/* The body is markdown (a promoted bug carries "## Reported"): render
          it, do not print the hashes. */}
      {task.body && (
        <div className="max-h-72 overflow-y-auto overscroll-contain border-t border-hairline pt-2" data-testid="task-body">
          <Prose body={task.body} suppress={[]} />
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
  measured,
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
  measured?: MeasuredSpend;
}) {
  const [chosen, setChosen] = useState<string[]>([]);
  const [roster, setRoster] = useState<RosterEntry[]>([]);
  const [runMode, setRunMode] = useState("solo");
  useEffect(() => {
    const getRoster = client.RosterGet ?? client.rosterGet ?? (() => Promise.resolve({ entries: [] as RosterEntry[] }));
    void getRoster.call(client, projectId, runMode).then((r) => setRoster(r.entries ?? [])).catch(() => setRoster([]));
  }, [client, projectId, runMode]);
  const [busy, setBusy] = useState(false);
  const [started, setStarted] = useState<string | null>(null);
  // The revert commit of a successful retire — the click's answer. The rail
  // reloads and the "failing test committed" message disappears, but absence
  // is not confirmation: the person is owed a sentence that says it happened.
  const [retired, setRetired] = useState<string | null>(null);
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
  // The run this task is IN — running, queued, or paused on something other
  // than a gate (a question, a budget, a provider). The gate case has its own
  // card above; this is everything else a task in flight can be doing, shown
  // where the task is: stage · mode · seats · state, a link, and — for a
  // question — the answer box, advice included. Answering about T-069
  // belongs on T-069.
  const liveRun = useRuns((s) =>
    Object.values(s.runs).find(
      (r) =>
        r.project_id === projectId &&
        r.task_id === task.id &&
        (r.status === "running" || r.status === "queued" || (r.status === "paused" && r.pending_kind !== "gate")),
    ) ?? null,
  );
  const [answer, setAnswer] = useState("");
  const [answerError, setAnswerError] = useState<string | null>(null);

  const go = async (
    what: "run" | "test" | "tdd" | "review",
    opts?: LaunchOpts,
    tdd?: { test: PhaseConfig; build: PhaseConfig },
  ) => {
    setBusy(true);
    setFailure(null);
    try {
      const run =
        what === "run"
          ? await client.runStart(projectId, task.id, opts ?? { mode: "solo", ducklings: chosen })
          : what === "tdd"
            ? await client.testStart(projectId, task.id, "", {
                thenBuild: true,
                testMode: tdd!.test.mode,
                testDucklings: tdd!.test.ducklings.filter(Boolean),
                mode: tdd!.build.mode,
                ducklings: tdd!.build.ducklings.filter(Boolean),
                maxTokens: tdd!.build.maxTokens,
                agentTurns: tdd!.build.agentTurns,
              })
            : what === "test"
              // From the TDD block, the test phase's own config; from the
              // secondary button, the seat picked in the plain launcher.
              ? await (tdd
                  ? client.testStart(projectId, task.id, "", {
                      thenBuild: false,
                      testMode: tdd.test.mode,
                      testDucklings: tdd.test.ducklings.filter(Boolean),
                    })
                  : client.testStart(projectId, task.id, chosen[0] ?? ""))
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
          // The full launcher, not a bare link: rebuilding an accepted task
          // is exactly when the person has something to SAY — "the fix
          // leaked, close every connection" — and the bare button offered no
          // note, no tokens, no seats.
          return (
            <div key={action} data-testid="run-again">
              <RunLauncher
            measured={measured}
                key={phaseDefaults.build}
                ducklings={ducklings}
                initialMode={phaseDefaults.build}
                initialDucklings={preferred[phaseDefaults.build] ?? []}
                preferred={preferred}
                estimates={estimates}
                label="Build again"
                busy={busy}
                roster={roster}
                onLaunch={(opts) => void go("run", opts)}
              />
            </div>
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
            measured={measured}
              key={phaseDefaults.build}
              ducklings={ducklings}
              initialMode={phaseDefaults.build}
              initialDucklings={preferred[phaseDefaults.build] ?? []}
              preferred={preferred}
              estimates={estimates}
              busy={busy}
              onDucklingsChange={setChosen}
              roster={roster}
              onModeChange={setRunMode}
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
            <TddLaunch
            measured={measured}
              key={`${phaseDefaults.test}:${phaseDefaults.build}`}
              ducklings={ducklings}
              preferred={preferred}
              phaseDefaults={phaseDefaults}
              estimates={estimates}
              busy={busy}
              roster={roster}
              onTdd={(t, b) => void go("tdd", undefined, { test: t, build: b })}
              onTestOnly={(t) => void go("test", undefined, { test: t, build: t })}
              onBuildOnly={(b) =>
                void go("run", { mode: b.mode, ducklings: b.ducklings.filter(Boolean), maxTokens: b.maxTokens, agentTurns: b.agentTurns })
              }
            />
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
      case "retire_test":
        // The other exit from a committed failing test: withdraw the promise.
        // A deterministic git revert on the engine — and it releases the
        // project's queue, which the outstanding red test was holding.
        return (
          <button
            key={action}
            type="button"
            onClick={() =>
              void client
                .testRetire(projectId, task.id)
                .then((r) => {
                  setRetired(r.revert_sha ?? "");
                  setFailure(null);
                  onDone();
                })
                .catch((err) => setFailure(err instanceof Error ? err.message : String(err)))
            }
            disabled={busy}
            data-testid="retire-test"
            title="Reverts the committed failing test; the task returns to a clean todo"
            className="text-xs text-ink-muted underline disabled:opacity-40"
          >
            retire the test
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
            onAbort={() =>
              void client
                .abort(pausedRun.id)
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

      {liveRun && (() => {
        const pd = (liveRun.pending_data ?? {}) as Record<string, unknown>;
        const question = typeof pd.question === "string" ? pd.question : "";
        const advice = typeof pd.advice === "string" ? pd.advice : "";
        const advisor = typeof pd.advisor === "string" ? pd.advisor : "";
        const questionId = typeof pd.question_id === "string" ? pd.question_id : "";
        const seats = Object.entries(liveRun.roster ?? {}).filter(([, d]) => d).map(([role, d]) => `${role} ${d}`).join(" · ");
        const state = liveRun.status === "paused" ? `paused: ${liveRun.pending_kind ?? "waiting"}` : liveRun.status;
        const send = (text: string) => {
          if (!text.trim()) return;
          setAnswerError(null);
          client.answer(liveRun.id, questionId, text).then(() => { setAnswer(""); onDone(); }).catch((e) => setAnswerError(e instanceof Error ? e.message : String(e)));
        };
        return (
          <section className="rounded border border-hairline p-2 text-xs" data-testid="task-live-run" data-state={liveRun.status}>
            <div className="flex flex-wrap items-baseline gap-2">
              <StatusChip role={liveRun.status === "paused" ? "serious" : "good"} label={state} />
              <span className="text-ink">{liveRun.stage} · {liveRun.mode}</span>
              {seats && <span className="truncate text-ink-muted" title={seats}>{seats}</span>}
              <a href={`#/runs/${liveRun.id}`} data-testid="task-live-run-link" className="ml-auto text-ink underline">open {liveRun.id}</a>
            </div>
            {question && (
              <div className="mt-2" data-testid="task-question">
                <p className="text-sm text-ink">{question}</p>
                {advice && (
                  <div className="mt-1 rounded border border-hairline p-2">
                    <p className="text-ink-muted">{advisor || "the advisor"} recommends:</p>
                    <p className="mt-1 whitespace-pre-wrap text-ink">{advice}</p>
                    <button type="button" data-testid="task-answer-advice" onClick={() => send(advice)} className="mt-1 rounded border border-good px-2 py-0.5 text-good">Answer with this</button>
                  </div>
                )}
                <div className="mt-1 flex gap-2">
                  <input aria-label="answer" data-testid="task-answer-input" value={answer} onChange={(e) => setAnswer(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") send(answer); }} className="flex-1 rounded border border-hairline bg-surface2 px-2 py-1" placeholder="your answer" />
                  <button type="button" data-testid="task-answer-button" onClick={() => send(answer)} className="rounded border border-hairline px-2 py-1">Answer</button>
                </div>
                {answerError && <p className="mt-1 text-critical">{answerError}</p>}
              </div>
            )}
            {liveRun.status !== "paused" && (
              <p className="mt-1 text-ink-muted">Working now — watch it before starting another.{" "}
                <button type="button" data-testid="abort-run" onClick={() => void client.abort(liveRun.id).then(() => onDone()).catch(() => {})} className="underline">abort</button>
              </p>
            )}
          </section>
        );
      })()}
      {running && !liveRun && (
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
      {/* Each its own line: rendered inline these two links fused into one
          reading — "remove from planchat about this" — and an action that
          deletes should never share a sentence with one that talks. */}
      {/* Chat and remove at the foot, past a hairline: talking about the
          task and deleting it are not launch controls, and the chat used to
          sit inside the gate's box. */}
      <div className="mt-2 flex items-center gap-3 border-t border-hairline pt-2 text-xs">
        <ChatAbout client={client} projectId={projectId} aboutKind="task" aboutId={task.id} ducklings={ducklings} />
        {next.includes("remove") && (
          <span className="ml-auto">
            <RemoveTask task={task} client={client} projectId={projectId} onDone={onDone} />
          </span>
        )}
      </div>

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
      {retired && (
        <p className="text-xs text-good" data-testid="retire-note">
          test retired — its commit was reverted{retired ? ` (${retired.slice(0, 8)})` : ""}
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
  ducklings,
  onDone,
}: {
  bug: Bug;
  client: EngineClient;
  projectId: string;
  ducklings: readonly Duckling[];
  onDone: () => void;
}) {
  return (
    <div className="space-y-3" data-testid="bug-rail">
      <div>
        <div className="flex items-baseline gap-2"><span className="font-mono text-xs text-ink-muted">{bug.id}</span><SeverityChip severity={bug.severity} /><span className="ml-auto text-xs text-ink-muted">{bug.status.replace("_", " ")}</span></div>
        <div className="mt-1 text-sm font-medium text-ink">{bug.title}</div>
      </div>
      {/* Actions first, together, under the title — what to do next depends
          on where the bug is, and the loop's rules live in the engine: the
          button acts and a refusal is what gets shown. They used to sit
          under a long body, past the fold. */}
      <BugNext bug={bug} client={client} projectId={projectId} onDone={onDone} />
      <dl className="space-y-1 text-xs text-ink-muted">
        <Row label="reported by" value={bug.reporter} />
        <Row label="reported" value={bug.created_at ? `${bug.created_at.slice(0, 16).replace("T", " ")} (${ageOf(bug.created_at)} ago)` : undefined} />
        <Row label="source" value={bug.source} />
        <Row label="duplicate of" value={bug.duplicate_of} />
        <Row label="task" value={bug.task_id} />
      </dl>
      <BugBody bug={bug} client={client} projectId={projectId} onDone={onDone} />
      <BugAttachments bug={bug} client={client} projectId={projectId} onChanged={onDone} />
      <BugHistory bug={bug} />
      <ChatAbout client={client} projectId={projectId} aboutKind="bug" aboutId={bug.id} ducklings={ducklings} />
    </div>
  );
}

/** The audit trail: every status transition, signed. B-041 went from fixed
 * back to in_progress overnight and nobody — not even the agent asked
 * directly — could say who moved it. A status without an author is
 * indistinguishable from a malfunction. */
function BugHistory({ bug }: { bug: Bug }) {
  const hist = bug.history ?? [];
  if (hist.length === 0) return null;
  return (
    <div data-testid="bug-history" className="space-y-0.5">
      <div className="text-xs text-ink-muted">history</div>
      {hist.map((h, i) => (
        <div key={i} className="font-mono text-[11px] text-ink-secondary">
          {h.ts.slice(5, 16).replace("T", " ")} · {h.from.replace("_", " ")} → {h.to.replace("_", " ")} · {h.actor}
          {h.via !== "move" ? ` (${h.via}${h.note ? ` ${h.note}` : ""})` : ""}
        </div>
      ))}
    </div>
  );
}

/** The report's screenshots, inline. The bytes come through the client (an
 * <img src> cannot carry the auth header) and render as local blob URLs. */
function BugAttachments({ bug, client, projectId, onChanged }: { bug: Bug; client: EngineClient; projectId: string; onChanged?: () => void }) {
  const [urls, setUrls] = useState<Record<string, string>>({});
  const names = bug.attachments ?? [];
  useEffect(() => {
    let cancelled = false;
    const made: string[] = [];
    for (const n of names) {
      void client
        .bugAttachmentUrl(projectId, bug.id, n)
        .then((u) => {
          made.push(u);
          if (!cancelled) setUrls((cur) => ({ ...cur, [n]: u }));
        })
        .catch(() => {});
    }
    return () => {
      cancelled = true;
      for (const u of made) URL.revokeObjectURL(u);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bug.id, names.join(","), client, projectId]);
  if (names.length === 0) return null;
  return (
    <div data-testid="bug-attachments-gallery" className="space-y-1">
      <div className="flex items-center gap-2 text-xs text-ink-muted">
        attachments
        {/* Evidence arrives late: a screenshot that missed the filing form
            had NOWHERE to go — the gallery was read-only, and B-036 went to
            triage imageless while its screenshot sat on the reporter's
            desktop. */}
        <label className="cursor-pointer underline">
          add
          <input
            type="file"
            accept="image/*"
            multiple
            className="hidden"
            data-testid="bug-attach-more"
            onChange={(e) => {
              const files = Array.from(e.target.files ?? []);
              e.target.value = "";
              void (async () => {
                for (const f of files) {
                  const dataUrl = await new Promise<string>((res, rej) => {
                    const r = new FileReader();
                    r.onload = () => res(String(r.result));
                    r.onerror = () => rej(new Error(`could not read ${f.name}`));
                    r.readAsDataURL(f);
                  });
                  await client.bugAttach(projectId, bug.id, f.name, dataUrl.split(",", 2)[1] ?? "");
                }
              })().then(() => onChanged?.()).catch(() => {});
            }}
          />
        </label>
      </div>
      <div className="flex flex-wrap gap-2">
        {names.map((n) =>
          urls[n] ? (
            <a key={n} href={urls[n]} target="_blank" rel="noreferrer" title={n}>
              <img src={urls[n]} alt={n} className="max-h-28 rounded border border-hairline" />
            </a>
          ) : (
            <span key={n} className="text-xs text-ink-muted">{n}</span>
          ),
        )}
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
      {/* One line: the command rarely changes and is whole on hover; the
          block used to spend three rows on it. */}
      <span className="min-w-0 max-w-[16rem] truncate font-mono text-ink-secondary" title={command || gate}>{command || gate}</span>
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
          onClick={() => act(() => client.triageBugs(projectId, bug.id))}
          className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          title="classify this bug — severity, duplicates, promotability"
        >
          Triage this bug
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
        {/* A body filed over MCP can carry literal "\n" sequences; a report
            that shows them is a report nobody reads. Paragraphs, not escapes. */}
        {bug.body && <div className="space-y-2 text-sm text-ink-secondary" data-testid="bug-body">{bug.body.replace(/\\n/g, "\n").split(/\n{2,}/).map((para, i) => <p key={i} className="whitespace-pre-wrap">{para}</p>)}</div>}
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
