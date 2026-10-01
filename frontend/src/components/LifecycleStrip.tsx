/**
 * Where the project stands on the whole road (B-461).
 *
 * Intent, requirements, specification and plan read as document types, not
 * stages; accepting a document and accepting code used similar words; and
 * nothing said that no code exists until the first build lands. The engine
 * computes the road (service/lifecycle_map.go) so the desktop and an MCP
 * operator read the same stages and the same next sentence.
 */

import type { ProjectLifecycle } from "../api/client";

const stateClass: Record<string, string> = {
  done: "border-good text-good",
  decision: "border-warning text-warning",
  current: "border-ink text-ink",
  pending: "border-hairline text-ink-muted",
};

const stateHint: Record<string, string> = {
  done: "done",
  decision: "a draft waits for your decision",
  current: "you are here",
  pending: "later",
};

export function LifecycleStrip({ lifecycle }: { lifecycle: ProjectLifecycle }) {
  const stages = lifecycle.stages ?? [];
  if (stages.length === 0) return null;
  return (
    <section className="space-y-2" data-testid="lifecycle-strip" aria-label="Project lifecycle">
      <ol className="flex flex-wrap items-center gap-1 text-xs">
        {stages.map((stage, i) => (
          <li key={stage.id} className="flex items-center gap-1">
            <span
              className={`rounded border px-2 py-0.5 ${stateClass[stage.state] ?? stateClass.pending}`}
              title={stateHint[stage.state] ?? ""}
              data-testid={`lifecycle-stage-${stage.id}`}
              data-state={stage.state}
              aria-current={stage.id === lifecycle.current ? "step" : undefined}
            >
              {stage.state === "done" ? "✓ " : stage.state === "decision" ? "● " : ""}
              {stage.label}
              {stage.id === "build" && lifecycle.tasks_total > 0 ? ` ${lifecycle.tasks_accepted}/${lifecycle.tasks_total}` : ""}
            </span>
            {i < stages.length - 1 && <span aria-hidden="true" className="text-ink-muted">→</span>}
          </li>
        ))}
        {!lifecycle.code_exists && (
          <li className="ml-2 text-ink-muted" data-testid="lifecycle-no-code">no code yet</li>
        )}
      </ol>
      <p className="text-sm text-ink-secondary" data-testid="lifecycle-next">{lifecycle.next}</p>
    </section>
  );
}
