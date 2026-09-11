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
      return 2;
    case "pair":
      return 3;
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
    // Position is meaning, so an unfilled seat stays an EMPTY position: a
    // pair recorded without an advisor used to compact to [implementer,
    // reviewer], and the role-keyed launcher then read the reviewer as the
    // advisor (B-394). Trailing empties are dropped; inner ones are kept.
    case "solo":
      return trimTrailingEmpty([r.implementer ?? "", r.advisor ?? ""]);
    case "pair":
      return trimTrailingEmpty([r.implementer ?? "", r.advisor ?? "", r.reviewer ?? ""]);
    case "tournament":
    case "split":
      // A run record names one duckling per role; the contestants / workers
      // are not recoverable from it, and a one-name positional list fails the
      // mode's cardinality. Seed nothing: the roster resolves the line-up.
      return [];
  }
  return Object.values(r).filter((id, i, all) => !!id && all.indexOf(id) === i);
}

function trimTrailingEmpty(seats: string[]): string[] {
  const out = [...seats];
  while (out.length && !out[out.length - 1]) out.pop();
  return out;
}

/** What the seat's position means in this mode — the position IS the role. */
export function seatLabel(mode: string, i: number): string {
  switch (mode) {
    case "solo":
      return i === 0 ? "implementer" : "advisor";
    case "pair":
      return i === 0 ? "implementer" : i === 1 ? "advisor" : "reviewer";
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

/** The roles a mode actually seats, in the order they speak. The run record's
 * roster names EVERY role (the engine resolves them all so a seat can be
 * found by role), but a pair run does not seat a judge, a scribe or a triager
 * — showing them read as "my whole team is on this run". The advisor is a real
 * seat of every task mode now (the rubber duck). Unknown modes show all. */
export function rolesForMode(mode: string): string[] | null {
  switch (mode) {
    case "solo":
      return ["implementer", "advisor"];
    case "pair":
      return ["implementer", "advisor", "reviewer"];
    case "tournament":
      return ["implementer", "advisor", "judge"];
    case "split":
      return ["architect", "implementer", "advisor", "reviewer"];
    case "council":
      return ["architect", "reviewer", "advisor"];
    case "triage":
      return ["triager"];
    case "release":
      return ["scribe"];
    case "chat":
      return null;
  }
  return null;
}
