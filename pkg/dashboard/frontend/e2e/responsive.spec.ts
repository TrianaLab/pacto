import { test, expect, type Page } from '@playwright/test';

// Responsive acceptance (requirement 8.11): at narrow widths no product route requires
// BODY-level horizontal scrolling (internal scroller regions may scroll; the page body
// must not). Runs against the built WASM demo in the desktop project with an explicit
// narrow viewport.
const WIDTHS = [320, 375];

// Product routes reachable by hash. Target detail shares the FleetEntityView layout with
// service detail (the same component), so service detail exercises the entity-page layout.
const ROUTES: Array<{ hash: string; ready: (p: Page) => Promise<unknown> }> = [
  { hash: '#/fleet', ready: (p) => expect(p.getByText('Needs attention')).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/services', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/attention', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/owners', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Owners' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/sources', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Sources' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/impact/payments-service', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Impact' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/services/payments-service', ready: (p) => expect(p.locator('.ev-head')).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/graph', ready: (p) => expect(p.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/graph/service/payments-service', ready: (p) => expect(p.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 }) },
];

async function bodyHasNoHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1);
}

for (const w of WIDTHS) {
  test.describe(`responsive @${w}px`, () => {
    test.use({ viewport: { width: w, height: 800 } });
    for (const r of ROUTES) {
      test(`no body-level horizontal scroll on ${r.hash}`, async ({ page }) => {
        await page.goto(`/${r.hash}`);
        await r.ready(page);
        expect(await bodyHasNoHorizontalOverflow(page), `horizontal overflow at ${w}px on ${r.hash}`).toBe(true);
      });
    }

    test('the graph toolbar fits/wraps and its controls remain reachable', async ({ page }) => {
      await page.goto('/#/fleet/graph/service/payments-service');
      await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
      await expect(page.getByTestId('view-observed')).toBeVisible();
      await expect(page.getByTestId('dir-both')).toBeVisible();
      expect(await bodyHasNoHorizontalOverflow(page)).toBe(true);
    });
  });
}
