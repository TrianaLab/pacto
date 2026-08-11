import { test, expect, type Page } from '@playwright/test';
import { installFetchGate, gate } from './fetchGate';

/**
 * Owner identity, and the ownership picture above the owner roster — browser acceptance.
 *
 * Three claims that only a browser can settle, all of them about what the product does
 * with an owner NAME:
 *
 *   * a filter the product itself hands the reader (a suggestion, a per-owner ranking
 *     row) means THAT owner, while what the reader types stays a generous search. The
 *     demo fleet holds `platform-foundations` and `platform-foundations-security`, so
 *     one key is a prefix of the other and a substring filter physically cannot tell
 *     them apart;
 *   * the ownership summary is a SECOND question with a second fate: when only it
 *     fails, only it says so, and the owner roster underneath stays usable;
 *   * the bar's denominator is the backend's service count, so buckets that do not
 *     add up to the population leave a visible unclassified remainder rather than
 *     silently inflating every percentage.
 *
 * The first is a browser claim because the downgrade it guards against is an EVENT: a
 * picked suggestion is adopted into the input, and the browser then fires `change` when
 * that input loses focus. jsdom will not produce that sequence on its own.
 */

const T = 30_000;
const FOUNDATIONS = 'platform-foundations';
const SECURITY = 'platform-foundations-security';

const serviceLabels = (page: Page) =>
  page.locator('[data-testid="service-list"] .ei-label').allTextContents();
/** The one row of active-filter chips, named for assistive tech rather than by testid. */
const filterChips = (page: Page) => page.locator('[aria-label="Active filters"]');
const chipTexts = async (page: Page) =>
  (await filterChips(page).locator('.chip').allTextContents()).map((s) => s.replace(/\s+/g, ' ').trim());
/** Everything the attention list says, as one blob: a row names its entity in several
 *  places (label, summary, next step) and any of them mentioning the wrong owner's
 *  service is the failure. */
const attentionText = async (page: Page) =>
  (await page.locator('[data-testid="attention-list"] li').allInnerTexts());

/**
 * Rewrites the ownership aggregate the Owners page reads, so the buckets deliberately
 * fail to add up to the service population — which is what a backend that grows a
 * fourth ownership state looks like to a frontend that knows three. Only the
 * `limit=1` service query is touched: that is the aggregate-only request the page makes
 * for the summary, never the roster and never the Services list.
 */
function installSparseOwnership(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => ServeResult;
  let real: Serve | null = null;

  const wrapped: Serve = (method, path, body) => {
    const res = (real as Serve)(method, path, body);
    const [route, search] = path.split('?');
    const query = new URLSearchParams(search || '');
    if (route !== '/api/fleet/entities' || query.get('kinds') !== 'service' || query.get('limit') !== '1' || res.status !== 200) return res;
    let doc: { aggregate?: Record<string, unknown> };
    try { doc = JSON.parse(res.body); } catch { return res; }
    if (!doc.aggregate) return res;
    doc.aggregate.services = 10;
    doc.aggregate.ownership = { consistent: 5, conflicting: 2, unowned: 1 };
    return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
  };

  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

const aggregate = (page: Page) => page.getByTestId('owners-aggregate');
const legendRow = (page: Page, label: string) =>
  aggregate(page).locator('.dist-item').filter({ hasText: label });

test.describe('WASM demo — an owner name is either an identity or a search', () => {
  test('the fleet really does hold one owner key inside another', async ({ page }) => {
    await page.goto('/#/fleet/owners');
    await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: T });
    // Every test below is about telling two owners apart. If the demo ever stops
    // containing the pair, they would all pass by having nothing to confuse.
    const keys = await page.evaluate(async () => {
      const r = await (await fetch('/api/fleet/entities?kinds=owner&text=platform-foundations&limit=25')).json();
      return ((r.entities || []) as Array<{ key: string }>).map((e) => e.key);
    });
    expect(keys, 'the demo fleet no longer holds a prefix pair of owner keys').toEqual(
      expect.arrayContaining([FOUNDATIONS, SECURITY]),
    );
  });

  test('picking an owner suggestion filters to that owner, and typing still searches', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const box = page.getByTestId('svc-owner');

    // TYPED: the generous search, which is the whole point of a search box. It returns
    // the superset, and the security team's service is in it.
    await box.fill(FOUNDATIONS);
    await box.press('Enter');
    await expect(page).toHaveURL(new RegExp(`[?&]owner=${FOUNDATIONS}(&|$)`), { timeout: T });
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const fuzzy = await serviceLabels(page);
    expect(fuzzy, 'the search no longer reaches the other owner, so nothing here is being told apart')
      .toContain('audit-log');

    // PICKED: the same string, chosen from the backend's own owner inventory, is an
    // identity. The suggestion list offers both owners; the exact one is chosen.
    await box.fill(FOUNDATIONS);
    const option = page.getByTestId('svc-owner-option').filter({ has: page.getByText(FOUNDATIONS, { exact: true }) });
    await expect(option).toHaveCount(1, { timeout: T });
    await option.click();
    // The browser fires `change` on the input when it loses focus, carrying the label
    // the pick just adopted. That echo must not re-read the chosen owner as a substring.
    await page.getByTestId('svc-search').click();

    await expect(page).toHaveURL(new RegExp(`ownerKey=${FOUNDATIONS}(&|$)`), { timeout: T });
    await expect(page).not.toHaveURL(/[?&]owner=/);
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const exact = await serviceLabels(page);
    expect(exact.length).toBeGreaterThan(0);
    expect(exact, `"${FOUNDATIONS}" selected a service owned by ${SECURITY}`).not.toContain('audit-log');
    expect(fuzzy, 'the exact answer is not a subset of the search that contains it')
      .toEqual(expect.arrayContaining(exact));

    // And the two are labelled apart, so a reader can see which kind of filter they hold.
    expect(await chipTexts(page)).toEqual([`Owner: ${FOUNDATIONS} ×`]);
  });

  test('a ranking row lands on the owner it counted, and the backlog agrees', async ({ page }) => {
    await page.goto('/#/fleet/owners');
    await expect(aggregate(page).locator('.dist-legend')).toBeVisible({ timeout: T });
    await aggregate(page).getByText('Per-owner breakdown').click();

    // The row for the SHORTER key. Its neighbour in the same chart is the longer one, so
    // a row that asked its question as a substring would count one population and open
    // another -- which is the whole reason these links carry ownerKey.
    const row = aggregate(page).locator('a.hb-inner')
      .filter({ has: page.getByText(FOUNDATIONS, { exact: true }) }).first();
    await expect(row).toBeVisible({ timeout: T });
    const counted = Number((await row.locator('.hb-value').innerText()).split(' ')[0]);
    await row.click();

    await expect(page).toHaveURL(new RegExp(`ownerKey=${FOUNDATIONS}(&|$)`), { timeout: T });
    await expect(page).not.toHaveURL(/[?&]owner=/);
    // The destination says which owner it is scoped to, in the exact-filter chip.
    await expect(filterChips(page)).toContainText(`Owner: ${FOUNDATIONS}`, { timeout: T });
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });

    // The list the row opened is the list the row counted, and nothing in it belongs to
    // the other owner.
    const scoped = await serviceLabels(page);
    expect(scoped, 'the ranking row counted a different population than it opened').toHaveLength(counted);
    expect(scoped).not.toContain('audit-log');

    // The backlog workspace is a second filter with the same two answers, and it is the
    // one an owner actually works from. Exact first...
    await page.goto(`/#/fleet/attention?ownerKey=${FOUNDATIONS}`);
    await expect(filterChips(page)).toContainText(`Owner: ${FOUNDATIONS}`, { timeout: T });
    await expect(page.getByTestId('attention-list')).toBeVisible({ timeout: T });
    const backlog = await attentionText(page);
    expect(backlog.length).toBeGreaterThan(0);
    expect(backlog.join('\n')).not.toContain('audit-log');

    // ...then the SEARCH, which is what the mistake would have looked like: strictly
    // more rows, one of them the other owner's.
    await page.goto(`/#/fleet/attention?owner=${FOUNDATIONS}`);
    await expect(filterChips(page)).toContainText(`Owner search: ${FOUNDATIONS}`, { timeout: T });
    await expect.poll(async () => (await attentionText(page)).join('\n'), { timeout: T }).toContain('audit-log');
    const searched = await attentionText(page);
    expect(searched.length).toBeGreaterThan(backlog.length);
  });
});

test.describe('WASM demo — the ownership summary is its own question', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(installFetchGate);
  });

  test('when only the summary fails, only the summary says so', async ({ page }) => {
    // Failing before the first paint: the aggregate request never lands, the roster's
    // does. Both are /api/fleet/entities, and only the service-scoped one is failed.
    await page.addInitScript(() => {
      (window as unknown as { __gate: { fail: string | null } }).__gate.fail = 'kinds=service';
    });
    await page.goto('/#/fleet/owners');

    await expect(aggregate(page)).toContainText(/Can.t reach the Pacto backend/, { timeout: T });
    // The roster is a different request with a different answer, and it arrived.
    await expect(page.getByTestId('owner-list').locator('li').first()).toBeVisible({ timeout: T });
    expect(await page.getByTestId('owner-list').locator('li').count()).toBeGreaterThan(1);
    // Nothing is drawn from an aggregate the page does not have.
    await expect(aggregate(page).locator('.dist-legend')).toHaveCount(0);

    // Retrying the summary retries the SUMMARY.
    await gate(page).fail(null);
    await aggregate(page).getByRole('button', { name: 'Retry' }).click();
    await expect(aggregate(page).locator('.dist-legend')).toBeVisible({ timeout: T });
    await expect(aggregate(page)).not.toContainText(/Can.t reach the Pacto backend/);
  });

  test('a summary refresh that fails keeps the picture and says it is not live', async ({ page }) => {
    await page.goto('/#/fleet/owners');
    await expect(aggregate(page).locator('.dist-legend')).toBeVisible({ timeout: T });
    const before = await aggregate(page).locator('.dist-item').allTextContents();

    const g = gate(page);
    await g.fail('kinds=service');
    await g.awaitPoll();

    // The picture survives -- a poll that could not reach the backend must not erase
    // an answer we already have...
    const stale = aggregate(page).getByTestId('stale-refresh');
    await expect(stale).toBeVisible({ timeout: T });
    expect(await aggregate(page).locator('.dist-item').allTextContents()).toEqual(before);
    // ...and it is not swallowed either.
    await expect(stale).toContainText(/ownership summary could not be refreshed/i);
    // The roster refreshed fine, so it says nothing.
    await expect(page.getByTestId('owner-list')).toBeVisible();

    await g.fail(null);
    await stale.getByRole('button', { name: 'Try again' }).click();
    await expect(aggregate(page).getByTestId('stale-refresh')).toHaveCount(0, { timeout: T });
    expect(await aggregate(page).locator('.dist-item').allTextContents()).toEqual(before);
  });

  test('the bar is drawn against the service population, not against its own buckets', async ({ page }) => {
    await page.addInitScript(installSparseOwnership);
    await page.goto('/#/fleet/owners');
    await expect(aggregate(page).locator('.dist-legend')).toBeVisible({ timeout: T });

    // 5 + 2 + 1 of 10 services: the two the buckets do not account for are shown as
    // unclassified, and every percentage is stated over 10 rather than over 8.
    await expect(legendRow(page, 'Unclassified')).toContainText('(20% of 10)');
    await expect(legendRow(page, 'One declared owner')).toContainText('(50% of 10)');
    await expect(aggregate(page)).toContainText('All 10 services in the snapshot');
  });
});
