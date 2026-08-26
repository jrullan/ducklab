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
await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true });
let server;
if (process.env.DUCKLAB_RENDER_START !== "false") {
  server = spawn("npm", ["run", "dev", "--", "--host", "0.0.0.0", "--cors"], { cwd: "frontend", stdio: "inherit" });
}
try {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: viewport[0], height: viewport[1] } });
  if (ready) await page.goto(ready, { waitUntil: "networkidle", timeout });
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
    await page.goto(target, { waitUntil: "networkidle", timeout });
    await page.screenshot({ path: `${output}/scene-${String(index + 1).padStart(2, "0")}.png`, fullPage: true });
  }
  await browser.close();
} finally {
  server?.kill();
}
