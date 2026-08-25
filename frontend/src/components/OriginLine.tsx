import { useEffect, useState } from "react";
import type { EngineClient, Task } from "../api/client";
import { routeHref } from "../app/routes";

/** The quiet link between work and the document that gave it a reason to exist. */
export function OriginLine({ client, projectId, task }: { client: EngineClient; projectId: string; task: Task }) {
  const [origin, setOrigin] = useState<{ id: string; title: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        // Start at the task, then ask for its Up nodes. The engine owns the
        // spine; the client only chooses the plan section to show.
        const taskNode = await client.traceShow(projectId, task.id);
        const up = Array.isArray(taskNode.up) ? taskNode.up.filter((id): id is string => typeof id === "string") : [];
        const ids = [task.milestone, ...up].filter((id, i, all) => id && all.indexOf(id) === i);
        const nodes = await Promise.all(ids.map((id) => client.traceShow(projectId, id).catch(() => null)));
        const index = nodes.findIndex((node, i) => node && (node.kind === "milestone" || ids[i] === task.milestone));
        const node = index >= 0 ? nodes[index] : null;
        if (!cancelled && node && typeof node.title === "string" && node.title.trim()) {
          const id = ids[index];
          if (id) setOrigin({ id, title: node.title });
        }
      } catch {
        if (!cancelled) setOrigin(null);
      }
    };
    void load();
    return () => { cancelled = true; };
  }, [client, projectId, task.id, task.milestone]);

  const bug = /\bFixes\s+(B-\d+)/i.exec(task.body ?? "")?.[1];
  const plan = origin ? `plan §${origin.id} · ${origin.title}` : null;
  const label = bug && plan ? `from bug ${bug} · ${plan}` : plan;
  const href = routeHref({ name: "cycle", stage: "plan" });

  return (
    <span data-testid="origin-line" className="mt-1 block text-xs text-ink-muted">
      {label ? <a href={href} onClick={(event) => event.stopPropagation()} className="underline" title="open the plan document">{label}</a> : "no document behind this task"}
    </span>
  );
}
