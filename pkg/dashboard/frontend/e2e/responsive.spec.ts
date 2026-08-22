import { test, expect, type Page } from '@playwright/test';

// Responsive acceptance: at narrow widths no product route requires
// BODY-level horizontal scrolling (internal scroller regions may scroll; the page body
// must not). Runs against the built WASM demo in the desktop project with an explicit
// narrow viewport.
const WIDTHS = [320, 375];

// Product routes reachable by hash. Target detail shares the FleetEntityView layout with
// service detail (the same component), so service detail exercises the entity-page layout.
const ROUTES: Array<{ hash: string; ready: (p: Page) => Promise<unknown> }> = [
  { hash: '#/fleet', ready: (p) => expect(p.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/services', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/attention', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/owners', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Owners' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/sources', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Data sources' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/changes/payments-service', ready: (p) => expect(p.getByRole('heading', { level: 1, name: 'Change analysis' })).toBeVisible({ timeout: 20_000 }) },
  { hash: '#/fleet/services/payments-service', ready: (p) => expect(p.locator('.ev-body')).toBeVisible({ timeout: 20_000 }) },
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

    // ── Deeper INTERACTIVE states: the states most likely to
    // overflow are not the resting routes but expanded filters, open drawers and populated
    // results. Body-level horizontal overflow must never appear in any of them (internal
    // scrollers are allowed). ──

    test('focused graph: legend, semantic navigator, node drawer and edge drawer stay within the body', async ({ page }) => {
      await page.goto('/#/fleet/graph/service/payments-service');
      await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
      await expect(page.getByTestId('graph-legend')).toBeVisible();
      expect(await bodyHasNoHorizontalOverflow(page), 'focused graph + legend').toBe(true);
      // Expand the semantic Relationships navigator (long endpoint labels are a wrap risk).
      await page.getByTestId('graph-textalt').locator('summary').click();
      await expect(page.getByTestId('graph-node-item').first()).toBeVisible();
      expect(await bodyHasNoHorizontalOverflow(page), 'graph navigator expanded').toBe(true);
      // Node drawer.
      await page.getByTestId('graph-node-item').first().click();
      await expect(page.getByTestId('graph-drawer')).toBeVisible();
      expect(await bodyHasNoHorizontalOverflow(page), 'graph node drawer').toBe(true);
      // Edge drawer (a long "from rel to" row is a wrap risk).
      await page.getByTestId('graph-edge').first().click();
      await expect(page.getByTestId('graph-drawer')).toBeVisible();
      expect(await bodyHasNoHorizontalOverflow(page), 'graph edge drawer').toBe(true);
    });

    test('attention advanced filters expanded stay within the body', async ({ page }) => {
      await page.goto('/#/fleet/attention');
      await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
      await page.locator('.av-advanced summary').click();
      await expect(page.locator('.av-advanced[open]')).toBeVisible();
      expect(await bodyHasNoHorizontalOverflow(page), 'attention advanced filters').toBe(true);
    });

    test('the mobile navigation menu opens within the body', async ({ page }) => {
      await page.goto('/#/fleet');
      await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
      const hamburger = page.getByRole('button', { name: 'Menu' });
      if (await hamburger.isVisible()) {
        await hamburger.click();
        expect(await bodyHasNoHorizontalOverflow(page), 'mobile nav open').toBe(true);
      }
    });

    test('Change analysis with populated selectors and results stays within the body', async ({ page }) => {
      await page.goto('/#/fleet/changes/payments-service');
      await expect(page.getByRole('heading', { level: 1, name: 'Change analysis' })).toBeVisible({ timeout: 20_000 });
      await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
      await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.getByText('breaking', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
      expect(await bodyHasNoHorizontalOverflow(page), 'change analysis populated results (long paths)').toBe(true);
    });
  });
}
