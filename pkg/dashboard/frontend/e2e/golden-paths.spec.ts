import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// Golden paths against the built WASM demo, at 1280px with the wide fallback font CI
// renders with: the tab, the http-service golden path page and gift-cards, the
// service created from it.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

async function axeClean(page: Page) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual([]);
}

test.describe('WASM demo — golden paths', () => {
  test('the Golden paths tab opens the list and the page never scrolls sideways', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet');
    await page.addStyleTag({ content: WIDE_FONT });
    const tab = page.locator('.navbar-nav-desktop').getByRole('link', { name: 'Golden paths' });
    await tab.scrollIntoViewIfNeeded();
    await tab.click();
    await expect(page).toHaveURL(/#\/fleet\/golden-paths$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Golden paths' })).toBeVisible({ timeout: T });
    await expect(tab).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('button', { name: 'Toggle theme' })).toBeInViewport();
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the tab lists http-service and its page shows the plugins, the command and gift-cards', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/golden-paths');
    await page.addStyleTag({ content: WIDE_FONT });
    const list = page.getByTestId('golden-path-list');
    await expect(list).toBeVisible({ timeout: T });
    const row = list.locator('tbody tr').filter({ hasText: 'http-service' });
    await expect(row).toHaveCount(1);
    await row.locator('a.entity-link').click();
    const gp = page.getByTestId('golden-path');
    await expect(gp).toBeVisible({ timeout: T });
    await expect(gp.getByTestId('golden-path-plugins')).toContainText('dockerfile');
    await expect(gp.getByTestId('golden-path-create')).toContainText('pacto init');
    await expect(page.getByTestId('golden-path-created')).toContainText('gift-cards');
    expect(await noSideScroll(page)).toBe(true);
    await axeClean(page);
  });

  test('gift-cards shows where it was created from and its creation status', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    // Through the path's own "Services created" count, so the filter carries the path's
    // real key rather than one this spec guesses.
    await page.goto('/#/fleet/golden-paths');
    const row = page.getByTestId('golden-path-list').locator('tbody tr').filter({ hasText: 'http-service' });
    await row.getByTestId('path-created').click({ timeout: T });
    await expect(page).toHaveURL(/createdFrom=/);
    await page.getByTestId('service-list').locator('a.entity-link').filter({ hasText: 'gift-cards' }).click();
    const origin = page.getByTestId('service-origin');
    await expect(origin).toContainText('Created from', { timeout: T });
    await expect(origin).toContainText('declared');
    const deployed = page.getByTestId('creation-status').locator('li[data-step="deployed"]');
    await expect(deployed).toContainText('Unknown');
    await axeClean(page);
  });
});
