import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// Workloads the operator linked to a published contract with no Pacto, against the
// built WASM demo (examples/demo/source_linked.go): recommender runs an image the signed
// images record of recommendations 1.1.0 holds, and ran uncontracted for 20 minutes
// first; campaigns carries pacto.trianalab.io/contract from its own chart.

const T = 20_000;
const DIGEST = 'sha256:7d3e9a1c5b2f8e4d6a0c9b7e3f1d5a8c2e6b4f0a9d7c3e1b5f8a2d6c4e0b9f71';
const target = (name: string) => `/#/fleet/targets/${encodeURIComponent(`growth/kubernetes/Deployment%2F${name}`)}`;

test.describe('WASM demo: a workload linked to a published contract with no Pacto', () => {
  test('the target page shows the link once, the full digest in the tip', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, target('recommender'));

    const row = page.getByTestId('target-link');
    await expect(row).toHaveCount(1, { timeout: T });
    await expect(row).toContainText('image digest');
    await expect(row.locator('bdi')).toHaveAttribute('title', DIGEST);
    await row.getByRole('button', { name: 'How this workload was linked' }).click();
    const tip = row.getByRole('note');
    await expect(tip).toContainText(`Linked with no Pacto by the running image digest ${DIGEST} · observed`);
    await expect(tip).toContainText('a signature the operator does not verify');
  });

  test('an annotation link names the reference in full', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, target('campaigns'));

    const row = page.getByTestId('target-link');
    await expect(row).toContainText('annotation', { timeout: T });
    await row.getByRole('button', { name: 'How this workload was linked' }).click();
    await expect(row.getByRole('note')).toContainText('the pacto.trianalab.io/contract annotation oci://demo.pacto.local/campaign-service:1.1.0');
  });

  test('a Pacto target carries no link mark', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/targets/prod%2FDeployment%2Fpayments-service');
    await expect(page.locator('.te-fact').first()).toBeVisible({ timeout: T });
    await expect(page.getByTestId('target-link')).toHaveCount(0);
  });

  test('the Replaces line says the workload was linked by its image', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/recommendations');

    const line = page.getByTestId('service-replaces');
    await expect(line).toContainText('Replaces k8s:5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11/growth/Deployment/recommender', { timeout: T });
    await line.getByRole('button', { name: 'About Replaces' }).click();
    await expect(line.getByRole('note')).toContainText(`Linked with no Pacto: the workload runs image ${DIGEST}, which this service published.`);
  });

  test('the key of the inferred twin opens the declared service', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, `/#/fleet/services/${encodeURIComponent('k8s:5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11%2Fgrowth%2FDeployment%2Frecommender')}`);

    const notice = page.getByTestId('entity-replaced-from');
    await expect(notice).toHaveText(/^k8s:5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11\/growth\/Deployment\/recommender was replaced by recommendations .*, linked with no Pacto by its image digest$/, { timeout: T });
    await expect(page.getByTestId('inferred-workload')).toHaveCount(0);
  });
});
