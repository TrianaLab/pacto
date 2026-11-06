import { test, expect, type Page } from '@playwright/test';

// Platform offerings against the built WASM demo, at 1280px with the wide fallback
// font CI renders with: the list, an offering's providers and consume snippet, and
// the offerings a service uses.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));

test.describe('WASM demo — platform offerings', () => {
  test('the Offerings tab opens the list and the page never scrolls sideways', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet');
    await page.addStyleTag({ content: WIDE_FONT });
    const nav = page.locator('.navbar-nav-desktop');
    const tab = nav.getByRole('link', { name: 'Offerings' });
    await tab.scrollIntoViewIfNeeded();
    await tab.click();
    await expect(page).toHaveURL(/#\/fleet\/offerings$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Platform offerings' })).toBeVisible({ timeout: T });
    await expect(tab).toHaveAttribute('aria-current', 'page');
    // The tabs scroll inside the bar: the menu button stays on screen.
    await expect(page.getByRole('button', { name: 'Toggle theme' })).toBeInViewport();
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the demo lists its five offerings and shared-config has two providers', async ({ page }) => {
    await page.goto('/#/fleet/offerings');
    const list = page.getByTestId('offering-list');
    await expect(list).toBeVisible({ timeout: T });
    const rows = list.locator('li.pl-row');
    await expect(rows).toHaveCount(5);
    for (const name of ['messaging', 'postgresql', 'redis', 'shared-config', 'shared-policy']) {
      await expect(list.locator(`a.entity-link[href$="/fleet/offerings/${name}"]`)).toBeVisible();
    }
    await expect(rows.filter({ has: page.locator('a.entity-link[href$="/fleet/offerings/shared-config"]') })).toContainText('2 providers');
  });

  test('the postgresql page shows its provider, a consume snippet and its consumers', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    const provider = page.getByTestId('offering-provider');
    await expect(provider).toHaveCount(1, { timeout: T });
    await expect(provider).toContainText('Primary relational database for transactional data');
    const snippet = provider.getByTestId('consume').locator('pre.consume-snippet');
    await expect(snippet).toContainText('dependencies:');
    await expect(snippet).toContainText('ref: oci://demo.pacto.local/postgresql');
    await expect(snippet).toContainText('compatibility: "^16.0.0"');
    const consumer = page.getByTestId('offering-consumer').filter({ hasText: 'payments-service' }).first();
    await expect(consumer).toBeVisible();
    // No provider reports provisioning in this demo, so the badge says Unknown.
    await expect(consumer.getByTestId('provisioning-badge').first().locator('.tag')).toHaveText('Unknown');
    await expect(provider.locator('.ov-chips li').first()).toBeVisible();
    expect(await noSideScroll(page)).toBe(true);
  });

  test('a range on the postgresql page asks the server which versions satisfy it', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    const provider = page.getByTestId('offering-provider');
    await expect(provider).toHaveCount(1, { timeout: T });
    await provider.getByLabel('Versions satisfying').fill('^16.0.0');
    await expect(provider.locator('.ov')).toContainText('Latest compatible', { timeout: T });
    await expect(provider.locator('.ov-chips li').first()).toContainText('satisfied');
    await provider.getByLabel('Versions satisfying').fill('not a range');
    await expect(provider.getByRole('alert')).toContainText('not a semver range', { timeout: T });
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the shared-policy provider shows a policies snippet', async ({ page }) => {
    await page.goto('/#/fleet/offerings/shared-policy');
    const snippet = page.getByTestId('offering-provider').getByTestId('consume').locator('pre.consume-snippet');
    await expect(snippet).toContainText('policies:', { timeout: T });
    await expect(snippet).toContainText('name: platform-http-policy');
  });
});
