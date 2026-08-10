import { test, expect, type Page } from '@playwright/test';

// "Keep my place." Browser acceptance for the one thing a dashboard that polls itself
// must never do: throw away what the user is reading.
//
// The regression this file locks down was concrete. Every entity page reset itself to a
// loading state on each refresh tick -- and the app advances that tick on its own poll --
// so an automatic background refresh removed the entity body from the DOM, the page
// collapsed to nothing and the browser clamped the scroll offset toward the top. The user
// lost their place several times a minute without touching anything.
//
// Only a real browser can prove the fix: it needs layout (a page tall enough to scroll),
// a real scroll offset and the real poll timer. The assertions are therefore about the
// two things the user actually perceives -- the section they were reading is still on
// screen, and the page did not jump -- plus the mechanism underneath: the entity body was
// never removed from the DOM in the first place. Asserting only the offset would pass a
// page that collapsed and was scrolled back by force.

const T = 20_000;

// A few pixels of tolerance: a re-render can settle a hair differently (a scrollbar, a
// tooltip, a re-measured chart) without the user perceiving any movement.
const TOLERANCE = 8;

async function boot(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: T });
}

async function openService(page: Page) {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
  // By canonical key, not by label: the demo publishes same-named services in two
  // domains, so a visible name is not an identity.
  await page.locator('.sv-item a.entity-link[href$="/fleet/services/payments-service"]').first().click();
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
}

async function openRevision(page: Page) {
  await openService(page);
  await page.locator('a.entity-link[href*="/fleet/revisions/"]').first().click();
  await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
}

async function openTarget(page: Page) {
  await openService(page);
  await page.locator('a.entity-link[href*="/fleet/targets/"]').first().click();
  await expect(page.getByRole('heading', { level: 1, name: /^Operational target: / })).toBeVisible({ timeout: T });
}

const PAGES: Array<[string, (p: Page) => Promise<void>]> = [
  ['service', openService],
  ['revision', openRevision],
  ['target', openTarget],
];

/**
 * Scroll to the last section heading on the page and report where we ended up. The
 * scroll is issued from INSIDE the page (Playwright's own scrollIntoViewIfNeeded is
 * driven over the protocol and does not dispatch a scroll event), so this is the same
 * event the product sees from a real wheel or keyboard scroll.
 */
async function scrollToLastSection(page: Page) {
  const heading = page.locator('main h2, main h3').last();
  await expect(heading).toBeVisible({ timeout: T });
  await heading.evaluate((el) => {
    window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY - 80);
  });
  await page.waitForTimeout(150);
  const y = await page.evaluate(() => window.scrollY);
  return { heading, y };
}

/**
 * Watch the entity body for the failure mode itself: a refresh that empties `main`.
 * Returns a reader for how many times the page went blank.
 */
async function watchForBlanking(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __blanked: number; __obs?: MutationObserver };
    w.__blanked = 0;
    const main = document.querySelector('main');
    if (!main) return;
    w.__obs?.disconnect();
    w.__obs = new MutationObserver(() => {
      if (!main.querySelector('h1')) w.__blanked++;
    });
    w.__obs.observe(main, { childList: true, subtree: true });
  });
  return () => page.evaluate(() => (window as unknown as { __blanked: number }).__blanked);
}

/** Count the app's own polling requests, so a test can wait for a REAL background poll. */
function installPollCounter(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => ServeResult;
  const w = window as unknown as { __polls: number };
  w.__polls = 0;
  let real: Serve | null = null;
  const wrapped: Serve = (method, path, body) => {
    // /health is fetched once per loadGlobal() and by nothing else, so it counts
    // refreshes exactly -- automatic and explicit alike.
    if (path.startsWith('/health')) w.__polls++;
    return real!(method, path, body);
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

const polls = (page: Page) => page.evaluate(() => (window as unknown as { __polls: number }).__polls);

test.describe('WASM demo — the product keeps the user\'s place', () => {
  for (const [kind, open] of PAGES) {
    test(`an explicit Refresh leaves a scrolled ${kind} page exactly where it was`, async ({ page }) => {
      await boot(page);
      await open(page);

      const { heading, y } = await scrollToLastSection(page);
      // A page that cannot scroll would make every assertion below vacuous.
      expect(y).toBeGreaterThan(0);
      const readBlanked = await watchForBlanking(page);

      await page.getByRole('button', { name: 'Refresh' }).click();
      // Let the refresh actually land, and give any collapse time to show up.
      await expect(page.getByRole('button', { name: 'Refresh' })).toBeEnabled({ timeout: T });
      await page.waitForTimeout(800);

      expect(await readBlanked()).toBe(0);
      await expect(heading).toBeVisible();
      expect(Math.abs((await page.evaluate(() => window.scrollY)) - y)).toBeLessThanOrEqual(TOLERANCE);
    });
  }

  test('the automatic poll does not move the user', async ({ page }) => {
    await page.addInitScript(installPollCounter);
    await boot(page);
    await openRevision(page);

    const { heading, y } = await scrollToLastSection(page);
    expect(y).toBeGreaterThan(0);
    const readBlanked = await watchForBlanking(page);

    // Wait for the app's OWN timer, with no user action at all -- this is the exact path
    // that used to reset the page under the reader.
    const before = await polls(page);
    await page.waitForFunction((n) => (window as unknown as { __polls: number }).__polls > n, before, { timeout: T });
    await page.waitForTimeout(500);

    expect(await readBlanked()).toBe(0);
    await expect(heading).toBeVisible();
    expect(Math.abs((await page.evaluate(() => window.scrollY)) - y)).toBeLessThanOrEqual(TOLERANCE);
  });

  test('a reload of a deep link restores the place once the content settles', async ({ page }) => {
    await boot(page);
    await openRevision(page);
    const { heading, y } = await scrollToLastSection(page);
    expect(y).toBeGreaterThan(0);

    await page.reload();
    // The offset can only be re-applied after the revision request lands, so the
    // assertion waits for the page rather than for a fixed delay.
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
    await expect
      .poll(async () => Math.abs((await page.evaluate(() => window.scrollY)) - y), { timeout: T })
      .toBeLessThanOrEqual(TOLERANCE);
    await expect(heading).toBeVisible();
  });

  test('a new entity opens at the top rather than inheriting the last page offset', async ({ page }) => {
    await boot(page);
    await openService(page);
    const { y } = await scrollToLastSection(page);
    expect(y).toBeGreaterThan(0);

    await page.locator('a.entity-link[href*="/fleet/revisions/"]').first().click();
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
    expect(await page.evaluate(() => window.scrollY)).toBeLessThanOrEqual(TOLERANCE);
  });

  test('Back returns to where you left the previous page', async ({ page }) => {
    await boot(page);
    await openService(page);
    // Read the destination BEFORE scrolling: clicking a link would make Playwright
    // scroll it into view first, which is itself a scroll and would move the place
    // we are trying to prove is remembered.
    const href = await page.locator('a.entity-link[href*="/fleet/revisions/"]').first().getAttribute('href');

    const { heading, y } = await scrollToLastSection(page);
    expect(y).toBeGreaterThan(0);

    await page.evaluate((h) => { location.hash = h!; }, href);
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
    expect(await page.evaluate(() => window.scrollY)).toBeLessThanOrEqual(TOLERANCE);

    await page.goBack();
    await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
    await expect
      .poll(async () => Math.abs((await page.evaluate(() => window.scrollY)) - y), { timeout: T })
      .toBeLessThanOrEqual(TOLERANCE);
    await expect(heading).toBeVisible();
  });
});
