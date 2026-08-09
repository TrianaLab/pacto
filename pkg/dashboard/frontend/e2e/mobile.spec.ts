import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

// Mobile-layout E2E (runs only on the mobile project, see playwright.config.ts):
// at a narrow viewport the desktop nav is hidden and the primary navigation
// collapses into an accessible hamburger drawer. This asserts the section A rules:
// hidden navigation is ABSENT from the accessibility tree until opened, the toggle
// exposes aria-expanded, and Escape closes the overlay.
test('the navbar collapses into an accessible hamburger drawer on mobile', async ({ page }) => {
  await page.goto('/');
  // Wait for the wasm engine (a service from the embedded fleet appears).
  await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });

  // Collapsed: the drawer's navigation is not in the DOM/accessibility tree, and
  // the toggle reports collapsed. The desktop nav is display:none (also absent),
  // so the Operational Graph link resolves to exactly zero accessible matches.
  const hamburger = page.getByRole('button', { name: 'Menu' });
  await expect(hamburger).toBeVisible();
  await expect(hamburger).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#mobile-drawer')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveCount(0);

  // Open: the drawer appears, the toggle reports expanded, and the primary nav
  // (a single landmark) is now reachable.
  await hamburger.click();
  const drawer = page.locator('#mobile-drawer');
  await expect(drawer).toBeVisible();
  await expect(hamburger).toHaveAttribute('aria-expanded', 'true');
  await expect(drawer.getByRole('link', { name: 'Operational Graph' })).toBeVisible();

  // Escape closes the overlay and the navigation leaves the accessibility tree.
  await page.keyboard.press('Escape');
  await expect(page.locator('#mobile-drawer')).toHaveCount(0);
  await expect(hamburger).toHaveAttribute('aria-expanded', 'false');
});

// WCAG A/AA axe gate over the mobile navigation open state (requirement 8.9); contrast
// is measured separately (requirement 8.8), so it is the only disabled rule.
test('mobile navigation open passes the WCAG A/AA axe gate', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole('button', { name: 'Menu' }).click();
  await expect(page.locator('#mobile-drawer')).toBeVisible();
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .disableRules(['color-contrast'])
    .analyze();
  expect(results.violations, JSON.stringify(results.violations.map((v) => v.id))).toEqual([]);
});
