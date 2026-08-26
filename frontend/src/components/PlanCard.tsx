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
  const raw = section.fields?.files ?? section.fields?.paths ?? section.fields?.file ?? "";
  return raw.split(/[\n,]+/).map((file) => file.trim()).filter(Boolean);
}

export function PlanCard({ artifact, traceErrors, onApprove, onChanges }: {
  artifact: Artifact;
  traceErrors: TraceError[];
  onApprove: () => void;
  onChanges: () => void;
}) {
  const [open, setOpen] = useState(false);
  const sections = allSections(artifact.proposal?.sections ?? artifact.sections);
  const tasks = sections.filter((section) => /^M[-_]?\d+/i.test(section.id) || section.id.startsWith("T-"));
  const taskCount = tasks.length || sections.length;
  const lanes = tasks.map((task) => task.fields?.lane || task.fields?.owner || "").filter(Boolean);
  const parallel = new Set(lanes).size || (taskCount ? 1 : 0);
  // Trace errors are the deterministic missing links in the proposal. Keep the
  // evidence grounded in that check rather than in the stage-name list.
  const covered = Math.max(0, taskCount - traceErrors.length);
  const ownersByFile = new Map<string, Set<string>>();
  for (const task of tasks) {
    const owner = task.fields?.owner || task.fields?.lane || task.id;
    for (const file of declaredFiles(task)) {
      const owners = ownersByFile.get(file) ?? new Set<string>();
      owners.add(owner);
      ownersByFile.set(file, owners);
    }
  }
  const collisions = [...ownersByFile.values()].filter((owners) => owners.size > 1).length;
  return <section className="mt-4 rounded-card border border-serious p-3" data-testid="now-plan-card">
    <h2 className="text-sm font-medium text-ink">Plan waiting for your decision</h2>
    <p className="mt-2 text-sm text-ink">The team turned the agreed spec into {taskCount} tasks — it is waiting for you to approve the scope before any duckling touches code.</p>
    <div className="mt-3 space-y-1 text-xs text-ink-secondary" data-testid="plan-evidence">
      <p>criteria covered: {covered}</p>
      <p>tasks proposed: {taskCount} · can run in parallel: {parallel}</p>
      <p>files with two owners: {collisions}</p>
    </div>
    <div className="mt-3 flex items-center gap-2">
      <button type="button" data-testid="plan-approve" onClick={onApprove} className="rounded border border-hairline px-2 py-1 text-xs">Approve</button>
      <button type="button" data-testid="plan-changes" onClick={onChanges} className="rounded border border-hairline px-2 py-1 text-xs">Ask for changes</button>
      <button type="button" data-testid="plan-examine" onClick={() => setOpen(true)} className="text-xs text-ink-muted underline">Examine</button>
    </div>
    <EvidenceDrawer plan={artifact.proposal} open={open} onClose={() => setOpen(false)} />
  </section>;
}
