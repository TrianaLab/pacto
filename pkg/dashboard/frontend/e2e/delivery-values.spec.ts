import { test, expect } from '@playwright/test';

// Each environment's values file in the browser demo (examples/demo/source_delivery.go),
// with no mock: payments ships one chart through Argo CD, each Application selects the
// values file of its environment, and a promote to prod cites the file it applies.

const T = 30_000;

test('each payments environment names the values file its applier selects', async ({ page }) => {
  await page.goto('/#/delivery/environments?service=payments');
  for (const env of ['test', 'prod']) {
    const applier = page.locator(`[data-testid="env-cell"][data-stage="${env}/payments"]`).getByTestId('env-applier');
    await expect(applier).toContainText(`values values-${env}.yaml`, { timeout: T });
  }
});

test('a promote to prod says which values file it applies', async ({ page }) => {
  await page.goto('/#/delivery/history?namespace=prod&stage=payments');
  const row = page.locator('a[href$="/delivery/changes/prod/payments-promote-2"]').first();
  await expect(row).toBeVisible({ timeout: T });
  // A primary click on the row opens the change, as it always has.
  await row.click();
  await expect(page).toHaveURL(/#\/delivery\/changes\/prod\/payments-promote-2$/, { timeout: T });
  const checks = page.getByTestId('change-checks');
  if (!(await checks.evaluate((d) => (d as HTMLDetailsElement).open))) await checks.locator('summary').first().click();
  await expect(checks.getByTestId('verdict-values').first()).toHaveText('Applies values-prod.yaml from this version.', { timeout: T });
});
