import { useState } from "react";
import type { ClipboardEvent, DragEvent } from "react";
import type { Duckling } from "../api/client";
import { VISION_LEGEND, pickerLabel, seeingDucklings, visionMark, visionSentence, visionState } from "../lib/vision";

/** A consultant duckling picker that says which ducklings can see (B-512).
 * Every place a person chooses who to talk to uses this one, so the marker
 * and its legend cannot drift between the start form and the live chat. */
export function ConsultantPicker({
  ducklings,
  value,
  onChange,
  testId = "chat-duckling",
  disabled = false,
  title,
  placeholder = "pick a duckling…",
  ariaLabel = "consultant duckling",
  className = "w-full rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs",
}: {
  ducklings: readonly Duckling[];
  value: string;
  onChange: (id: string) => void;
  testId?: string;
  disabled?: boolean;
  title?: string;
  placeholder?: string;
  ariaLabel?: string;
  className?: string;
}) {
  const known = ducklings.some((d) => d.id === value);
  return (
    <div className="min-w-0 space-y-0.5">
      <select
        aria-label={ariaLabel}
        value={value}
        disabled={disabled}
        title={title}
        onChange={(e) => onChange(e.target.value)}
        data-testid={testId}
        className={className}
      >
        {!value && <option value="">{placeholder}</option>}
        {/* A seated duckling missing from the fleet (removed since) still
            shows as itself rather than as the first option. */}
        {value && !known && <option value={value}>{value}</option>}
        {ducklings.map((d) => (
          <option key={d.id} value={d.id} data-vision={visionState(d)}>
            {pickerLabel(d)}
          </option>
        ))}
      </select>
      <p className="text-[11px] leading-snug text-ink-muted" data-testid={`${testId}-legend`}>{VISION_LEGEND}</p>
    </div>
  );
}

/** The composer's plain statement that the current consultant cannot see,
 * visible without hovering anything. */
export function VisionNote({ duckling, id }: { duckling: Duckling | undefined; id: string }) {
  const sentence = id ? visionSentence(duckling, id) : "";
  if (!sentence) return null;
  return (
    <p className="text-xs text-warning" data-testid="chat-vision-note">
      {sentence}
    </p>
  );
}

/** Offered when a person tries to give a blind consultant a screenshot: why it
 * cannot take it, and the ducklings that can. */
export function SwitchToSeeing({
  ducklings,
  current,
  reason,
  onSwitch,
  disabled = false,
  disabledWhy,
}: {
  ducklings: readonly Duckling[];
  current: string;
  reason: string;
  onSwitch: (id: string) => void;
  disabled?: boolean;
  disabledWhy?: string;
}) {
  const seeing = seeingDucklings(ducklings, current);
  return (
    <div className="mt-1 rounded border border-warning p-2 text-xs" role="status" data-testid="chat-switch-offer">
      <p className="text-ink">{reason}</p>
      {seeing.length > 0 ? (
        <div className="mt-1 flex flex-wrap items-center gap-1">
          <span className="text-ink-muted">Switch to a duckling that can see:</span>
          {seeing.slice(0, 4).map((d) => (
            <button
              key={d.id}
              type="button"
              data-testid={`chat-switch-to-${d.id}`}
              disabled={disabled}
              title={disabled ? disabledWhy : `Continue this conversation with ${d.id}`}
              onClick={() => onSwitch(d.id)}
              className="rounded border border-hairline px-2 py-0.5 disabled:opacity-40"
            >
              {d.id} · {visionMark(d)}
            </button>
          ))}
        </div>
      ) : (
        <p className="mt-1 text-ink-muted" data-testid="chat-switch-none">
          No duckling in your fleet can see images. Add one, or tick “vision” on a model that can, in Settings → Ducklings &amp; providers.
        </p>
      )}
      {disabled && disabledWhy && <p className="mt-1 text-ink-muted">{disabledWhy}</p>}
    </div>
  );
}

export type DraftImage = { name: string; data: string };

/** Screenshots on their way into a chat message, from any route: the picker
 * button, a paste, a drop. One draft so every route behaves the same. */
export function useImageDraft() {
  const [images, setImages] = useState<DraftImage[]>([]);
  const [error, setError] = useState<string | null>(null);
  const add = (files: readonly File[]) => {
    if (files.length === 0) return;
    setError(null);
    if (files.some((file) => !file.type.startsWith("image/"))) {
      setError("Only image files can be attached.");
      return;
    }
    void Promise.all(files.map((file) => new Promise<DraftImage>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve({ name: file.name || "pasted image", data: String(reader.result) });
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    }))).then((picked) => setImages((current) => [...current, ...picked]))
      .catch(() => setError("Could not read the selected image."));
  };
  // A paste of text stays a paste of text; only pasted image files attach.
  const onPaste = (event: ClipboardEvent) => {
    const files = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith("image/"));
    if (files.length === 0) return;
    event.preventDefault();
    add(files);
  };
  const onDragOver = (event: DragEvent) => {
    if (Array.from(event.dataTransfer?.types ?? []).includes("Files")) event.preventDefault();
  };
  const onDrop = (event: DragEvent) => {
    const files = Array.from(event.dataTransfer?.files ?? []);
    if (files.length === 0) return;
    event.preventDefault();
    add(files);
  };
  return {
    images,
    error,
    setError,
    add,
    onPaste,
    onDragOver,
    onDrop,
    remove: (index: number) => setImages((current) => current.filter((_, i) => i !== index)),
    clear: () => setImages([]),
  };
}

export function ImageChips({ images, onRemove }: { images: readonly DraftImage[]; onRemove: (index: number) => void }) {
  if (images.length === 0) return null;
  return (
    <div className="mt-1 flex flex-wrap gap-1" data-testid="chat-image-chips">
      {images.map((image, index) => (
        <span key={`${image.name}-${index}`} data-testid="chat-image-chip" className="flex items-center gap-1 rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs">
          <img src={image.data} alt="" className="h-6 w-6 object-cover" />
          {image.name}
          <button type="button" aria-label={`remove image ${image.name}`} onClick={() => onRemove(index)}>×</button>
        </span>
      ))}
    </div>
  );
}
