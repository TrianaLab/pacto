import { test, expect, type Page, type Locator } from '@playwright/test';

// The service page as one place for a service's whole context (#458), against the
// built WASM demo: facts, targets with their environment, expected dependencies,
// delivery, configuration content, signatures and the revision's provenance. The
// demo's registries are memory sources, so signatures read "not checked" here; the
// other signature states are pinned by the Go and Vitest suites.

const T = 20_000;
const WIDE_FONT = '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}';

async function openPaymentsService(page: Page) {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
  const row = page.locator('.sv-item a.entity-link[href$="/fleet/services/payments-service"]').first();
  await expect(row).toBeVisible({ timeout: T });
  await row.click();
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
}

// Nothing on the page is wider than the viewport, and long registry or schema strings
// wrap inside their cells instead of overflowing them.
async function expectNoOverflow(page: Page, cells: Locator) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(1280);
  const over = await cells.evaluateAll((els) =>
    els.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.textContent?.trim() ?? ''));
  expect(over).toEqual([]);
}

test.describe('WASM demo — service context', () => {
  test('the service page states every fact, including the missing ones', async ({ page }) => {
    await openPaymentsService(page);

    for (const id of ['service-domain', 'service-owner', 'service-contract', 'service-capabilities', 'service-sources']) {
      await expect(page.getByTestId(id)).toBeVisible();
    }
    await expect(page.getByTestId('service-contract').locator('a.entity-link')).toHaveAttribute('href', /\/fleet\/revisions\/.*payments-service/);
    await expect(page.getByTestId('service-sources')).toContainText('k8s-production');

    // Every operational target names its environment or says it is unknown.
    const targets = page.getByTestId('service-targets').locator('li.ref-row');
    expect(await targets.count()).toBeGreaterThan(0);
    for (const row of await targets.all()) await expect(row.locator('.pl-env')).toHaveText(/\S/);

    // The Expected dependencies total is the Declared dependencies drift count.
    const deps = page.getByTestId('service-dependencies');
    await expect(deps).toBeVisible();
    const declared = page.locator('.se-drift li').filter({ hasText: 'Declared dependencies' });
    await expect(declared).toBeVisible();
    const n = (await declared.locator('b').innerText()).trim();
    await expect(deps.getByTestId('preview-count')).toHaveText(new RegExp(`of ${n}$`));

    // No demo Stage is named after a fleet service, so the exact match finds none.
    const delivery = page.getByTestId('service-delivery');
    await expect(delivery.getByText('No Stages for this service. Delivery is opt-in per service.', { exact: true })).toBeVisible({ timeout: T });

    // The configuration content is read from the bundle.
    const config = page.getByTestId('service-configuration');
    await expect(config.locator('.cl-name', { hasText: /^app$/ })).toBeVisible();
    await config.locator('details.sc > summary').first().click();
    await expect(config.locator('.kv-table tbody td:first-child').first()).toBeVisible();

    await expect(page.getByTestId('signatures-not-checked')).toHaveText('No source that read these revisions checks signatures.');

    // The current revision: provenance not checked, configuration content rendered.
    await page.getByTestId('service-contract').locator('a.entity-link').click();
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
    await page.locator('details.re-prov > summary').click();
    await expect(page.getByTestId('bundle-provenance')).toContainText('Not checked.');
    const revConfig = page.locator('details').filter({ has: page.getByRole('heading', { name: 'Configuration', exact: true }) });
    await revConfig.locator('summary').first().click();
    await revConfig.locator('details.sc > summary').first().click();
    await expect(revConfig.locator('.kv-table tbody tr').first()).toBeVisible();
  });

  test('the service and revision pages hold their layout at 1280px with a wide font', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await openPaymentsService(page);
    await page.addStyleTag({ content: WIDE_FONT });
    await expect(page.getByTestId('service-delivery').getByText('No Stages for this service. Delivery is opt-in per service.')).toBeVisible({ timeout: T });
    const config = page.getByTestId('service-configuration');
    await config.locator('details.sc > summary').first().click();
    await expect(config.locator('.kv-table').first()).toBeVisible();

    // No two facts overlap.
    const boxes = (await Promise.all((await page.locator('.se-fact').all()).map((f) => f.boundingBox()))).filter((b) => b !== null);
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i], b = boxes[j];
        const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
        expect(overlap, `facts ${i} and ${j} overlap`).toBe(false);
      }
    }

    // Every environment lies inside its target row.
    const rows = page.getByTestId('service-targets').locator('li.ref-row');
    for (const row of await rows.all()) {
      const r = await row.boundingBox();
      const e = await row.locator('.pl-env').boundingBox();
      expect(r && e).toBeTruthy();
      expect(e!.x).toBeGreaterThanOrEqual(r!.x - 1);
      expect(e!.x + e!.width).toBeLessThanOrEqual(r!.x + r!.width + 1);
    }
    await expectNoOverflow(page, page.locator('.kv-table td, .se-sig-signer'));

    await page.getByTestId('service-contract').locator('a.entity-link').click();
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
    await page.locator('details.re-prov > summary').click();
    await expect(page.getByTestId('bundle-provenance')).toBeVisible();
    await expectNoOverflow(page, page.locator('.kv-table td, .bp-v'));
  });
});
