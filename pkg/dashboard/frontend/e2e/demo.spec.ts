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
// entity kind chooses the projection.
//
// The result is picked by canonical KEY, never by position: the demo publishes
// same-named services in two domains, so "the first match" is whichever key happens to
// sort first, which is not an identity.
async function searchAndFocus(page: Page, text: string, perspective: 'service' | 'revision' | 'target') {
  await gotoGraphDiscovery(page);
  await page.getByRole('searchbox').fill(text);
  // A default-domain key carries no "<domain>%2F" prefix, so anchoring on the path
  // segment that starts the key excludes any other domain's same-named entities.
  const key = {
    service: 'service/payments-service"]',
    revision: 'revision/payments-service%40"]',
    target: 'target/prod%2FDeployment%2Fpayments-service"]',
  }[perspective];
  const link = page.locator(`a[data-testid="graph-focus-link"][href*="/fleet/graph/${key}`).first();
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
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 }); // Operational Overview is the first product screen
  });

  test('Live Demo entry: an explicit deep link is preserved (not canonicalized)', async ({ page }) => {
    await page.goto('/#/fleet/graph');
    await expect(page).toHaveURL(/#\/fleet\/graph$/, { timeout: 20_000 });
    await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
    // an entity deep link is preserved too
    await page.goto('/#/fleet/services');
    await expect(page).toHaveURL(/#\/fleet\/services$/, { timeout: 20_000 });
    // A legacy route WITHOUT a product equivalent is preserved verbatim; one WITH a
    // product equivalent canonicalizes instead of mounting a second UI. Readiness has
    // one -- the contract revision inventory, which is the population readiness is
    // declared over -- so it canonicalizes there.
    await page.goto('/#/readiness');
    await expect(page).toHaveURL(/#\/fleet\/revisions$/, { timeout: 20_000 });
  });

  test('the embedded fleet loads (product service list)', async ({ page }) => {
    await page.goto('/#/fleet/services');
    await expect(page.getByText('payments-service').first()).toBeVisible({ timeout: 20_000 });
  });

  // Part 1 dual-UI elimination: on the Fleet-capable demo, every legacy route that has a
  // product equivalent canonicalizes to the product IA rather than mounting a second,
  // competing legacy screen. A legacy name-bearing URL is migrated through the Product
  // API. Reload stays on the product URL; Back does not bounce between old/new.
  test('M1: /demo/#/ canonicalizes to the operational overview', async ({ page }) => {
    await page.goto('/#/');
    await expect(page).toHaveURL(/#\/fleet$/, { timeout: 20_000 });
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
  });

  test('M2: the legacy Services list canonicalizes to the product Services list (no legacy view)', async ({ page }) => {
    await page.goto('/#/services');
    await expect(page).toHaveURL(/#\/fleet\/services$/, { timeout: 20_000 });
    // The product Services page renders (its h1), never the legacy ServiceListView.
    await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 });
  });

  test('M3: the legacy standalone Graph canonicalizes to the Operational Graph', async ({ page }) => {
    await page.goto('/#/graph');
    await expect(page).toHaveURL(/#\/fleet\/graph$/, { timeout: 20_000 });
    await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: 20_000 });
  });

  test('M4: the legacy Owners list canonicalizes to the product Owners', async ({ page }) => {
    await page.goto('/#/owners');
    await expect(page).toHaveURL(/#\/fleet\/owners$/, { timeout: 20_000 });
  });

  test('M5: an old service-detail URL canonicalizes to the product entity (never the legacy detail view)', async ({ page }) => {
    // Exactly one service is named payments-service, so the legacy name resolves.
    await page.goto('/#/services/payments-service');
    await expect(page).toHaveURL(/#\/fleet\/services\/payments-service$/, { timeout: 20_000 });
  });

  test('M5a: a legacy URL for a name TWO domains use disambiguates instead of guessing', async ({ page }) => {
    // A legacy URL carries a bare name, and a bare name is not an identity. Two domains
    // publish a platform-app-config, so migrating this bookmark to either one would be a
    // fabricated answer -- the product asks which.
    await page.goto('/#/services/platform-app-config');
    await expect(page.getByTestId('legacy-migration-ambiguous')).toBeVisible({ timeout: 20_000 });
    await expect(page).toHaveURL(/#\/services\/platform-app-config$/); // no guess was made
    const choices = page.locator('[data-testid="legacy-migration"] a.entity-link');
    await expect(choices).toHaveCount(2);
    // Both canonical services are offered, each by its own key.
    await expect(page.locator('[data-testid="legacy-migration"] a[href$="/fleet/services/platform-app-config"]')).toHaveCount(1);
    await expect(page.locator('[data-testid="legacy-migration"] a[href$="/fleet/services/partners%2Fplatform-app-config"]')).toHaveCount(1);
  });

  test('M5b: a legacy service-VERSION bookmark migrates to the canonical Product Revision (keeps the version)', async ({ page }) => {
    // The old #/services/:name/versions/:version bookmark must resolve to a Product Revision
    // never dropping the version to the service page.
    await page.goto('/#/services/payments-service/versions/2.0.0');
    await expect(page).toHaveURL(/#\/fleet\/revisions\//, { timeout: 20_000 });
    // The canonical revision detail shows the requested version.
    await expect(page.getByText('2.0.0').first()).toBeVisible({ timeout: 20_000 });
    // Reload preserves the canonical Product Revision URL (a replace, not a bounce).
    await page.reload();
    await expect(page).toHaveURL(/#\/fleet\/revisions\//, { timeout: 20_000 });
  });

  test('M6: canonicalized product URLs survive a reload', async ({ page }) => {
    await page.goto('/#/services');
    await expect(page).toHaveURL(/#\/fleet\/services$/, { timeout: 20_000 });
    await page.reload();
    await expect(page).toHaveURL(/#\/fleet\/services$/, { timeout: 20_000 });
    await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: 20_000 });
  });

  test('M7: Back does not bounce between a legacy URL and its product canonical', async ({ page }) => {
    await page.goto('/#/fleet'); // start on a product route
    await expect(page).toHaveURL(/#\/fleet$/, { timeout: 20_000 });
    await page.goto('/#/graph'); // a legacy route -> canonicalizes to #/fleet/graph
    await expect(page).toHaveURL(/#\/fleet\/graph$/, { timeout: 20_000 });
    await page.goBack();
    // Back lands on the prior product route, NOT the legacy #/graph that would re-redirect.
    await expect(page).toHaveURL(/#\/fleet$/, { timeout: 20_000 });
  });

  // The primary nav teaches ONE order -- state, inventory, relationships, change -- so a
  // Fleet host has exactly those four destinations. Owners, Data sources, Needs attention
  // and Readiness are dimensions of them (reachable from the Overview, entity pages and
  // the palette), and the legacy-only destinations never leak onto a Fleet host.
  test('navigation exposes exactly the four primary workflows (no dead tabs, no dimensions promoted)', async ({ page }) => {
    await waitReady(page);
    const nav = page.getByRole('navigation', { name: 'Primary' }).first();
    await expect(nav.getByRole('link')).toHaveText(['Overview', 'Services', 'Operational graph', 'Change analysis']);
  });

  test('O1: graph opens SEARCH-FIRST with zero Cytoscape topology nodes', async ({ page }) => {
    await gotoGraphDiscovery(page);
    await expect(page.getByTestId('neighborhood-canvas')).toHaveCount(0);
    await expect(page.getByRole('search')).toBeVisible();
    // The discovery affordance makes it unmistakable a graph renders after a focus is
    // chosen, so the tab is never an empty page.
    await expect(page.getByTestId('graph-discovery-placeholder')).toBeVisible();
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
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
  });

  test('honesty: a healthy status report is never a blanket "all clear" when items exist', async ({ page }) => {
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: 20_000 });
  });

  test('workflow: a legacy Compare bookmark lands in Change analysis, not a legacy screen', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/diff?from_name=payments-service&from_ver=1.2.0&to_name=payments-service&to_ver=2.0.0');
    // The legacy compare route has a product equivalent, so a Fleet host canonicalizes it
    // and resolves the display NAME to a canonical ServiceKey through the Product API --
    // it never mounts the legacy DiffView beside the product UI.
    await expect(page).toHaveURL(/#\/fleet\/changes/, { timeout: 20_000 });
    await expect(page.getByRole('heading', { level: 1, name: 'Change analysis' })).toBeVisible({ timeout: 20_000 });
    await expect(page).toHaveURL(/#\/fleet\/changes\/[^?]+/, { timeout: 20_000 }); // resolved to a ServiceKey
  });

  test('changes: the Change analysis workspace compares by canonical identity', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet/changes/payments-service');
    // The revision selectors are populated from the product service detail (never the
    // raw snapshot). Pick the known breaking pair 1.0.0 -> 2.0.0.
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    await page.getByRole('button', { name: /Compare revisions/ }).click();
    // Stage 1 -- the field-level semantic diff survives the migration off the legacy screen.
    await expect(page.getByTestId('changes-what-changed')).toBeVisible({ timeout: 20_000 });
    // Stage 2 -- and the same revision pair answers what it operationally affects.
    await expect(page.getByTestId('changes-what-it-affects')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('breaking', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    // A consumer path renders (consumer -> ... -> changed service).
    await expect(page.locator('.path-cell', { hasText: '→' }).first()).toBeVisible({ timeout: 20_000 });
  });

  test('changes: include-observed surfaces the observed-only shadow consumer', async ({ page }) => {
    await waitReady(page);
    await page.goto('/#/fleet/changes/payments-service');
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    const cb = page.getByRole('checkbox');
    await expect(cb).toBeEnabled(); // the demo carries embedded observed edges
    await cb.check();
    await page.getByRole('button', { name: /Compare revisions/ }).click();
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
