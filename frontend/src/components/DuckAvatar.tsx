import { ducklingColor } from "../lib/colors";

/** The duck glyph, tinted with the duckling's stable slot colour.
 *
 * It bobs while its turn is in flight (08 §4.4). A thinking turn otherwise
 * looked exactly like a finished one that said nothing — the difference
 * between "wait" and "something is wrong". */
export function DuckAvatar({
  id,
  roster,
  color,
  bobbing = false,
}: {
  id: string;
  roster: readonly string[];
  /** The duckling's fleet colour. Passed in rather than derived from `roster`,
   * which is only ever the list a particular view had to hand: the same model
   * came out blue in one run and orange in the next. */
  color?: string;
  bobbing?: boolean;
}) {
  return (
    <span
      data-testid="duck-avatar"
      data-bobbing={bobbing ? "true" : "false"}
      title={id}
      className={bobbing ? "inline-block duck-bob" : "inline-block"}
      style={{ color: color ?? ducklingColor(id, roster) }}
      aria-hidden="true"
    >
      🦆
    </span>
  );
}
