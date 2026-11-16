import { test, expect, type Page } from '@playwright/test';
import { appReady, settle } from './ready';
import { installScaleFixture, layoutProblems, loaded, toggleSidebar } from './layoutGate';

// Browser acceptance for the Metrics page (#493 S4). The fixture replaces the demo's own run
// records (#493 S5), so every test installs the scale fixture and picks its state through
// `sessionStorage['fixture.stats']` before the first load: `off` serves no repository,
// `restricted` a narrow signed-in view and unset a recorded window. Playwright shards by
// test, so each test sets its own state and depends on no other.

const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

async function withStats(page: Page, mode: string) {
  await page.addInitScript(installScaleFixture);
  await page.addInitScript((m) => sessionStorage.setItem('fixture.stats', m), mode);
}

async function openMetrics(page: Page, mode = '', hash = '#/metrics') {
  await withStats(page, mode);
  await appReady(page, `/${hash}`);
  if (mode !== 'off') await expect(page.getByTestId('metrics-content')).toBeVisible({ timeout: 30_000 });
}

/** What the fixture answers for /api/stats, read through the same serve the page uses. */
const answer = (page: Page, query = '') => page.evaluate(async (q) => {
  const w = window as unknown as { __pactoServe: (method: string, path: string, body: string | null) => Promise<{ body: string }> };
  return JSON.parse((await w.__pactoServe('GET', `/api/stats${q}`, null)).body) as { environments: { environment: string }[] };
}, query);

test('the sidebar opens Metrics, folded and unfolded', async ({ page }) => {
  await withStats(page, '');
  await appReady(page, '/#/fleet');
  const link = page.locator('[data-testid="sidebar"]').getByRole('link', { name: 'Metrics' });
  await link.click();
  await expect(page).toHaveURL(/#\/metrics$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Metrics' })).toBeVisible();
  await page.goto('/#/fleet');
  await toggleSidebar(page);
  await link.click();
  await expect(page).toHaveURL(/#\/metrics$/);
  await expect(page.getByTestId('metrics-content')).toBeVisible({ timeout: 30_000 });
});

test('the palette opens Metrics', async ({ page }) => {
  await withStats(page, '');
  await appReady(page, '/#/fleet');
  await page.keyboard.press('ControlOrMeta+k');
  await expect(page.locator('.cp-panel')).toBeVisible();
  await page.locator('.cp-panel input').fill('metrics');
  await expect(page.locator('.cp-item.selected .cp-item-label')).toHaveText('Metrics');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/metrics$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Metrics' })).toBeVisible();
});

test('with no stats repository the page explains recording and shows no number', async ({ page }) => {
  await openMetrics(page, 'off');
  const off = page.getByTestId('metrics-off');
  await expect(off.getByRole('heading', { name: 'Metrics are off' })).toBeVisible();
  await expect(page.getByTestId('metrics-content')).toHaveCount(0);
  await expect(page.getByTestId('metrics-tiles')).toHaveCount(0);
  // The explainer's copy lines name a repository, never a count.
  const prose = await off.locator('h2, p').allInnerTexts();
  expect(prose.join(' ')).not.toMatch(/\d/);
});

test('every chart says how sure it is', async ({ page }) => {
  await openMetrics(page);
  const charts = page.locator('[data-testid="metrics-trend"], [data-testid="metrics-coverage-trend"]');
  await expect(charts.first()).toBeVisible();
  const n = await charts.count();
  expect(n).toBeGreaterThan(1);
  for (let i = 0; i < n; i++) {
    const strip = charts.nth(i).getByTestId('stats-strip');
    await expect(strip).toContainText('unsigned statistics');
    await expect(strip).toContainText(/as of /);
    await expect(strip.locator('.badge')).toHaveText(/^(Partial|Complete|No records)$/);
    await expect(strip.locator('.badge')).toHaveText('Partial'); // the fixture skipped 3 records
  }
});

test('never draws zero for unknown', async ({ page }) => {
  await openMetrics(page);
  // The fixture's next-to-newest bucket holds no record: every trend says so, hatched.
  const trends = page.getByTestId('metrics-trend');
  const n = await trends.count();
  expect(n).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) {
    const rows = trends.nth(i).locator('.hb-row');
    const row = rows.nth((await rows.count()) - 2);
    await expect(row.locator('.hb-value')).toHaveText('no records');
    await expect(row.locator('.hb-value')).toHaveClass(/hb-unknown/);
    await expect(row.locator('.hb-fill')).toHaveClass(/hatch/);
  }
  // A rate with nothing to take it over is unknown, never 0%.
  const rolledBack = page.getByTestId('metrics-env-row').nth(1).locator('td[data-label="Rolled back"]');
  await expect(rolledBack).toHaveText('unknown');
  await expect(rolledBack.locator('.hatch')).toHaveCount(1);
});

test('environments follow the Stage order, not their names', async ({ page }) => {
  await openMetrics(page);
  const section = page.getByTestId('metrics-environments');
  await expect(section.getByRole('heading', { name: 'Environments, in the order their Stages promote' })).toBeVisible();
  const want = (await answer(page)).environments.map((e) => e.environment);
  const shown = await section.locator('[data-testid="metrics-env-row"] th a').evaluateAll((as) => as.map((a) => a.getAttribute('title')));
  expect(shown).toEqual(want);
  expect([...want].sort((a, b) => a.localeCompare(b))).not.toEqual(want);
});

test('the window picker changes the URL and the bucket count', async ({ page }) => {
  await openMetrics(page);
  const bars = page.getByTestId('metrics-trend').first().locator('.hb-row');
  await expect(bars).toHaveCount(5); // 30 days: four weeks and a short 2-day bucket
  await page.getByLabel('Window').selectOption('7d');
  await expect(page).toHaveURL(/#\/metrics\?(.*&)?window=7d/);
  await expect(bars).toHaveCount(7);
  await page.getByLabel('Window').selectOption('90d');
  await expect(page).toHaveURL(/window=90d/);
  await expect(bars).toHaveCount(13);
});

test('a blocked gate opens its records, each with its change analysis', async ({ page }) => {
  await openMetrics(page);
  const tile = page.getByTestId('metrics-tiles').locator('.mx-tile').filter({ hasText: 'Gates blocked' }).getByRole('link');
  const lists = Number(await tile.getAttribute('data-lists'));
  await tile.click();
  await expect(page).toHaveURL(/metric=blocked/);
  const panel = page.getByTestId('metrics-records');
  await expect(panel.getByTestId('metrics-record')).toHaveCount(Math.min(25, lists));
  const changes = panel.getByRole('link', { name: 'Change analysis' });
  await expect(changes).toHaveCount(2);
  const href = (await changes.first().getAttribute('href')) ?? '';
  const q = new URLSearchParams(href.split('?')[1]);
  expect(q.get('old')).toMatch(/^sha256:/);
  expect(q.get('new')).toMatch(/^sha256:/);
  await changes.first().click();
  await expect(page).toHaveURL((u) => u.hash.startsWith('#/fleet/changes/payments-service?')
    && new URLSearchParams(u.hash.split('?')[1]).get('old') === q.get('old')
    && new URLSearchParams(u.hash.split('?')[1]).get('new') === q.get('new'));
  await expect(page.getByRole('heading', { level: 1, name: /Change analysis/ })).toBeVisible();
});

test('a failed promotion count opens exactly its records, each linked to its Stage history', async ({ page }) => {
  await openMetrics(page);
  const cell = page.getByTestId('metrics-env-row').first().locator('td[data-label="Failed"] a');
  await expect(cell).toHaveAttribute('data-lists', '2');
  await cell.click();
  const rows = page.getByTestId('metrics-records').getByTestId('metrics-record');
  await expect(rows).toHaveCount(2);
  const stage = rows.first().locator('td[data-label="Stage"] a').first();
  const href = (await stage.getAttribute('href')) ?? '';
  const name = new URLSearchParams(href.split('?')[1]).get('stage');
  expect(name).toBeTruthy();
  await stage.click();
  await expect(page).toHaveURL((u) => u.hash.startsWith('#/delivery/history?') && new URLSearchParams(u.hash.split('?')[1]).get('stage') === name);
  await expect(page.getByRole('heading', { level: 1 })).toContainText(`History of `);
});

test('a service link lands on a service page that renders', async ({ page }) => {
  await openMetrics(page);
  const link = page.getByTestId('metrics-service-row').first().locator('th a');
  await expect(link).toHaveText('payments-service');
  await link.click();
  await expect(page).toHaveURL(/#\/fleet\/services\/payments-service$/);
  await expect(page.getByRole('heading', { level: 1, name: 'payments-service' })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(/not found|no service named/i)).toHaveCount(0);
});

test('a restricted reader is told records are left out and sees no coverage history', async ({ page }) => {
  await openMetrics(page, 'restricted');
  await expect(page.getByTestId('metrics-hidden')).toHaveText('Some run records name services outside your view and are left out of every number.');
  await expect(page.getByTestId('metrics-coverage-trend')).toHaveCount(0);
  await expect(page.getByTestId('metrics-coverage-restricted')).toBeVisible();
  await expect(page.getByTestId('metrics-coverage-now')).toBeVisible();
});

test('Metrics holds its layout in a wide font', async ({ page }) => {
  await openMetrics(page);
  await page.addStyleTag({ content: WIDE_FONT });
  await loaded(page);
  await settle(page);
  expect(await layoutProblems(page)).toEqual([]);
});
