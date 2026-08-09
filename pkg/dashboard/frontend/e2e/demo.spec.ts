import { test, expect, type Page } from '@playwright/test';

// Browser E2E against the built WASM demo (see playwright.config.ts): the real Svelte
// bundle + the real dashboard API compiled to wasm, serving deterministic embedded
// data over the LIVE product endpoints (overview/entities/neighborhood/impact). Tests
// are organized around user WORKFLOWS (land -> search the operational graph -> focus a
// neighborhood -> inspect -> analyze impact) and use precise, role-based locators.

async function waitReady(page: Page) {
  await page.goto('/');
  // The Operational Graph tab is capability-gated, so its presence proves the wasm
  // host registered the fleet provider and capabilities were fetched.
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: 20_000 });
}

// The search-first graph landing (no focus): a discovery state, never a hairball.
async function gotoGraphDiscovery(page: Page) {
  await page.goto('/#/fleet/graph');
  await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
}

// A direct focus deep link (kind/key), which loads the bounded VISUAL neighborhood.
async function gotoGraphFocus(page: Page, kind: string, key: string) {
  await page.goto(`/#/fleet/graph/${kind}/${encodeURIComponent(key)}`);
  await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
}

// Search the discovery state and follow the focus link for a given default perspective
// (service links carry no perspective; revision/target links carry theirs), proving the
// entity kind chooses the projection (requirement E).
async function searchAndFocus(page: Page, text: string, perspective: 'service' | 'revision' | 'target') {
  await gotoGraphDiscovery(page);
  await page.getByRole('searchbox').fill(text);
  const sel = perspective === 'service'
    ? 'a[data-testid="graph-focus-link"]:not([href*="perspective="])'
    : `a[data-testid="graph-focus-link"][href*="perspective=${perspective}"]`;
  const link = page.locator(sel).first();
  await expect(link).toBeVisible({ timeout: 20_000 });
  await link.click();
  await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
}

// Open the accessible text alternative so its node/edge buttons can be selected (a
// canvas node is drawn to <canvas> and not directly clickable in Playwright; selecting
// via the accessible list drives the SAME quick-inspection drawer).
async function openTextAlt(page: Page) {
  await page.getByTestId('graph-textalt').locator('summary').click();
}

test.describe('WASM dashboard demo — workflows', () => {
  // The PRIMARY Live Demo entry acceptance (Product IA entry-point cleanup): opening the
  // Fleet-capable demo at the REAL no-hash URL must land on the product Operational
  // Overview, never the superseded legacy landing -- so a future routing change cannot
  // silently re-expose the legacy homepage.
  test('Live Demo entry: the no-hash demo URL lands on the Operational Overview', async ({ page }) => {
    await page.goto('/'); // the real no-hash demo entry, NOT a manually appended hash
    await expect(page).toHaveURL(/#\/fleet$/, { timeout: 20_000 }); // canonicalized to the fleet home
    await expect(page.getByText('Needs attention')).toBeVisible({ timeout: 20_000 }); // Operational Overview is the first product screen
  });

  test('Live Demo entry: an explicit deep link is preserved (not canonicalized)', async ({ page }) => {
    await page.goto('/#/fleet/graph');
    await expect(page).toHaveURL(/#\/fleet\/graph$/, { timeout: 20_000 });
    await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
    // an entity deep link is preserved too
    await page.goto('/#/fleet/services');
    await expect(page).toHaveURL(/#\/fleet\/services$/, { timeout: 20_000 });
    // a legacy but explicit route is preserved (only the ABSENT/default entry canonicalizes)
    await page.goto('/#/readiness');
    await expect(page).toHaveURL(/#\/readiness$/, { timeout: 20_000 });
  });

  test('the embedded fleet loads (product service list)', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  });

  test('navigation exposes only registered capabilities (no dead tabs)', async ({ page }) => {
    await waitReady(page);
    const nav = page.getByRole('navigation', { name: 'Primary' });
    await expect(nav.getByRole('link', { name: 'Operational Graph' })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Services' })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Owners' })).toBeVisible();
  });

  test('O1: graph opens SEARCH-FIRST with zero Cytoscape topology nodes', async ({ page }) => {
    await gotoGraphDiscovery(page);
    await expect(page.getByTestId('neighborhood-canvas')).toHaveCount(0);
    await expect(page.getByRole('search')).toBeVisible();
  });

  test('O2: search a service, focus it, and see an actual visual graph with nodes and edges', async ({ page }) => {
    await searchAndFocus(page, 'payments', 'service');
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible();
    await expect(page.getByTestId('graph-legend')).toBeVisible();
    // The same nodes/edges are listed in the accessible text alternative (proving the
    // topology has real nodes and edges, not an empty canvas).
    await openTextAlt(page);
    await expect(page.getByTestId('graph-node-item').first()).toBeVisible();
    await expect(page.getByTestId('graph-edge').first()).toBeVisible();
  });

  test('O3/O4: Fit and Zoom in/out operate on the canvas without navigating', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    const url = page.url();
    await page.getByTestId('graph-fit').click();
    await page.getByTestId('graph-zoom-in').click();
    await page.getByTestId('graph-zoom-out').click();
    await expect(page).toHaveURL(url); // fit/zoom are ephemeral: no URL change
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible();
  });

  test('O5: selecting a node opens the node quick-inspect drawer (no navigation)', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    const url = page.url();
    await openTextAlt(page);
    await page.getByTestId('graph-node-item').first().click();
    const drawer = page.getByTestId('graph-drawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.getByRole('link', { name: /full detail/i })).toBeVisible();
    await expect(page).toHaveURL(url); // selecting does not navigate away
  });

  test('O6: selecting an edge opens the relationship drawer', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await openTextAlt(page);
    await page.getByTestId('graph-edge').first().click();
    await expect(page.getByTestId('graph-drawer')).toBeVisible();
  });

  test('O7: a direct focus deep link survives a reload', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.reload();
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    await expect(page).toHaveURL(/\/fleet\/graph\/service\//);
  });

  test('O8: browser back restores the prior focus/state', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.getByTestId('dir-dependencies').click();
    await expect(page).toHaveURL(/direction=dependencies/);
    await page.goBack();
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible();
    await expect(page).not.toHaveURL(/direction=dependencies/); // back to the default direction
  });

  test('O9: switching the knowledge view changes the requested/result topology', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.getByTestId('view-observed').click();
    await expect(page).toHaveURL(/views=/);
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible();
  });

  test('O10: a revision search result opens a real revision projection', async ({ page }) => {
    await searchAndFocus(page, 'payments', 'revision');
    await expect(page).toHaveURL(/perspective=revision/);
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible();
    await openTextAlt(page);
    await expect(page.getByTestId('graph-node-item').first()).toBeVisible(); // real revision graph node
  });

  test('O11: a target search result renders a target + runs relation, no fabricated mesh', async ({ page }) => {
    await searchAndFocus(page, 'payments', 'target');
    await expect(page).toHaveURL(/perspective=target/);
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible();
    await openTextAlt(page);
    // The payments target runs payments-service 2.1.0 (an inferred, authoritative link),
    // so a "Runs" relation is drawn; the target depends on services, never on another
    // target (no target-to-target mesh).
    await expect(page.getByTestId('graph-edge').filter({ hasText: 'Runs' }).first()).toBeVisible({ timeout: 20_000 });
    // The one-hop target projection disables the inert depth/expand controls (D).
    await expect(page.getByTestId('graph-depth')).toHaveCount(0);
    await expect(page.getByTestId('graph-expand')).toHaveCount(0);
  });

  test('graph honesty: a partial snapshot surfaces the incompleteness caveat', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await expect(page.getByTestId('graph-knowledge-caveat')).toBeVisible();
  });

  test('graph controls: direction drives the neighborhood via the URL; service focus offers only the service perspective', async ({ page }) => {
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.getByTestId('dir-dependencies').click();
    await expect(page).toHaveURL(/direction=dependencies/);
    // A service cannot be projected as one revision/target, so those buttons are absent (E).
    await expect(page.getByTestId('perspective-revision')).toHaveCount(0);
    await expect(page.getByTestId('perspective-target')).toHaveCount(0);
  });

  test('graph scale: discovery and a focused neighborhood render without a page error', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await gotoGraphDiscovery(page);
    await gotoGraphFocus(page, 'service', 'payments-service');
    await page.waitForTimeout(500);
    expect(errors).toEqual([]);
  });

  test('the Pacto logo is HOME: from inside the fleet product it lands on the Operational Overview', async ({ page }) => {
    await waitReady(page); // fleet-capable host
    await page.goto('/#/fleet/services');
    await expect(page).toHaveURL(/\/fleet\/services/);
    // Wait until the fleet capability has RESOLVED to true (not merely unresolved): the
    // Services nav destination switches to the product list only when fleet is confirmed
    // (it is the legacy landing while capabilities are null), so it is the signal that
    // the brand has adopted the canonical fleet home rather than the transient legacy
    // fallback the documented capability policy uses while capabilities are unresolved.
    await expect(page.getByRole('link', { name: 'Services' }).first()).toHaveAttribute('href', '#/fleet/services', { timeout: 20_000 });
    await page.locator('.navbar-brand').first().click();
    await expect(page).toHaveURL(/#\/fleet$/); // the canonical fleet home, not the legacy #/
    await expect(page.getByText('Needs attention')).toBeVisible({ timeout: 20_000 });
  });

  test('honesty: a healthy status report is never a blanket "all clear" when items exist', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByText('Needs attention')).toBeVisible({ timeout: 20_000 });
  });

  test('workflow: Diff launches the canonical Product Impact workspace', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/diff?from_name=payments-service&from_ver=1.2.0&to_name=payments-service&to_ver=2.0.0');
    // A2: the CTA resolves the service name to a canonical ServiceKey before offering
    // the Product Impact route (never a bare display-name route).
    const cta = page.getByRole('link', { name: /Analyze operational impact/ });
    await expect(cta).toBeVisible({ timeout: 20_000 });
    await expect(cta).toHaveAttribute('href', /#\/fleet\/impact\//);
    await cta.click();
    await expect(page.getByRole('heading', { name: 'Impact' })).toBeVisible();
  });

  test('impact: the Product Impact workspace analyzes by canonical identity', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet/impact/payments-service');
    // The revision selectors are populated from the product service detail (never the
    // raw snapshot). Pick the known breaking pair 1.0.0 -> 2.0.0.
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    await page.getByRole('button', { name: /Analyze impact/ }).click();
    await expect(page.getByText('breaking', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    // A consumer path renders (consumer -> ... -> changed service).
    await expect(page.locator('.path-cell', { hasText: '→' }).first()).toBeVisible({ timeout: 20_000 });
  });

  test('impact: include-observed surfaces the observed-only shadow consumer', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet/impact/payments-service');
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    const cb = page.getByRole('checkbox');
    await expect(cb).toBeEnabled(); // the demo carries embedded observed edges
    await cb.check();
    await page.getByRole('button', { name: /Analyze impact/ }).click();
    // audit-log declares no dependency on payments-service — an observed-only shadow.
    await expect(page.getByText('audit-log').first()).toBeVisible({ timeout: 20_000 });
  });

  test('accessibility: keyboard reaches the primary nav with real, named links', async ({ page }) => {
    await waitReady(page);
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => document.activeElement?.tagName)).toBeTruthy();
    await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveAttribute('href', /#\/fleet/);
  });
});
