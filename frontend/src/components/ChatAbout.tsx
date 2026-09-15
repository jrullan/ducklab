import { useEffect, useRef, useState } from "react";
import type { DiagnosticDefaultsView, Duckling, EngineClient } from "../api/client";
import { useRuns } from "../store/runs";

/** A conversation that ended, however it ended, is a record, not a door. */
const TERMINAL = new Set(["done", "failed", "aborted", "canceled", "cancelled", "ended"]);

/** "Chat about this": a conversation with a chosen duckling about one
 * subject, its history as context, read-only tools to investigate. The chat
 * is a run; starting one lands the person in the run view, which is the
 * conversation panel. */
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
  const [images, setImages] = useState<{ name: string; data: string }[]>([]);
  const [imageError, setImageError] = useState<string | null>(null);
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
  const canSee = !!selectedDuckling?.caps?.vision;
  const readImages = (files: FileList | null) => {
    if (!files) return;
    setImageError(null);
    const picked = Array.from(files);
    if (picked.some((file) => !file.type.startsWith("image/"))) {
      setImageError("Only image files can be attached.");
      return;
    }
    void Promise.all(picked.map((file) => new Promise<{ name: string; data: string }>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve({ name: file.name, data: String(reader.result) });
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    }))).then((pickedImages) => setImages((current) => [...current, ...pickedImages]))
      .catch(() => setImageError("Could not read the selected image."));
  };
  if (!open && liveChat) {
    return (
      <a href={`#/runs/${liveChat.id}`} data-testid="chat-about-existing" className="text-xs text-ink underline">
        continue the chat ({liveChat.id})
      </a>
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
    <div className="space-y-1 rounded border border-hairline p-2" data-testid="chat-about-form">
      <select
        value={duckling}
        onChange={(e) => { pickerTouched.current = true; setDuckling(e.target.value); }}
        data-testid="chat-duckling"
        className="w-full rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
      >
        <option value="">pick a duckling…</option>
        {ducklings.map((d) => (
          <option key={d.id} value={d.id}>{d.id}</option>
        ))}
      </select>
      <textarea
        value={message}
        onChange={(e) => setMessage(e.target.value)}
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
      ) : diagnostics && diagnostics.harness_project_id !== projectId ? (
        <p className="text-xs text-ink-muted" data-testid="chat-diagnostic-unavailable">
          Cross-project diagnosis is not configured. Choose a harness project in Settings → Engine.
        </p>
      ) : null}
      {images.length > 0 && (
        <div className="flex flex-wrap gap-1" data-testid="chat-image-chips">
          {images.map((image, index) => (
            <span key={`${image.name}-${index}`} data-testid="chat-image-chip" className="flex items-center gap-1 rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs">
              <img src={image.data} alt="" className="h-6 w-6 object-cover" />
              {image.name}
              <button type="button" aria-label={`remove image ${image.name}`} onClick={() => setImages((current) => current.filter((_, i) => i !== index))}>×</button>
            </span>
          ))}
        </div>
      )}
      <input ref={imageInput} type="file" accept="image/*" multiple data-testid="chat-image" className="hidden" onChange={(e) => { readImages(e.target.files); e.currentTarget.value = ""; }} />
      <div className="flex items-center gap-2">
        <button
          type="button"
          data-testid="chat-add-image"
          disabled={!canSee}
          title={canSee ? "Add images" : "Pick a duckling with vision to attach images"}
          onClick={() => imageInput.current?.click()}
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          Add image
        </button>
        <button
          type="button"
          data-testid="chat-start"
          disabled={busy || !duckling || !message.trim()}
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
                setImages([]);
                location.hash = `#/runs/${r.id}`;
              })
              .catch((e) => setError(e instanceof Error ? e.message : String(e)))
              .finally(() => setBusy(false));
          }}
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          {busy ? "Starting…" : "Start chat"}
        </button>
        <button type="button" onClick={() => setOpen(false)} className="text-xs text-ink-muted underline">
          cancel
        </button>
      </div>
      {imageError && <p className="text-xs text-critical" data-testid="chat-image-error">{imageError}</p>}
      {error && <p className="text-xs text-critical" data-testid="chat-error">{error}</p>}
    </div>
  );
}
