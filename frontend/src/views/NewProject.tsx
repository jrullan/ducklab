/**
 * Starting another project (Jose, after the greenfield arc): the guided form
 * a first-run person sees — what to build, kind of project, references — is
 * the way to create every new project, not only the first. It used to be
 * reachable only with zero projects; everyone else got the old folder form
 * in Settings, which asks for an absolute path before the idea.
 *
 * The folder form survives in Settings as "Add an existing folder": the
 * guided start refuses a folder that already holds files, and adopting an
 * existing codebase is a different act.
 */

import { useEffect, useState } from "react";
import type { EngineClient, ProjectStartResult } from "../api/client";
import { StartProject } from "../components/StartProject";
import { routeHref } from "../app/routes";
import { readiness } from "./FirstRun";

export function NewProject({ client, onStarted }: { client: EngineClient; onStarted: (result: ProjectStartResult) => void }) {
  const [noModel, setNoModel] = useState(false);
  const [stalled, setStalled] = useState<ProjectStartResult | null>(null);

  useEffect(() => {
    let live = true;
    Promise.all([client.providers(), client.ducklings()])
      .then(([providers, ducklings]) => { if (live) setNoModel(readiness(providers, ducklings).usable.length === 0); })
      .catch(() => {});
    return () => { live = false; };
  }, [client]);

  return (
    <section className="mx-auto max-w-2xl space-y-4 p-6" data-testid="new-project">
      <div>
        <h2 className="text-xl font-medium text-ink">Start a new project</h2>
        <p className="mt-1 text-sm text-ink-secondary">
          Describe what you want to build. Ducklab turns it into requirements, a specification and a plan that you
          approve one at a time. To bring in code that already exists,{" "}
          <a href={routeHref({ name: "projects" })} className="underline">add an existing folder</a> instead.
        </p>
      </div>
      {stalled ? (
        <div className="space-y-2 rounded-card border border-warning p-4" data-testid="new-project-stalled">
          <p className="text-sm text-ink">
            Project <strong>{stalled.project.name}</strong> was created at <code>{stalled.project.path}</code>, but drafting
            could not start: {stalled.intake_error}
          </p>
          <button type="button" onClick={() => onStarted(stalled)} className="rounded border border-hairline px-3 py-1 text-xs" data-testid="new-project-continue">
            Continue to the project
          </button>
        </div>
      ) : (
        <div className="rounded-card border border-hairline p-4">
          <StartProject
            client={client}
            onStarted={(result) => (result.run_id ? onStarted(result) : setStalled(result))}
            modelWarning={noModel ? "No model is ready to draft with. You can create the project now; add a model in Settings → ducklings to draft the requirements." : undefined}
          />
        </div>
      )}
    </section>
  );
}
