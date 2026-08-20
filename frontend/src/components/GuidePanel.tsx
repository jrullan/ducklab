import { useEffect, useState } from "react";
import type { AutopilotState, Duckling, EngineClient, NextStep, Run } from "../api/client";
import { useRuns } from "../store/runs";
import { routeHref, type Route } from "../app/routes";
import { ChatAbout } from "./ChatAbout";
import { tokens } from "../lib/format";
import { statusIcon, statusVar, verdictStatus } from "../lib/colors";

/** Where a step's button lives. The guide points, it never duplicates. */
function hrefFor(step: NextStep): string {
  const route: Route =
    step.kind === "run" && step.ref
      ? { name: "run", id: step.ref }
      : step.kind === "stage"
        ? { name: "cycle", stage: step.ref || undefined }
        : step.kind === "bug"
          ? { name: "board", tab: "bugs" }
          : step.kind === "task"
            ? { name: "board" }
            : step.kind === "release"
              ? { name: "release" }
              : { name: "cycle" };
  return routeHref(route);
}

const STORE = "ducklab.guide";

/** The step's headline without its instruction: "Verify 3 fixed bugs — confirm
 *  each…" → "Verify 3 fixed bugs"; "Start T-003 (test first, then build)" →
 *  "Start T-003". The whole sentence rides the title. */
export function shortAction(action: string): string {
  return action.split(" — ")[0]!.replace(/\s*\([^)]*\)\s*$/, "").trim();
}

/** Where one of a grouped step's refs lives. */
function hrefForRef(step: NextStep, ref: string): string {
  return hrefFor({ ...step, ref });
}

/** Views where the guide's steps are acted on in place. Elsewhere (roster,
 *  settings, ducklings, projects, reports, reviews, bench) the rail starts
 *  folded to its strip: 240px of "next steps" beside a settings form is
 *  noise, and one click brings it back — remembered per view. */
const ACTIONABLE = new Set(["now", "board", "cycle", "runs", "run", "release"]);

/**
 * The guide rail: the engine's ProjectNext, always in the left margin.
 *
 * Ducklab's cycle is a state machine, so "what do I do now?" always has a
 * computable answer — and for a new user that answer is the whole mental
 * load of the tool. It began as a panel inside Now, which meant the thread
 * broke the moment you followed it: click a step, land on another view, and
 * the guide is gone. A thread you can only see from one room is not a
 * thread.
 *
 * Living beside every view, it shows the WHOLE guide in the engine's own
 * order — paused runs first (work already paid for), then the lifecycle,
 * then the next buildable task. Inside Now some of these repeat the inbox's
 * cards; across the app, the rail is the only place they are visible at
 * all, and a compact echo on one screen is cheaper than a broken thread on
 * every other.
 *
 * Collapsible to a thin counted strip, and the choice survives: guidance
 * for the first weeks, a pulse for after.
 */
export function GuideRail({ client, projectId, view = "now" }: { client: EngineClient; projectId: string; view?: string }) {
  const [steps, setSteps] = useState<NextStep[]>([]);
  const [fleet, setFleet] = useState<Duckling[]>([]);
  const [consultant, setConsultant] = useState("");
  const actionable = ACTIONABLE.has(view);
  const viewKey = `${STORE}.${view}`;
  const [open, setOpen] = useState(() => localStorage.getItem(STORE) !== "off" && (actionable || localStorage.getItem(viewKey) === "on"));
  const [showAll, setShowAll] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [installMsg, setInstallMsg] = useState<string | null>(null);
  useEffect(() => { setOpen(localStorage.getItem(STORE) !== "off" && (actionable || localStorage.getItem(viewKey) === "on")); }, [view]); // eslint-disable-line react-hooks/exhaustive-deps
  // Runs are the guide's pulse: an accept, a pause, a landed proposal all
  // change what is next.
  const runs = useRuns((s) => s.runs);

  useEffect(() => {
    if (!projectId) return;
    client.projectNext(projectId).then(setSteps).catch(() => setSteps([]));
  }, [client, projectId, runs]);
  // Runs are not the only thing that moves the guide: promoting a bug, parking
  // one, filing a task all change the steps with no run to pulse the rail.
  // A slow poll covers every such mutation without wiring each one.
  useEffect(() => {
    if (!projectId) return;
    const t = setInterval(() => {
      client.projectNext(projectId).then(setSteps).catch(() => {});
    }, 10000);
    return () => clearInterval(t);
  }, [client, projectId]);
  useEffect(() => {
    client.ducklings().then(setFleet).catch(() => setFleet([]));
  }, [client]);
  useEffect(() => {
    if (!projectId) return;
    client.RosterGet(projectId, "common")
      .then((roster) => {
        const entry = roster.entries.find((item) => item.role === "consultant");
        setConsultant(entry?.source === "project pin" || entry?.source === "global role fallback" ? entry.duckling : "");
      })
      .catch(() => setConsultant(""));
  }, [client, projectId]);
  // The autopilot's state, refreshed on the same pulse as the guide: its
  // whole point is acting between the person's glances.
  const [ap, setAp] = useState<AutopilotState | null>(null);
  const [apBusy, setApBusy] = useState(false);
  useEffect(() => {
    if (!projectId) return;
    client.autopilot(projectId).then(setAp).catch(() => setAp(null));
  }, [client, projectId, runs]);
  // While the loop is ON its state changes between run events — "starting"
  // becomes "needs you: …" with no run to pulse the rail — so it polls.
  useEffect(() => {
    if (!projectId || !ap?.on) return;
    const t = setInterval(() => {
      client.autopilot(projectId).then(setAp).catch(() => {});
    }, 4000);
    return () => clearInterval(t);
  }, [client, projectId, ap?.on]);
  const toggleAutopilot = () => {
    if (!ap && !projectId) return;
    setApBusy(true);
    client
      .autopilotSet(projectId, !(ap?.on ?? false))
      .then(setAp)
      .catch(() => {})
      .finally(() => setApBusy(false));
  };

  // The live pulse, above the plan: what is happening outranks what is next,
  // and the rail is the one place both survive every view change.
  const active = Object.values(runs).filter(
    (r) => r.status === "running" || r.status === "queued",
  );
  const live = useRuns((s) => s.spend);

  if (steps.length === 0 && active.length === 0) return null;

  if (!open) {
    return (
      <button
        type="button"
        data-testid="guide-pill"
        onClick={() => {
          if (actionable) localStorage.removeItem(STORE);
          else localStorage.setItem(viewKey, "on");
          setOpen(true);
        }}
        title="show the guide rail"
        className="shrink-0 self-start rounded-r border border-l-0 border-hairline px-1.5 py-2 text-xs text-ink-muted"
      >
        ›{active.length + steps.length}
      </button>
    );
  }

  return (
    <aside
      data-testid="guide-rail"
      className="flex h-full max-h-full w-60 shrink-0 flex-col border-r border-hairline p-3"
    >
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
      {/* First thing in the panel, because it acts on ALL of it — parked
          beside "next steps" it read as hiding that one section. */}
      <div className="mb-2 flex justify-end">
        <button
          type="button"
          data-testid="guide-hide"
          onClick={() => {
            if (actionable) localStorage.setItem(STORE, "off");
            else localStorage.removeItem(viewKey);
            setOpen(false);
          }}
          title="collapse the guide (a strip stays to bring it back)"
          className="text-xs text-ink-muted underline"
        >
          hide
        </button>
      </div>
      {active.length > 0 && (
        <section data-testid="rail-running" className="mb-3">
          <h2 className="text-sm text-ink-muted">running</h2>
          <ul className="mt-1 space-y-1">
            {active.map((r) => (
              <RailRun key={r.id} run={r} tokensUsed={live[r.id]?.tokens} />
            ))}
          </ul>
        </section>
      )}
      {/* The loop's switch lives where its effects show. On: it drives the
          guide's own next step whenever that step is mechanical, and idles —
          saying why — when a human gate stands. Off with a reason: the rail
          says what stopped it instead of showing a silently cleared toggle. */}
      <section data-testid="rail-autopilot" className="mb-3">
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs text-ink-muted">autopilot</span>
          <button
            type="button"
            data-testid="autopilot-toggle"
            disabled={apBusy}
            onClick={toggleAutopilot}
            className={`rounded border px-2 py-0.5 text-xs ${
              ap?.on ? "border-good text-good" : "border-hairline text-ink-muted"
            }`}
          >
            {ap?.on ? "on — stop" : "start"}
          </button>
        </div>
        {ap?.on && (
          <p className="mt-1 text-xs text-ink-muted" data-testid="autopilot-status">
            {ap.started}/{ap.max_tasks} tasks started · {ap.last_action || "…"}
          </p>
        )}
        {!ap?.on && ap?.stopped_reason && (
          <p className="mt-1 text-xs text-serious" data-testid="autopilot-stopped">
            {ap.stopped_reason}
          </p>
        )}
      </section>

      <h2 className="text-xs text-ink-muted">next steps</h2>
      {/* The first step is THE next step: headline and why. The rest are one
          line each — verb and object, the sentence on hover — capped at
          four with the remainder behind "+N more". A grouped step shows its
          objects as chips, each a link. */}
      <ol className="mt-1.5 space-y-1.5">
        {(showAll ? steps : steps.slice(0, 4)).map((s, i) => (
          <li key={`${s.id}:${s.ref ?? i}`} data-testid="guide-step" data-primary={i === 0 ? "true" : undefined}>
            {/* The self-hosted install step is a BUTTON: the whole point is
                not leaving ducklab for a terminal. It runs the project's
                declared [install] command and reports; the engine restart
                stays a separate, deliberate click. */}
            {s.id === "install" ? (
              <span className={i === 0 ? "text-sm" : "text-xs"}>
                <button
                  type="button"
                  data-testid="guide-install"
                  disabled={installing}
                  title={`${s.action} — ${s.reason}`}
                  onClick={() => {
                    if (typeof client.projectInstall !== "function") return;
                    setInstalling(true);
                    setInstallMsg(null);
                    client.projectInstall(projectId)
                      .then((r) => setInstallMsg(r.ok ? `installed in ${Math.round(r.seconds)}s — now Restart engine (Settings) and relaunch the app` : `install failed (exit ${r.exit_code}):
${r.output.slice(-600)}`))
                      .catch((e) => setInstallMsg(e instanceof Error ? e.message : String(e)))
                      .finally(() => setInstalling(false));
                  }}
                  className={`rounded border px-2 py-1 ${i === 0 ? "border-ink text-ink" : "border-hairline text-ink-muted hover:text-ink"} disabled:opacity-50`}
                >
                  {installing ? "Installing…" : shortAction(s.action)}
                </button>
                {installMsg && <p className="mt-1 whitespace-pre-wrap text-xs text-ink-muted" data-testid="guide-install-result">{installMsg}</p>}
              </span>
            ) : (
            <a href={hrefFor(s)} title={`${s.action} — ${s.reason}`} className={i === 0 ? "text-sm text-ink underline" : "text-xs text-ink-muted underline hover:text-ink"}>
              {i === 0 ? s.action : shortAction(s.action)}
            </a>
            )}
            {i === 0 && <p className="mt-0.5 text-xs text-ink-muted">{s.reason}</p>}
            {s.refs && s.refs.length > 1 && (
              <span className="mt-0.5 flex flex-wrap gap-1" data-testid="guide-step-refs">
                {s.refs.slice(0, 6).map((ref) => <a key={ref} href={hrefForRef(s, ref)} className="rounded border border-hairline px-1 text-[11px] text-ink-muted hover:text-ink">{ref}</a>)}
                {s.refs.length > 6 && <span className="text-[11px] text-ink-muted">+{s.refs.length - 6}</span>}
              </span>
            )}
          </li>
        ))}
      </ol>
      {steps.length > 4 && (
        <button type="button" data-testid="guide-more" className="mt-1.5 text-xs text-ink-muted underline" onClick={() => setShowAll((v) => !v)}>
          {showAll ? "fewer" : `+${steps.length - 4} more`}
        </button>
      )}
      {/* The rail says WHAT; this chat explains WHY. The consultant gets the
          embedded harness dossier plus the live state the rail itself
          computes, so both always tell the same story. */}
      <div className="mt-4 border-t border-hairline pt-2" data-testid="guide-ask">
        <ChatAbout
          client={client}
          projectId={projectId}
          aboutKind="ducklab"
          aboutId=""
          ducklings={fleet}
          label="ask how & why · chat about Ducklab"
          placeholder="e.g. why does the test come before the build?"
          preselectedDuckling={consultant}
        />
      </div>
      </div>
      <RecentRuns runs={runs} projectId={projectId} />
    </aside>
  );
}

function RecentRuns({ runs, projectId }: { runs: Record<string, Run>; projectId: string }) {
  const completed = Object.values(runs)
    .filter((run) => run.project_id === projectId && (run.status === "done" || run.status === "failed"))
    .sort((a, b) => b.started_at.localeCompare(a.started_at))
    .slice(0, 10);

  return (
    <section data-testid="rail-recent" className="shrink-0 border-t border-hairline pt-2">
      <h2 className="text-xs text-ink-muted">Recent runs</h2>
      {completed.length === 0 ? (
        <p className="mt-1 text-xs text-ink-muted">no completed runs</p>
      ) : (
        <ul className="mt-1 space-y-0.5">
          {completed.map((run) => <RecentRun key={run.id} run={run} />)}
        </ul>
      )}
    </section>
  );
}

function RecentRun({ run }: { run: Run }) {
  const label = run.task_id || run.stage || run.id;
  const role = run.status === "failed" || ["FAILED", "ABORTED", "BUDGET_EXCEEDED"].includes(run.verdict)
    ? verdictStatus("FAILED")
    : run.verdict === "PASSED" || run.accepted === true
      ? verdictStatus("PASSED")
      : verdictStatus("UNVERIFIED");
  const glyph = role === "warning" ? "UNVERIFIED" : statusIcon(role);
  return (
    <li className="flex items-center gap-1 text-xs">
      <span role="img" aria-label={glyph} style={{ color: statusVar(role) }}>{glyph}</span>
      <a href={routeHref({ name: "run", id: run.id })} className="truncate text-ink underline">{label}</a>
    </li>
  );
}

/** One live run, rail-compact: the status in its color, the shortest name
 * that identifies it, live tokens when they exist. The inbox keeps the
 * fuller row; this is the pulse. */
function RailRun({ run, tokensUsed }: { run: Run; tokensUsed?: number }) {
  const label = run.task_id || run.stage || run.id;
  // "T-120 pair" says who but not WHAT: a task runs as test or build, and
  // which one decides whether you expect a red gate or a diff. Shown only
  // when the label did not already say it (a stage run's label IS its stage).
  const stage = run.task_id && run.stage ? run.stage + " \u00b7 " : "";
  return (
    <li data-testid="rail-run" className="text-xs">
      <span className={run.status === "running" ? "text-good" : "text-ink-muted"}>
        {run.status === "running" ? "\u25cf" : "\u25cb"}
      </span>{" "}
      <a href={routeHref({ name: "run", id: run.id })} className="text-ink underline">
        {label}
      </a>{" "}
      <span className="text-ink-muted">
        {stage}
        {run.mode}
        {tokensUsed ? " \u00b7 " + tokens(tokensUsed) : ""}
      </span>
    </li>
  );
}
