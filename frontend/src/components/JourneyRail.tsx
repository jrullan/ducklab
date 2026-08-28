import { useEffect, useState } from "react";
import type { EngineClient, Journey } from "../api/client";

/** The guide, localized.
 *
 * Now aggregates the project's next steps; a record needs the same answer at
 * the point where the person is looking at it: where this bug or task stands
 * on its ladder, and which door is next. Same visual as the run header's
 * cycle map (done → current → next), plus ONE primary door with its reason.
 *
 * Doors are rendered by the host — the bug rail already owns "Triage this
 * bug" / "Make it a task" and the task rail owns the launchers — so a rail
 * that also rendered buttons would be a second decision surface for the
 * same act (B-261). The rail states the door in words; the host's control
 * IS the button, and can take the door's wording as its label. */
export function JourneyRail({ journey, testId = "journey-rail" }: { journey: Journey | null; testId?: string }) {
  if (!journey) return null;
  const door = journey.door ?? journey.steps?.[0] ?? null;
  return (
    <div data-testid={testId} className="space-y-1">
      <span data-testid={`${testId}-rungs`} className="flex flex-wrap items-center gap-1 text-xs" title={`where ${journey.ref} sits on its ladder`}>
        {journey.rungs.map((r, i) => (
          <span key={r.id} className="flex items-center gap-1">
            {i > 0 && <span className="text-ink-muted">→</span>}
            <span
              data-rung={r.id}
              data-state={r.state}
              title={r.at ? `${r.label} · ${r.at.slice(0, 16).replace("T", " ")}${r.actor ? ` · ${r.actor}` : ""}` : r.label}
              className={
                r.state === "current"
                  ? "font-medium text-ink underline decoration-hairline underline-offset-4"
                  : r.state === "done"
                    ? "text-ink-secondary"
                    : r.state === "next"
                      ? "text-ink"
                      : r.state === "exit"
                        ? "text-serious"
                        : "text-ink-muted"
              }
            >
              {r.state === "done" ? "✓ " : ""}
              {r.label}
            </span>
          </span>
        ))}
      </span>
      {door && (
        <p data-testid={`${testId}-door`} className="text-xs text-ink-secondary">
          <span className="font-medium text-ink">next: {door.action}</span>
          {door.reason && <span className="text-ink-muted"> — {door.reason}</span>}
        </p>
      )}
    </div>
  );
}

/** Fetches a journey for a ref and keeps it fresh when the ref or a
 * version key changes (the host passes something that changes on status
 * moves — the bug's status, the task's status — so the rail follows the
 * ladder without polling). Tolerates clients that lack the method (older
 * fakes, older engines): the rail simply stays absent. */
export function useJourney(client: EngineClient, projectId: string, ref: string | undefined, version: string): Journey | null {
  const [journey, setJourney] = useState<Journey | null>(null);
  useEffect(() => {
    if (!ref || typeof (client as { nextFor?: unknown }).nextFor !== "function") {
      setJourney(null);
      return;
    }
    let live = true;
    client
      .nextFor(projectId, ref)
      .then((j) => {
        if (live) setJourney(j);
      })
      .catch(() => {
        if (live) setJourney(null);
      });
    return () => {
      live = false;
    };
  }, [client, projectId, ref, version]);
  return journey;
}
