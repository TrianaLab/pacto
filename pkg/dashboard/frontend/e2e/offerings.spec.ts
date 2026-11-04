import { test, expect, type Page } from '@playwright/test';

// The platform-offering list against the built WASM demo, at 1280px with the wide
// fallback font CI renders with. The provider cards, the consume snippet and the
// payments-service journey join this spec once the demo bundles declare offerings.

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
});
