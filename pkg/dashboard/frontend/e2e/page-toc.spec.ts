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
 *     through jump targets instead of pages;
 *   * and WHICH entry is current, which is a question about scroll position and about
 *     nothing else -- jsdom has no layout, so the rule can only be exercised there
 *     against rects the test itself invented.
 */

const TOC = '[data-testid="page-toc"]';

async function revisionPage(page: Page): Promise<void> {
  const k = await canonicalKeys(page);
  await boot(page, `#/fleet/revisions/${encodeURIComponent(k.revision)}`);
  await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });
}

/** The entries marked current -- as a LIST, so "exactly one" is part of every assertion. */
function currentEntries(page: Page): Promise<string[]> {
  return page.$$eval(`${TOC} .toc-link[aria-current="true"]`, (n) => n.map((b) => b.textContent!.trim()));
}

/**
 * Scrolls until `label`'s section is just past the reading line it is measured against,
 * the way a reader arrives at it -- not scrollIntoView, which is the gesture the contents
 * list itself performs and would prove nothing about scrolling.
 */
async function scrollPast(page: Page, label: string): Promise<boolean> {
  return page.evaluate((wanted: string) => {
    const el = [...document.querySelectorAll('[data-toc][id]')].find((e) => e.getAttribute('data-toc') === wanted)!;
    const line = parseFloat(getComputedStyle(el).scrollMarginTop) || 0;
    window.scrollTo(0, window.scrollY + el.getBoundingClientRect().top - line + 4);
    // The page can run out before its last section reaches the line. Say so rather than
    // asserting against a scroll the browser clamped.
    return el.getBoundingClientRect().top <= line + 1;
  }, label);
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

test('the contents list says which section the reader is in', async ({ page }) => {
  test.setTimeout(240_000);
  await revisionPage(page);
  const labels = (await page.locator(`${TOC} .toc-link`).allTextContents()).map((s) => s.trim());
  expect(labels.length).toBeGreaterThanOrEqual(3);

  // At the top of the page the reader is in its first section, not in none of them.
  await expect.poll(() => currentEntries(page), { timeout: 15_000 }).toEqual([labels[0]]);

  // And the marker travels with them. Every section is stepped through in turn, so a rule
  // that sticks on the first, skips two at a time, or marks several at once fails here
  // rather than being averaged away by only checking the last one.
  let moved = 0;
  for (const label of labels.slice(1)) {
    if (!await scrollPast(page, label)) break; // the page ran out before this section did
    await expect.poll(() => currentEntries(page), { timeout: 15_000 }).toEqual([label]);
    moved++;
  }
  expect(moved, 'the page was too short to scroll between sections, so this proved nothing').toBeGreaterThanOrEqual(2);

  // The distinction is not carried by hue: the current entry is heavier and gains a
  // marker, both of which survive greyscale and a colour-blind reader.
  const cue = await page.evaluate(() => {
    const cur = document.querySelector('.toc-link[aria-current="true"]')!;
    const other = [...document.querySelectorAll('.toc-link')].find((b) => b !== cur)!;
    const [a, b] = [getComputedStyle(cur), getComputedStyle(other)];
    return { weight: [Number(a.fontWeight), Number(b.fontWeight)], marker: [a.borderLeftColor, b.borderLeftColor] };
  });
  expect(cue.weight[0], `current entry weighs ${cue.weight[0]}, the others ${cue.weight[1]}`).toBeGreaterThan(cue.weight[1]);
  expect(cue.marker[0]).not.toBe(cue.marker[1]);
});

test('choosing a section makes it current at once, and holds until the reader drives', async ({ page }) => {
  test.setTimeout(240_000);
  // Deliberately NOT reduced motion: the smooth scroll is the hazard being measured.
  await revisionPage(page);
  const labels = (await page.locator(`${TOC} .toc-link`).allTextContents()).map((s) => s.trim());
  const before = await page.evaluate(() => ({ hash: location.hash, entries: history.length }));

  // The last entry: furthest to travel, most sections to cross on the way, and the one
  // most likely to be too short to ever reach the reading line on arrival.
  const chosen = labels[labels.length - 1];
  await page.getByRole('button', { name: chosen, exact: true }).click();
  expect(await currentEntries(page), 'the choice was not answered until the scroll arrived').toEqual([chosen]);

  await page.waitForTimeout(1500); // the animation lands, having crossed every section
  expect(await currentEntries(page), 'the scroll walked the marker off the chosen section').toEqual([chosen]);

  // Current is somewhere the reader can actually see: opened, if it arrived collapsed.
  expect(await page.evaluate((wanted: string) => {
    const el = [...document.querySelectorAll('[data-toc][id]')].find((e) => e.getAttribute('data-toc') === wanted)!;
    return el.tagName !== 'DETAILS' || (el as HTMLDetailsElement).open;
  }, chosen)).toBe(true);

  // None of it touched the route. In a hash-routed product that is the whole reason
  // these are buttons.
  expect(await page.evaluate(() => ({ hash: location.hash, entries: history.length }))).toEqual(before);

  // Once the reader takes the scroll back, geometry answers again -- at the top of the
  // page, that is the first section.
  for (let i = 0; i < 20 && await page.evaluate(() => window.scrollY) > 0; i++) {
    await page.mouse.wheel(0, -4000);
  }
  expect(await page.evaluate(() => Math.round(window.scrollY))).toBe(0);
  await expect.poll(() => currentEntries(page), { timeout: 15_000 }).toEqual([labels[0]]);
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

test('a section jump is the place the product restores on Back and Forward', async ({ page }) => {
  test.setTimeout(240_000);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const k = await canonicalKeys(page);
  await boot(page, '#/fleet/revisions');
  await page.evaluate((key: string) => { location.hash = `#/fleet/revisions/${encodeURIComponent(key)}`; }, k.revision);
  await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });

  // Jump deep into the page. Because the rail pushes no history entry, this scroll
  // belongs to the CURRENT entry -- so the product's place-keeping has to record it on
  // the way out, or Back returns the reader to the top of a page they had left halfway.
  const labels = await page.locator(`${TOC} .toc-link`).allTextContents();
  await page.getByRole('button', { name: labels[labels.length - 1].trim(), exact: true }).click();
  const jumped = await page.evaluate(() => Math.round(window.scrollY));
  expect(jumped, 'the last section is at the top of the page, so this proves nothing').toBeGreaterThan(200);

  // Leave, come back, go forward again. The revision page keeps the section the reader
  // chose; the list it came from is still the list. (A revision's breadcrumb trail goes
  // up to its service, not to the inventory, so the route is pushed directly -- through
  // the same router the trail uses.)
  await page.evaluate(() => { location.hash = '#/fleet/revisions'; });
  await expect(page.locator('[data-testid="page-title"]')).toHaveText('Contract revisions', { timeout: 30_000 });
  await page.goBack();
  await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });
  // As far down as the returning document allows. Not "exactly where you were": the jump
  // OPENED a disclosure, and a page rendered fresh from its default states is shorter than
  // the one that was left, so the recorded offset can exceed the new maximum. Restoring to
  // the bottom of a shorter page is the honest outcome; returning to the top is not.
  await expect.poll(async () => page.evaluate((want: number) => {
    const max = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
    const y = Math.round(window.scrollY);
    return y >= Math.min(want, max) - 8 ? 'restored' : `left at ${want}, came back to ${y} of a possible ${max}`;
  }, jumped), { timeout: 15_000 }).toBe('restored');

  await page.goForward();
  await expect(page.locator('[data-testid="page-title"]')).toHaveText('Contract revisions', { timeout: 30_000 });
  expect(await page.evaluate(() => location.hash)).toBe('#/fleet/revisions');
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
