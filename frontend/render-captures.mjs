#!/usr/bin/env node
// The [render] contract lives in .ducklab/project.toml. This command is
// driven by the engine from the run worktree and writes PNGs to its output dir.
//
// [render]
// command = "node frontend/render-captures.mjs"
// url = "http://127.0.0.1:5173/?engine={engine}&token={token}"
// ready = "http://127.0.0.1:5173/"
// scenes = ["#/now", "#/documents", "#/records/runs"]
// viewport = "1440x900"
// timeout_s = 120
// artifacts = ".ducklab-render-captures/*.png"
//
import { spawn } from "node:child_process";
import { mkdir, rm } from "node:fs/promises";
import { chromium } from "./node_modules/playwright/index.mjs";

// The engine supplies the URL (including {engine}/{token} interpolation) through
// DUCKLAB_RENDER_URL; this script remains a thin command-side renderer.
const url = process.env.DUCKLAB_RENDER_URL || "http://127.0.0.1:5173";
const token = process.env.DUCKLAB_RENDER_TOKEN || "";
const ready = process.env.DUCKLAB_RENDER_READY;
const scenes = (process.env.DUCKLAB_RENDER_SCENES || "/").split("\n").filter(Boolean);
const output = process.env.DUCKLAB_RENDER_OUTPUT || ".ducklab-render-captures";
const viewport = (process.env.DUCKLAB_RENDER_VIEWPORT || "1440x900").split("x").map(Number);
const timeout = Number(process.env.DUCKLAB_RENDER_TIMEOUT_S || 120) * 1000;
const readyRetryDelay = 250;
await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true });
let server;
if (process.env.DUCKLAB_RENDER_START !== "false") {
  // Keep the server and all of its descendants in a separate process group.
  // Piping (and draining) their output prevents inherited descriptors from
  // keeping the render command's CombinedOutput pipes open after this exits.
  server = spawn("npm", ["run", "dev", "--", "--host", "0.0.0.0", "--cors"], {
    cwd: "frontend",
    detached: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  server.stdout?.resume();
  server.stderr?.resume();
}
try {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: viewport[0], height: viewport[1] } });
    if (ready) {
      const deadline = Date.now() + timeout;
      let lastError;
      while (Date.now() < deadline) {
        const remaining = deadline - Date.now();
        try {
          await page.goto(ready, { waitUntil: "domcontentloaded", timeout: Math.min(2000, remaining) });
          lastError = undefined;
          break;
        } catch (error) {
          lastError = error;
          await new Promise((resolve) => setTimeout(resolve, Math.min(readyRetryDelay, Math.max(0, deadline - Date.now()))));
        }
      }
      if (lastError) {
        throw new Error(`render server did not become ready at ${ready} within ${timeout / 1000}s`, { cause: lastError });
      }
    }
    for (const [index, scene] of scenes.entries()) {
      // Scenes are hash routes in the SPA. Resolve them against the contract URL
      // without discarding its interpolated engine/token query parameters.
      const targetURL = new URL(url);
      if (scene.startsWith("#")) targetURL.hash = scene;
      else targetURL.pathname = scene;
      if (token && !targetURL.searchParams.has("token")) {
        const engine = targetURL.searchParams.get("engine");
        if (engine) targetURL.searchParams.set("engine", engine);
        targetURL.searchParams.set("token", token);
      }
      const target = targetURL.toString();
      try {
        await page.goto(target, { waitUntil: "networkidle", timeout });
      } catch (error) {
        console.warn(`Skipping scene ${scene}: ${error.message}`);
        continue;
      }
      await page.screenshot({ path: `${output}/scene-${String(index + 1).padStart(2, "0")}.png`, fullPage: true });
    }
  } finally {
    await browser.close();
  }
} finally {
  if (server?.pid) {
    // kill() only reaches npm; the negative pid kills the entire detached
    // process group, including the Vite descendants holding our pipes.
    try {
      process.kill(-server.pid, "SIGKILL");
    } catch (error) {
      if (error.code !== "ESRCH") throw error;
    }
  }
}
