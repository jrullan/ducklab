import { useEffect, useState } from "react";
import type { AutopilotState, Duckling, EngineClient, NextStep, Run } from "../api/client";
import { useRuns } from "../store/runs";
import { routeHref, type Route } from "../app/routes";
import { ChatAbout } from "./ChatAbout";
import { tokens } from "../lib/format";

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
            : { name: "cycle" };
  return routeHref(route);
}

const STORE = "ducklab.guide";

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
export function GuideRail({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [steps, setSteps] = useState<NextStep[]>([]);
  const [fleet, setFleet] = useState<Duckling[]>([]);
  const [open, setOpen] = useState(() => localStorage.getItem(STORE) !== "off");
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
          localStorage.removeItem(STORE);
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
      className="w-60 shrink-0 overflow-y-auto border-r border-hairline p-3"
    >
      {/* First thing in the panel, because it acts on ALL of it — parked
          beside "next steps" it read as hiding that one section. */}
      <div className="mb-2 flex justify-end">
        <button
          type="button"
          data-testid="guide-hide"
          onClick={() => {
            localStorage.setItem(STORE, "off");
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
      <section data-testid="rail-autopilot" className="mb-3 rounded-card border border-hairline p-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm text-ink-muted">autopilot</span>
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

      <h2 className="text-sm text-ink-muted">next steps</h2>
      <ol className="mt-2 space-y-3">
        {steps.slice(0, 6).map((s, i) => (
          <li key={`${s.id}:${s.ref ?? i}`} data-testid="guide-step">
            <a href={hrefFor(s)} className="text-sm text-ink underline">
              {s.action}
            </a>
            <p className="mt-0.5 text-xs text-ink-muted">{s.reason}</p>
          </li>
        ))}
      </ol>
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
        />
      </div>
    </aside>
  );
}

/** One live run, rail-compact: the status in its color, the shortest name
 * that identifies it, live tokens when they exist. The inbox keeps the
 * fuller row; this is the pulse. */
function RailRun({ run, tokensUsed }: { run: Run; tokensUsed?: number }) {
  const label = run.task_id || run.stage || run.id;
  return (
    <li data-testid="rail-run" className="text-xs">
      <span className={run.status === "running" ? "text-good" : "text-ink-muted"}>
        {run.status === "running" ? "\u25cf" : "\u25cb"}
      </span>{" "}
      <a href={routeHref({ name: "run", id: run.id })} className="text-ink underline">
        {label}
      </a>{" "}
      <span className="text-ink-muted">
        {run.mode}
        {tokensUsed ? " \u00b7 " + tokens(tokensUsed) : ""}
      </span>
    </li>
  );
}
