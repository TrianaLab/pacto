import { test, expect, type Page } from '@playwright/test';
import { boot, canonicalKeys } from './typographyChecks';

/**
 * The shared "On this page" navigator, measured in a browser.
 *
 * Three claims a unit test cannot make, because all three are about layout, scrolling
 * and history rather than about markup:
 *
 *   * a long entity page can be navigated without scrolling to find out what is on it;
 *   * a section that is collapsed on arrival is OPENED by the contents list, not merely
 *     scrolled to -- a closed disclosure has no layout of its own, so scrolling to a
 *     target inside one lands the reader on whatever collapsed row occupies that pixel
 *     and tells them they arrived somewhere they cannot see;
 *   * the list is not a second meaning for the URL fragment. This is a hash-routed
 *     product: an `href="#section-id"` would leave the route, and Back would then walk
 *     through jump targets instead of pages.
 */

const TOC = '[data-testid="page-toc"]';

async function revisionPage(page: Page): Promise<void> {
  const k = await canonicalKeys(page);
  await boot(page, `#/fleet/revisions/${encodeURIComponent(k.revision)}`);
  await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });
}

test('a long revision page says what is on it before the reader scrolls', async ({ page }) => {
  test.setTimeout(240_000);
  await revisionPage(page);

  const m = await page.evaluate(() => {
    const nav = document.querySelector('[data-testid="page-toc"]')!;
    const box = nav.getBoundingClientRect();
    const labels = [...nav.querySelectorAll('.toc-link')].map((b) => b.textContent!.trim());
    const sections = [...document.querySelectorAll('[data-toc][id]')].map((e) => e.getAttribute('data-toc'));
    return {
      labels,
      sections,
      open: !!nav.querySelector('details[open]'),
      sticky: getComputedStyle(nav).position,
      bottom: Math.round(box.bottom),
      viewport: window.innerHeight,
    };
  });

  // Every entry names a section the page actually rendered. A contents list that offers
  // a section the page does not have is worse than no contents list at all.
  expect(m.labels.length).toBeGreaterThanOrEqual(3);
  expect(m.sections).toEqual(expect.arrayContaining(m.labels));
  // Open on arrival, entirely above the fold, and parked so it survives the scroll.
  expect(m.open).toBe(true);
  expect(m.bottom, `the contents rail runs past the fold (${m.bottom} > ${m.viewport})`).toBeLessThanOrEqual(m.viewport);
  expect(m.sticky).toBe('sticky');
});

test('choosing a collapsed section opens it, rather than scrolling to a closed row', async ({ page }) => {
  test.setTimeout(240_000);
  await revisionPage(page);
  // Reduced motion, so the jump is instant and the measurement below is of where the
  // reader ended up rather than of where an in-flight smooth scroll happened to be.
  // Set here rather than through the `reducedMotion` fixture: the fixture leaves
  // matchMedia reporting no preference in this browser, so the jump kept animating.
  await page.emulateMedia({ reducedMotion: 'reduce' });

  // A section that is CLOSED on arrival -- the diagnostic depth an entity page keeps out
  // of the way. If the page has none, this test has nothing to prove and should fail
  // loudly rather than pass vacuously.
  const closed = await page.evaluate(() => {
    const el = [...document.querySelectorAll('details[data-toc][id]')].find((d) => !(d as HTMLDetailsElement).open);
    return el ? { id: el.id, label: el.getAttribute('data-toc')! } : null;
  });
  expect(closed, 'no section on the revision page is collapsed on arrival').not.toBeNull();

  await page.getByRole('button', { name: closed!.label, exact: true }).click();

  const after = await page.evaluate((id: string) => {
    const el = document.getElementById(id) as HTMLDetailsElement;
    const b = el.getBoundingClientRect();
    return { open: el.open, top: Math.round(b.top), viewport: window.innerHeight, focused: document.activeElement === el };
  }, closed!.id);

  expect(after.open, `"${closed!.label}" was still closed after being chosen from the contents`).toBe(true);
  expect(after.top).toBeGreaterThanOrEqual(0);
  expect(after.top).toBeLessThan(after.viewport);
  // The keyboard caret travels with the viewport, so the next Tab continues from the
  // section the reader asked for and not from the next entry in the rail.
  expect(after.focused).toBe(true);
});

test('the contents list is not a second kind of URL', async ({ page }) => {
  test.setTimeout(240_000);
  const k = await canonicalKeys(page);
  // Two real route entries, so there is something for a broken Back to fall into.
  await boot(page, '#/fleet/revisions');
  await page.locator(`${TOC}, main`).first().waitFor();
  await page.evaluate((key: string) => { location.hash = `#/fleet/revisions/${encodeURIComponent(key)}`; }, k.revision);
  await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });

  const before = await page.evaluate(() => ({ hash: location.hash, entries: history.length }));
  const labels = await page.locator(`${TOC} .toc-link`).allTextContents();
  for (const label of labels.slice(0, 3)) {
    await page.getByRole('button', { name: label.trim(), exact: true }).click();
  }
  const after = await page.evaluate(() => ({ hash: location.hash, entries: history.length }));

  expect(after.hash, 'jumping to a section rewrote the route').toBe(before.hash);
  expect(after.entries, 'jumping to a section pushed history entries').toBe(before.entries);

  // And Back still means the page before, not the section before.
  await page.goBack();
  await expect(page.locator('[data-testid="page-title"]')).toHaveText('Contract revisions', { timeout: 30_000 });
});

test('below the rail breakpoint it is the same control, closed, under the title', async ({ page }) => {
  test.setTimeout(240_000);
  await page.setViewportSize({ width: 900, height: 900 });
  await revisionPage(page);

  const m = await page.evaluate(() => {
    const nav = document.querySelector('[data-testid="page-toc"]')!;
    const details = nav.querySelector('details') as HTMLDetailsElement;
    const header = document.querySelector('main .page-hd')!;
    return {
      open: details.open,
      // A closed disclosure still lists nothing, and still has its summary reachable.
      links: nav.querySelectorAll('.toc-link').length,
      summary: nav.querySelector('summary')!.textContent!.trim().replace(/\s+/g, ' '),
      afterHeader: header.compareDocumentPosition(nav) === Node.DOCUMENT_POSITION_FOLLOWING,
      sticky: getComputedStyle(nav).position,
    };
  });

  expect(m.open, 'the contents list opened by default where it costs a screenful').toBe(false);
  expect(m.links).toBeGreaterThanOrEqual(3);
  expect(m.summary).toContain('On this page');
  expect(m.afterHeader).toBe(true);
  expect(m.sticky).not.toBe('sticky');
});
