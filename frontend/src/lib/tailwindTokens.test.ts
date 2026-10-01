import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";

const SRC = path.resolve(__dirname, "..");

function sources(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) sources(file, out);
    else if (/\.(ts|tsx)$/.test(entry.name) && !entry.name.includes(".test.")) out.push(file);
  }
  return out;
}

describe("semantic Tailwind colors", () => {
  it("uses the configured warning token instead of the nonexistent warn token", () => {
    const invalid = sources(SRC).filter((file) =>
      /(?:border|text|bg)-warn(?!ing)/.test(fs.readFileSync(file, "utf8")),
    );
    expect(invalid, `invalid *-warn classes in: ${invalid.join(", ")}`).toEqual([]);
  });
});
