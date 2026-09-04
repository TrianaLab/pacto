import { test, expect, type Page } from '@playwright/test';
import { installFetchGate, gate } from './fetchGate';

// Stale-while-revalidate, scoped to the QUESTION -- browser acceptance.
//
// Every product list keeps its rows while a refresh is in flight, so the app's own poll
// does not tear the page out from under a reader. That is right for a REFRESH and wrong
// for a NEW QUERY: rows retained across a filter, a page or a scope change are one
// question's answer rendered under another question's heading. The list says "42
// services", the pager says "Showing 1-25 of 42", and none of it describes what the user
// just asked for.
//
// The fix is a caller-owned `queryIdentity` (filters + page + scope, deliberately WITHOUT
// the refresh tick) recorded against the data on hand. `refreshTick` re-asks the same
// question; anything else is a different one, and a different one shows a loading state
// rather than the previous answer.
//
// Only a real browser with a REAL delay proves it. A resolved-immediately request never
// leaves the window in which the wrong rows are on screen, so the whole class of bug is
// invisible to a test that does not hold the response open. These tests hold it open
// explicitly, through the fetch gate in ./fetchGate -- the one seam every API call
// passes. Playwright's own network interception never sees a wasm-served fetch, so it
// cannot be used here.

const T = 30_000;

/**
 * Inflates the service population so real pagination exists. The demo fleet is a
 * narrative of sixteen services; page 2 of it is empty, and an empty page cannot show
 * whether page 1's rows were wrongly retained. Only the service Entities response is
 * amended, and it is amended into exactly the shape the Product API promises.
 */
function installBigServices(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => ServeResult;
  let real: Serve | null = null;
  const TOTAL = 60;

  const wrapped: Serve = (method, path, body) => {
    const res = (real as Serve)(method, path, body);
    const [route, search] = path.split('?');
    const query = new URLSearchParams(search || '');
    if (route !== '/api/fleet/entities' || query.get('kinds') !== 'service' || res.status !== 200) return res;
    let doc: Record<string, unknown>;
    try { doc = JSON.parse(res.body); } catch { return res; }

    const offset = Number(query.get('offset') || 0);
    const limit = Number(query.get('limit') || 25);
    const entities = [];
    for (let i = offset; i < Math.min(offset + limit, TOTAL); i++) {
      const key = `domain-load/service-${String(i).padStart(3, '0')}`;
      entities.push({
        kind: 'service', key, label: `service-${String(i).padStart(3, '0')}`, domain: 'domain-load',
        status: 'Compliant', href: `/fleet/services/${encodeURIComponent(key)}`,
      });
    }
    doc.entities = entities;
    doc.total = TOTAL; doc.count = entities.length; doc.offset = offset; doc.limit = limit;
    doc.truncated = offset + entities.length < TOTAL;
    if (doc.truncated) doc.nextOffset = offset + limit; else delete doc.nextOffset;
    return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
  };

  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

/** The skeleton the shared EmptyState renders while a question has no answer yet. */
const skeleton = (page: Page) => page.getByTestId('loading-skeleton');

test.describe('WASM demo — retained rows answer the question that is on screen', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(installFetchGate);
  });

  test('a Services filter committed mid-flight does not leave the previous answer on screen', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const before = await page.getByTestId('service-list').locator('li').count();
    expect(before).toBeGreaterThan(1);

    const g = gate(page);
    await g.hold('/api/fleet/entities');
    // Commit a filter the way a user does -- through the URL the form writes.
    await page.evaluate(() => { location.hash = '#/fleet/services?text=payments'; });
    await g.awaitHeld();

    // The heading, the chips and the URL already describe the NEW question. Rows from the
    // old one standing underneath them would be a lie with a visible timestamp.
    await expect(page.getByTestId('service-list')).toHaveCount(0);
    await expect(skeleton(page)).toBeVisible();

    await g.release();
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const after = await page.getByTestId('service-list').locator('li').count();
    expect(after).toBeLessThan(before);
    for (const t of await page.getByTestId('service-list').locator('li').allTextContents()) {
      expect(t.toLowerCase()).toContain('payment');
    }
  });

  test('paging Services mid-flight does not show page 1 under page 2', async ({ page }) => {
    await page.addInitScript(installBigServices);
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(25, { timeout: T });
    await expect(page.getByText(/Showing\s+1[–-]25\s+of\s+60/)).toBeVisible();
    expect(await page.getByTestId('service-list').locator('li').first().textContent()).toContain('service-000');

    const g = gate(page);
    await g.hold('/api/fleet/entities');
    await page.getByTestId('svc-next').click();
    await g.awaitHeld();

    await expect(page.getByTestId('service-list')).toHaveCount(0);
    await expect(skeleton(page)).toBeVisible();

    await g.release();
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(25, { timeout: T });
    await expect(page.getByText(/Showing\s+26[–-]50\s+of\s+60/)).toBeVisible();
    expect(await page.getByTestId('service-list').locator('li').first().textContent()).toContain('service-025');
  });

  test('an Attention filter committed mid-flight does not leave the previous backlog on screen', async ({ page }) => {
    await page.goto('/#/fleet/attention');
    await expect(page.getByTestId('attention-list')).toBeVisible({ timeout: T });
    const before = await page.getByTestId('attention-list').locator('li').count();
    expect(before).toBeGreaterThan(0);

    const g = gate(page);
    await g.hold('/api/fleet/attention');
    await page.getByLabel('Filter by severity').selectOption('error');
    await g.awaitHeld();

    await expect(page.getByTestId('attention-list')).toHaveCount(0);
    await expect(skeleton(page)).toBeVisible();

    await g.release();
    // The honest answer to the new question, whatever it is: rows for that severity, or a
    // filtered-empty state. What it may never be is the unfiltered backlog.
    await expect(skeleton(page)).toHaveCount(0, { timeout: T });
    const rows = page.getByTestId('attention-list').locator('li');
    if (await rows.count()) {
      for (const t of await rows.allTextContents()) expect(t).toContain('Error');
    } else {
      await expect(page.getByText('No matching attention items')).toBeVisible();
    }
  });

  test('an Owners search committed mid-flight does not leave the previous owners on screen', async ({ page }) => {
    await page.goto('/#/fleet/owners');
    await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: T });
    const before = await page.getByTestId('owner-list').locator('li').count();
    expect(before).toBeGreaterThan(1);
    const firstOwner = (await page.getByTestId('owner-list').locator('li').first().textContent())!.trim();

    const g = gate(page);
    await g.hold('/api/fleet/entities');
    await page.getByLabel('Search owners').fill(firstOwner.split(/\s+/)[0]);
    // The list's own Search submit, not the navbar's global search trigger.
    await page.locator('form[role="search"] button[type="submit"]').click();
    await g.awaitHeld();

    await expect(page.getByTestId('owner-list')).toHaveCount(0);
    await expect(skeleton(page)).toBeVisible();

    await g.release();
    await expect(skeleton(page)).toHaveCount(0, { timeout: T });
    expect(await page.getByTestId('owner-list').locator('li').count()).toBeLessThanOrEqual(before);
  });

  test('changing the scoped Revisions inventory mid-flight does not show one service under another', async ({ page }) => {
    await page.goto('/#/fleet/revisions?service=payments-service');
    await expect(page.getByTestId('entity-list')).toBeVisible({ timeout: T });
    for (const href of await page.getByTestId('entity-list').locator('a.entity-link').evaluateAll(
      (els) => els.map((e) => e.getAttribute('href') || ''),
    )) {
      expect(decodeURIComponent(href)).toContain('payments-service');
    }

    const g = gate(page);
    await g.hold('/api/fleet/entities');
    await page.evaluate(() => { location.hash = '#/fleet/revisions?service=orders-service'; });
    await g.awaitHeld();

    // The h1 already reads "Contract revisions of orders-service". Payments revisions
    // listed underneath it would be a different service's inventory, mislabelled.
    await expect(page.getByRole('heading', { level: 1, name: /orders-service/ })).toBeVisible();
    await expect(page.getByTestId('entity-list')).toHaveCount(0);
    await expect(skeleton(page)).toBeVisible();

    await g.release();
    await expect(page.getByTestId('entity-list')).toBeVisible({ timeout: T });
    for (const href of await page.getByTestId('entity-list').locator('a.entity-link').evaluateAll(
      (els) => els.map((e) => e.getAttribute('href') || ''),
    )) {
      expect(decodeURIComponent(href)).toContain('orders-service');
    }
  });

  test('the automatic poll re-asks the SAME question and keeps the rows and the place', async ({ page }) => {
    await page.addInitScript(installBigServices);
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(25, { timeout: T });

    // Scroll from inside the page, so the product sees the same scroll event a real
    // wheel produces.
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await page.waitForTimeout(150);
    const y = await page.evaluate(() => window.scrollY);
    expect(y).toBeGreaterThan(0);

    // Watch for the failure mode itself: a poll that empties the list.
    await page.evaluate(() => {
      const w = window as unknown as { __blanked: number; __obs?: MutationObserver };
      w.__blanked = 0;
      const main = document.querySelector('main');
      if (!main) return;
      w.__obs = new MutationObserver(() => {
        if (!main.querySelector('[data-testid="service-list"]')) w.__blanked++;
      });
      w.__obs.observe(main, { childList: true, subtree: true });
    });

    await gate(page).awaitPoll();
    await page.waitForTimeout(600);

    expect(await page.evaluate(() => (window as unknown as { __blanked: number }).__blanked)).toBe(0);
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(25);
    expect(Math.abs((await page.evaluate(() => window.scrollY)) - y)).toBeLessThanOrEqual(8);
  });

  test('a poll that FAILS keeps the rows and says the list could not be refreshed', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const before = await page.getByTestId('service-list').locator('li').count();

    const g = gate(page);
    await g.fail('/api/fleet/entities');
    await g.awaitPoll();

    // Rows survive -- good data must not decay into an error screen...
    await expect(page.getByTestId('stale-refresh')).toBeVisible({ timeout: T });
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(before);
    // ...and the failure is not swallowed either: a frozen list must not read as a live one.
    await expect(page.getByTestId('stale-refresh')).toContainText(/could not be refreshed/i);

    // Recovering clears the notice rather than requiring a reload.
    await g.fail(null);
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByTestId('stale-refresh')).toHaveCount(0, { timeout: T });
    await expect(page.getByTestId('service-list').locator('li')).toHaveCount(before);
  });

  test('an older in-flight answer never overwrites the newer one it lost the race to', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });

    const g = gate(page);
    await g.hold('/api/fleet/entities');
    await page.evaluate(() => { location.hash = '#/fleet/services?text=payments'; });
    await g.awaitHeld(1);
    await page.evaluate(() => { location.hash = '#/fleet/services?text=orders'; });
    await g.awaitHeld(2);

    // Newest first: the "orders" answer lands, and the older "payments" answer arrives
    // afterwards -- the exact ordering that used to clobber the current question.
    await g.release(true);

    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    await page.waitForTimeout(400); // give the loser time to try
    const texts = await page.getByTestId('service-list').locator('li').allTextContents();
    expect(texts.length).toBeGreaterThan(0);
    for (const t of texts) {
      expect(t.toLowerCase()).toContain('order');
      expect(t.toLowerCase()).not.toContain('payment');
    }
    await expect(page).toHaveURL(/text=orders/);
  });
});
