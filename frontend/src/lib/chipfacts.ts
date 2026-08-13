/** Which facts ride a seat chip — the person's own pick.
 *
 * Chips are glanceable promises, and which facts matter varies by fleet: an
 * all-local fleet cares about context, a mixed fleet about price. Stored
 * beside the other appearance choices (localStorage: display preference,
 * no engine involvement), read at render.
 */

export type ChipFact = "context" | "vision" | "price" | "mprice" | "tools" | "json";

export const CHIP_FACTS: { id: ChipFact; label: string; hint: string }[] = [
  { id: "context", label: "context", hint: "context window, e.g. 384.0k" },
  { id: "vision", label: "vision 👁️", hint: "an eye when the model can be shown images" },
  { id: "price", label: "avg price", hint: "average of declared input/output cost per Mtok" },
  { id: "mprice", label: "measured $/run", hint: "what this duckling actually cost per run it took part in, from this project's own record" },
  { id: "tools", label: "tools 🔧", hint: "a wrench when the model calls tools natively" },
  { id: "json", label: "json {}", hint: "braces when the model has a JSON mode" },
];

const KEY = "ducklab.chipfacts";
const DEFAULT: ChipFact[] = ["context", "vision"];

export function loadChipFacts(): ChipFact[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return DEFAULT;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return DEFAULT;
    const valid = parsed.filter((f): f is ChipFact => CHIP_FACTS.some((c) => c.id === f));
    return valid;
  } catch {
    return DEFAULT;
  }
}

export function saveChipFacts(facts: ChipFact[]) {
  localStorage.setItem(KEY, JSON.stringify(facts));
}
