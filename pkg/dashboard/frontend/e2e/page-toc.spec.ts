import { test, expect, type Page } from '@playwright/test';
import { boot } from './typographyChecks';
import { DOC_ORIGIN } from './ports';

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

/**
 * A revision of api-gateway: a long page whose ten sections are what the rail is sized
 * for. Named, not "the fleet's first revision": that is whichever service sorts first,
 * and when the demo gained analytics-pipeline, a dozen-line contract that fits a laptop
 * screen with nothing to scroll, every claim below silently moved onto it.
 */
async function longRevision(page: Page): Promise<string> {
  await boot(page, '#/fleet');
  const revision = await page.evaluate(async () => {
    const r = await (await fetch('/api/fleet/entities?kinds=revision&service=api-gateway&limit=1')).json();
    return (r.entities || [])[0]?.key || '';
  });
  expect(revision, 'the demo has no revision of api-gateway').not.toBe('');
  return revision;
}

async function revisionPage(page: Page): Promise<void> {
  await boot(page, `#/fleet/revisions/${encodeURIComponent(await longRevision(page))}`);
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
  // The demo's own notice is a band above the dashboard; this measures the dashboard.
  await page.getByTestId('demo-strip-close').click();

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

test('a section that parks lower than the rest is still the one the reader is in', async ({ page }) => {
  test.setTimeout(240_000);
  await revisionPage(page);

  // Sections share a default scroll-margin-top today, and nothing makes them: one
  // parking under a sticky sub-header clears more than a top-level section under the app
  // bar. Give one section its own line, which is the case a single shared line gets wrong.
  const chosen = (await page.locator(`${TOC} .toc-link`).allTextContents())[1].trim();
  const id = await page.evaluate((wanted: string) =>
    [...document.querySelectorAll('[data-toc][id]')].find((e) => e.getAttribute('data-toc') === wanted)!.id, chosen);
  await page.addStyleTag({ content: `#${id} { scroll-margin-top: 320px; }` });

  // The reader SCROLLS to it rather than clicking the entry: a click pins its own answer,
  // which would let this pass on the pin instead of on the rule. Nothing is pinned here,
  // so the marker is whatever the geometry rule computes at this scroll position.
  expect(await scrollPast(page, chosen), `the page is too short to scroll "${chosen}" to its own line`).toBe(true);
  const m = await page.evaluate((secId: string) => {
    const first = document.querySelector('[data-toc][id]')!;
    return {
      top: Math.round(document.getElementById(secId)!.getBoundingClientRect().top),
      shared: parseFloat(getComputedStyle(first).scrollMarginTop) || 0,
    };
  }, id);
  // Where it came to rest has to be BELOW the line the page's other sections are
  // measured against, or the two rules would agree here and this would prove nothing.
  // Against one shared line taken from the first visible section this section reads as
  // "not reached yet", and the marker sits on the section above it.
  expect(m.top, `"${chosen}" rests at ${m.top}, above the shared line at ${m.shared}`)
    .toBeGreaterThan(m.shared + 1);

  await expect.poll(() => currentEntries(page), { timeout: 15_000 }).toEqual([chosen]);
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

  // The animation lands, having crossed every section: the section rests on its own
  // line, or the page has run out beneath it. Waited on, not timed -- a loaded machine
  // stretches the animation, and a wheel sent mid-flight would race the scroll.
  await page.waitForFunction((wanted: string) => {
    const el = [...document.querySelectorAll('[data-toc][id]')].find((e) => e.getAttribute('data-toc') === wanted)!;
    const line = parseFloat(getComputedStyle(el).scrollMarginTop) || 0;
    const end = document.documentElement.scrollHeight - window.innerHeight;
    return Math.abs(el.getBoundingClientRect().top - line) <= 1 || window.scrollY >= end - 1;
  }, chosen);
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
  const revision = await longRevision(page);
  // Two real route entries, so there is something for a broken Back to fall into.
  await boot(page, '#/fleet/revisions');
  await page.locator(`${TOC}, main`).first().waitFor();
  await page.evaluate((key: string) => { location.hash = `#/fleet/revisions/${encodeURIComponent(key)}`; }, revision);
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
  const revision = await longRevision(page);
  await boot(page, '#/fleet/revisions');
  await page.evaluate((key: string) => { location.hash = `#/fleet/revisions/${encodeURIComponent(key)}`; }, revision);
  await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });

  // Jump deep into the page. Because the rail pushes no history entry, this scroll
  // belongs to the CURRENT entry -- so the product's place-keeping has to record it on
  // the way out, or Back returns the reader to the top of a page they had left halfway.
  const labels = await page.locator(`${TOC} .toc-link`).allTextContents();
  await page.getByRole('button', { name: labels[labels.length - 1].trim(), exact: true }).click();
  const jumped = await page.evaluate(() => Math.round(window.scrollY));
  expect(jumped, 'the last section is at the top of the page, so this proves nothing').toBeGreaterThan(200);

  // The product learns where the reader is from a scroll EVENT, which the browser
  // dispatches on the next rendered frame -- not from the scroll position itself. A
  // test that navigates away in the same frame as the jump measures a place the page
  // was never told about. One frame is the product's own precondition, not a grace
  // period: a reader cannot leave a page faster than it repaints.
  await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));

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

/**
 * A revision with more sections than the rail used to be sized for: a payments-service
 * revision with deploy targets carries eleven or more. The first such revision is found
 * by counting, so the claim does not depend on which revision key sorts first.
 */
async function crowdedRevisionPage(page: Page): Promise<number> {
  await boot(page, '#/fleet');
  const keys: string[] = await page.evaluate(async () => {
    const r = await (await fetch('/api/fleet/entities?kinds=revision&service=payments-service&limit=50')).json();
    return (r.entities || []).map((e: { key: string }) => e.key);
  });
  for (const key of keys) {
    await boot(page, `#/fleet/revisions/${encodeURIComponent(key)}`);
    await expect(page.locator(TOC)).toBeVisible({ timeout: 30_000 });
    const n = await page.locator(`${TOC} .toc-link`).count();
    if (n >= 11) return n;
  }
  throw new Error('no payments-service revision in the demo has eleven or more sections');
}

/** Where the rail's list ends and where the current entry sits, against the fold. */
function railGeometry(page: Page) {
  return page.evaluate(() => {
    const nav = document.querySelector('[data-testid="page-toc"]')!;
    const list = nav.querySelector('.toc-list')!.getBoundingClientRect();
    const cur = nav.querySelector('.toc-link[aria-current="true"]');
    const c = cur?.getBoundingClientRect();
    return {
      bottom: Math.round(nav.getBoundingClientRect().bottom),
      viewport: window.innerHeight,
      current: cur?.textContent?.trim() ?? '',
      currentInList: !!c && c.top >= list.top - 1 && c.bottom <= list.bottom + 1,
      currentOnScreen: !!c && c.top >= 0 && c.bottom <= window.innerHeight,
    };
  });
}

test('a rail with more entries than fit still fits, and keeps the current one in view', async ({ page }) => {
  test.setTimeout(240_000);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const n = await crowdedRevisionPage(page);
  await page.getByTestId('demo-strip-close').click();
  const labels = (await page.locator(`${TOC} .toc-link`).allTextContents()).map((s) => s.trim());
  expect(labels.length).toBe(n);

  // On arrival: above the fold, with the first section current and visible.
  await expect.poll(async () => (await railGeometry(page)).current, { timeout: 15_000 }).toBe(labels[0]);
  let g = await railGeometry(page);
  expect(g.bottom, `the contents rail runs past the fold (${g.bottom} > ${g.viewport}) with ${n} entries`).toBeLessThanOrEqual(g.viewport);
  expect(g.currentInList && g.currentOnScreen, 'the current entry is scrolled out of sight').toBe(true);

  // Not vacuous: at this size the entries do not all fit, so the list scrolls itself.
  expect(await page.$eval(`${TOC} .toc-list`, (l) => l.scrollHeight > l.clientHeight), 'every entry fits, so this proves nothing').toBe(true);

  // The reader scrolls to the end of the page: whichever section the marker reaches, the
  // rail shows it and still ends above the fold.
  for (let i = 0; i < 20; i++) await page.mouse.wheel(0, 4000);
  await expect.poll(async () => labels.indexOf((await railGeometry(page)).current), { timeout: 15_000 }).toBeGreaterThan(0);
  g = await railGeometry(page);
  expect(g.bottom).toBeLessThanOrEqual(g.viewport);
  expect(g.currentInList && g.currentOnScreen, `"${g.current}" is current but scrolled out of sight in the rail`).toBe(true);

  // Every entry stays reachable by keyboard, and choosing the last one keeps it in view
  // as the current entry.
  const lastLabel = labels[labels.length - 1];
  const last = page.getByRole('button', { name: lastLabel, exact: true });
  await last.focus();
  await page.keyboard.press('Enter');
  await expect.poll(async () => (await railGeometry(page)).current).toBe(lastLabel);
  g = await railGeometry(page);
  expect(g.bottom).toBeLessThanOrEqual(g.viewport);
  expect(g.currentInList && g.currentOnScreen, `the chosen "${lastLabel}" is hidden inside the rail`).toBe(true);
});

test('the pacto doc page rail fits too, however many sections the contract fills', async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto(`${DOC_ORIGIN}/index.html`);
  const rail = page.locator('nav.section-toc');
  await expect(rail).toBeVisible({ timeout: 60_000 });
  const m = await rail.evaluate((nav) => {
    const cur = nav.querySelector('[aria-current="true"]')!.getBoundingClientRect();
    const box = nav.getBoundingClientRect();
    return {
      entries: nav.querySelectorAll('.section-toc-item').length,
      overflows: nav.scrollHeight > nav.clientHeight,
      bottom: Math.round(box.bottom),
      viewport: window.innerHeight,
      currentInRail: cur.top >= box.top - 1 && cur.bottom <= box.bottom + 1,
    };
  });
  expect(m.entries).toBeGreaterThanOrEqual(11);
  expect(m.overflows, 'every entry fits, so this proves nothing').toBe(true);
  expect(m.bottom, `the doc page rail runs past the fold (${m.bottom} > ${m.viewport})`).toBeLessThanOrEqual(m.viewport);
  expect(m.currentInRail).toBe(true);

  // At the end of the page the marker has moved down the list, and the rail moved with it.
  const first = await rail.locator('[aria-current="true"]').textContent();
  for (let i = 0; i < 20; i++) await page.mouse.wheel(0, 4000);
  await expect.poll(() => rail.locator('[aria-current="true"]').textContent(), { timeout: 15_000 }).not.toBe(first);
  expect(await rail.evaluate((nav) => {
    const r = nav.querySelector('[aria-current="true"]')!.getBoundingClientRect();
    const box = nav.getBoundingClientRect();
    return r.top >= box.top - 1 && r.bottom <= box.bottom + 1 && box.bottom <= window.innerHeight;
  }), 'the current entry is hidden inside the rail').toBe(true);
});
