import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// External systems a repository names (508-S5), against the built WASM demo:
// newsletter-service's folder names Stripe in chart/values.yaml and requirements.txt,
// so its inferred record lists stripe. The External page shows those rows muted with a
// mark that names the repository; the service page keeps stripe out of what it declares.

const T = 20_000;

test.describe('WASM demo — external systems a repository names', () => {
  test('stripe lists the newsletter-service repository rows as inferred', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/externals/stripe');

    const repo = page.locator('[data-testid="external-source"][data-provenance="inferred"]')
      .filter({ has: page.locator('[data-cell="service"]', { hasText: 'newsletter-service' }) });
    await expect(repo.first()).toBeVisible({ timeout: T });
    for (const kind of ['Helm values', 'SDK']) {
      const row = repo.filter({ has: page.locator('.xp-kind', { hasText: kind }) });
      await expect(row).toHaveCount(1);
      await expect(row).toHaveClass(/xp-inferred/);
      await expect(row.locator('.xp-mark')).toHaveAttribute('title', /repository/);
    }
    await expect(page.locator('[data-testid="external-source"][data-provenance="declared"]')).toHaveCount(0);
    // Stripe has only inferred sources in the demo, so the filter is not offered.
    await expect(page.getByTestId('external-show-inferred')).toHaveCount(0);
  });

  test('newsletter-service never reads stripe as declared', async ({ page }) => {
    await appReady(page, '/#/fleet/services/newsletter-service');
    const inferred = page.getByTestId('service-inferred');
    await expect(inferred).toBeVisible({ timeout: T });
    await inferred.locator('summary').first().click();
    // The inferred block's rows past the first sit in a closed disclosure; text still counts.
    await expect(inferred).toContainText('dependencies[stripe]');
    const expected = page.getByTestId('preview-section').filter({ has: page.getByRole('heading', { name: 'Expected dependencies' }) });
    await expect(expected).toHaveCount(1);
    await expect(expected).not.toContainText('stripe');
  });
});
