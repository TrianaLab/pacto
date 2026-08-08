import { test, expect, type Page } from '@playwright/test';

// Browser E2E for the Phase-2 fleet information architecture against the built WASM
// demo (real Svelte bundle + real dashboard API compiled to wasm, serving the
// product endpoints /api/fleet/overview and /api/fleet/entities). These cover the
// navigation workflows that only a real browser exercises: the overview landing,
// actionable navigation, global search, deep-link reload and browser back. No
// physical-device testing is claimed — these run headless in CI's chromium.

async function waitReady(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

async function openSearch(page: Page) {
  await page.keyboard.press('/');
  await expect(page.getByRole('textbox', { name: 'Search the fleet' })).toBeVisible({ timeout: 10_000 });
}

test.describe('WASM demo — fleet product IA', () => {
  test('scenario 1: /fleet loads the operational overview from the product endpoint', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Operational overview' })).toBeVisible({ timeout: 20_000 });
    // The overview summary (revision-match breakdown) proves it consumed the product
    // overview, not a graph.
    await expect(page.getByText('Revision match')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible();
  });

  test('scenario 4: an overview summary tile navigates to its exact filtered view', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Operational overview' })).toBeVisible({ timeout: 20_000 });
    // The lead attention tile is always present and links to the attention list.
    await page.getByRole('link', { name: /need attention/ }).click();
    await expect(page).toHaveURL(/#\/fleet\/attention/);
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
  });

  test('scenario 5: a degraded source on the overview is visible and navigable', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Operational overview' })).toBeVisible({ timeout: 20_000 });
    // The demo's snapshot is partial (a source is unavailable); its chip links to the
    // source detail.
    const chip = page.locator('a.sh-chip').first();
    await expect(chip).toBeVisible({ timeout: 20_000 });
    await chip.click();
    await expect(page).toHaveURL(/#\/fleet\/sources\//);
  });

  test('scenario 6/8-10: global search finds an entity and opens it by canonical identity', async ({ page }) => {
    await waitReady(page);
    await openSearch(page);
    await page.getByRole('textbox', { name: 'Search the fleet' }).fill('payment');
    const result = page.getByTestId('search-result').first();
    await expect(result).toBeVisible({ timeout: 20_000 });
    await result.click();
    // Opened an exact entity route (any kind) with a canonical, copyable key.
    await expect(page).toHaveURL(/#\/fleet\/(services|revisions|targets|owners|sources)\//);
    await expect(page.getByText('Canonical key')).toBeVisible({ timeout: 20_000 });
  });

  test('scenario 11 + 13: a deep-linked entity route survives a reload (encoded key round-trips)', async ({ page }) => {
    await waitReady(page);
    await openSearch(page);
    await page.getByRole('textbox', { name: 'Search the fleet' }).fill('payment');
    await page.getByTestId('search-result').first().click();
    await expect(page.getByText('Canonical key')).toBeVisible({ timeout: 20_000 });
    const url = page.url();
    expect(url).toMatch(/#\/fleet\//);
    await page.reload();
    // The same entity resolves after a reload from the deep link alone.
    await expect(page).toHaveURL(url);
    await expect(page.getByText('Canonical key')).toBeVisible({ timeout: 20_000 });
  });

  test('scenario 12: browser back returns from an entity to the overview', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Operational overview' })).toBeVisible({ timeout: 20_000 });
    await openSearch(page);
    await page.getByRole('textbox', { name: 'Search the fleet' }).fill('payment');
    await page.getByTestId('search-result').first().click();
    await expect(page.getByText('Canonical key')).toBeVisible({ timeout: 20_000 });
    await page.goBack();
    await expect(page.getByRole('heading', { name: 'Operational overview' })).toBeVisible({ timeout: 20_000 });
  });
});
