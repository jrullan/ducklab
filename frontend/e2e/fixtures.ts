import { test as base, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

// package.json sets "type": "module", so __dirname does not exist here.
const HERE = dirname(fileURLToPath(import.meta.url));
const ENGINE_BIN = resolve(HERE, "../../fake-engine-bin");

export interface FakeEngine {
  port: number;
  token: string;
  stop: () => void;
}

/** Starts cmd/fake-engine and waits for it to report its port. */
export async function startFakeEngine(scenario: string, delayMs = 20): Promise<FakeEngine> {
  return new Promise((resolvePromise, reject) => {
    const proc: ChildProcess = spawn(
      ENGINE_BIN,
      ["--port", "0", "--scenario", scenario, "--delay-ms", String(delayMs),
       "--allow-origin", "http://127.0.0.1:5178"],
      { stdio: ["ignore", "pipe", "pipe"] },
    );
    const timer = setTimeout(() => reject(new Error("fake-engine did not start")), 10_000);
    proc.stdout!.on("data", (chunk: Buffer) => {
      const m = /127\.0\.0\.1:(\d+) token=(\S+)/.exec(chunk.toString());
      if (m) {
        clearTimeout(timer);
        resolvePromise({
          port: Number(m[1]),
          token: m[2]!,
          stop: () => proc.kill("SIGKILL"),
        });
      }
    });
    proc.on("error", reject);
  });
}

/** Injects the engine connection details the Wails host normally provides. */
export async function connect(page: Page, engine: FakeEngine) {
  await page.addInitScript(
    ([port, token]) => {
      (window as unknown as { ducklab: unknown }).ducklab = {
        baseUrl: `http://127.0.0.1:${port}`,
        token,
        version: "0.3.0",
      };
    },
    [engine.port, engine.token] as const,
  );
}

export const test = base;
export { expect } from "@playwright/test";
