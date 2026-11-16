import { test, expect, type Locator, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { layoutProblems, loaded } from './layoutGate';

// The browser demo's own Metrics page (#493 S5): about 90 days of run records generated
// from the payments story (examples/story/runrecords.go) and served by the engine, with
// no fixture. Each test opens its own page, so the file passes alone in any shard.

const T = 30_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

async function openMetrics(page: Page, hash = '#/metrics') {
  await appReady(page, `/${hash}`);
  await expect(page.getByTestId('metrics-content')).toBeVisible({ timeout: T });
}

test('the demo records say what they are and when they were read', async ({ page }) => {
  await openMetrics(page);
  const strip = page.getByTestId('metrics-content').getByTestId('stats-strip').first();
  await expect(strip).toContainText('unsigned statistics');
  await expect(strip).toContainText(/as of /);
  const caught = page.getByTestId('metrics-tiles').locator('.mx-tile').first();
  await expect(caught).toContainText('Breaking changes caught');
  await expect(caught.locator('a')).toHaveText('2');
});

test('environments follow each Stage path: test before the prod-eu and prod-us fork', async ({ page }) => {
  await openMetrics(page);
  const names = await page.locator('[data-testid="metrics-env-row"] th a').evaluateAll((as) => as.map((a) => a.getAttribute('title')));
  for (const env of ['test', 'prod-eu', 'prod-us']) expect(names).toContain(env);
  expect(names.indexOf('test')).toBeLessThan(names.indexOf('prod-eu'));
  expect(names.indexOf('test')).toBeLessThan(names.indexOf('prod-us'));
});

test('unknowns stay unknown: an unjoined lead time is named and an empty week is hatched, never 0', async ({ page }) => {
  await openMetrics(page);
  await expect(page.getByText(/promotions? (has|have) no publish record with their digest, so their lead time is unknown/)).toBeVisible();
  await expect(page.getByTestId('metrics-tiles').locator('.mx-tile').filter({ hasText: 'Lead time p50', has: page.locator('.mx-tile-sub', { hasText: /^test$/ }) })).toContainText('with no publish record');
  const unknown = page.getByTestId('metrics-trend').first().locator('.hb-row').filter({ has: page.locator('.hb-value.hb-unknown') });
  await expect(unknown.first()).toBeVisible();
  await expect(unknown.first().locator('.hb-value')).not.toHaveText('0');
  await expect(unknown.first().locator('.hb-fill')).toHaveClass(/hatch/);
});

test('history older than the story names revisions as text; a story revision opens its page', async ({ page }) => {
  await openMetrics(page, '#/metrics?window=90d&metric=breaking');
  const rows = page.getByTestId('metrics-records').getByTestId('metrics-record');
  await expect(rows.first()).toBeVisible({ timeout: T });
  const revs = (row: Locator) => row.locator('td[data-label="Change"] .mx-revs');
  // A revision no fleet holds (the seeded history) is plain text, never a dead link.
  const history = rows.filter({ hasNot: page.locator('td[data-label="Change"] .mx-revs a') });
  await expect(history.first()).toBeVisible();
  await expect(revs(history.first()).locator('.cell-clip')).toHaveCount(2);
  // The story's own revisions are the ones the demo fleet keys, so they link.
  const link = revs(rows.first()).locator('a').first();
  const digest = (await link.getAttribute('title')) ?? '';
  expect(digest).toMatch(/^sha256:/);
  await link.click();
  await expect(page).toHaveURL(/#\/fleet\/revisions\//);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: T });
  await expect(page.getByText(/not found|couldn.t show/i)).toHaveCount(0);
});

test('the populated page holds its layout in a wide font and on a phone', async ({ page }) => {
  await openMetrics(page);
  await page.addStyleTag({ content: WIDE_FONT });
  await loaded(page);
  await settle(page);
  expect(await layoutProblems(page)).toEqual([]);
  await page.setViewportSize({ width: 390, height: 844 });
  await settle(page);
  expect(await layoutProblems(page)).toEqual([]);
});
