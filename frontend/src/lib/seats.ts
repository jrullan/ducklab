/** Seats: the stable dimension of a line-up.
 *
 * One dropdown per seat instead of one checkbox per duckling — a fleet of ten
 * models made every picker a wall of boxes that widened with each model
 * added. Shared by Settings (saved line-ups) and the run launcher (this run's
 * line-up), because two pickers that disagree about what a seat means would
 * be worse than the wall.
 */

/** How many seats a mode has: fixed for solo and pair, 0 for "as many as you
 * fill" (council, tournament, split — they start at two). */
export function fixedSeats(mode: string): number {
  switch (mode) {
    case "solo":
      return 1;
    case "pair":
      return 2;
  }
  return 0;
}

/** The seats a finished run's roster fills, in seat order for its mode.
 *
 * A roster names EVERY role — architect, judge, scribe, triager included —
 * and Object.values() hands them back in key order, architect first. Seeding
 * a relaunch from that put the run's ARCHITECT in the implementer seat: the
 * panel showed a pair this run never was. Position is meaning (seatLabel),
 * so the extraction must be positional too; modes whose seats the roster
 * cannot name (tournament's contestants, split's workers) fall back to the
 * deduplicated list. */
export function seatsFromRoster(
  mode: string,
  roster: Record<string, string> | undefined,
): string[] {
  const r = roster ?? {};
  switch (mode) {
    case "solo":
      return [r.implementer].filter(Boolean) as string[];
    case "pair":
      return [r.implementer, r.reviewer].filter(Boolean) as string[];
  }
  return Object.values(r).filter((id, i, all) => !!id && all.indexOf(id) === i);
}

/** What the seat's position means in this mode — the position IS the role. */
export function seatLabel(mode: string, i: number): string {
  switch (mode) {
    case "solo":
      return "implementer";
    case "pair":
      return i === 0 ? "implementer" : "reviewer";
    case "council":
      // Named for the ROLE, verb attached: this seat runs as "architect"
      // everywhere else — transcripts, roster, the plan panels' chips — and
      // labelling it only "drafts" left the product's most-configured seat
      // with no findable home in Settings.
      return i === 0 ? "architect · drafts" : `critic ${i}`;
    case "tournament":
      return `contestant ${i + 1}`;
    case "split":
      return `worker ${i + 1}`;
  }
  return `#${i + 1}`;
}
