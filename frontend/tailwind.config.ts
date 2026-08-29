import type { Config } from "tailwindcss";

// Tokens mirror 08-DESKTOP-UI.md §2.1 and resolve to CSS custom properties,
// so light/dark swap in one place and no component ever hardcodes a colour.
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        page: "var(--page)",
        surface1: "var(--surface-1)",
        surface2: "var(--surface-2)",
        ink: "var(--text-primary)",
        "ink-secondary": "var(--text-secondary)",
        "ink-muted": "var(--text-muted)",
        hairline: "var(--border)",
        gridline: "var(--gridline)",
        axis: "var(--axis)",
        accent: "var(--accent)",
        good: "var(--status-good)",
        warning: "var(--status-warning)",
        serious: "var(--status-serious)",
        critical: "var(--status-critical)",
      },
      fontFamily: {
        sans: ["system-ui", "-apple-system", "Segoe UI", "sans-serif"],
        mono: ["ui-monospace", "JetBrains Mono", "Cascadia Code", "Menlo", "Consolas", "monospace"],
      },
      fontSize: {
        xs: "11px", sm: "12px", base: "13px", md: "15px",
        lg: "20px", xl: "32px", hero: "48px",
      },
      borderRadius: { DEFAULT: "6px", card: "10px" },
    },
  },
  plugins: [],
} satisfies Config;
