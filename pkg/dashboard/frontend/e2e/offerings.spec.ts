import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { settle } from './ready';

// Platform offerings against the built WASM demo, at 1280px with the wide fallback
// font CI renders with: the list, an offering's providers and consume snippet, and
// the offerings a service uses. postgresql has two providers with two owners:
// postgresql, whose 16.1.0 is deprecated and names the 17.0.0 the demo ships, and
// postgresql-ha, which ticketing-service 1.2.0 cannot reach in staging.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';
const noSideScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.body].every((e) => e.scrollWidth <= e.clientWidth + 1));
const PG_ROW = 'a.entity-link[href$="/fleet/offerings/postgresql"]';

async function audit(page: Page, what: string) {
  await settle(page);
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(r.violations, `${what}: ${JSON.stringify(r.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })))}`).toEqual([]);
}

test.describe('WASM demo — platform offerings', () => {
  test('the Offerings item opens the list and the page never scrolls sideways', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet');
    await page.addStyleTag({ content: WIDE_FONT });
    const nav = page.getByRole('navigation', { name: 'Primary' });
    const tab = nav.getByRole('link', { name: 'Offerings' });
    await tab.scrollIntoViewIfNeeded();
    await tab.click();
    await expect(page).toHaveURL(/#\/fleet\/offerings$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Platform offerings' })).toBeVisible({ timeout: T });
    await expect(tab).toHaveAttribute('aria-current', 'page');
    // The bar's controls stay on screen beside the sidebar.
    await expect(page.getByRole('button', { name: 'Toggle theme' })).toBeInViewport();
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the demo lists its five offerings and postgresql and shared-config have two providers', async ({ page }) => {
    await page.goto('/#/fleet/offerings');
    const list = page.getByTestId('offering-list');
    await expect(list).toBeVisible({ timeout: T });
    const rows = list.locator('tbody tr');
    await expect(rows).toHaveCount(5);
    for (const name of ['messaging', 'postgresql', 'redis', 'shared-config', 'shared-policy']) {
      await expect(list.locator(`a.entity-link[href$="/fleet/offerings/${name}"]`)).toBeVisible();
    }
    await expect(rows.filter({ has: page.locator('a.entity-link[href$="/fleet/offerings/shared-config"]') })).toContainText('2 providers');
    await expect(rows.filter({ has: page.locator(PG_ROW) })).toContainText('2 providers');
    await expect(list.locator('thead')).toContainText('Observed targets');
    await expect(page.locator('main')).not.toContainText('operational target');
  });

  test('the list draws where offering requests stand and narrows to the offering with a failed request', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto('/#/fleet/offerings');
    await page.addStyleTag({ content: WIDE_FONT });
    const fig = page.getByTestId('offerings-figure');
    await expect(fig).toBeVisible({ timeout: T });
    await expect(fig).toContainText('Are the requests for platform offerings ready?');
    await expect(fig).toContainText(/across 5 offerings/);
    // The Failed station and the offering it names both open the failed-request list.
    await expect(fig.locator('a[data-lists="1"]')).toHaveAttribute('href', /[?&]failed=1/);
    await expect(fig.locator(`a.chip[href$="/fleet/offerings/postgresql"]`)).toBeVisible();
    const pg = page.getByTestId('offering-list').locator('tbody tr').filter({ has: page.locator(PG_ROW) });
    // Two teams maintain a provider each, and the newest postgresql, 17.0.0, declares no lifecycle.
    await expect(pg.locator('[data-label="Owners"]')).toHaveText('2 owners claim it');
    await expect(pg.locator('[data-label="Lifecycle"]')).toHaveText('supported');
    // production is available and staging unknown: the row says how Pacto knows.
    await expect(pg.locator('[data-label="Availability"]')).toContainText('observed');
    await expect(pg.locator('[data-label="Failed"]')).toHaveText('1');
    await expect(pg.locator('[data-label="Observed targets"]')).not.toHaveText('0');
    await fig.locator('a[data-lists="1"]').click();
    await expect(page).toHaveURL(/failed=1/);
    await expect(page.getByTestId('offering-list').locator('tbody tr')).toHaveCount(1, { timeout: T });
    await expect(page.getByTestId('offering-list').locator(PG_ROW)).toBeVisible();
    await expect(page.getByTestId('offering-failed-filter')).toBeChecked();
    await expect(page.locator('.filter-chips')).toContainText('a failed request');
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'offerings list, failed filter');
  });

  test('no demo offering is deprecated: the lifecycle filter narrows to none', async ({ page }) => {
    await page.goto('/#/fleet/offerings');
    await expect(page.getByTestId('offering-list')).toBeVisible({ timeout: T });
    await page.getByLabel('Lifecycle').selectOption('deprecated');
    await expect(page).toHaveURL(/#\/fleet\/offerings\?lifecycle=deprecated$/);
    await expect(page.locator('.filter-chips')).toContainText('Deprecated');
    await expect(page.getByTestId('offering-list')).toHaveCount(0, { timeout: T });
  });

  test('the postgresql page shows its providers, a consume snippet and its consumers', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByTestId('offering-provider')).toHaveCount(2, { timeout: T });
    const provider = page.getByTestId('offering-provider').filter({ hasText: 'infra-data' });
    await expect(provider).toContainText('Primary relational database for transactional data');
    const snippet = provider.getByTestId('consume').locator('pre.consume-snippet');
    await expect(snippet).toContainText('dependencies:');
    await expect(snippet).toContainText('ref: oci://demo.pacto.local/postgresql');
    await expect(snippet).toContainText('compatibility: "^17.0.0"');
    const consumer = page.getByTestId('offering-consumer').filter({ hasText: 'payments-service' }).first();
    await expect(consumer).toBeVisible();
    // No provider reports on payments-service's database, so its badge says Unknown.
    await expect(consumer.getByTestId('provisioning-badge').first().locator('.tag')).toHaveText('Unknown');
    await expect(provider.locator('.ov-chips li').first()).toBeVisible();
    expect(await noSideScroll(page)).toBe(true);
  });

  test('postgresql 16.1.0 is in use and deprecated, and consumers show what the adapter reports', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByTestId('offering-provider')).toHaveCount(2, { timeout: T });
    const provider = page.getByTestId('offering-provider').filter({ hasText: 'infra-data' });
    // 17.0.0 is the newest and supported; the 16.1.0 the targets run is deprecated.
    await expect(provider.locator('.oe-row').filter({ hasText: 'Current' })).toContainText('17.0.0');
    await expect(provider.locator('.oe-row').filter({ hasText: 'Latest supported' })).toContainText('17.0.0');
    const v16 = provider.locator('.ov-chips li').filter({ hasText: '16.1.0' });
    await expect(v16.locator('.tag', { hasText: 'Deprecated' })).toBeVisible();
    await expect(v16).toContainText('in use');
    // catalog-service requests its database by configuration reference; the adapter reports it Ready.
    const catalog = page.getByTestId('offering-consumer').filter({ hasText: 'catalog-service' }).filter({ hasText: 'configuration' });
    await expect(catalog.getByTestId('provisioning-badge').locator('.tag')).toHaveText('production: Ready');
    await expect(catalog.locator('.tag', { hasText: 'Deprecated' })).toBeVisible();
    // The adapter could not observe orders-service's database, so it stays Unknown.
    const orders = page.getByTestId('offering-consumer').filter({ hasText: 'orders-service' });
    await expect(orders.getByTestId('provisioning-badge').locator('.tag')).toHaveText('Unknown');
    // Consumers show their own compliance.
    await expect(orders).toContainText('Not compliant');
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the postgresql page says where it is available, who can move to 17.0.0 and who else calls it', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    // The offering: production available from both providers, staging unknown because the
    // only postgresql evidence there is stale, even though postgresql-ha is unavailable there.
    const offering = page.getByTestId('availability').first();
    const env = (a: typeof offering, name: string) => a.getByTestId('availability-env').filter({ hasText: name });
    await expect(env(offering, 'production')).toHaveAttribute('data-state', 'available', { timeout: T });
    await expect(env(offering, 'production')).toContainText('observed');
    await expect(env(offering, 'staging')).toHaveAttribute('data-state', 'unknown');
    // postgresql-ha has no target in staging, and ticketing-service there cannot reach it.
    const ha = page.getByTestId('offering-provider').filter({ hasText: 'platform-data' }).getByTestId('availability');
    await expect(env(ha, 'staging')).toHaveAttribute('data-state', 'unavailable');
    await expect(env(ha, 'staging')).toContainText('inferred');
    await expect(env(ha, 'staging')).toContainText('ticketing-service');
    // Each ^16 consumer reaches the deprecated 16.1.0, and its range excludes the successor.
    const payments = page.getByTestId('offering-consumer').filter({ hasText: 'payments-service' });
    await expect(payments).toContainText('excludes successor');
    // ticketing-service 1.2.0 accepts >=16.0.0 <18.0.0, so it already reaches 17.0.0 and
    // its use names no deprecation; it consumes postgresql-ha too, whose request failed.
    const ticketing = page.getByTestId('offering-consumer').filter({ hasText: 'ticketing-service' });
    await expect(ticketing.filter({ hasText: 'postgresql-ha' }).getByTestId('provisioning-badge').locator('.tag')).toHaveText('staging: Failed');
    await expect(ticketing.filter({ hasText: 'successor' })).toHaveCount(0);
    // reporting-api reads postgresql without declaring any use of the offering.
    const observed = page.getByTestId('observed-consumer');
    await expect(observed).toHaveCount(1);
    await expect(observed).toContainText('reporting-api');
    await expect(observed).toContainText('observed');
    await expect(observed).toContainText('last seen');
    await expect(observed).not.toContainText('stale');
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'offering page postgresql');
  });

  test('the impact action asks what breaks if every postgresql provider goes down', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    await page.getByRole('link', { name: 'What if it becomes unavailable?' }).click();
    await expect(page).toHaveURL((url) => url.hash.startsWith('#/fleet/changes?offering=postgresql'), { timeout: T });
    const answer = page.getByTestId('changes-answer');
    await expect(answer.locator('a[href$="/fleet/services/postgresql-ha"]').first()).toBeVisible({ timeout: T });
    await expect(answer.locator('a[href$="/fleet/services/postgresql"]').first()).toBeVisible();
    await expect(page.locator('[data-testid="impact-figure"] .lane-name')).toContainText(['Breaks']);
    expect(await noSideScroll(page)).toBe(true);
    await audit(page, 'change analysis offering event');
  });

  test('a range on the postgresql page asks the server which versions satisfy it', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/#/fleet/offerings/postgresql');
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByTestId('offering-provider')).toHaveCount(2, { timeout: T });
    const provider = page.getByTestId('offering-provider').filter({ hasText: 'infra-data' });
    await provider.getByLabel('Versions satisfying').fill('^16.0.0');
    await expect(provider.locator('.ov')).toContainText('Latest compatible', { timeout: T });
    await expect(provider.locator('.ov-chips li').filter({ hasText: '16.1.0' })).toContainText(/(^|[^n])satisfied/);
    await expect(provider.locator('.ov-chips li').filter({ hasText: '17.0.0' })).toContainText('unsatisfied');
    await provider.getByLabel('Versions satisfying').fill('not a range');
    await expect(provider.getByRole('alert')).toContainText('not a semver range', { timeout: T });
    expect(await noSideScroll(page)).toBe(true);
  });

  test('the shared-policy provider shows a policies snippet', async ({ page }) => {
    await page.goto('/#/fleet/offerings/shared-policy');
    const snippet = page.getByTestId('offering-provider').getByTestId('consume').locator('pre.consume-snippet');
    await expect(snippet).toContainText('policies:', { timeout: T });
    await expect(snippet).toContainText('name: platform-http-policy');
  });
});
