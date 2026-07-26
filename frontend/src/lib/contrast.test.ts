import { describe, it, expect } from "vitest";
import {
  contrastRatio, parseHex, auditPalette, LIGHT, DARK,
  RELIEF_ALLOWED, TEXT_MIN, UI_MIN,
} from "./contrast";
import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

describe("contrastRatio", () => {
  it("matches known WCAG values", () => {
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 1);
    expect(contrastRatio("#ffffff", "#ffffff")).toBeCloseTo(1, 3);
  });
  it("is symmetric", () => {
    expect(contrastRatio("#2a78d6", "#fcfcfb")).toBeCloseTo(contrastRatio("#fcfcfb", "#2a78d6"), 6);
  });
  it("parses shorthand hex", () => {
    expect(parseHex("#fff")).toEqual({ r: 255, g: 255, b: 255 });
  });
  it("rejects a non-colour rather than returning a plausible number", () => {
    expect(() => parseHex("var(--x)")).toThrow();
  });
});

// AC-36: both themes audited, with the documented exceptions encoded rather
// than the threshold lowered until everything passes.
describe("palette audit", () => {
  it("light mode has no unexpected failures", () => {
    const findings = auditPalette(LIGHT, "light");
    expect(findings, JSON.stringify(findings, null, 2)).toEqual([]);
  });

  it("dark mode has no unexpected failures", () => {
    const findings = auditPalette(DARK, "dark");
    expect(findings, JSON.stringify(findings, null, 2)).toEqual([]);
  });

  it("body text clears 4.5:1 in both modes", () => {
    for (const p of [LIGHT, DARK]) {
      expect(contrastRatio(p.textPrimary, p.surface)).toBeGreaterThanOrEqual(TEXT_MIN);
      expect(contrastRatio(p.textSecondary, p.surface)).toBeGreaterThanOrEqual(TEXT_MIN);
    }
  });

  // The exceptions are real and must stay visible: if one of these ever clears
  // 3:1 on its own, the relief entry should be removed rather than left as a
  // permanently unnecessary allowance.
  it("the light-mode exceptions are genuinely below 3:1", () => {
    for (const role of RELIEF_ALLOWED.light) {
      const colour = LIGHT.status[role as keyof typeof LIGHT.status];
      expect(contrastRatio(colour, LIGHT.surface)).toBeLessThan(UI_MIN);
    }
  });

  it("dark mode needs no relief at all", () => {
    expect(RELIEF_ALLOWED.dark).toHaveLength(0);
    for (const colour of Object.values(DARK.status)) {
      expect(contrastRatio(colour, DARK.surface)).toBeGreaterThanOrEqual(UI_MIN);
    }
  });

  it("every series slot is distinguishable from its own surface", () => {
    for (const [name, p] of [["light", LIGHT], ["dark", DARK]] as const) {
      p.series.forEach((c, i) => {
        const r = contrastRatio(c, p.surface);
        // Series marks are shapes, not text; 1.5:1 is the floor at which a
        // fill stops disappearing into the background entirely.
        expect(r, `${name} series-${i + 1} (${c}) = ${r.toFixed(2)}`).toBeGreaterThan(1.5);
      });
    }
  });
});

// The audit is only meaningful if it reads the same values the stylesheet uses.
describe("tokens.css agreement", () => {
  const css = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), "../app/tokens.css"),
    "utf8",
  );

  it("declares every colour the audit checks", () => {
    for (const colour of [
      LIGHT.surface, LIGHT.textPrimary, LIGHT.textSecondary, LIGHT.textMuted,
      ...Object.values(LIGHT.status), ...LIGHT.series,
      DARK.surface, DARK.textPrimary, DARK.textSecondary, ...DARK.series,
    ]) {
      expect(css.toLowerCase(), `missing ${colour}`).toContain(colour.toLowerCase());
    }
  });

  // Dark must be a selected set under both scopes, or the in-app toggle cannot
  // beat the OS setting in both directions.
  it("declares dark under both the media query and the data-theme scope", () => {
    expect(css).toContain("prefers-color-scheme: dark");
    expect(css).toContain('[data-theme="dark"]');
    expect(css).toContain('[data-theme="light"]');
  });
});
