import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { appReady, settle } from './ready';
import { revisionGraphUrl } from './graphFocus';

// Browser acceptance for "what if" answers in Change analysis (#471): a service becomes
// unavailable, is removed or regressed, and a policy change. Every assertion reads what
// the WASM demo answers for its known subjects (redis has required and optional
// dependents, postgresql is an offering with provisioning reports, every policy
// reference to platform-http-policy is unpinned), never a literal count of one publish.

const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const FIGURE = '[data-testid="impact-figure"]';
const T = 30_000;

// The lanes the figure draws, by name, with the count each prints.
const lanes = (page: Page) => page.locator(`${FIGURE} .lane:not(.ghost)`).evaluateAll((ls) => ls.map((l) => ({
  name: l.querySelector('.lane-name')?.textContent?.trim() ?? '',
  n: Number(l.querySelector('.lane-count b')?.textContent?.replace(/[^\d]/g, '') ?? 0),
})));

async function answered(page: Page) {
  await expect(page.locator(`${FIGURE} svg[role="img"]`)).toBeAttached({ timeout: T });
  await expect(page.getByTestId('changes-answer')).toBeVisible({ timeout: T });
}

async function openConsumers(page: Page) {
  const fold = page.locator('#sec-affected-consumers');
  if ((await fold.getAttribute('open')) === null) await fold.locator('> summary').click();
  await expect(page.getByTestId('consumer-row').first()).toBeVisible({ timeout: T });
}

async function chooseRevision(page: Page, sel: string, version: string) {
  const read = () => page.locator(`${sel} option`).evaluateAll(
    (os, v) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${v} `))?.value ?? '', version);
  await expect.poll(read, { message: `no ${version} revision in ${sel}`, timeout: 20_000 }).not.toBe('');
  await page.locator(sel).selectOption(await read());
}

test.describe('impact events', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the service page asks what happens if redis becomes unavailable', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/services/redis');
    await page.getByRole('link', { name: 'What if it becomes unavailable?' }).click();
    await expect(page).toHaveURL((u) => u.hash === '#/fleet/changes/redis?event=unavailable', { timeout: T });
    await answered(page);
    await expect(page.locator(FIGURE)).toContainText('If redis becomes unavailable, who breaks?');
  });

  test('the graph drawer asks what happens if the selected service becomes unavailable', async ({ page }) => {
    // A service focus is the fleet map; a fraud-service revision keeps the neighbourhood, with redis in it.
    await page.goto(await revisionGraphUrl(page, 'fraud-service'));
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: T });
    await page.getByTestId('graph-textalt').locator('summary').click();
    await page.getByTestId('graph-node-item').filter({ hasText: /^.*redis/ }).first().click();
    const link = page.getByTestId('graph-drawer').getByTestId('drawer-what-if');
    await expect(link).toBeVisible();
    const href = (await link.getAttribute('href')) ?? '';
    expect(href).toMatch(/^#\/fleet\/changes\/[^?]+\?event=unavailable$/);
    await link.click();
    await answered(page);
    await expect(page.getByTestId('changes-answer')).toContainText('Unavailable');
  });

  test('redis unavailable: Breaks and May degrade lanes, never blocked, and why each consumer is affected', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes/redis?mode=event');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.locator('#impact-event')).toHaveValue('unavailable', { timeout: T });
    await page.getByRole('button', { name: 'Ask' }).click();
    await answered(page);
    // Every effect lane is drawn (an empty one says so); redis fills both.
    const ls = await lanes(page);
    const n = (name: string) => ls.find((l) => l.name === name)?.n ?? -1;
    expect(ls.map((l) => l.name)).toEqual(['Breaks', 'May degrade', 'Unknown']);
    expect(n('Breaks'), JSON.stringify(ls)).toBeGreaterThan(0);
    expect(n('May degrade'), JSON.stringify(ls)).toBeGreaterThan(0);
    // The lanes partition the consumers: their counts add up to the total the table pages.
    await openConsumers(page);
    const total = Number(await page.locator('.consumer-pager').getAttribute('data-view-total'));
    expect(ls.reduce((a, l) => a + l.n, 0), JSON.stringify(ls)).toBe(total);
    // An event has no version to refuse, so its gate is a person or clear, never blocked.
    await expect(page.locator(`${FIGURE} .stamp`, { hasText: 'Blocked' })).toHaveCount(0);
    await expect(page.getByTestId('impact-gate')).toContainText(/Needs a person|Clears/);
    // Effect and Why: the effect badge, the edge it came through and where that is known from.
    const table = page.locator('#sec-affected-consumers table');
    await expect(table.getByRole('columnheader', { name: 'Effect', exact: true })).toBeVisible();
    await expect(table.getByRole('columnheader', { name: /^Why/ })).toBeVisible();
    const row = (name: string) => page.getByTestId('consumer-row')
      .filter({ has: page.locator('td[data-label="Consumer"]').getByRole('link', { name: `Service ${name}`, exact: true }) });
    const pricing = row('pricing-service');
    await expect(pricing.locator('td[data-label="Effect"]')).toContainText('May degrade');
    await expect(pricing.locator('td[data-label="Why"]')).toContainText('optional');
    const auth = row('auth-service');
    await expect(auth.locator('td[data-label="Effect"]')).toContainText('Breaks');
    await expect(auth.locator('td[data-label="Why"]')).toContainText('required');
    await expect(auth.locator('td[data-label="Why"]')).toContainText(/declared|observed|inferred/);
    await expect(auth.locator('td[data-label="Why"] .badge').first()).toContainText(/dependency|config reference|observed call/);
    await settle(page);
    const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
    expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))).toEqual([]);
    // An owner link opens an owner page that renders its estate, not the not-found state.
    const owner = auth.locator('td[data-label="Owner"] a').first();
    await expect(owner).toBeVisible();
    await owner.click();
    await expect(page).toHaveURL(/#\/fleet\/owners\//, { timeout: T });
    await expect(page.locator('[data-testid="preview-section"][data-toc="Services"]')).toBeVisible({ timeout: T });
    await expect(page.locator('main')).toContainText('auth-service');
  });

  test('payments-service unavailable has no optional dependents, so its May degrade lane is empty', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes/payments-service?event=unavailable');
    await answered(page);
    const ls = await lanes(page);
    expect(ls.find((l) => l.name === 'Breaks')?.n, JSON.stringify(ls)).toBeGreaterThan(0);
    expect(ls.find((l) => l.name === 'May degrade')?.n, JSON.stringify(ls)).toBe(0);
  });

  test('a leaf service lists no consumer, never blocks and never says nobody is affected', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes/storefront-web?event=unavailable');
    await answered(page);
    expect((await lanes(page)).every((l) => l.n === 0)).toBe(true);
    await expect(page.locator(`${FIGURE} .stamp`, { hasText: 'Blocked' })).toHaveCount(0);
    await expect(page.getByTestId('impact-gate')).toContainText(/Needs a person|Clears/);
    await expect(page.getByTestId('changes-answer')).toContainText(/No consumer (you can see )?is known to depend on it/);
    await expect(page.getByTestId('changes-answer')).not.toContainText('No consumer is affected');
    const fold = page.locator('#sec-affected-consumers');
    if ((await fold.getAttribute('open')) === null) await fold.locator('> summary').click();
    await expect(fold).toContainText('Nothing in the operational graph depends on this service.');
  });

  test('postgresql removed names the resources each consumer uses, with their phase', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes/postgresql?event=removed');
    await answered(page);
    await expect(page.getByTestId('changes-answer')).toContainText('Removed');
    await openConsumers(page);
    const resources = page.locator('#sec-affected-consumers .why-resources li');
    await expect(resources.first()).toBeVisible({ timeout: T });
    await expect(resources.first().locator('.badge')).toHaveText(/\S/);
  });

  test('a policy change with unpinned references is partial and names no referrer', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes/platform-http-policy');
    await chooseRevision(page, '#impact-old-rev', '1.0.0');
    await chooseRevision(page, '#impact-new-rev', '1.0.1');
    await page.getByRole('button', { name: /Compare revisions/ }).click();
    await answered(page);
    const lims = page.getByTestId('changes-limitations');
    await expect(lims).toBeVisible({ timeout: T });
    await lims.locator('> summary').click();
    await expect(lims).toContainText('POLICY_REF_CONSUMERS_UNRESOLVED');
    await expect(page.getByTestId('changes-answer')).toContainText(/partial/i);
    // An unresolved referrer is never taken as safe: the non-breaking change needs a person.
    await expect(page.getByTestId('changes-gate')).toContainText('waits for a person');
    await expect(page.locator('#sec-affected-consumers .badge', { hasText: 'policy reference' })).toHaveCount(0);
  });

  test('a regression named by version says delivery evidence was not read', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes/payments-service?event=regressed&rev=2.0.1');
    await answered(page);
    await expect(page.locator('#impact-event-rev')).toHaveValue('2.0.1');
    const ev = page.getByTestId('changes-delivery-evidence');
    await expect(ev).toBeVisible();
    // The demo has no delivery host for the fleet, and a version names no artifact: the
    // section says so, and never "no failure recorded".
    await expect(ev).toContainText('DELIVERY_EVIDENCE_UNAVAILABLE');
    await expect(ev).not.toContainText('recorded no failure');
  });

  test('stripe changed: every consumer needs attention, the gate is unverified and the pin is offered', async ({ page }) => {
    // examples/demo/source_inferred.go: stripe's inferred contract moved its linked
    // OpenAPI digest from 0.0.1 to 0.0.2, and no source checked either signature.
    await appReady(page);
    await page.goto('/#/fleet/changes?external=stripe&event=changed');
    await page.addStyleTag({ content: WIDE_FONT });
    await answered(page);
    await expect(page.locator('#impact-event')).toHaveValue('changed');
    // Only changed and unavailable are asked of an external system.
    expect(await page.locator('#impact-event option').evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value))).toEqual(['changed', 'unavailable']);
    const ls = await lanes(page);
    const att = ls.find((l) => l.name === 'Attention')?.n ?? 0;
    expect(att, JSON.stringify(ls)).toBeGreaterThan(0);
    expect(ls.filter((l) => l.name !== 'Attention').every((l) => l.n === 0), JSON.stringify(ls)).toBe(true);
    await openConsumers(page);
    expect(Number(await page.locator('.consumer-pager').getAttribute('data-view-total'))).toBe(att);
    // An external is never verified: the gate waits for a person, never clears or blocks.
    await expect(page.locator(`${FIGURE} .stamp`, { hasText: /Blocked|Clears/ })).toHaveCount(0);
    await expect(page.getByTestId('impact-gate')).toContainText('Needs a person');
    const change = page.getByTestId('changes-external-change');
    await expect(change).toContainText('Inferred contract 0.0.1 → 0.0.2');
    await expect(change).toContainText(/OpenAPI sha256:\S+ → sha256:\S+/);
    await expect(change).toContainText('unchecked: no source checked these revisions');
    // Each demo stripe consumer is named by an inferred source only, so its row is muted;
    // newsletter-service's manifest names the stripe client major.
    await expect(page.getByTestId('consumer-row').first()).toHaveClass(/row-muted/);
    await expect(page.getByTestId('consumer-client')).toContainText(/stripe v\d+/);
    const pin = page.getByTestId('changes-pin');
    await expect(pin).not.toHaveAttribute('open', '');
    await pin.locator('summary').click();
    await expect(pin.locator('pre')).toContainText('ref: oci://registry.pacto.demo/demo/pacto-inferred/stripe');
    await expect(pin.locator('pre')).toContainText('compatibility: "unknown"');
    await settle(page);
    const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
    expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))).toEqual([]);
  });

  test('stripe unavailable keeps the event lanes', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes?external=stripe&event=unavailable');
    await answered(page);
    const ls = await lanes(page);
    expect(ls.map((l) => l.name)).toEqual(['Breaks', 'May degrade', 'Unknown']);
    // Neither demo consumer declares stripe, so an outage's effect on them is unknown.
    expect(ls.find((l) => l.name === 'Unknown')?.n, JSON.stringify(ls)).toBeGreaterThan(0);
    await expect(page.locator(`${FIGURE} .stamp`, { hasText: /Blocked|Clears/ })).toHaveCount(0);
    await expect(page.getByTestId('impact-gate')).toContainText('Needs a person');
  });

  test('stripe removed is not a question about an external system and is never asked', async ({ page }) => {
    await appReady(page);
    await page.goto('/#/fleet/changes?external=stripe&event=removed');
    await expect(page.getByTestId('changes-event-not-external')).toBeVisible({ timeout: T });
    await settle(page);
    // Nothing was asked: no answer is drawn.
    await expect(page.locator(FIGURE)).toHaveCount(0);
    await expect(page.getByTestId('changes-answer')).toHaveCount(0);
    const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
    expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))).toEqual([]);
  });

  test('a restored event link shows the final frame without playing', async ({ page }) => {
    await page.addInitScript(() => sessionStorage.clear());
    await appReady(page);
    await page.goto('/#/fleet/changes/redis?event=unavailable');
    await answered(page);
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => document.getAnimations()
      .filter((a) => String(a.id).startsWith('narr:') && a.playState !== 'finished').length)).toBe(0);
    expect(await page.evaluate((sel) => [...document.querySelectorAll(`${sel} .mk`)]
      .filter((m) => m.getClientRects().length)
      .every((m) => { const cs = getComputedStyle(m); return cs.opacity === '1' && cs.transform === 'none'; }), FIGURE)).toBe(true);
  });
});
