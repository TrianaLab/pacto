import { test, expect, type Page } from '@playwright/test';

// Keyboard-only acceptance (requirement 8.10) against the built WASM demo: global
// shortcuts behave (open/close overlays, never hijack typing), and the Operational Graph
// is fully operable by keyboard through the accessible relationships navigator (select a
// node/edge, open inspection, Escape closes and restores focus, full-detail link works).
async function ready(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

test.describe('keyboard operability', () => {
  test('"/" opens Fleet search outside an input; Escape closes it', async ({ page }) => {
    await ready(page);
    await page.keyboard.press('/');
    await expect(page.locator('.es-panel')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.es-panel')).toHaveCount(0);
  });

  test('"/" typed inside an input stays literal text (no shortcut hijack)', async ({ page }) => {
    await ready(page);
    await page.keyboard.press('/'); // open search so there is a focused input
    const input = page.locator('.es-panel input');
    await expect(input).toBeFocused();
    await input.press('/');
    await expect(input).toHaveValue('/'); // the slash is text, not a re-trigger
    await page.keyboard.press('Escape');
  });

  test('Cmd/Ctrl-K opens the command palette; Escape closes it', async ({ page }) => {
    await ready(page);
    await page.keyboard.press('ControlOrMeta+k');
    await expect(page.locator('.cp-panel')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.cp-panel')).toHaveCount(0);
  });

  test('the graph is discoverable and inspectable keyboard-only (node + edge + Escape restores focus)', async ({ page }) => {
    await page.goto('/#/fleet/graph/service/payments-service');
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    // The accessible relationships navigator is the keyboard model for the canvas. Open
    // it by keyboard (focus the disclosure summary and press Enter) as a keyboard user
    // would, then operate its node/edge controls.
    const summary = page.getByTestId('graph-textalt').locator('summary');
    await summary.focus();
    await page.keyboard.press('Enter');
    const nodeBtn = page.getByTestId('graph-node-item').first();
    await nodeBtn.focus();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('graph-drawer')).toBeVisible();
    // A full-detail link is reachable by keyboard.
    await expect(page.getByTestId('graph-drawer').getByRole('link', { name: /full detail/i })).toBeVisible();
    // Escape closes the drawer and returns focus to the node control that opened it.
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('graph-drawer')).toHaveCount(0);
    await expect(nodeBtn).toBeFocused();
    // An edge is likewise selectable and opens the relationship inspection.
    const edgeBtn = page.getByTestId('graph-edge').first();
    await edgeBtn.focus();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('graph-drawer')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('graph-drawer')).toHaveCount(0);
    await expect(edgeBtn).toBeFocused();
  });

  test('graph knowledge and direction controls are keyboard operable', async ({ page }) => {
    await page.goto('/#/fleet/graph/service/payments-service');
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    const observed = page.getByTestId('view-observed');
    await observed.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/views=/, { timeout: 20_000 });
    const deps = page.getByTestId('dir-dependencies');
    await deps.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/direction=dependencies/, { timeout: 20_000 });
  });

  test('the target one-hop projection does not expose inert depth/expand controls', async ({ page }) => {
    // Reach a target focus via search (its default projection is the one-hop target view).
    await page.goto('/#/fleet/graph');
    await page.getByRole('searchbox').fill('payments');
    const targetLink = page.locator('a[data-testid="graph-focus-link"][href*="perspective=target"]').first();
    await expect(targetLink).toBeVisible({ timeout: 20_000 });
    await targetLink.click();
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    // The one-hop target projection exposes no depth or expand control at all.
    await expect(page.getByTestId('graph-depth')).toHaveCount(0);
    await expect(page.getByTestId('graph-expand')).toHaveCount(0);
    // Even a hand-crafted depth=6 URL cannot pretend a deeper target hop was evaluated:
    // the controls stay absent and an effective-depth note explains only one hop ran.
    const cur = page.url();
    await page.goto(cur.includes('?') ? `${cur}&depth=6` : `${cur}?depth=6`);
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('graph-depth')).toHaveCount(0);
    await expect(page.getByTestId('graph-effective-depth')).toBeVisible();
  });
});
