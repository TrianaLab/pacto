import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// A declaration replacing its inferred twin, against the built WASM demo
// (examples/demo/source_handover.go): ana@acme.example declared invoices 0.1.0 from the
// invoices Deployment the cluster ran with no contract, so the fleet lists one service
// invoices whose page says where each value comes from.

const T = 20_000;
// The demo is a hub with members (514-S3), so an inferred key names its cluster.
const HUB = '5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11';
const TWIN = `k8s:${HUB}%2Fbilling%2FDeployment%2Finvoices`;

test.describe('WASM demo: a declaration replaces its inferred service', () => {
  test('the service page folds where each value comes from, one mark per row', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/invoices');

    const fold = page.getByTestId('field-provenance-fold');
    await expect(fold).toHaveCount(1, { timeout: T });
    await expect(fold).not.toHaveAttribute('open', '');
    await expect(fold.locator('summary')).toContainText('1 accepted');
    await fold.locator('summary').click();

    // The fold also lists contract completeness (its own "Owner" row); field rows live in field-provenance.
    const fields = fold.getByTestId('field-provenance');
    const row = (field: string) => fields.locator('.fpf-row', { has: page.locator('dt', { hasText: field }) });
    await expect(row('owner')).toContainText('billing');
    await expect(row('owner')).toHaveAttribute('data-provenance', 'declared');

    const accepted = row('loyalty-service range');
    await expect(accepted).toHaveAttribute('data-provenance', 'accepted');
    await accepted.getByRole('button', { name: 'Where loyalty-service range comes from' }).click();
    await expect(accepted.getByRole('note')).toContainText('Accepted by ana@acme.example');

    const unknown = row('shipping-rates range');
    await expect(unknown).toContainText('unknown');
    await unknown.getByRole('button', { name: 'Where shipping-rates range comes from' }).click();
    await expect(unknown.getByRole('note')).toContainText('Proposed ^2.0.0');
    await expect(unknown.getByRole('note')).toContainText('never declared');

    // One checkbox hides the inferred rows, never a second list.
    const inferred = fields.locator('.fpf-row[data-provenance="inferred"]');
    await expect(inferred).toHaveCount(1);
    await fold.getByTestId('field-provenance-show-inferred').uncheck();
    await expect(inferred).toHaveCount(0);
    await expect(row('owner')).toBeVisible();

    // The replaced twin: one line and the workload it showed.
    await expect(fold.getByTestId('field-provenance-replacement')).toContainText(`Declared by ana@acme.example from inferred k8s:${HUB}/billing/Deployment/invoices`);
    await expect(fold).toContainText('registry.example.com/billing/invoices:0.9.0');
  });

  test('the Replaces line says who declared it in the tooltip', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/invoices');

    const line = page.getByTestId('service-replaces');
    await expect(line).toContainText(`Replaces k8s:${HUB}/billing/Deployment/invoices`, { timeout: T });
    await line.getByRole('button', { name: 'About Replaces' }).click();
    await expect(line.getByRole('note')).toContainText(/^Declared by ana@acme\.example from this inferred service in revision invoices@sha256:/);
  });

  test('the key of the inferred twin opens invoices with one notice line', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, `/#/fleet/services/${encodeURIComponent(TWIN)}`);

    const notice = page.getByTestId('entity-replaced-from');
    await expect(notice).toHaveCount(1, { timeout: T });
    await expect(notice).toHaveText(`k8s:${HUB}/billing/Deployment/invoices was replaced by invoices`);
    await expect(page.getByTestId('inferred-workload')).toHaveCount(0);
    await expect(page.getByTestId('field-provenance-fold')).toBeVisible();
  });

  test('the contract link opens the declared revision', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/invoices');

    await page.getByTestId('service-contract').locator('a').first().click();
    await expect(page).toHaveURL(/#\/fleet\/revisions\//, { timeout: T });
    await expect(page.getByTestId('inferred-declaration')).toContainText('Declared by ana@acme.example', { timeout: T });
  });

  test('on a phone the open fold never scrolls sideways', async ({ page }) => {
    // Ready at a desktop width, where the nav link appReady waits for is shown.
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/invoices');
    await page.setViewportSize({ width: 390, height: 844 });

    const fold = page.getByTestId('field-provenance-fold');
    await expect(fold).toHaveCount(1, { timeout: T });
    await fold.locator('summary').click();
    await expect(fold.locator('.fpf-row').first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });
});
