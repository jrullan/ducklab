import { describe, it, expect } from "vitest";
import { tokenizeShell } from "./ShellCmd";

// A hundred-char command is a wall in monochrome; four rules give the eye
// anchors: env prefixes, the executable, flags, and — loudest — the
// operators that split one command from the next.
describe("the shell tokenizer", () => {
  it("colors the person's own gate command correctly", () => {
    const toks = tokenizeShell(
      "TEST_DATABASE_URL=postgresql://tracker@localhost:55433/test_db .venv/bin/pytest -q && cd frontend && npm run build",
    );
    const kinds = Object.fromEntries(toks.filter((t) => t.kind !== "plain").map((t) => [t.text, t.kind]));
    expect(kinds["TEST_DATABASE_URL=postgresql://tracker@localhost:55433/test_db"]).toBe("env");
    expect(kinds[".venv/bin/pytest"]).toBe("exe");
    expect(kinds["-q"]).toBe("flag");
    expect(kinds["&&"]).toBe("op");
    expect(kinds["cd"]).toBe("exe");
    expect(kinds["npm"]).toBe("exe");
    // "frontend", "run", "build" are arguments — no color claim on them.
    expect(toks.find((t) => t.text === "frontend")?.kind).toBe("plain");
  });

  it("keeps the string byte-for-byte", () => {
    const cmd = `set -a; . ./.env; set +a; DATABASE_URL=x .venv/bin/python app.py`;
    expect(tokenizeShell(cmd).map((t) => t.text).join("")).toBe(cmd);
  });

  it("marks quoted strings", () => {
    const toks = tokenizeShell(`echo "hello world" 'single'`);
    expect(toks.find((t) => t.text === '"hello world"')?.kind).toBe("str");
    expect(toks.find((t) => t.text === "'single'")?.kind).toBe("str");
  });
});
