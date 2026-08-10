import { test, expect, type Page } from '@playwright/test';

// Search-as-you-type on the Services workspace.
//
// The workspace had a plain submit-only text box: you typed a name, pressed Search and
// hoped. The product knows what exists, so the field now asks it -- but a suggestion
// list is only worth having if it is honest about identity, and this demo is built to
// punish the shortcut: two DOMAINS publish a platform-app-config. A suggestion list
// that showed one row for that name, or that navigated by name, would send half its
// users to the wrong contract. So every assertion below is about the canonical key on
// the row, never the label on it.
//
// The rest is the discipline that separates a suggestion box from a nuisance: typing
// commits nothing and writes no history, the keyboard alone can reach every
// suggestion, an abandoned request can never overwrite a newer one, and the popup fits
// a phone.

const T = 20_000;

/** Hold exactly one matching fetch until the test releases it. */
function installFetchGate(): void {
  interface Gate { match: string }
  interface W { __gate: Gate | null; __held: Array<() => void> }
  const w = window as unknown as W;
  w.__gate = null;
  w.__held = [];
  // Captured before the accessor is installed, so this is the genuine network fetch;
  // the demo's own wasm fetch shim arrives later through the setter.
  let real: typeof fetch = window.fetch.bind(window);
  const gated: typeof fetch = (input, init) => {
    const url = String(typeof input === 'string' ? input : (input as Request).url ?? input);
    if (w.__gate && url.includes(w.__gate.match)) {
      w.__gate = null; // one request only; everything after it flows normally
      return new Promise<Response>((resolve) => { w.__held.push(() => resolve(real(input, init))); });
    }
    return real(input, init);
  };
  Object.defineProperty(window, 'fetch', {
    configurable: true,
    get() { return gated; },
    set(v: typeof fetch) { real = v; },
  });
}

async function openServices(page: Page) {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: T });
}

const searchBox = (page: Page) => page.getByTestId('svc-search');
const options = (page: Page) => page.getByTestId('svc-search-option');

/** Type into the service field and wait for the suggestions to settle. */
async function suggest(page: Page, text: string) {
  await searchBox(page).fill(text);
  await expect(page.getByTestId('svc-search-list')).toBeVisible({ timeout: T });
  await expect(page.getByTestId('svc-search-list')).not.toContainText('Searching…', { timeout: T });
}

test.describe('WASM demo — Services search suggests what exists', () => {
  test('typing suggests real services and commits nothing on its own', async ({ page }) => {
    await openServices(page);
    const before = await page.evaluate(() => history.length);

    await suggest(page, 'payments');
    await expect(options(page).first()).toBeVisible();
    // Identified by the canonical key the backend put on the row, not by its text.
    await expect(page.locator('[data-testid="svc-search-option"][data-key="payments-service"]')).toHaveCount(1);

    // Three more distinct queries: each one is a backend search, none is a navigation.
    for (const q of ['pay', 'orders', 'auth']) await suggest(page, q);
    await expect(page).toHaveURL(/#\/fleet\/services$/);
    expect(await page.evaluate(() => history.length)).toBe(before);
  });

  test('the keyboard alone reaches a suggestion and opens that exact service', async ({ page }) => {
    await openServices(page);
    await suggest(page, 'payments-service');

    const input = searchBox(page);
    await input.press('ArrowDown');
    // The highlighted option is named to assistive technology, not merely tinted.
    const active = await input.getAttribute('aria-activedescendant');
    expect(active).toBeTruthy();
    await expect(page.locator(`#${active}`)).toHaveAttribute('aria-selected', 'true');

    await input.press('Enter');
    await expect(page).toHaveURL(/#\/fleet\/services\/payments-service$/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
  });

  test('a name TWO domains use suggests both, distinguishably, and opens the one picked', async ({ page }) => {
    await openServices(page);
    await suggest(page, 'platform-app-config');

    // Two services, one name. The suggestion list says so instead of collapsing them.
    await expect(options(page)).toHaveCount(2);
    await expect(page.locator('[data-testid="svc-search-option"][data-key="platform-app-config"]')).toHaveCount(1);
    const partner = page.locator('[data-testid="svc-search-option"][data-key="partners/platform-app-config"]');
    await expect(partner).toHaveCount(1);
    // The partner one is legible as the partner one before it is clicked.
    await expect(partner).toContainText('domain partners');

    await partner.click();
    await expect(page).toHaveURL(/#\/fleet\/services\/partners%2Fplatform-app-config$/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
  });

  test('a query nothing matches says so rather than showing an empty box', async ({ page }) => {
    await openServices(page);
    await suggest(page, 'no-such-service-anywhere');
    await expect(options(page)).toHaveCount(0);
    await expect(page.getByTestId('svc-search-empty')).toContainText('No matches');
  });

  test('a slow answer to an abandoned search cannot overwrite the current one', async ({ page }) => {
    await page.addInitScript(installFetchGate);
    await openServices(page);

    // Hold the answer to the first search in flight.
    await page.evaluate(() => { (window as unknown as { __gate: unknown }).__gate = { match: 'text=settlement' }; });
    await searchBox(page).fill('settlement');
    await page.waitForFunction(() => (window as unknown as { __held: unknown[] }).__held.length === 1, null, { timeout: T });
    await expect(page.getByTestId('svc-search-list')).toContainText('Searching…');

    // The user moves on, and the second search answers normally.
    await suggest(page, 'platform-app-config');
    await expect(options(page)).toHaveCount(2);

    // Now the abandoned one finally arrives. It is answering a question nobody is asking.
    await page.evaluate(() => (window as unknown as { __held: Array<() => void> }).__held.forEach((f) => f()));
    await page.waitForTimeout(500);
    await expect(options(page)).toHaveCount(2);
    await expect(page.locator('[data-testid="svc-search-option"][data-key="partners/settlement-service"]')).toHaveCount(0);
  });

  test('Enter without a highlighted suggestion still runs the plain text filter', async ({ page }) => {
    await openServices(page);
    await suggest(page, 'platform');
    await searchBox(page).press('Enter');
    await expect(page).toHaveURL(/text=platform/, { timeout: T });
    // The filter ran against the backend, not against the suggestion list.
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    await expect(page.locator('[data-testid="service-list"] a.entity-link').first()).toBeVisible();
  });

  test('Escape dismisses the suggestions without navigating or clearing the filter', async ({ page }) => {
    await openServices(page);
    await suggest(page, 'payments');
    await searchBox(page).press('Escape');
    await expect(page.getByTestId('svc-search-list')).toBeHidden();
    await expect(searchBox(page)).toHaveAttribute('aria-expanded', 'false');
    await expect(page).toHaveURL(/#\/fleet\/services$/);
  });

  test('Owner suggests the owners the fleet actually has, and picking one filters', async ({ page }) => {
    await openServices(page);
    await page.getByTestId('svc-owner').fill('partner-integrations');
    const opt = page.getByTestId('svc-owner-option').first();
    await expect(opt).toBeVisible({ timeout: T });
    await opt.click();

    await expect(page).toHaveURL(/owner=partner-integrations/, { timeout: T });
    await expect(page.locator('[data-testid="service-list"] a[href$="/fleet/services/partners%2Fsettlement-service"]'))
      .toHaveCount(1, { timeout: T });
  });

  test('the suggestion popup fits a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 780 });
    await openServices(page);
    await suggest(page, 'platform-app-config');

    const list = page.getByTestId('svc-search-list');
    const box = await list.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
    // And it is still usable, not merely present.
    await options(page).first().click();
    await expect(page).toHaveURL(/#\/fleet\/services\//, { timeout: T });
  });
});
