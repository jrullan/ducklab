import { useState } from "react";
import type { Artifact, Section, TraceError } from "../api/client";
import { EvidenceDrawer } from "./EvidenceDrawer";

function allSections(sections: Section[] | null | undefined): Section[] {
  const out: Section[] = [];
  const visit = (items: Section[] | null | undefined) => items?.forEach((item) => { out.push(item); visit(item.children); });
  visit(sections);
  return out;
}

function declaredFiles(section: Section): string[] {
  const raw = section.fields?.owns ?? section.fields?.files ?? section.fields?.paths ?? section.fields?.file ?? "";
  return raw.split(/[\n,]+/).map((file) => file.trim()).filter(Boolean);
}

function taskSections(sections: Section[] | null | undefined): Section[] {
  return allSections(sections).filter((section) => /^T-\d+$/i.test(section.id));
}

function stableFields(fields: Record<string, string> | undefined): [string, string][] {
  return Object.entries(fields ?? {}).sort(([left], [right]) => left.localeCompare(right));
}

function sameTask(left: Section, right: Section): boolean {
  return JSON.stringify({ title: left.title, body: left.body, implements: [...(left.implements ?? [])].sort(), fields: stableFields(left.fields) }) ===
    JSON.stringify({ title: right.title, body: right.body, implements: [...(right.implements ?? [])].sort(), fields: stableFields(right.fields) });
}

export function PlanCard({ artifact, traceErrors, onApprove, onChanges }: {
  artifact: Artifact;
  traceErrors: TraceError[];
  onApprove: () => void;
  onChanges: () => void;
}) {
  const [open, setOpen] = useState(false);
  const approvedTasks = taskSections(artifact.sections);
  const proposedTasks = taskSections(artifact.proposal?.sections ?? artifact.sections);
  const approvedByID = new Map(approvedTasks.map((task) => [task.id, task]));
  const proposedByID = new Map(proposedTasks.map((task) => [task.id, task]));
  const isAmendment = approvedTasks.length > 0;
  const added = proposedTasks.filter((task) => !approvedByID.has(task.id));
  const changed = proposedTasks.filter((task) => {
    const current = approvedByID.get(task.id);
    return !!current && !sameTask(current, task);
  });
  const removed = approvedTasks.filter((task) => !proposedByID.has(task.id));
  const focus = isAmendment ? [...added, ...changed] : proposedTasks;
  const focusIDs = new Set(focus.map((task) => task.id));
  // Trace errors are the deterministic missing links in the proposal. An
  // amendment reports only errors attached to what it changes; inherited
  // debt in the approved plan is not evidence about this proposal.
  const relevantErrors = isAmendment
    ? traceErrors.filter((error) => focusIDs.has(error.id) || [...focusIDs].some((id) => error.detail.includes(id)))
    : traceErrors;
  const covered = Math.max(0, focus.length - relevantErrors.length);
  const lanes = focus.filter((task) => declaredFiles(task).length > 0).length;
  const ownersByFile = new Map<string, Set<string>>();
  for (const task of proposedTasks) {
    const owner = task.id;
    for (const file of declaredFiles(task)) {
      const owners = ownersByFile.get(file) ?? new Set<string>();
      owners.add(owner);
      ownersByFile.set(file, owners);
    }
  }
  const collisions = [...ownersByFile.values()].filter((owners) => owners.size > 1).length;
  return <section className="mt-4 rounded-card border border-serious p-3" data-testid="now-plan-card">
    <h2 className="text-sm font-medium text-ink">Plan waiting for your decision</h2>
    <p className="mt-2 text-sm text-ink">
      {isAmendment
        ? `This amendment adds ${added.length}, changes ${changed.length}, and removes ${removed.length} task${added.length + changed.length + removed.length === 1 ? "" : "s"} from the approved ${approvedTasks.length}-task plan.`
        : `The team proposes ${proposedTasks.length} task${proposedTasks.length === 1 ? "" : "s"} from the agreed specification.`}
    </p>
    <div className="mt-3 space-y-1 text-xs text-ink-secondary" data-testid="plan-evidence">
      <p>{isAmendment ? "changed tasks covered" : "tasks covered"}: {covered}/{focus.length}</p>
      <p>ownership lanes declared: {lanes}/{focus.length}</p>
      <p>ownership collisions in proposed plan: {collisions}</p>
    </div>
    <div className="mt-3 flex items-center gap-2">
      <button type="button" data-testid="plan-approve" onClick={onApprove} className="rounded border border-hairline px-2 py-1 text-xs">Approve</button>
      <button type="button" data-testid="plan-changes" onClick={onChanges} className="rounded border border-hairline px-2 py-1 text-xs">Ask for changes</button>
      <button type="button" data-testid="plan-examine" onClick={() => setOpen(true)} className="text-xs text-ink-muted underline">Examine</button>
    </div>
    <EvidenceDrawer plan={artifact.proposal} open={open} onClose={() => setOpen(false)} />
  </section>;
}
