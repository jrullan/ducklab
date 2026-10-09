import { useEffect, useRef, useState } from "react";
import type { DiagnosticDefaultsView, Duckling, EngineClient } from "../api/client";
import { useRuns } from "../store/runs";
import { useConsultant } from "../store/consultant";
import { canSeeImages, knownBlind } from "../lib/vision";
import { ConsultantPicker, ImageChips, SwitchToSeeing, VisionNote, useImageDraft } from "./ConsultantVision";

/** A conversation that ended, however it ended, is a record, not a door. */
const TERMINAL = new Set(["done", "failed", "aborted", "canceled", "cancelled", "ended"]);

/** "Chat about this": a conversation with a chosen duckling about one
 * subject, its history as context, read-only tools to investigate. The chat
 * is a run; starting one — or continuing one — opens it in the consultant
 * pane (B-514), which stays beside whatever the person is looking at. It used
 * to land them in the run view, and every look at another run meant leaving
 * the conversation and finding it again through Runs. */
export function ChatAbout({
  client,
  projectId,
  aboutKind,
  aboutId,
  ducklings,
  label = "chat about this",
  placeholder,
  preselectedDuckling = "",
  /** A finding can open a consultation with its evidence already in the draft. */
  initialMessage = "",
  startOpen = false,
  onCancel,
}: {
  client: EngineClient;
  projectId: string;
  /** "ducklab" is the harness itself: the consultant gets the embedded
   * concept dossier plus the project's live state instead of one subject's
   * history — the guide rail says WHAT, this chat explains WHY. */
  aboutKind: "bug" | "task" | "run" | "ducklab" | "document";
  aboutId: string;
  ducklings: readonly Duckling[];
  label?: string;
  placeholder?: string;
  /** The resolved Common consultant, when the roster pins one. */
  preselectedDuckling?: string;
  initialMessage?: string;
  startOpen?: boolean;
  /** Where "cancel" goes when the form has no closed state to fall back to
   * (the consultant pane's new-conversation view). */
  onCancel?: () => void;
}) {
  const [open, setOpen] = useState(startOpen);
  const [duckling, setDuckling] = useState(preselectedDuckling);
  // B-285: a chat about this subject already open is the door back to it.
  // The engine stamps every chat run with its subject; the runs store is the
  // same one Now reads, so no second request is needed.
  const runs = useRuns((s) => s.runs);
  const subject = `chat about ${aboutKind} ${aboutId}`;
  const liveChat = Object.values(runs)
    .filter((r) => r.stage === "chat" && r.note === subject && !TERMINAL.has(String(r.status).toLowerCase()))
    .sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""))[0];
  const pickerTouched = useRef(false);
  // The consultant is a roster decision, not a second question at the chat door.
  // Resolve it here so project, task, and bug chats all share the same seat.
  useEffect(() => {
    if (!open || preselectedDuckling || typeof client.roster !== "function") return;
    // Give a person who is about to pick a seat precedence over the async
    // roster lookup, while filling an untouched picker from the resolved seat.
    if (pickerTouched.current) return;
    let cancelled = false;
    void client.roster(projectId).then(({ entries }) => {
      if (cancelled || pickerTouched.current) return;
      const consultant = entries.find((entry) => entry.role === "consultant")?.duckling;
      if (consultant) setDuckling((current) => current || consultant);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [client, projectId, preselectedDuckling, open]);
  const [message, setMessage] = useState(initialMessage);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const draft = useImageDraft();
  const images = draft.images;
  const [triedToAttach, setTriedToAttach] = useState(false);
  const [diagnostics, setDiagnostics] = useState<DiagnosticDefaultsView | null>(null);
  const [inspectHarness, setInspectHarness] = useState(false);
  const [bugTarget, setBugTarget] = useState<"subject" | "harness">("subject");
  const imageInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (!open || typeof client.diagnosticDefaults !== "function") return;
    let cancelled = false;
    void client.diagnosticDefaults().then((value) => {
      if (!cancelled) setDiagnostics(value);
    }).catch(() => {
      if (!cancelled) setDiagnostics(null);
    });
    return () => { cancelled = true; };
  }, [client, open]);
  const harnessAvailable = !!diagnostics?.available && !!diagnostics.harness_project_id && diagnostics.harness_project_id !== projectId;
  // The roster arrives after the rail. Fill an untouched picker when it does,
  // but never replace a person's free choice.
  useEffect(() => {
    if (preselectedDuckling) setDuckling((current) => current || preselectedDuckling);
  }, [preselectedDuckling]);
  const selectedDuckling = ducklings.find((d) => d.id === duckling);
  const canSee = canSeeImages(selectedDuckling);
  // Known blind, not merely unknown: a duckling missing from the list is the
  // engine's to judge, and the desktop must not claim it cannot see.
  const blind = !!duckling && knownBlind(selectedDuckling);
  const holdingImages = blind && images.length > 0;
  const pick = (id: string) => { pickerTouched.current = true; setDuckling(id); setTriedToAttach(false); };
  if (!open && liveChat) {
    return (
      <button
        type="button"
        data-testid="chat-about-existing"
        onClick={() => useConsultant.getState().openChat(liveChat.id)}
        title="Opens the conversation in the consultant pane"
        className="text-left text-xs text-ink underline"
      >
        continue the chat ({liveChat.id})
      </button>
    );
  }
  if (!open) {
    return (
      <button
        type="button"
        data-testid="chat-about"
        onClick={() => setOpen(true)}
        className="text-xs text-ink-muted underline"
      >
        {label}
      </button>
    );
  }
  return (
    <div
      className="space-y-1 rounded border border-hairline p-2"
      data-testid="chat-about-form"
      onDragOver={draft.onDragOver}
      onDrop={(e) => { draft.onDrop(e); setTriedToAttach(true); }}
    >
      <ConsultantPicker ducklings={ducklings} value={duckling} onChange={pick} />
      <VisionNote duckling={selectedDuckling} id={duckling} />
      <textarea
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        onPaste={(e) => {
          const pastedImage = Array.from(e.clipboardData?.files ?? []).some((file) => file.type.startsWith("image/"));
          draft.onPaste(e);
          if (pastedImage) setTriedToAttach(true);
        }}
        placeholder={placeholder ?? `e.g. this ${aboutKind} is not actually fixed — investigate why`}
        data-testid="chat-message"
        rows={2}
        className="w-full rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
      />
      {harnessAvailable ? (
        <div className="rounded border border-hairline bg-surface2 p-2 text-xs" data-testid="chat-diagnostic-scope">
          <label className="flex items-center gap-2 text-ink-secondary">
            <input
              type="checkbox"
              data-testid="chat-inspect-harness"
              checked={inspectHarness}
              onChange={(e) => {
                setInspectHarness(e.target.checked);
                if (!e.target.checked) setBugTarget("subject");
              }}
            />
            Also inspect {diagnostics?.harness_project_name || diagnostics?.harness_project_id}
          </label>
          <p className="mt-1 text-ink-muted">Adds its source, runs and bug board as a named read-only scope.</p>
          {inspectHarness && (
            <label className="mt-2 flex items-center gap-2 text-ink-muted">
              File bugs requested in this chat in
              <select
                data-testid="chat-bug-target"
                value={bugTarget}
                onChange={(e) => setBugTarget(e.target.value as "subject" | "harness")}
                className="rounded border border-hairline bg-surface px-1 py-0.5 text-ink-secondary"
              >
                <option value="subject">this project</option>
                <option value="harness">{diagnostics?.harness_project_name || "Ducklab"}</option>
              </select>
            </label>
          )}
        </div>
      ) : diagnostics?.harness_project_id && diagnostics.harness_project_id !== projectId ? (
        <p className="text-xs text-warning" data-testid="chat-diagnostic-unavailable">
          Configured harness project {diagnostics.harness_project_name || diagnostics.harness_project_id} is unavailable. Repair its registered path or choose another project in Settings → Engine.
        </p>
      ) : diagnostics && diagnostics.harness_project_id !== projectId ? (
        <p className="text-xs text-ink-muted" data-testid="chat-diagnostic-unavailable">
          Cross-project diagnosis is not configured. Choose a harness project in Settings → Engine.
        </p>
      ) : null}
      <ImageChips images={images} onRemove={draft.remove} />
      <input ref={imageInput} type="file" accept="image/*" multiple data-testid="chat-image" className="hidden" onChange={(e) => { draft.add(Array.from(e.target.files ?? [])); setTriedToAttach(true); e.currentTarget.value = ""; }} />
      <div className="flex items-center gap-2">
        <button
          type="button"
          data-testid="chat-add-image"
          title={canSee ? "Add images" : blind ? `${duckling} can't see images — click to see who can` : "Pick a duckling that can see images (👁) to attach screenshots"}
          onClick={() => {
            // Blind, or nobody picked yet: explain and offer the seeing
            // ducklings instead of a file dialog whose result nobody sees.
            if (canSee || (duckling && !blind)) imageInput.current?.click();
            else setTriedToAttach(true);
          }}
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          Add image
        </button>
        <button
          type="button"
          data-testid="chat-start"
          disabled={busy || !duckling || !message.trim() || holdingImages}
          title={holdingImages ? `${duckling} can't see the attached images: pick a duckling that can, or remove them` : undefined}
          onClick={() => {
            setBusy(true);
            setError(null);
            void client
              .chatStart(projectId, {
                duckling,
                aboutKind,
                aboutId,
                message: message.trim(),
                images: images.map((image) => image.data),
                diagnosticScope: inspectHarness ? "subject+harness" : "subject",
                bugTarget: inspectHarness ? bugTarget : "subject",
              })
              .then((r) => {
                draft.clear();
                setOpen(startOpen);
                setMessage(initialMessage);
                useConsultant.getState().openChat(r.id);
              })
              .catch((e) => setError(e instanceof Error ? e.message : String(e)))
              .finally(() => setBusy(false));
          }}
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          {busy ? "Starting…" : "Start chat"}
        </button>
        <button type="button" data-testid="chat-cancel" onClick={() => (onCancel ? onCancel() : setOpen(false))} className="text-xs text-ink-muted underline">
          cancel
        </button>
      </div>
      {(blind || !duckling) && (triedToAttach || images.length > 0) && (
        <SwitchToSeeing
          ducklings={ducklings}
          current={duckling}
          reason={!duckling
            ? "Pick who to talk to first. Only a duckling that can see images (👁) can look at a screenshot."
            : images.length > 0
              ? `${duckling} can't see images, so it would not see ${images.length === 1 ? "this screenshot" : "these screenshots"}. Pick a duckling that can, or remove ${images.length === 1 ? "it" : "them"}.`
              : `${duckling} can't see images. To show a screenshot, pick a duckling that can.`}
          onSwitch={pick}
        />
      )}
      {draft.error && <p className="text-xs text-critical" data-testid="chat-image-error">{draft.error}</p>}
      {error && <p className="text-xs text-critical" data-testid="chat-error">{error}</p>}
    </div>
  );
}
