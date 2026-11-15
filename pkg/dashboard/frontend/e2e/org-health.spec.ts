import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';

// Organizational health against the built WASM demo (#460): every count on the overview
// opens a list of exactly what it counts. The demo holds one target whose health check
// is failing (gift-cards in prod), so Failing and Unknown are both non-zero. Every
// expected number is read from the engine or the page, never a literal of the fixture.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const api = (page: Page, path: string): Promise<any> => page.evaluate(async (p) => (await fetch(p)).json(), path);
const num = (s: string | null) => Number((s ?? '').replace(/[^\d]/g, ''));

// The total of the list the page landed on, from the `data-view-total` its pager carries.
const listTotal = (page: Page) => page.evaluate(() => {
  const v = document.querySelector('[data-view-total]')?.getAttribute('data-view-total');
  return v == null ? null : Number(v);
});

async function overview(page: Page) {
  await appReady(page, '/#/fleet');
  await page.addStyleTag({ content: WIDE_FONT });
  await expect(page.locator('#sec-health')).toBeVisible({ timeout: T });
}

const legend = (page: Page, band: string, label: string) =>
  page.locator(`${band} .dist-item`).filter({ has: page.locator('.dist-label', { hasText: new RegExp(`^${label}$`) }) });

test.describe('WASM demo — organizational health', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('Failing opens the health attention list of the same size, and its target shows HEALTH_FAILING', async ({ page }) => {
    await overview(page);
    const failing = legend(page, '#sec-health', 'Failing');
    const n = num(await failing.locator('.dist-value').textContent());
    expect(n).toBeGreaterThan(0);
    // Failing and Unknown are two buckets, never one word for both.
    expect(num(await legend(page, '#sec-health', 'Unknown').locator('.dist-value').textContent())).toBeGreaterThan(0);
    // The linked legend item sits on the same line as the plain ones beside it.
    const middle = async (label: string) => {
      const b = await legend(page, '#sec-health', label).locator('.dist-label').boundingBox();
      return b ? b.y + b.height / 2 : NaN;
    };
    expect(Math.abs((await middle('Failing')) - (await middle('Unknown')))).toBeLessThanOrEqual(1);
    await failing.locator('a').click();
    await expect(page).toHaveURL(/#\/fleet\/attention\?category=health$/);
    await expect.poll(() => listTotal(page), { timeout: T }).toBe(n);
    const item = page.getByTestId('attention-item').first();
    await expect(item).toContainText('HEALTH_FAILING');
    await item.locator('a.entity-link').click();
    await expect(page).toHaveURL(/#\/fleet\/targets\//);
    await expect(page.locator('.finding .f-code').filter({ hasText: 'HEALTH_FAILING' })).toBeVisible({ timeout: T });
  });

  test('the No summary row opens the services with that gap, the same total', async ({ page }) => {
    await overview(page);
    const row = page.getByTestId('knowledge-gaps').locator('a').filter({ hasText: 'No summary' });
    const n = num(await row.locator('.ov-gap-count').textContent());
    expect(n).toBeGreaterThan(0);
    await row.click();
    await expect(page).toHaveURL(/#\/fleet\/services\?gap=no-summary$/);
    await expect.poll(() => listTotal(page), { timeout: T }).toBe(n);
  });

  test('the lead tile counts the distinct services of its list and its items, and the badge only the actionable ones', async ({ page }) => {
    await overview(page);
    const items = [];
    for (let off: number | undefined = 0; off != null;) {
      const p = await api(page, `/api/fleet/attention?limit=200&offset=${off}`);
      items.push(...(p.items ?? []));
      off = p.nextOffset;
    }
    const services = new Set(items.map((i: { service?: string }) => i.service).filter(Boolean)).size;
    const tile = page.locator('.tile-lead');
    await expect(tile.locator('.tile-count')).toHaveText(String(services));
    await expect(tile.locator('.tile-sub')).toHaveText(`${items.length.toLocaleString('en-US')} attention items`);
    // The sidebar badge counts what is actionable (Decision 15): fewer than every item.
    const ov = await api(page, '/api/fleet/overview');
    const badge = num(await page.getByTestId('nav-attention-count').first().textContent());
    expect(badge).toBe(ov.attention.actionable);
    expect(badge).toBeLessThan(items.length);
    await tile.click();
    await expect(page).toHaveURL(/#\/fleet\/attention$/);
    await expect.poll(() => listTotal(page), { timeout: T }).toBe(items.length);
  });

  test('Incomplete sources opens the incomplete sources, the same count', async ({ page }) => {
    await overview(page);
    const chip = page.locator('.op-summary a[href*="sourceHealth=incomplete"]');
    const n = num(await chip.locator('.op-sec-count, .tile-count').textContent());
    expect(n).toBeGreaterThan(0);
    await chip.click();
    await expect(page).toHaveURL(/#\/fleet\/sources\?sourceHealth=incomplete$/);
    await expect.poll(() => listTotal(page), { timeout: T }).toBe(n);
  });

  test('Owner unknown opens the services whose owner Pacto cannot read, when there are any', async ({ page }) => {
    await overview(page);
    const ov = await api(page, '/api/fleet/overview');
    const unknown = legend(page, '#sec-org', 'Owner unknown');
    if (!ov.summary.ownership.unknown) {
      await expect(unknown).toHaveCount(0);
      return;
    }
    const n = num(await unknown.locator('.dist-value').textContent());
    expect(n).toBe(ov.summary.ownership.unknown);
    await unknown.locator('a').click();
    await expect(page).toHaveURL(/ownership=unknown/);
    await expect.poll(() => listTotal(page), { timeout: T }).toBe(n);
  });

  test('Compliant, evidence stale opens the stale list narrowed to compliant targets, the same total', async ({ page }) => {
    await overview(page);
    const seg = legend(page, '#sec-posture', 'Compliant, evidence stale');
    const n = num(await seg.locator('.dist-value').textContent());
    expect(n).toBeGreaterThan(0);
    await seg.locator('a').click();
    await expect(page).toHaveURL(/#\/fleet\/attention\?category=stale&status=Compliant$/);
    await expect.poll(() => listTotal(page), { timeout: T }).toBe(n);
  });

  test('Domains ranked by health put a domain with a problem first', async ({ page }) => {
    await appReady(page, '/#/fleet/domains?sort=health');
    await page.addStyleTag({ content: WIDE_FONT });
    const first = page.getByTestId('domain-list').locator('tbody tr').first();
    await expect(first).toBeVisible({ timeout: T });
    const problems = await first.evaluate((tr) => ['Non-compliant', 'Unknown', 'Failing', 'Stale']
      .map((l) => Number((tr.querySelector(`td[data-label="${l}"]`)?.textContent ?? '').replace(/[^\d]/g, '')))
      .reduce((a, b) => a + b, 0));
    expect(problems).toBeGreaterThan(0);
  });

  test('the overview health and knowledge bands are axe clean in Verdana', async ({ page }) => {
    await overview(page);
    await page.locator('#sec-knowledge').scrollIntoViewIfNeeded();
    await settle(page);
    const r = await new AxeBuilder({ page }).include('#sec-health').include('#sec-knowledge').include('#sec-org')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
    expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1)).toBe(true);
  });
});

// The overview Domains table must show as a table, not stacked cards, inside the capped
// content column on every desktop width, in the default font and in Verdana.
test.describe('WASM demo — overview domains table form', () => {
  for (const width of [1440, 1920, 3440]) {
    test(`is a table at ${width}px wide`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await overview(page);
      const table = page.getByTestId('overview-by-domain');
      await table.scrollIntoViewIfNeeded();
      await expect.poll(() => table.evaluate((t) => getComputedStyle(t).display), { timeout: T }).toBe('table');
      await expect(table.locator('thead th[scope="colgroup"]')).toBeVisible();
    });
  }
});
