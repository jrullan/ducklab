import { useEffect, useState } from "react";
import type { EngineClient, NextStep } from "../api/client";
import { useRuns } from "../store/runs";
import { routeHref, type Route } from "../app/routes";

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
  const [open, setOpen] = useState(() => localStorage.getItem(STORE) !== "off");
  // Runs are the guide's pulse: an accept, a pause, a landed proposal all
  // change what is next.
  const runs = useRuns((s) => s.runs);

  useEffect(() => {
    if (!projectId) return;
    client.projectNext(projectId).then(setSteps).catch(() => setSteps([]));
  }, [client, projectId, runs]);

  if (steps.length === 0) return null;

  if (!open) {
    return (
      <button
        type="button"
        data-testid="guide-pill"
        onClick={() => {
          localStorage.removeItem(STORE);
          setOpen(true);
        }}
        title="show the next-step guide"
        className="shrink-0 self-start rounded-r border border-l-0 border-hairline px-1.5 py-2 text-xs text-ink-muted"
      >
        ›{steps.length}
      </button>
    );
  }

  return (
    <aside
      data-testid="guide-rail"
      className="w-60 shrink-0 overflow-y-auto border-r border-hairline p-3"
    >
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-sm text-ink-muted">next steps</h2>
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
    </aside>
  );
}
