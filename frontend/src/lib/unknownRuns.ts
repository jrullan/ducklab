import type { DucklabEvent } from "../api/events";
import type { Run } from "../api/client";

/**
 * Adopts a run the store has never heard of when one of its events arrives.
 *
 * The store makes a run's row from its live run_start and ignores events for
 * runs it does not know. A run_start the client never received — the engine
 * persisted a triage's before publishing it (B-538), or it fell in a gap of
 * the stream — left the run invisible until something refetched the list,
 * though its events kept arriving. This fetches the run's record (with its
 * events, so nothing that arrived meanwhile is lost) the first time an
 * event of the current project names an unknown run.
 *
 * Only events that carry the current project's id qualify: token_delta has
 * a run_id but no project_id, and adopting from it would pull another
 * project's run into this one's store.
 */
export function makeUnknownRunAdopter(opts: {
  projectId: () => string | undefined;
  known: (runId: string) => boolean;
  fetchRun: (runId: string) => Promise<{ run: Run; events: unknown[] }>;
  adopt: (run: Run, events: DucklabEvent[]) => void;
}): (e: DucklabEvent) => void {
  const inFlight = new Set<string>();
  return (e) => {
    const runId = e.run_id;
    const project = opts.projectId();
    if (!runId || !project || e.project_id !== project || e.type === "run_start") return;
    if (opts.known(runId) || inFlight.has(runId)) return;
    inFlight.add(runId);
    opts
      .fetchRun(runId)
      .then((d) => {
        if (d.run.project_id === opts.projectId() && !opts.known(runId)) {
          opts.adopt(d.run, d.events as DucklabEvent[]);
        }
      })
      .catch(() => {})
      .finally(() => inFlight.delete(runId));
  };
}
