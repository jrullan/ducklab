import { useState } from "react";
import type { EngineClient, Task } from "../api/client";

/** Remove-from-plan with its confirmation, shared by every surface that shows
 * a task. It lived only in the Board's rail, so removing the task whose
 * failed run you were LOOKING AT meant leaving for Work → Tasks and finding
 * it again — any legal manipulation should be offerable where the task is on
 * screen. Render it gated on task.next.includes("remove"): the engine
 * refuses once anything ran and was accepted, and a button that only ever
 * errors is worse than none. */
export function RemoveTask({
  task,
  client,
  projectId,
  onDone,
}: {
  task: Task;
  client: EngineClient;
  projectId: string;
  onDone: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  if (!confirming) {
    return (
      <button
        type="button"
        data-testid="task-remove"
        onClick={() => setConfirming(true)}
        className="text-xs text-ink-muted underline"
      >
        remove from plan
      </button>
    );
  }
  return (
    <div className="space-y-1" data-testid="task-remove-confirm">
      <p className="text-xs text-ink-secondary">
        Remove {task.id} from the plan? If it came from a report, the report goes
        back to triaged.
      </p>
      <div className="flex items-center gap-2">
        <button
          type="button"
          data-testid="task-remove-yes"
          onClick={() => {
            setFailure(null);
            void client
              .taskRemove(projectId, task.id)
              .then(() => onDone())
              .catch((e) => setFailure(e instanceof Error ? e.message : String(e)));
          }}
          className="rounded border border-critical px-2 py-1 text-xs text-critical"
        >
          Remove
        </button>
        <button
          type="button"
          onClick={() => setConfirming(false)}
          className="text-xs text-ink-muted underline"
        >
          keep it
        </button>
      </div>
      {failure && (
        <p className="text-xs text-critical" data-testid="task-remove-error">
          {failure}
        </p>
      )}
    </div>
  );
}
