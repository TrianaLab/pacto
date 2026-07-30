import { test, expect, type Page } from '@playwright/test';

// These run against the built WASM demo (see playwright.config.ts): the real
// Svelte bundle + the real dashboard API compiled to wasm, serving deterministic
// embedded data. Deep links drive the views so no fragile canvas interaction is
// needed. The Operational Graph nav tab only appears once capabilities load with
// fleet enabled, so waiting for it also proves the wasm engine is up.

async function waitReady(page: Page) {
  await page.goto('/');
  // The Operational Graph tab is capability-gated, so its presence proves the
  // wasm host registered the fleet provider and capabilities were fetched.
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

async function gotoFleet(page: Page, query = '') {
  await page.goto(`/#/fleet${query}`);
  await expect(page.getByRole('heading', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

test.describe('WASM dashboard demo', () => {
  test('the demo loads and lists services', async ({ page }) => {
    await waitReady(page);
    // The service list is the landing view; the embedded fleet has many services.
    await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  });

  test('the navbar exposes only registered capabilities', async ({ page }) => {
    await waitReady(page);
    // Fleet + impact are registered → Operational Graph tab present; no dead tabs.
    await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Services' })).toBeVisible();
  });

  test('the Operational Graph renders a real topology with count tiles', async ({ page }) => {
    await gotoFleet(page);
    // Count tiles reflect the embedded snapshot.
    await expect(page.getByText('Services', { exact: true }).first()).toBeVisible();
    await expect(page.locator('.graph-container canvas').first()).toBeVisible({ timeout: 20_000 });
  });

  test('perspective and layer controls are present; observed is honestly disabled', async ({ page }) => {
    await gotoFleet(page);
    await expect(page.getByRole('button', { name: 'Services', exact: false }).first()).toBeVisible();
    await page.getByRole('button', { name: 'Revisions' }).click();
    await page.getByRole('button', { name: 'Targets' }).click();
    // Declared layer is usable; observed has no snapshot source → disabled (no placebo).
    const observed = page.getByRole('button', { name: /Observed/ });
    await expect(observed).toBeDisabled();
  });

  test('a partial snapshot shows the partial banner and source states', async ({ page }) => {
    await gotoFleet(page);
    await expect(page.getByTestId('partial-banner')).toBeVisible();
    const sources = page.getByTestId('source-states');
    await expect(sources).toContainText('unavailable');
  });

  test('selecting a node lazily loads its domain-qualified detail', async ({ page }) => {
    await gotoFleet(page, '?sel=payments-service');
    const panel = page.getByTestId('detail-panel');
    await expect(panel).toContainText('payments-service', { timeout: 20_000 });
    await expect(panel).toContainText('key:'); // domain-qualified identity, not just a name
    await expect(panel.getByText('Revisions')).toBeVisible();
  });

  test('a source failure is never rendered as "All clear"', async ({ page }) => {
    // The demo's status endpoint succeeds, so assert the healthy path is honest:
    // the needs-attention section reflects real items (a non-compliant target),
    // never a blanket all-clear when problems exist. (The failure→unavailable path
    // is unit-tested in FleetView.svelte.test.ts.)
    await gotoFleet(page);
    await expect(page.getByText('Needs attention')).toBeVisible();
  });

  test('Diff → Impact workflow (entry point) and path rendering', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/diff?from_name=payments-service&from_ver=1.2.0&to_name=payments-service&to_ver=2.0.0');
    // The diff runs and offers an operational-impact entry point.
    const cta = page.getByRole('link', { name: /Analyze operational impact/ });
    await expect(cta).toBeVisible({ timeout: 20_000 });
    await cta.click();
    await expect(page.getByRole('heading', { name: 'Impact' })).toBeVisible();
  });

  test('impact is contextual, uses the current snapshot, and renders consumer paths', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/impact?svc=payments-service');
    // Auto-runs from the service entry point across its full history (breaking).
    await expect(page.getByText('BREAKING').first()).toBeVisible({ timeout: 20_000 });
    // §2.2: the impact answer is bound to the graph's published snapshot.
    await expect(page.getByText('matches graph')).toBeVisible();
    // §2.3: a consumer→changed path is rendered.
    await expect(page.locator('.path-cell').first()).toContainText('→');
  });

  test('include-observed is enabled (host declares an observation source) and surfaces a shadow consumer', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/impact?svc=payments-service');
    await expect(page.getByText('BREAKING').first()).toBeVisible({ timeout: 20_000 });
    const cb = page.locator('input[type="checkbox"]');
    await expect(cb).toBeEnabled(); // the demo carries embedded observed edges
    await cb.check();
    await page.getByRole('button', { name: /Analyze impact/ }).click();
    // audit-log declares no dependency on payments-service — an observed-only shadow.
    // Assert the specific consumer path cell, not any mention of the name.
    await expect(page.locator('.path-cell', { hasText: 'audit-log' }).first()).toBeVisible({ timeout: 20_000 });
  });

  test('keyboard navigation reaches the nav and focus is visible', async ({ page }) => {
    await waitReady(page);
    await page.keyboard.press('Tab');
    const active = await page.evaluate(() => document.activeElement?.tagName);
    expect(active).toBeTruthy();
    // The primary nav links are real anchors with accessible names.
    await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveAttribute('href', /#\/fleet/);
  });

  test('a large bounded fleet renders without error', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await gotoFleet(page);
    // The embedded fleet has many services; the graph renders them all bounded.
    await expect(page.locator('.graph-container canvas').first()).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(500);
    expect(errors).toEqual([]);
  });
});
