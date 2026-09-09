import type { NextStep } from "../api/client";
import { routeHref } from "../app/routes";

/** Where a guide step's action happens. */
export function nextStepHref(step: NextStep): string {
  if (step.kind === "run" && step.ref) return routeHref({ name: "run", id: step.ref });
  if (step.kind === "stage") return routeHref({ name: "cycle", stage: step.ref || undefined });
  if (step.kind === "bug") return routeHref({ name: "board", tab: "bugs" });
  if (step.kind === "task") return routeHref({ name: "board" });
  if (step.kind === "release") return routeHref({ name: "release" });
  return routeHref({ name: "cycle" });
}

/** The action names the outcome first and the harness term second; the
 * button keeps the outcome and the tooltip keeps the rest. */
function actionLabel(step: NextStep): string {
  return step.action.split(" — ")[0]!.replace(/\s*\([^)]*\)\s*$/, "").trim();
}

const GROUPS: { kind: NextStep["kind"]; title: string }[] = [
  { kind: "bug", title: "Bugs" },
  { kind: "task", title: "Tasks" },
  { kind: "run", title: "Runs" },
  { kind: "stage", title: "Documents" },
  { kind: "release", title: "Release" },
  { kind: "project", title: "Project" },
];

/**
 * The guide's next steps as typed action cards (B-282). The engine already
 * produces {id, action, reason, kind}; rendering them as a pile of underlined
 * links threw that away. Each card carries its consequence beside the action,
 * and its cost when the estimate exists.
 *
 * One deletion on purpose: the `install` step (the repo is ahead of the
 * running engine) is not rendered here. The sidebar footer owns that truth
 * (T-228); the same fact in two surfaces with two wordings is what O-6 was.
 */
export function NextStepCards({
  steps,
  costFor,
}: {
  steps: NextStep[];
  /** A short cost line for a step, when known ("opens pair · ~$0.94"). */
  costFor?: (step: NextStep) => string | undefined;
}) {
  const visible = steps.filter((step) => step.id !== "install");
  if (visible.length === 0) return null;
  const groups = GROUPS.map((group) => ({ ...group, steps: visible.filter((s) => s.kind === group.kind) })).filter(
    (group) => group.steps.length > 0,
  );
  const known = new Set(GROUPS.map((g) => g.kind));
  const other = visible.filter((s) => !known.has(s.kind));
  if (other.length > 0) groups.push({ kind: "project", title: "Other", steps: other });
  return (
    <section className="mb-4" data-testid="now-next-steps">
      <h2 className="text-sm font-medium text-ink">Next steps</h2>
      <div className="mt-2 space-y-3">
        {groups.map((group) => (
          <div key={group.title} data-testid={`now-next-group-${group.kind}`}>
            <h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{group.title}</h3>
            <ul className="mt-1 grid gap-2 sm:grid-cols-2">
              {group.steps.map((step, index) => {
                const cost = costFor?.(step);
                return (
                  <li
                    key={`${step.id}:${step.ref ?? index}`}
                    data-testid="now-next-step"
                    data-kind={step.kind}
                    className="rounded-card border border-hairline p-2"
                  >
                    <a
                      href={nextStepHref(step)}
                      title={step.action}
                      className="inline-block rounded border border-hairline px-2 py-1 text-sm text-ink hover:bg-surface2"
                    >
                      {actionLabel(step)}
                    </a>
                    <p className="mt-1 text-xs text-ink-muted">
                      {step.reason}
                      {cost ? <span data-testid="now-next-step-cost"> · {cost}</span> : null}
                    </p>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </div>
    </section>
  );
}
