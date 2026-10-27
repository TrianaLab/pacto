import { test, expect, type Page } from '@playwright/test';

// Responsive acceptance: at narrow widths no product route requires
// BODY-level horizontal scrolling (internal scroller regions may scroll; the page body
// must not). Runs against the built WASM demo in the desktop project with an explicit
// narrow viewport.
// 1280 is here because narrow widths were the only ones checked, and the layouts that
// overflow at a desktop width are a different set: the side-by-side rules live behind
// min-width media queries, so a phone viewport never reaches them.
const WIDTHS = [320, 375, 1280];

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

// Both boxes, because the failure mode differs by who clips. When the page can scroll,
// the overflow shows up on documentElement; when an ancestor clips it, documentElement is
// clean and the content is simply gone off the right edge with no way to reach it -- that
// second case is the worse one for a reader and only body's own scrollWidth sees it.
async function bodyHasNoHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const de = document.documentElement;
    const b = document.body;
    return de.scrollWidth <= de.clientWidth + 1 && b.scrollWidth <= b.clientWidth + 1;
  });
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
      // The pickers read "1.0.0 · <short digest>": pick by version.
      for (const [sel, v] of [['#impact-old-rev', '1.0.0'], ['#impact-new-rev', '2.0.1']]) {
        const value = await page.locator(`${sel} option`).evaluateAll(
          (os, ver) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${ver} `))?.value ?? '', v);
        await page.locator(sel).selectOption(value);
      }
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.getByText('breaking', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
      // The long paths live in the folds: open them all before measuring.
      await page.locator('details.ca-fold').evaluateAll((ds) => ds.forEach((d) => { (d as HTMLDetailsElement).open = true; }));
      expect(await bodyHasNoHorizontalOverflow(page), 'change analysis populated results (long paths)').toBe(true);
    });
  });
}

// Every navbar control stays on screen. The bar used to collapse only at 768px while
// its full layout needed about 1,130px, so Refresh, Theme and Search were clipped and
// unreachable in between, and a long version string pushed the menu button off a phone.
for (const w of [390, 800, 1024, 1280]) {
  test(`every navbar control is within the window @${w}px`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: 800 });
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
    const outside = await page.evaluate(() => {
      const bad: string[] = [];
      for (const el of document.querySelectorAll<HTMLElement>('.navbar a, .navbar button')) {
        const r = el.getBoundingClientRect();
        if (r.width === 0 || getComputedStyle(el).visibility === 'hidden') continue;
        if (r.left < 0 || r.right > window.innerWidth + 1) bad.push(el.getAttribute('aria-label') || el.textContent?.trim() || el.tagName);
      }
      return bad;
    });
    expect(outside, `navbar controls off screen at ${w}px`).toEqual([]);
  });
}

// At 390px every copy button and every filter chip's remove button stays on screen. A
// long identifier used to push its copy button off the edge, and a long filter value
// pushed its chip, and the × that removes it, past the window.
test('copy and chip-remove buttons stay within the window @390px', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const long = 'a-very-long-filter-value-that-keeps-going-well-past-the-edge-of-a-phone';
  await page.goto(`/#/fleet/services?text=${long}&domain=${long}`);
  await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 });
  await page.goto('/#/fleet/services/payments-service');
  await expect(page.locator('.ev-body')).toBeVisible({ timeout: 20_000 });
  await page.getByTestId('entity-identifier').locator('summary').click();
  const off = async (sel: string) => page.evaluate((s) => [...document.querySelectorAll<HTMLElement>(s)]
    .filter((b) => { const r = b.getBoundingClientRect(); return r.width > 0 && (r.left < 0 || r.right > window.innerWidth + 1); })
    .map((b) => b.getAttribute('aria-label') || b.textContent || s), sel);
  expect(await off('.copyable-btn')).toEqual([]);
  await page.goto(`/#/fleet/services?text=${long}&domain=${long}`);
  await expect(page.locator('.chip').first()).toBeVisible({ timeout: 20_000 });
  expect(await off('.chip-x')).toEqual([]);
});
