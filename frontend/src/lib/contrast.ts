/**
 * WCAG contrast, computed rather than eyeballed.
 *
 * The palette documents two deliberate exceptions: --status-warning and
 * --status-serious sit below 3:1 on the light surface. They are not mistakes
 * to be "fixed" by re-picking hues — the icon + label pairing is the agreed
 * mitigation. So the check below encodes the actual rule (text must clear
 * 4.5:1; a status colour must clear 3:1 OR be paired with icon and label)
 * rather than a blanket threshold that would either fail on the exceptions or
 * be lowered until everything passes.
 */

export interface Rgb {
  r: number;
  g: number;
  b: number;
}

export function parseHex(hex: string): Rgb {
  const h = hex.replace("#", "").trim();
  const full =
    h.length === 3
      ? h.split("").map((c) => c + c).join("")
      : h;
  if (!/^[0-9a-fA-F]{6}$/.test(full)) {
    throw new Error(`not a hex colour: ${hex}`);
  }
  return {
    r: parseInt(full.slice(0, 2), 16),
    g: parseInt(full.slice(2, 4), 16),
    b: parseInt(full.slice(4, 6), 16),
  };
}

function channel(v: number): number {
  const s = v / 255;
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

export function relativeLuminance(c: Rgb): number {
  return 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
}

/** WCAG contrast ratio, 1..21. */
export function contrastRatio(a: string, b: string): number {
  const la = relativeLuminance(parseHex(a));
  const lb = relativeLuminance(parseHex(b));
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/** Body text must clear 4.5:1. */
export const TEXT_MIN = 4.5;
/** Non-text and large text must clear 3:1. */
export const UI_MIN = 3;

export interface Palette {
  surface: string;
  textPrimary: string;
  textSecondary: string;
  textMuted: string;
  status: Record<"good" | "warning" | "serious" | "critical", string>;
  series: string[];
}

/** The tokens of 08 §2.1, kept here so the check reads the same values the
 * stylesheet does. A drift between the two is itself a failure worth catching. */
export const LIGHT: Palette = {
  surface: "#fcfcfb",
  textPrimary: "#0b0b0b",
  textSecondary: "#52514e",
  textMuted: "#898781",
  status: { good: "#0ca30c", warning: "#fab219", serious: "#ec835a", critical: "#d03b3b" },
  series: ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"],
};

export const DARK: Palette = {
  surface: "#1a1a19",
  textPrimary: "#ffffff",
  textSecondary: "#c3c2b7",
  textMuted: "#898781",
  status: { good: "#0ca30c", warning: "#fab219", serious: "#ec835a", critical: "#d03b3b" },
  series: ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"],
};

/**
 * Status roles allowed below 3:1 on a given surface, because they always ship
 * with an icon and a label. Listing them explicitly means adding a new
 * exception is a visible edit, not a silently loosened threshold.
 */
export const RELIEF_ALLOWED: Record<"light" | "dark", readonly string[]> = {
  light: ["warning", "serious"],
  dark: [],
};

export interface ContrastFinding {
  token: string;
  ratio: number;
  required: number;
  mode: "light" | "dark";
}

/** Returns every token that fails its required ratio. Empty means clean. */
export function auditPalette(p: Palette, mode: "light" | "dark"): ContrastFinding[] {
  const findings: ContrastFinding[] = [];

  const check = (token: string, colour: string, required: number) => {
    const ratio = contrastRatio(colour, p.surface);
    if (ratio < required) findings.push({ token, ratio, required, mode });
  };

  check("text-primary", p.textPrimary, TEXT_MIN);
  check("text-secondary", p.textSecondary, TEXT_MIN);
  // Muted is metadata, held to the non-text bar.
  check("text-muted", p.textMuted, UI_MIN);

  for (const [role, colour] of Object.entries(p.status)) {
    if (RELIEF_ALLOWED[mode].includes(role)) continue;
    check(`status-${role}`, colour, UI_MIN);
  }
  return findings;
}
