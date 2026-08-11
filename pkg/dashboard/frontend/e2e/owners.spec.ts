import { test, expect, type Page } from '@playwright/test';
import { installFetchGate, gate } from './fetchGate';

/**
 * Owner identity, and the ownership picture above the owner roster — browser acceptance.
 *
 * Four claims that only a browser can settle, all of them about what the product does
 * with an owner NAME:
 *
 *   * a filter the product itself hands the reader (a suggestion, a per-owner ranking
 *     row) means THAT owner, while what the reader types stays a generous search. The
 *     demo fleet holds `platform-foundations` and `platform-foundations-security`, so
 *     one key is a prefix of the other and a substring filter physically cannot tell
 *     them apart;
 *   * a name is not an identity. The demo fleet also holds a TEAM and a DRI both called
 *     `partner-integrations`, which are two owners with two estates, and one team whose
 *     services name two different DRIs, which is one owner. Every link, filter and
 *     ranking row travels on the canonical `kind:name` key, so neither pair can be
 *     merged or split by how it is spelled on screen;
 *   * the ownership summary is a SECOND question with a second fate: when only it
 *     fails, only it says so, and the owner roster underneath stays usable;
 *   * the bar's denominator is the backend's service count, in BOTH directions: buckets
 *     that fall short leave a visible unclassified remainder, and buckets that overshoot
 *     say so out loud rather than quietly widening the denominator to fit.
 *
 * The first is a browser claim because the downgrade it guards against is an EVENT: a
 * picked suggestion is adopted into the input, and the browser then fires `change` when
 * that input loses focus. jsdom will not produce that sequence on its own.
 */

const T = 30_000;
const FOUNDATIONS = 'platform-foundations';
const SECURITY = 'platform-foundations-security';
/** The one name the demo fleet spells as two owners: a team and a DRI. */
const PARTNERS = 'partner-integrations';
/** A canonical owner key, as the browser writes it into a URL it builds itself. */
const k = (key: string) => encodeURIComponent(key);
/** `ownerKey=` carrying exactly this identity and nothing appended to it. */
const exactly = (key: string) => new RegExp(`[?&]ownerKey=${k(key)}(&|$)`);

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

/**
 * Rewrites the same aggregate the other way: the buckets add up to MORE than the
 * population, which is what double-counting looks like from the frontend. A bar that
 * quietly renormalised would render this as a tidy 100% and hide the defect.
 */
function installOverCount(): void {
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
    doc.aggregate.services = 8;
    doc.aggregate.ownership = { consistent: 6, conflicting: 3, unowned: 1 };
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
      expect.arrayContaining([`team:${FOUNDATIONS}`, `team:${SECURITY}`]),
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

    await expect(page).toHaveURL(exactly(`team:${FOUNDATIONS}`), { timeout: T });
    await expect(page).not.toHaveURL(/[?&]owner=/);
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    const exact = await serviceLabels(page);
    expect(exact.length).toBeGreaterThan(0);
    expect(exact, `"${FOUNDATIONS}" selected a service owned by ${SECURITY}`).not.toContain('audit-log');
    expect(fuzzy, 'the exact answer is not a subset of the search that contains it')
      .toEqual(expect.arrayContaining(exact));

    // And the two are labelled apart, so a reader can see which kind of filter they
    // hold — the exact one naming the namespace it resolved to, never the wire key.
    expect(await chipTexts(page)).toEqual([`Owner: ${FOUNDATIONS} (Team) ×`]);
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

    await expect(page).toHaveURL(exactly(`team:${FOUNDATIONS}`), { timeout: T });
    await expect(page).not.toHaveURL(/[?&]owner=/);
    // The destination says which owner it is scoped to, in the exact-filter chip.
    await expect(filterChips(page)).toContainText(`Owner: ${FOUNDATIONS} (Team)`, { timeout: T });
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });

    // The list the row opened is the list the row counted, and nothing in it belongs to
    // the other owner.
    const scoped = await serviceLabels(page);
    expect(scoped, 'the ranking row counted a different population than it opened').toHaveLength(counted);
    expect(scoped).not.toContain('audit-log');

    // The backlog workspace is a second filter with the same two answers, and it is the
    // one an owner actually works from. Exact first...
    await page.goto(`/#/fleet/attention?ownerKey=${k(`team:${FOUNDATIONS}`)}`);
    await expect(filterChips(page)).toContainText(`Owner: ${FOUNDATIONS} (Team)`, { timeout: T });
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

test.describe('WASM demo — an owner label is not an owner identity', () => {
  /** The aggregate the Owners page itself reads, straight from the backend. */
  const ownership = (page: Page) => page.evaluate(async () => {
    const r = await (await fetch('/api/fleet/entities?kinds=service&limit=1')).json();
    return r.aggregate as {
      services: number;
      ownership: { consistent: number; conflicting: number; unowned: number };
      byOwner: Array<{ key: string; label: string; kind: string; services: number }>;
      beyondRanking?: number; unidentifiedOwnership?: number;
      rankedOwners: number; distinctOwners: number;
    };
  });

  /** The per-owner SERVICE ranking. The breakdown holds a second chart for targets. */
  const ranking = (page: Page) => aggregate(page).locator('.hbars').filter({ hasText: 'Services per owner' });

  const openBreakdown = async (page: Page) => {
    await page.goto('/#/fleet/owners');
    await expect(aggregate(page).locator('.dist-legend')).toBeVisible({ timeout: T });
    await aggregate(page).getByText('Per-owner breakdown').click();
    await expect(ranking(page)).toBeVisible({ timeout: T });
  };

  test('the fleet really does spell one name as two different owners', async ({ page }) => {
    await page.goto('/#/fleet/owners');
    await expect(page.getByTestId('owner-list')).toBeVisible({ timeout: T });
    // Same guard as the prefix pair: if the demo ever loses the collision, every test
    // below would pass by having nothing left to tell apart.
    const keys = await page.evaluate(async (name) => {
      const r = await (await fetch(`/api/fleet/entities?kinds=owner&text=${name}&limit=25`)).json();
      return ((r.entities || []) as Array<{ key: string }>).map((e) => e.key);
    }, PARTNERS);
    expect(keys, 'the demo fleet no longer holds a team and a DRI with one name').toEqual(
      expect.arrayContaining([`team:${PARTNERS}`, `dri:${PARTNERS}`]),
    );

    // And the roster shows them as two rows, told apart by the only thing that differs.
    const rows = page.locator('[data-testid="owner-list"] li').filter({ hasText: PARTNERS });
    await expect(rows).toHaveCount(2);
    const hrefs = await rows.locator('a').evaluateAll((els) =>
      els.map((e) => (e as HTMLAnchorElement).getAttribute('href') || ''));
    expect(new Set(hrefs).size, 'both rows link to the same page, so they are one owner on screen').toBe(2);
    expect(hrefs.some((h) => decodeURIComponent(h).endsWith(`/team:${PARTNERS}`))).toBe(true);
    expect(hrefs.some((h) => decodeURIComponent(h).endsWith(`/dri:${PARTNERS}`))).toBe(true);
  });

  test('each same-named owner opens its own estate, and neither inherits the other', async ({ page }) => {
    for (const [kind, service] of [['team', 'platform-app-config'], ['dri', 'settlement-service']] as const) {
      await page.goto(`/#/fleet/owners/${k(`${kind}:${PARTNERS}`)}`);
      // The page says which of the two it is, above the estate it is about to list.
      const kindLabel = kind === 'team' ? 'Team' : 'DRI';
      await expect(page.locator('main')).toContainText(PARTNERS, { timeout: T });
      await expect(page.locator('main')).toContainText(kindLabel);
      const services = page.locator('[data-testid="preview-section"][data-toc="Services"]');
      await expect(services).toBeVisible({ timeout: T });
      await expect(services.locator('.ei-label')).toHaveText([service]);
    }
  });

  test('a canonical owner filter keeps them apart, and a typed search still finds both', async ({ page }) => {
    // EXACT, twice: each identity answers with its own service and no more.
    for (const [kind, service] of [['team', 'platform-app-config'], ['dri', 'settlement-service']] as const) {
      await page.goto(`/#/fleet/services?ownerKey=${k(`${kind}:${PARTNERS}`)}`);
      await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
      await expect.poll(() => serviceLabels(page), { timeout: T }).toEqual([service]);
      // The chip names the namespace, because the label alone cannot say which owner.
      expect(await chipTexts(page)).toEqual([`Owner: ${PARTNERS} (${kind === 'team' ? 'Team' : 'DRI'}) ×`]);
    }

    // SEARCH: a reader who types the name has not chosen a namespace, and should not
    // silently be given one. Both estates come back.
    await page.goto(`/#/fleet/services?owner=${PARTNERS}`);
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    await expect.poll(() => serviceLabels(page), { timeout: T })
      .toEqual(expect.arrayContaining(['platform-app-config', 'settlement-service']));
  });

  test('one team whose services name different DRIs is still one owner', async ({ page }) => {
    await openBreakdown(page);
    // infra-data owns postgresql and redis; the two declarations differ in DRI and in
    // contacts. A ranking that identified owners by the whole declaration would show
    // this team twice, with one service each.
    const rows = ranking(page).locator('a.hb-inner').filter({ hasText: 'infra-data' });
    await expect(rows).toHaveCount(1, { timeout: T });
    await expect(rows).toContainText('2 services');
    await rows.click();

    await expect(page).toHaveURL(exactly('team:infra-data'), { timeout: T });
    await expect(page.getByTestId('service-list')).toBeVisible({ timeout: T });
    await expect.poll(() => serviceLabels(page), { timeout: T }).toEqual(['postgresql', 'redis']);
  });

  test('ownership that names nobody is still counted, and said out loud', async ({ page }) => {
    await openBreakdown(page);
    const agg = await ownership(page);
    const unidentified = agg.unidentifiedOwnership || 0;
    expect(unidentified, 'the demo fleet no longer holds a contacts-only owner declaration')
      .toBeGreaterThan(0);

    // It is its own sentence in its own node — not folded into the ranking's tail,
    // which is about a different population (owners the top-N bound left out).
    const note = aggregate(page).getByTestId('owners-unidentified');
    await expect(note).toContainText(`${unidentified} service`);
    await expect(note).toContainText('names no team or DRI');
    await expect(ranking(page).locator('.hb-scope')).not.toContainText('names no team or DRI');

    // And the three populations reconcile against the consistent bucket exactly: what
    // the rows hold, what the bound left out, and what has no identity to hold.
    const shown = agg.byOwner.reduce((n, o) => n + o.services, 0);
    expect(shown + (agg.beyondRanking || 0) + unidentified).toBe(agg.ownership.consistent);
  });

  test('owners named only by disagreeing revisions never become a ranking total', async ({ page }) => {
    await openBreakdown(page);
    const agg = await ownership(page);
    // The demo's older payments/commerce/platform revisions name a different team than
    // the newest one, so those owners exist but no service is consistently theirs.
    expect(agg.distinctOwners, 'the demo fleet no longer holds a disputed-only owner')
      .toBeGreaterThan(agg.rankedOwners);
    const disputed = agg.distinctOwners - agg.rankedOwners;

    const scope = ranking(page).locator('.hb-scope');
    // The count of rows is stated over the rankable population, and the larger "named"
    // population is stated separately with the reason the difference exists.
    await expect(scope).toContainText(`of ${agg.rankedOwners} rankable owner`);
    await expect(scope).toContainText(`out of ${agg.distinctOwners} named`);
    await expect(scope).toContainText(`the other ${disputed}`);
    await expect(scope).toContainText('revisions disagree');
    // No row claims them: the ranking holds only owners with a consistent service.
    await expect(ranking(page).locator('a.hb-inner').filter({ hasText: 'team/payments' })).toHaveCount(0);
    // But the roster still names them, so they are reachable rather than erased.
    await expect(page.locator('[data-testid="owner-list"] li').filter({ hasText: 'team/payments' }))
      .toHaveCount(1, { timeout: T });
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

  test('buckets that overshoot the population say so instead of renormalising', async ({ page }) => {
    await page.addInitScript(installOverCount);
    await page.goto('/#/fleet/owners');
    await expect(aggregate(page).locator('.dist-legend')).toBeVisible({ timeout: T });

    // 6 + 3 + 1 buckets over 8 services. The denominator stays the population the
    // backend reported, so the notice is the only honest thing to draw.
    const warn = aggregate(page).getByTestId('dist-inconsistent');
    await expect(warn).toContainText('account for 10 across a population of 8');
    await expect(warn).toContainText('2 more than there are');
    await expect(warn).toHaveAttribute('role', 'status');

    // Every percentage is still a share of 8, and they total more than 100% — the
    // symptom is left visible rather than divided away.
    await expect(legendRow(page, 'One declared owner')).toContainText('(75% of 8)');
    await expect(legendRow(page, 'Revisions name different owners')).toContainText('(37.5% of 8)');
    // And nothing is invented to make the sum land: there is no missing remainder here.
    await expect(legendRow(page, 'Unclassified')).toHaveCount(0);
  });
});
