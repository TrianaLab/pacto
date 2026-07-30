import { test, expect } from '@playwright/test';

// Mobile-layout E2E (runs only on the mobile project, see playwright.config.ts):
// at a narrow viewport the desktop nav is hidden and the primary navigation
// collapses into a hamburger drawer.
test('the navbar collapses to a hamburger drawer on mobile', async ({ page }) => {
  await page.goto('/');
  // Wait for the wasm engine (a service from the embedded fleet appears).
  await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });

  const hamburger = page.locator('.hamburger');
  await expect(hamburger).toBeVisible();
  await hamburger.click();
  const drawer = page.locator('.mobile-drawer');
  await expect(drawer).toBeVisible();
  // The capability-gated Operational Graph link is reachable from the drawer.
  await expect(drawer.getByRole('link', { name: 'Operational Graph' })).toBeVisible();
});
