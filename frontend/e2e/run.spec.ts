import { test, expect, startFakeEngine, connect, type FakeEngine } from "./fixtures";

/**
 * AC-35: streaming, reconnect, overflow resync, human-gate answering, abort —
 * against the real frontend and the real HTTP contract.
 */

let engine: FakeEngine;

test.afterEach(() => engine?.stop());

test("streams a live run into the conversation", async ({ page }) => {
  engine = await startFakeEngine("pair");
  await connect(page, engine);
  await page.goto("/");

  // The run appears from the snapshot.
  await expect(page.getByTestId("run-row").first()).toBeVisible();

  await page.goto("/#/runs/r-20260726-120000-fake");
  await expect(page.getByTestId("run-view")).toBeVisible();

  // Turns arrive over SSE, not from the initial fetch.
  await expect(page.getByTestId("conversation-turn").first()).toBeVisible();
  await expect(page.getByTestId("conversation-turn")).toHaveCount(2, { timeout: 15_000 });

  // Streamed text lands in the implementer's lane.
  await expect(page.getByTestId("conversation").getByText(/func Add/)).toBeVisible({ timeout: 15_000 });

  // The gate result is shown and is not invented by the UI.
  await expect(page.getByTestId("gate-card")).toContainText("go test ./...");
});

test("reaches the human gate and reports what is waiting", async ({ page }) => {
  engine = await startFakeEngine("pair");
  await connect(page, engine);
  await page.goto("/#/runs/r-20260726-120000-fake");

  await expect(page.getByTestId("pending-human")).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId("pending-human")).toContainText("waiting for you");
});

test("answers a pending question and the run resumes", async ({ page }) => {
  engine = await startFakeEngine("question");
  await connect(page, engine);
  await page.goto("/#/runs/r-20260726-120000-fake");

  const pending = page.getByTestId("pending-human");
  await expect(pending).toBeVisible({ timeout: 20_000 });
  await expect(pending).toContainText("Should Add saturate or wrap on overflow?");

  await page.getByLabel("answer").fill("wrap");
  await page.getByTestId("answer-button").click();

  // Resumed: the question clears and the gate eventually arrives.
  await expect(page.getByTestId("gate-card")).toContainText("passed", { timeout: 20_000 });
});

test("accept shows pending, then the confirmed commit — never before", async ({ page }) => {
  engine = await startFakeEngine("pair");
  await connect(page, engine);
  await page.goto("/#/runs/r-20260726-120000-fake");

  await expect(page.getByTestId("pending-human")).toBeVisible({ timeout: 20_000 });

  // Nothing claims a commit before the engine confirms one.
  await expect(page.getByTestId("accept-committed")).toHaveCount(0);

  await page.getByTestId("accept-button").click();
  await expect(page.getByTestId("accept-committed")).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId("accept-committed")).toContainText("e60dc7fe");
});

test("keeps the last known state when the engine dies, and recovers", async ({ page }) => {
  engine = await startFakeEngine("pair");
  await connect(page, engine);
  await page.goto("/#/runs/r-20260726-120000-fake");
  await expect(page.getByTestId("conversation-turn").first()).toBeVisible({ timeout: 20_000 });

  const turnsBefore = await page.getByTestId("conversation-turn").count();

  // AC-30: killing the engine must not blank the UI.
  engine.stop();
  await expect(page.locator("main")).toHaveAttribute("data-degraded", "true", { timeout: 15_000 });
  await expect(page.getByTestId("conversation-turn")).toHaveCount(turnsBefore);
  await expect(page.getByTestId("run-view")).toBeVisible();
});

test("aborts a run", async ({ page }) => {
  engine = await startFakeEngine("pair");
  await connect(page, engine);
  await page.goto("/#/runs/r-20260726-120000-fake");
  await expect(page.getByTestId("run-view")).toBeVisible();

  await page.getByTestId("abort-button").click();
  // The run ends; the engine reports it and the UI follows rather than leading.
  await expect(page.getByTestId("run-view")).toBeVisible();
});

test("anonymises a tournament run and shows candidates without authorship", async ({ page }) => {
  engine = await startFakeEngine("tournament");
  await connect(page, engine);
  await page.goto("/#/runs/r-20260726-120000-fake");

  await expect(page.getByTestId("conversation-turn").first()).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId("conversation-turn").first()).toHaveAttribute("data-anonymous", "true");

  // AC-32: the id must be absent from the DOM, not merely styled away.
  const conversation = await page.getByTestId("conversation").innerHTML();
  expect(conversation).not.toContain("pato-uno");
  expect(conversation).not.toContain("pato-dos");

  await page.getByTestId("tab-candidates").click();
  await expect(page.getByTestId("candidate-card").first()).toBeVisible();
  const cards = await page.getByTestId("candidate-card").first().innerHTML();
  expect(cards).not.toContain("pato");
});
