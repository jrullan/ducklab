import type { AcceptResult, EngineClient } from "../api/client";
import { useRuns, type AcceptState } from "../store/runs";

/**
 * Accept, as every desktop surface performs it (B-529).
 *
 * The engine runs an accept on the run's own context and answers the request
 * after a bounded wait: either with the outcome, or with in_progress while it
 * is still reproducing the gate from a clean checkout. In the second case the
 * outcome arrives only on the run's stream (accept_settled), so this resolves
 * when the store sees it — a commit is never shown before the engine confirms
 * one (AC-34), and an accept still verifying never reads as a failed one.
 */
export const ACCEPT_VERIFYING_LABEL = "Accepting… verifying from a clean checkout";

/** The button's words while an accept is pending. */
export function acceptingLabel(state: AcceptState | undefined): string {
  return state?.kind === "pending" && state.verifying ? ACCEPT_VERIFYING_LABEL : "Accepting…";
}

export class AcceptSettledError extends Error {
  constructor(message: string, readonly interrupted: boolean) {
    super(message);
  }
}

type AcceptArgs = Parameters<EngineClient["accept"]>;

export async function acceptAndSettle(
  client: Pick<EngineClient, "accept">,
  ...args: AcceptArgs
): Promise<AcceptResult> {
  const runId = args[0];
  const store = useRuns.getState();
  store.beginAccept(runId);
  let res: AcceptResult;
  try {
    res = await client.accept(...args);
  } catch (e) {
    useRuns.getState().failAccept(runId, e instanceof Error ? e.message : String(e));
    throw e;
  }
  if (!res.in_progress) {
    useRuns.getState().confirmAccept(runId, res.commit_sha);
    return res;
  }
  useRuns.getState().acceptInProgress(runId);
  return settled(runId);
}

/** Resolves when the run's accept leaves pending, from the stream. */
function settled(runId: string): Promise<AcceptResult> {
  return new Promise((resolve, reject) => {
    const check = (): boolean => {
      const state = useRuns.getState();
      const st = state.acceptState[runId];
      if (st?.kind === "committed") {
        // The settlement event carries the publication caveats the HTTP
        // answer would have.
        const last = [...(state.events[runId] ?? [])].reverse().find((e) => e.type === "accept_settled");
        resolve({
          commit_sha: st.sha,
          warning: typeof last?.data?.warning === "string" ? last.data.warning : undefined,
          info: typeof last?.data?.info === "string" ? last.data.info : undefined,
        });
        return true;
      }
      if (st?.kind === "error") {
        reject(new AcceptSettledError(st.message, st.interrupted === true));
        return true;
      }
      return false;
    };
    if (check()) return;
    const unsubscribe = useRuns.subscribe(() => {
      if (check()) unsubscribe();
    });
  });
}
