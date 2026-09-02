import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

const baseURL = (process.env.REPOARK_E2E_URL || 'http://127.0.0.1:19787').replace(/\/$/, '');
const artifactsDir = process.env.REPOARK_E2E_ARTIFACTS || 'artifacts/web-e2e';
await mkdir(artifactsDir, { recursive: true });

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const browserErrors = [];

page.on('pageerror', error => browserErrors.push(`pageerror: ${error.message}`));
page.on('console', message => {
  if (message.type() === 'error') browserErrors.push(`console: ${message.text()}`);
});

try {
  const sseResponsePromise = page.waitForResponse(
    response => response.url() === `${baseURL}/api/v1/console/events`,
    { timeout: 15_000 },
  );
  const homeResponse = await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  assert(homeResponse, 'dashboard did not return a response');
  assert.equal(homeResponse.status(), 200, 'dashboard should return HTTP 200');
  assert.equal(await page.title(), 'RepoArk Console');

  const homeHeaders = await homeResponse.allHeaders();
  assert.equal(homeHeaders['x-frame-options'], 'DENY');
  assert.equal(homeHeaders['x-content-type-options'], 'nosniff');
  assert.match(homeHeaders['content-security-policy'] || '', /frame-ancestors 'none'/);

  await page.locator('#session').filter({ hasText: 'Local console' }).waitFor({ timeout: 10_000 });
  await page.locator('#actions .action').first().waitFor({ timeout: 10_000 });
  const actionCount = await page.locator('#actions .action').count();
  assert(actionCount >= 2, `expected operation cards, got ${actionCount}`);

  const sseResponse = await sseResponsePromise;
  assert.equal(sseResponse.status(), 200, 'SSE endpoint should return HTTP 200');
  const sseHeaders = await sseResponse.allHeaders();
  assert.match(sseHeaders['content-type'] || '', /^text\/event-stream/);
  assert.equal(sseHeaders['x-accel-buffering'], 'no');

  const sessionResponse = await fetch(`${baseURL}/api/v1/console/session`);
  assert.equal(sessionResponse.status, 200);
  const session = await sessionResponse.json();
  assert.equal(session.authenticated, true);
  assert.equal(session.local, true);
  assert.equal(session.role, 'local');

  const postRoot = await fetch(`${baseURL}/`, { method: 'POST' });
  assert.equal(postRoot.status, 405, 'POST / must be rejected');
  assert.match(postRoot.headers.get('allow') || '', /GET/);

  const missing = await fetch(`${baseURL}/not-found`);
  assert.equal(missing.status, 404, 'unknown paths must not be swallowed by the dashboard route');

  await page.waitForTimeout(3500);
  const jobPollCount = await page.evaluate(() => performance.getEntriesByType('resource')
    .filter(entry => new URL(entry.name).pathname === '/api/v1/console/job').length);
  assert(jobPollCount <= 1, `SSE connected but job polling repeated ${jobPollCount} times`);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('#actions .action').first().waitFor({ timeout: 10_000 });
  const horizontallyOverflowing = await page.evaluate(() =>
    document.documentElement.scrollWidth > window.innerWidth + 1,
  );
  assert.equal(horizontallyOverflowing, false, 'mobile viewport has horizontal page overflow');
  assert(await page.locator('#jobTitle').isVisible(), 'activity panel should remain visible on mobile');

  assert.deepEqual(browserErrors, [], `browser emitted errors:\n${browserErrors.join('\n')}`);
  console.log(`RepoArk web E2E passed: actions=${actionCount}, repeatedJobPolls=${jobPollCount}`);
} catch (error) {
  await page.screenshot({ path: `${artifactsDir}/failure.png`, fullPage: true }).catch(() => {});
  throw error;
} finally {
  await browser.close();
}
