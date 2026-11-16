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

type Ratio = { count: number; of: number };
type DemoStats = {
  totals: { breakingCaught: { distinct: number }; gates: { clear: number; blocked: number; unverified: number } };
  environments: ({ environment: string } & Record<string, Ratio | string>)[];
  leadTimeGroups: { key: string; leadTime: { unknown?: boolean; joined: number } }[];
  buckets: { metrics?: { environments: Record<string, Ratio | string>[] } }[];
};

/** The demo engine's own answer for the page's window, read through the page's serve. */
const demoStats = (page: Page): Promise<DemoStats> => page.evaluate(async () => {
  const w = window as unknown as { __pactoServe: (method: string, path: string, body: string | null) => Promise<{ body: string }> };
  return JSON.parse((await w.__pactoServe('GET', '/api/stats', null)).body);
});
const fmt = (n: number) => n.toLocaleString('en-US');

test('the demo records say what they are and when they were read', async ({ page }) => {
  await openMetrics(page);
  const strip = page.getByTestId('metrics-content').getByTestId('stats-strip').first();
  await expect(strip).toContainText('unsigned statistics');
  await expect(strip).toContainText(/as of /);
  const caught = page.getByTestId('metrics-tiles').locator('.mx-tile').first();
  await expect(caught).toContainText('Breaking changes caught');
  const st = await demoStats(page);
  await expect(caught.locator('a')).toHaveText(fmt(st.totals.breakingCaught.distinct));
});

test('the gates tile and its key read the demo\'s own verdicts, an unverified one among them', async ({ page }) => {
  await openMetrics(page);
  const { gates } = (await demoStats(page)).totals;
  expect(gates.unverified).toBeGreaterThan(0);
  const tile = page.getByTestId('metrics-tiles').locator('.mx-tile').filter({ hasText: 'Release gates' });
  await expect(tile.locator('.mx-tile-value a')).toHaveText(fmt(gates.blocked));
  await expect(tile.locator('.mx-tile-value small')).toHaveText(`blocked of ${fmt(gates.clear + gates.blocked + gates.unverified)}`);
  const keys = page.getByTestId('metrics-gates').locator('.mx-key');
  await expect(keys.filter({ hasText: 'Unverified' }).locator('b')).toHaveText(fmt(gates.unverified));
  await expect(keys.filter({ hasText: 'Unverified' }).locator('i.dots')).toHaveCount(1);
});

test('the lead tile reads the joined promotions into the chain end, and a stray rollback never makes a rate', async ({ page }) => {
  await openMetrics(page);
  const st = await demoStats(page);
  const end = st.leadTimeGroups.find((g) => g.key === 'chain-end');
  expect(end && !end.leadTime.unknown && end.leadTime.joined).toBeTruthy();
  const tile = page.getByTestId('metrics-tiles').locator('.mx-tile').nth(2);
  await expect(tile.locator('.mx-tile-label')).toHaveText('Lead time to production');
  await expect(tile.locator('.mx-tile-value')).not.toContainText('unknown');
  // A week holds a rollback with no succeeded promote: its count never enters a rate,
  // so no panel reads over 100%.
  const stray = st.buckets.some((b) => (b.metrics?.environments ?? []).some((e) => { const r = e.rolledBack as Ratio; return r.of === 0 && r.count > 0; }));
  expect(stray).toBe(true);
  const pcts = (await page.getByTestId('metrics-env-panel').locator('.hb-value').allTextContents())
    .flatMap((t) => [...t.matchAll(/\((\d+)%\)/g)].map((m) => Number(m[1])));
  expect(pcts.length).toBeGreaterThan(0);
  for (const p of pcts) expect(p).toBeLessThanOrEqual(100);
});

test('environments follow each Stage path: test before the prod-eu and prod-us fork', async ({ page }) => {
  await openMetrics(page);
  const health = page.getByTestId('metrics-health');
  await health.getByRole('button', { name: 'Show as table' }).click();
  const names = await health.locator('table tbody th').evaluateAll((ths) => ths.map((th) => th.getAttribute('title')));
  for (const env of ['test', 'prod-eu', 'prod-us']) expect(names).toContain(env);
  expect(names.indexOf('test')).toBeLessThan(names.indexOf('prod-eu'));
  expect(names.indexOf('test')).toBeLessThan(names.indexOf('prod-us'));
});

test('unknowns stay unknown: an unjoined lead time is named and an empty week is hatched, never 0', async ({ page }) => {
  await openMetrics(page);
  await expect(page.getByText(/promotions? (has|have) no publish record with their digest, so their lead time is unknown/)).toBeVisible();
  // The weekly charts hatch a week with no records and never draw it as a 0 column.
  const stopped = page.getByTestId('metrics-stopped');
  await expect(stopped.locator('.cc-unknown').first()).toBeVisible();
  await expect(stopped.locator('.cc-plate').first()).toHaveText(/^no (CI )?records$/);
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
