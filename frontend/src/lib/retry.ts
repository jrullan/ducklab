/**
 * Which door a run's retry goes through (B-517).
 *
 * "Retry with this note" on a failed INTAKE (TI-36X r-20261010-115519-tpfn)
 * called runStart with the intake's empty task and its council mode; the
 * engine recorded three builds that each died on `unknown mode "council"`.
 * A document stage's retry is a revision of that stage — the request the
 * correct retry (r-20261010-121543-eett) went through — and only a code run
 * relaunches as code. Every desktop surface asks this module, so the rule
 * lives once.
 */

import type { EngineClient, Run } from "../api/client";

/** The document stages a note revises through POST /stages/{stage}. */
export const REVISABLE_STAGES = ["intake", "spec", "plan"] as const;

export type RetryRoute =
  /** A build of its task: runStart. */
  | "build"
  /** A test-first run of its task, chain included: testStart. */
  | "test"
  /** A revision of the document stage: stageStart with revise. */
  | "stage"
  /** A revision of the release draft: releasePlan with revise. */
  | "release";

/**
 * How a retry of this run starts, or null when there is no honest retry: a
 * code run without a task (nothing to build — the record of the B-517
 * failures themselves), and every other stage (chat, triage, review), which
 * has no note-carrying relaunch.
 */
export function retryRoute(run: Pick<Run, "stage" | "task_id">): RetryRoute | null {
  if ((REVISABLE_STAGES as readonly string[]).includes(run.stage)) return "stage";
  if (run.stage === "release") return "release";
  if (run.stage === "build" || run.stage === "test") return run.task_id ? run.stage : null;
  return null;
}

/** True when a note on this run starts a revision rather than a code run. */
export function revisesDocument(run: Pick<Run, "stage" | "task_id">): boolean {
  const route = retryRoute(run);
  return route === "stage" || route === "release";
}

/**
 * Starts the revision of a document-stage run with the person's note — the
 * same request whether the note came from "Request changes" or "Retry with
 * this note". A plan amendment replays its own request (change, mode,
 * rounds), as MCP's request_changes does, so the revision stays the small
 * amendment it was instead of becoming a full plan revision.
 */
export function reviseRun(
  client: Pick<EngineClient, "stageStart" | "releasePlan">,
  run: Pick<Run, "project_id" | "stage" | "task_id" | "stage_request">,
  note: string,
): Promise<Run> {
  const route = retryRoute(run);
  if (route === "release") return client.releasePlan(run.project_id, "", note);
  if (route !== "stage") {
    return Promise.reject(new Error(`a ${run.stage} run is not a document; it has no revision`));
  }
  const saved = run.stage_request ?? {};
  const extend = typeof saved.extend === "string" ? saved.extend.trim() : "";
  if (extend) {
    return client.stageStart(run.project_id, run.stage, {
      revise: note,
      extend,
      mode: typeof saved.mode === "string" ? saved.mode : undefined,
      rounds: typeof saved.rounds === "number" ? saved.rounds : undefined,
    });
  }
  return client.stageStart(run.project_id, run.stage, { revise: note });
}
