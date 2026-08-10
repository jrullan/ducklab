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
 * The guide: the engine's ProjectNext, rendered.
 *
 * Ducklab's cycle is a state machine, so "what do I do now?" always has a
 * computable answer — and for a new user that answer is the whole mental
 * load of the tool. Each step names the outcome, says why it is next, and
 * links the surface whose buttons already do it.
 *
 * Only the steps the inbox does not already show: paused runs are Now's
 * waiting cards and the next buildable task is Now's launcher, so the guide
 * showing them again would be a second inbox. What Now never surfaces is the
 * LIFECYCLE — which document comes next, that open bugs want triage, that a
 * finished project grows by brief — and that is exactly the part a new user
 * cannot know yet.
 *
 * Dismissible and it stays dismissed: guidance for the first weeks, a pill
 * for after.
 */
export function GuidePanel({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [steps, setSteps] = useState<NextStep[]>([]);
  const [open, setOpen] = useState(() => localStorage.getItem(STORE) !== "off");
  // Runs are the guide's pulse: an accept, a pause, a landed proposal all
  // change what is next.
  const runs = useRuns((s) => s.runs);

  useEffect(() => {
    if (!projectId) return;
    client.projectNext(projectId).then(setSteps).catch(() => setSteps([]));
  }, [client, projectId, runs]);

  const shown = steps.filter(
    (s) => s.kind === "stage" || s.kind === "bug" || s.kind === "project",
  );
  if (shown.length === 0) return null;

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
        className="mb-3 rounded-full border border-hairline px-2 py-0.5 text-xs text-ink-muted"
      >
        guide · {shown.length}
      </button>
    );
  }

  return (
    <section className="mb-4 rounded-card border border-hairline p-3" data-testid="guide-panel">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-sm text-ink-muted">next step</h2>
        <button
          type="button"
          data-testid="guide-hide"
          onClick={() => {
            localStorage.setItem(STORE, "off");
            setOpen(false);
          }}
          title="hide the guide (a pill stays to bring it back)"
          className="text-xs text-ink-muted underline"
        >
          hide
        </button>
      </div>
      <ol className="mt-2 space-y-2">
        {shown.slice(0, 3).map((s, i) => (
          <li key={`${s.id}:${s.ref ?? i}`} data-testid="guide-step">
            <a href={hrefFor(s)} className="text-sm text-ink underline">
              {s.action}
            </a>
            <p className="text-xs text-ink-muted">{s.reason}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}
