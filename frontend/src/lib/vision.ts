import type { Duckling } from "../api/client";

/** Whether a consultant can see a screenshot, said the way the chat decides it
 * (B-512). The pickers listed bare ids, the composer disabled "Add image" with
 * only a hover title for a reason, and a person found out a duckling was blind
 * by being refused.
 *
 * - verified: declared, and an image test passed on its server;
 * - declared: declared, not yet tested — the first screenshot tests it;
 * - refuted:  declared, but its server rejected an image (no vision projector);
 * - none:     text only;
 * - unknown:  not in the fleet this view has (still loading, or removed) —
 *             the engine decides, so the desktop does not claim either way. */
export type VisionState = "verified" | "declared" | "refuted" | "none" | "unknown";

export function visionState(duckling: Duckling | undefined): VisionState {
  if (!duckling) return "unknown";
  const status = duckling.vision_status;
  if (status === "verified" || status === "declared" || status === "refuted" || status === "none") return status;
  // An engine that predates vision_status: the listed capability is all there is.
  return duckling.caps?.vision ? "declared" : "none";
}

/** The chat accepts screenshots for this duckling. */
export function canSeeImages(duckling: Duckling | undefined): boolean {
  const state = visionState(duckling);
  return state === "verified" || state === "declared";
}

/** Known not to see: the case every composer must say out loud. */
export function knownBlind(duckling: Duckling | undefined): boolean {
  const state = visionState(duckling);
  return state === "none" || state === "refuted";
}

/** The marker beside a duckling's name in a picker. Plain words after the eye,
 * because a native <option> renders text only and the eye alone is a code. */
export function visionMark(duckling: Duckling | undefined): string {
  switch (visionState(duckling)) {
    case "verified":
      return "👁 sees images";
    case "declared":
      return "👁 sees images (not yet tested)";
    case "refuted":
      return "no images: its server has no vision support";
    default:
      return "";
  }
}

/** A picker option's label: the id, then the marker when there is one. */
export function pickerLabel(duckling: Duckling): string {
  const mark = visionMark(duckling);
  return mark ? `${duckling.id} · ${mark}` : duckling.id;
}

/** The legend every consultant picker shows once, under itself. */
export const VISION_LEGEND =
  "👁 sees images — it can look at screenshots you attach. “Not yet tested” means its settings say it can see; the first screenshot checks. No mark: text only.";

/** One sentence for the composer, at the point of use. Empty when there is
 * nothing to warn about. */
export function visionSentence(duckling: Duckling | undefined, id: string): string {
  switch (visionState(duckling)) {
    case "none":
      return `${id} can't see images — it reads text only, so screenshots won't reach it.`;
    case "refuted":
      return `${id} can't see images — its settings say it can, but its server rejected a test image (no vision support loaded).`;
    default:
      return "";
  }
}

/** Ducklings that can see, confirmed ones first, excluding the current one. */
export function seeingDucklings(ducklings: readonly Duckling[], except = ""): Duckling[] {
  const rank = (d: Duckling) => (visionState(d) === "verified" ? 0 : 1);
  return ducklings
    .filter((d) => d.id !== except && canSeeImages(d))
    .sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
}
