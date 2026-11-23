import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// Inferred services against the built WASM demo (examples/demo/source_fleet.go): the
// k8s-production-workloads source reads three workloads no contract describes, and the
// cluster's kernel-style observer sees legacy-billing call payments-service. The
// Overview counts them in one segment, the Services list mutes them behind a filter,
// each has its own page and the uncontracted caller on attention links to it.

const T = 20_000;
const INFERRED = 4;

test.describe('WASM demo: inferred services', () => {
  test('the Overview segment counts them and lands on the Services list filtered to them', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet');

    const line = page.getByTestId('overview-services-line');
    await expect(line).toContainText(`${INFERRED} inferred`, { timeout: T });
    const segment = page.getByTestId('overview-inferred');
    await expect(segment).toHaveAttribute('href', '#/fleet/services?declaration=inferred');
    await segment.click();
    await expect(page).toHaveURL(/#\/fleet\/services\?declaration=inferred$/);

    const rows = page.locator('[data-testid="service-list"] li');
    await expect(rows).toHaveCount(INFERRED, { timeout: T });
    await expect(page.locator('[data-testid="service-list"] li.sv-inferred')).toHaveCount(INFERRED);
    await expect(page.getByLabel('Filter by declaration')).toHaveValue('inferred');
  });

  test('the Services list filter keeps one muted row per inferred workload and Clear removes it', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services');

    const select = page.getByLabel('Filter by declaration');
    await expect(select).toHaveValue('');
    await select.selectOption('inferred');
    await expect(page).toHaveURL(/declaration=inferred/);
    const row = page.locator('[data-testid="service-list"] li.sv-inferred', { hasText: 'legacy-billing' });
    await expect(row).toBeVisible({ timeout: T });
    await expect(row).toContainText('registry.example.com/shop/legacy-billing@sha256:');
    // Muted through the text token, never a fade.
    expect(await row.evaluate((el) => getComputedStyle(el).opacity)).toBe('1');
    await expect(row.getByRole('button', { name: 'Inferred service: from its Deployment' })).toBeVisible();

    await select.selectOption('declared');
    await expect(page.locator('[data-testid="service-list"] li.sv-inferred')).toHaveCount(0, { timeout: T });
    await expect(page.locator('[data-testid="service-list"] li').first()).toBeVisible();
  });

  test('the inferred entity page shows its essentials first and the paths behind one fold', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services?declaration=inferred');
    await page.locator('[data-testid="service-list"] a', { hasText: 'legacy-billing' }).first().click();

    const body = page.getByTestId('inferred-workload');
    await expect(body).toBeVisible({ timeout: T });
    await expect(page.getByTestId('inferred-identity')).toContainText('namespace shop, Deployment');
    await expect(page.getByTestId('inferred-identity')).toContainText('k8s-production-workloads');
    await expect(page.getByTestId('inferred-image')).toContainText('legacy-billing@sha256:');
    await expect(body).toContainText('8080 http TCP');
    await expect(body).toContainText('readiness: /healthz');
    // It calls payments-service, and the page links to it.
    await expect(page.getByTestId('inferred-calls').getByRole('link', { name: /payments-service/ })).toBeVisible();

    const fold = page.getByTestId('inferred-paths');
    await expect(fold).not.toHaveAttribute('open', '');
    await fold.locator('summary').click();
    await expect(fold).toContainText('spec.template.spec.containers[name=billing].image');

    // A mark's tip opens by keyboard.
    const mark = body.getByRole('button', { name: 'Inferred service: from k8s-production-workloads' }).first();
    await mark.focus();
    await page.keyboard.press('Enter');
    await expect(mark).toHaveAttribute('aria-expanded', 'true');
    await expect(page.getByRole('note').filter({ hasText: 'containers[name=billing].image' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(mark).toHaveAttribute('aria-expanded', 'false');
  });

  test('the inferred service shows the owner and domain the Backstage catalog holds as muted text with one mark', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services?declaration=inferred');
    await page.locator('[data-testid="service-list"] a', { hasText: 'legacy-billing' }).first().click();

    // examples/demo/backstage-catalog.json: group billing-ops and system billing.
    const owner = page.getByTestId('owner-inferred');
    await expect(owner).toHaveText('inferred: billing-ops', { timeout: T });
    await expect(page.getByTestId('domain-inferred')).toHaveText('inferred: billing');
    const fact = owner.locator('..');
    const mark = fact.getByRole('button', { name: 'About the inferred owner' });
    await expect(mark).toHaveCount(1);
    await mark.click();
    const tip = fact.getByRole('note');
    await expect(tip).toContainText('Inferred from backstage-catalog (component:default/legacy-billing)');
    await expect(tip).toContainText('Pacto never routes approvals or clears a gate on it.');
  });

  test('a contract service with no declared domain shows the domain the catalog holds as muted text with one mark', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/analytics-pipeline');

    // examples/demo/backstage-catalog.json: system analytics; its contract declares an
    // owner, so the catalog's owner adds nothing.
    const domain = page.getByTestId('service-domain');
    await expect(domain.getByTestId('domain-inferred')).toHaveText('inferred: analytics', { timeout: T });
    await expect(domain).toContainText('No domain');
    await expect(page.getByTestId('service-owner').getByTestId('owner-inferred')).toHaveCount(0);
    const mark = domain.getByRole('button', { name: 'About the inferred domain' });
    await expect(mark).toHaveCount(1);
    await mark.click();
    await expect(domain.getByRole('note')).toContainText('Inferred from backstage-catalog (component:default/analytics-pipeline)');
  });

  test('the filtered list states nothing positive about workloads with no contract', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services?declaration=inferred');
    await expect(page.getByTestId('services-answer')).toContainText(`${INFERRED} inferred services`, { timeout: T });
    await expect(page.locator('body')).not.toContainText('Nothing failing');
    await expect(page.locator('.sv-inventory')).toHaveCount(0);
  });

  test('on a phone a row mark opens its tip inside the viewport', async ({ page }) => {
    // Ready at desktop width, where the nav link appReady waits on is on screen.
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services?declaration=inferred');
    await page.setViewportSize({ width: 390, height: 844 });
    const row = page.locator('[data-testid="service-list"] li.sv-inferred', { hasText: 'legacy-billing' });
    const mark = row.getByRole('button', { name: 'Inferred service: from its Deployment' });
    await expect(mark).toBeVisible({ timeout: T });
    await mark.click();
    const note = row.getByRole('note');
    await expect(note).toBeVisible();
    const box = await note.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  });

  test('an inferred service names its group and repository, and the group lists its services', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services?declaration=inferred');
    await page.locator('[data-testid="service-list"] a', { hasText: 'legacy-billing' }).first().click();

    const group = page.getByTestId('inferred-group');
    await expect(group).toContainText('Argo CD Application shop/shop', { timeout: T });
    const repo = page.getByTestId('inferred-repository');
    await expect(repo.getByRole('link', { name: 'https://github.com/example/shop' })).toHaveAttribute('href', 'https://github.com/example/shop');
    const mark = repo.getByRole('button', { name: 'Where Pacto read the repository' });
    await mark.click();
    await expect(repo.getByRole('note')).toContainText('spec.source.repoURL');
    await page.keyboard.press('Escape');

    await group.getByRole('link', { name: 'Services in this group' }).click();
    await expect(page).toHaveURL(/group=5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11%2Fargocd%2Fshop%2Fshop/);
    await expect(page.locator('[data-testid="service-list"] li')).toHaveCount(2, { timeout: T });
    await expect(page.locator('.chip', { hasText: 'Group' })).toContainText('Argo CD Application shop/shop');
    // The group lives in the list and the page: the primary navigation gained no entry.
    await expect(page.getByRole('navigation', { name: 'Primary' }).first().getByRole('link', { name: /group/i })).toHaveCount(0);
  });

  test('a Helm release names no repository', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services?declaration=inferred');
    await page.locator('[data-testid="service-list"] a', { hasText: 'nightly-export' }).first().click();
    await expect(page.getByTestId('inferred-group')).toContainText('Helm release shop/exporter', { timeout: T });
    await expect(page.getByTestId('inferred-repository')).toHaveCount(0);
  });

  test('the uncontracted caller on attention links to its inferred service', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/attention?category=undeclared');

    const caller = page.getByTestId('attention-caller').filter({ hasText: 'legacy-billing' }).first();
    await expect(caller).toBeVisible({ timeout: T });
    await caller.getByRole('link').click();
    await expect(page.getByTestId('inferred-workload')).toBeVisible({ timeout: T });
  });
});
