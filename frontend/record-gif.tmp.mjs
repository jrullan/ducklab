// Records the real ducklab UI against the real engine while a run streams.
import { chromium } from '@playwright/test';

const [,, baseUrl, token, runId, outDir, seconds] = process.argv;
const browser = await chromium.launch({
  args: ['--disable-web-security', '--hide-scrollbars'],
});
const ctx = await browser.newContext({
  viewport: { width: 1280, height: 800 },
  recordVideo: { dir: outDir, size: { width: 1280, height: 800 } },
});
await ctx.addInitScript(([b, t]) => {
  window.ducklab = { baseUrl: b, token: t };
}, [baseUrl, token]);
const page = await ctx.newPage();
await page.goto(`http://127.0.0.1:5199/index.html#/runs/${runId}`);
await page.waitForTimeout(Number(seconds) * 1000);
await ctx.close();
await browser.close();
console.log('recorded');
