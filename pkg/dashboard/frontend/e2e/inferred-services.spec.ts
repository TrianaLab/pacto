import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// Inferred services against the built WASM demo (examples/demo/source_fleet.go): the
// k8s-production-workloads source reads three workloads no contract describes, and the
// cluster's kernel-style observer sees legacy-billing call payments-service. The
// Overview counts them in one segment, the Services list mutes them behind a filter,
// each has its own page and the uncontracted caller on attention links to it.

const T = 20_000;
const INFERRED = 3;

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

  test('the uncontracted caller on attention links to its inferred service', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/attention?category=undeclared');

    const caller = page.getByTestId('attention-caller').filter({ hasText: 'legacy-billing' }).first();
    await expect(caller).toBeVisible({ timeout: T });
    await caller.getByRole('link').click();
    await expect(page.getByTestId('inferred-workload')).toBeVisible({ timeout: T });
  });
});
