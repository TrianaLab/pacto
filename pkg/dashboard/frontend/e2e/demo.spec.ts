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

// The tour is opt-in: it exists only after the invitation in the demo strip is pressed,
// which is also the only thing that lets it move the route.
async function startTour(page: Page) {
  await waitReady(page);
  const start = page.getByTestId('demo-tour-start');
  await expect(start).toBeVisible({ timeout: 20_000 });
  await start.click();
  await expect(page.getByTestId('demo-tour-bubble')).toBeVisible({ timeout: 20_000 });
}

// The same, at a phone viewport, from the first paint. waitReady's probe is a desktop
// signal -- a narrow window keeps that nav link in the hamburger drawer -- and the
// invitation is the better readiness signal anyway: the strip only offers it once the
// engine has resolved. Sized before the goto, because a tour started wide and then
// narrowed is a reflow, not the geometry a visitor who arrives on a phone actually gets.
async function startTourAt(page: Page, width: number, height: number) {
  await page.setViewportSize({ width, height });
  await page.goto('/');
  const start = page.getByTestId('demo-tour-start');
  await expect(start).toBeVisible({ timeout: 30_000 });
  await start.click();
  await expect(page.getByTestId('demo-tour-bubble')).toBeVisible({ timeout: 20_000 });
}

// Does the cut-out actually contain the element it claims to spotlight? A rotted
// selector leaves a tour pointing at nothing, and nothing else in the suite would notice.
async function spotlightEncloses(page: Page, selector: string): Promise<boolean> {
  return page.evaluate((sel) => {
    const s = document.querySelector('[data-testid="demo-tour-spotlight"]');
    const t = document.querySelector(sel);
    if (!s || !t) return false;
    const a = s.getBoundingClientRect();
    const b = t.getBoundingClientRect();
    return a.left <= b.left && a.right >= b.right && a.top <= b.top && a.bottom >= b.bottom;
  }, selector);
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
    await page.goto('/#/services/payments-service/versions/2.0.1');
    await expect(page).toHaveURL(/#\/fleet\/revisions\//, { timeout: 20_000 });
    // The canonical revision detail shows the requested version.
    await expect(page.getByText('2.0.1').first()).toBeVisible({ timeout: 20_000 });
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
    await page.goto('/#/diff?from_name=payments-service&from_ver=1.2.1&to_name=payments-service&to_ver=2.0.1');
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
    // raw snapshot). Pick the known breaking pair 1.0.0 -> 2.0.1.
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.1' });
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
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.1' });
    const cb = page.getByRole('checkbox');
    await expect(cb).toBeEnabled(); // the demo carries embedded observed edges
    await cb.check();
    await page.getByRole('button', { name: /Compare revisions/ }).click();
    // audit-log declares no dependency on payments-service — an observed-only shadow.
    await expect(page.getByText('audit-log').first()).toBeVisible({ timeout: 20_000 });
  });

  // The demo strip is DEMO-ONLY chrome (examples/demo/boot.js), never dashboard UI,
  // because none of what it says is true of a real deployment. A visitor arriving
  // from the site's primary call to action gets an unlabelled dashboard full of
  // fabricated data with no way back unless this exists — and app.wasm is tens of
  // megabytes, so for several seconds the content area is an empty heading with no
  // explanation. Assert what the visitor is owed: the fixture is named, and the
  // documentation is one click away.
  test('demo strip: the fixture is labelled and the docs are one click away', async ({ page }) => {
    await waitReady(page);
    const strip = page.getByTestId('demo-strip');
    await expect(strip).toBeVisible({ timeout: 20_000 });
    await expect(strip).toContainText('fixture fleet', { timeout: 20_000 });
    await expect(strip).toContainText('Nothing here is a real system');
    // The fast path from the home page's primary call to action never passes the
    // explainer, so the two deliberate oddities (degraded-source banner, duplicated
    // config name) have to be flagged here or the visitor reads them as defects.
    await expect(strip).toContainText('two things that look like bugs are deliberate');
    // Announced, not silently swapped, when the engine finishes loading.
    await expect(strip).toHaveAttribute('role', 'status');
    await expect(strip).toHaveAttribute('aria-live', 'polite');
    // The download counter ticks once a second inside a live region, so it must be
    // hidden from assistive technology or it announces every second; and it must be
    // empty once the engine has landed rather than counting on forever.
    const meter = strip.locator('#pacto-demo-meter');
    await expect(meter).toHaveAttribute('aria-hidden', 'true');
    await expect(meter).toHaveText('');
    // The explainer page, not the docs root: two properties of this fixture are
    // deliberate and read as bugs (the degraded-source banner, the duplicated config
    // name), and that page is where they are explained. The site's primary call to
    // action jumps straight into the demo, so this link is the only route to it.
    // Relative, so it resolves whether the demo is mounted at /demo/ or
    // /<version>/demo/.
    await expect(strip.getByRole('link', { name: 'About this demo' }))
      .toHaveAttribute('href', '../examples/dashboard-demo/');
  });

  // The strip floats over the bottom of the dashboard and never leaves. On a short
  // window that is where the content is, so a reader who has read it needs a way to put
  // it away — otherwise the notice explaining the demo is also obscuring it.
  test('demo strip: the reader can dismiss it once they have read it', async ({ page }) => {
    await waitReady(page);
    const strip = page.getByTestId('demo-strip');
    await expect(strip).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('demo-strip-close').click();
    await expect(strip).toBeHidden();
  });

  // The walkthrough. Labelling the fixture tells a visitor what they are looking at and
  // nothing about what it is FOR, so the strip offers a guided tour: six steps, each one
  // making the reader drive the real app to the screen that answers one question.
  //
  // It is a HARD gate, so the thing worth asserting is not the copy and not even the
  // route -- it is that the tour does not move until the action really happened and that
  // the action really produced something. A tour whose gates open on their own is the
  // slideshow this replaced.
  //
  // And the gate is what MOVES it. A gate is an observation, so a Next button after one
  // asks the reader to confirm what the tour just watched them do. The assertions below
  // are therefore mostly negatives: on a step with an action there is no forward control
  // at all, nothing is clicked, and the tour is expected to arrive at the next step on its
  // own. The steps with nothing to observe are the mirror image -- they keep a real button
  // and must be proved NOT to move without it.
  //
  // Demo-only by construction: this lives in examples/demo/boot.js, which ships with the
  // demo and nothing else, so there is no deployment flag that could turn it on in front
  // of a real fleet.
  test('demo tour: it never starts itself — the strip invites, and nothing more', async ({ page }) => {
    await waitReady(page);
    const strip = page.getByTestId('demo-strip');
    // The fixture disclosure is the strip's resting state, owed before anything is
    // pressed: the fast path from the home page's call to action never passes the
    // explainer.
    await expect(strip).toContainText('fixture fleet', { timeout: 20_000 });
    await expect(strip).toContainText('two things that look like bugs are deliberate');
    await expect(page.getByTestId('demo-tour-start')).toBeVisible({ timeout: 20_000 });
    // No overlay, no dimming, no coach bubble until the invitation is accepted.
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(0);
    await expect(page.getByTestId('demo-tour-bubble')).toHaveCount(0);
    // And the first paint still navigates nowhere it was not already going.
    await expect(page).toHaveURL((url) => url.hash === '#/fleet');
  });

  test('demo tour: six steps, and every step with an action advances itself', async ({ page }) => {
    // The whole walkthrough, and every action step now carries a deliberate ~1.1s pause
    // between the gate settling and the view changing.
    test.setTimeout(60_000);
    await startTour(page);
    const next = page.getByTestId('demo-tour-next');
    const auto = page.getByTestId('demo-tour-auto');
    const stepNo = page.getByTestId('demo-tour-step');

    // 1 — the disclosure, on the overview. There is nothing here the tour could watch for,
    // so it keeps a real button and waits to be pressed.
    await expect(stepNo).toHaveText('1 / 6');
    await expect(page.getByTestId('demo-tour-text')).toContainText('two things that look like bugs are deliberate');
    await expect(page.getByTestId('demo-tour-back')).toBeDisabled();
    await expect(next).toBeVisible();
    await expect(next).toHaveText('Continue');
    await expect(auto).toBeHidden();
    // The light is on something real. A spotlight whose selector has rotted points at
    // nothing and the tour is dead, so prove the cut-out encloses the target it names.
    await expect(page.getByTestId('demo-tour-spotlight')).toBeVisible();
    expect(await spotlightEncloses(page, '#sec-attention')).toBe(true);
    await next.click();

    // 2 — search. The gate is the reader's own committed search, not the presence of a
    // row: this fleet is small enough that payments-service is on the unfiltered page 1.
    // Nothing is pressed to leave this step; doing the step IS pressing Next.
    await expect(stepNo).toHaveText('2 / 6');
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services', { timeout: 20_000 });
    await expect(next).toBeHidden();
    await expect(auto).toBeVisible();
    const search = page.getByTestId('svc-search');
    await search.fill('payments');
    await search.press('Enter');
    const row = page.locator('[data-testid="service-list"] a[href$="/fleet/services/payments-service"]');
    await expect(row).toBeVisible({ timeout: 20_000 });
    await expect(stepNo).toHaveText('3 / 6', { timeout: 20_000 });

    // 3 — open it. The step stays on the list route, so the search the reader just
    // committed survives the step change rather than being navigated away.
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services?text=payments');
    await expect(next).toBeHidden();
    await row.click();
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services/payments-service', { timeout: 20_000 });
    await expect(stepNo).toHaveText('4 / 6', { timeout: 20_000 });

    // 4 — compare two revisions and get a real field-level diff back.
    await expect(page).toHaveURL((url) => url.hash.split('?')[0] === '#/fleet/changes/payments-service', { timeout: 20_000 });
    await expect(next).toBeHidden();
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.getByRole('button', { name: /Compare revisions/ }).click();
    await expect(page.getByTestId('changes-what-changed')).toBeVisible({ timeout: 20_000 });
    await expect(stepNo).toHaveText('5 / 6', { timeout: 20_000 });

    // 5 — search the operational graph and focus a result, which is what renders a
    // neighborhood at all (the graph tab opens search-first, never a hairball).
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/graph', { timeout: 20_000 });
    await expect(next).toBeHidden();
    await page.locator('[data-testid="graph-discovery"] input[type=search]').fill('orders');
    const focus = page.getByTestId('graph-focus-link').first();
    await expect(focus).toBeVisible({ timeout: 20_000 });
    await focus.click();
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: 20_000 });
    await expect(stepNo).toHaveText('6 / 6', { timeout: 20_000 });

    // 6 — terminal. No screen of its own: it is the hand-off to the CLI tour, which
    // answers the same six questions from a terminal. Nothing left to observe, so it keeps
    // a real button, and since there is no step seven that button finishes the tour.
    await expect(next).toBeVisible();
    await expect(next).toHaveText('Done');
    await expect(auto).toBeHidden();
    await expect(page.getByTestId('demo-tour-skip')).toBeHidden();
    await expect(page.getByTestId('demo-tour-more')).toHaveAttribute('href', '../examples/demo-tour/');

    // Back re-opens the previous step, and re-closes its gate: a tour you cannot re-read
    // a step of is a tour you have to restart. It must also not bounce straight forward
    // again on the result the reader already produced -- a step only moves on a state
    // change it watched happen, never on one it was dropped into.
    await page.getByTestId('demo-tour-back').click();
    await expect(stepNo).toHaveText('5 / 6');
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/graph', { timeout: 20_000 });
    await expect(page.getByTestId('demo-tour-more')).toBeHidden();
    await page.waitForTimeout(2500);
    await expect(stepNo).toHaveText('5 / 6');

    // Done ends it, and leaves the reader where they got to.
    await page.getByTestId('demo-tour-skip').click();
    await expect(stepNo).toHaveText('6 / 6', { timeout: 20_000 });
    await next.click();
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(0);
    await expect(page.getByTestId('demo-strip')).toBeVisible();
  });

  // The reader did the step; the tour watched them do it. Asking them to press Next after
  // that is asking for the step twice, so the forward control is GONE on an action step --
  // and the empty space it left has to say why, or the reader stands there waiting for a
  // button that is never coming.
  test('demo tour: an action step has no forward button and says the tour moves by itself', async ({ page }) => {
    await startTour(page);
    const next = page.getByTestId('demo-tour-next');
    await next.click();
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services', { timeout: 20_000 });
    await expect(next).toBeHidden();
    const auto = page.getByTestId('demo-tour-auto');
    await expect(auto).toBeVisible();
    await expect(auto).toContainText('the tour moves on when you do it');
    // A tour that is waiting with no reason given is a wall, so the bubble says what for.
    await expect(page.getByTestId('demo-tour-bubble')).toContainText('Waiting for a search');
    // And it is genuinely waiting: an auto-advance that fires on the state the step opened
    // in is a slideshow with extra steps.
    await page.waitForTimeout(2500);
    await expect(page.getByTestId('demo-tour-step')).toHaveText('2 / 6');
    const search = page.getByTestId('svc-search');
    await search.fill('payments');
    await search.press('Enter');
    await expect(page.getByTestId('demo-tour-step')).toHaveText('3 / 6', { timeout: 20_000 });
    // The two ways out are untouched by any of this: they are on every step, always.
    await expect(page.getByTestId('demo-tour-skip')).toBeVisible();
    await expect(page.getByTestId('demo-tour-exit')).toBeVisible();
  });

  // The mirror image. A step with no action to observe would auto-advance on a gate that is
  // trivially true and flash past before it was read, so those two keep a real button and
  // must be proved to wait for it.
  test('demo tour: the steps with nothing to observe keep a real button and wait for it', async ({ page }) => {
    test.setTimeout(60_000);
    await startTour(page);
    const next = page.getByTestId('demo-tour-next');
    const stepNo = page.getByTestId('demo-tour-step');
    await expect(next).toHaveText('Continue');
    await expect(page.getByTestId('demo-tour-auto')).toBeHidden();
    await expect(page.getByTestId('demo-tour-skip')).toBeHidden(); // nothing to skip either
    await page.waitForTimeout(2500);
    await expect(stepNo).toHaveText('1 / 6'); // the disclosure does not read itself
    await next.click();
    for (const n of ['3 / 6', '4 / 6', '5 / 6', '6 / 6']) {
      await page.getByTestId('demo-tour-skip').click();
      await expect(stepNo).toHaveText(n, { timeout: 20_000 });
    }
    await expect(next).toHaveText('Done');
    await page.waitForTimeout(2500);
    await expect(stepNo).toHaveText('6 / 6'); // the hand-off does not dismiss itself either
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(1);
  });

  // Always skippable, and skipping must not cost the reader the step: it performs the
  // action for them, so they land where a performer lands rather than one screen behind.
  // Skip fires the step's own action, and that action satisfies the very gate the
  // auto-advance is watching -- so the two are racing for the same move and a reader who
  // pressed Skip would be carried two steps, losing the one they paid a button press for.
  // Sampled on mutation rather than polled: a double advance lands and settles between two
  // polls, and the poll would then see a perfectly consistent tour that is wrong by one.
  test('demo tour: Skip advances exactly one step on every skippable step, never two', async ({ page }) => {
    test.setTimeout(60_000);
    await page.addInitScript(() => {
      const log: string[] = [];
      (window as unknown as { __stepLog: string[] }).__stepLog = log;
      new MutationObserver(() => {
        const s = document.querySelector('[data-testid="demo-tour-step"]')?.textContent ?? '';
        if (s && log[log.length - 1] !== s) log.push(s);
      }).observe(document, { subtree: true, childList: true, characterData: true });
    });
    await startTour(page);
    const stepNo = page.getByTestId('demo-tour-step');
    await page.getByTestId('demo-tour-next').click();
    for (const n of ['3 / 6', '4 / 6', '5 / 6', '6 / 6']) {
      await page.getByTestId('demo-tour-skip').click();
      await expect(stepNo).toHaveText(n, { timeout: 20_000 });
    }
    // And nothing arrives late: no second advance from a gate that opened after the move.
    await page.waitForTimeout(2000);
    const log = await page.evaluate(() => (window as unknown as { __stepLog: string[] }).__stepLog);
    expect(log.slice(log.indexOf('2 / 6'))).toEqual(['2 / 6', '3 / 6', '4 / 6', '5 / 6', '6 / 6']);
  });

  // The report this feature came from: the tour fired the instant the hash carried a text
  // filter and a payments-service row was on the page, which is true after ONE committed
  // character -- so it took the screen away mid-word. The gate has to hold through a quiet
  // period, and a keystroke is not quiet.
  test('demo tour: a search typed one character at a time never advances mid-word', async ({ page }) => {
    await startTour(page);
    await page.getByTestId('demo-tour-next').click();
    const stepNo = page.getByTestId('demo-tour-step');
    await expect(stepNo).toHaveText('2 / 6');
    const search = page.getByTestId('svc-search');
    await search.click();
    // Commit a one-character search. From here the step-2 gate is SATISFIED and the reader
    // is still mid-word, which is exactly the state that used to yank them forward.
    await search.pressSequentially('p');
    await search.press('Enter');
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services?text=p', { timeout: 20_000 });
    await expect(page.locator('[data-testid="service-list"] a[href$="/fleet/services/payments-service"]'))
      .toBeVisible({ timeout: 20_000 });
    // Finish the word at a natural speed, checking after every keystroke.
    for (const ch of 'ayments') {
      await search.pressSequentially(ch);
      await page.waitForTimeout(180);
      await expect(stepNo).toHaveText('2 / 6');
    }
    // Only once they stop does it move.
    await search.press('Enter');
    await expect(stepNo).toHaveText('3 / 6', { timeout: 20_000 });
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services?text=payments', { timeout: 20_000 });
  });

  test('demo tour: Skip step performs the action and lands on the same screen', async ({ page }) => {
    await startTour(page);
    await page.getByTestId('demo-tour-next').click();
    await expect(page.getByTestId('demo-tour-step')).toHaveText('2 / 6');
    await expect(page.getByTestId('demo-tour-next')).toBeHidden(); // an action step has no forward button
    await page.getByTestId('demo-tour-skip').click();
    await expect(page.getByTestId('demo-tour-step')).toHaveText('3 / 6', { timeout: 20_000 });
    // The search really was committed -- a bare `value =` assignment leaves Svelte's own
    // state on the old value and this URL would read `#/fleet/services` -- and the row
    // step 3 asks for is on screen.
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/services?text=payments', { timeout: 20_000 });
    await expect(page.locator('[data-testid="service-list"] a[href$="/fleet/services/payments-service"]'))
      .toBeVisible({ timeout: 20_000 });
  });

  test('demo tour: Exit tour removes the overlay and gives the notice back', async ({ page }) => {
    await startTour(page);
    // One announcer at a time: the strip is a role=status live region, so it stands down
    // while the bubble is speaking.
    await expect(page.getByTestId('demo-strip')).toBeHidden();
    await page.getByTestId('demo-tour-exit').click();
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(0);
    const strip = page.getByTestId('demo-strip');
    await expect(strip).toBeVisible();
    await expect(strip).toContainText('fixture fleet');
    await expect(page.getByTestId('demo-tour-start')).toBeVisible();
  });

  // Escape exits, and it still does after the handler was scoped to the bubble: every
  // step change puts focus back in the bubble, so the key is live exactly where the
  // reader last was.
  test('demo tour: Escape exits it', async ({ page }) => {
    await startTour(page);
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(0);
    await expect(page.getByTestId('demo-strip')).toBeVisible();
  });

  // ...but the tour does not OWN Escape — the app does. The dashboard's own widgets each
  // close or clear on Escape, and while the handler sat on the document every one of them
  // also tore the tour down and lost the reader's progress. Three widgets, three proofs,
  // because they are three different code paths in the app (a drawer, an overlay dialog
  // and a browser-native search field) and only the last one reaches the document unaided.

  test('demo tour: Escape closing the mobile nav drawer leaves the tour standing', async ({ page }) => {
    await startTourAt(page, 390, 844); // the hamburger appears at <=768px
    await expect(page.getByTestId('demo-tour-step')).toHaveText('1 / 6');
    await page.getByRole('button', { name: 'Menu' }).click();
    await expect(page.locator('#mobile-drawer')).toBeVisible();
    // Focus is inside the drawer (it moves there on open), so this Escape belongs to it.
    await page.keyboard.press('Escape');
    await expect(page.locator('#mobile-drawer')).toHaveCount(0);
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(1);
    await expect(page.getByTestId('demo-tour-step')).toHaveText('1 / 6'); // and no progress lost
  });

  test('demo tour: Escape closing the command palette leaves the tour standing', async ({ page }) => {
    await startTour(page);
    await page.keyboard.press('ControlOrMeta+k');
    const palette = page.getByRole('dialog', { name: 'Command palette' });
    await expect(palette).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(palette).toHaveCount(0);
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(1);
    await expect(page.getByTestId('demo-tour-step')).toHaveText('1 / 6');
  });

  test('demo tour: Escape in a search field leaves the tour standing, and the step still completes', async ({ page }) => {
    await startTour(page);
    await page.getByTestId('demo-tour-next').click();
    await expect(page.getByTestId('demo-tour-step')).toHaveText('2 / 6');
    // A type=search field clears itself on Escape and deliberately lets the key through
    // when it has no suggestion popup to dismiss — so this is the one that used to reach
    // the document handler unopposed, on the very step that asks the reader to type.
    const search = page.getByTestId('svc-search');
    await search.click();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('demo-tour-overlay')).toHaveCount(1);
    await expect(page.getByTestId('demo-tour-step')).toHaveText('2 / 6');
    // Alive is not enough: the reader can still finish the step they were asked to do, and
    // the tour still carries them on when they have.
    await search.fill('payments');
    await search.press('Enter');
    await expect(page.getByTestId('demo-tour-step')).toHaveText('3 / 6', { timeout: 20_000 });
  });

  // Skip performs the action and then WAITS for that step's own gate. Step 4 is where it
  // mattered: its action starts an impact analysis that writes the URL when it returns, so
  // a skip that navigated in the same tick left the address bar describing a screen the
  // reader was no longer on.
  test('demo tour: Skip waits for the step it skipped, so the analysis never desyncs the URL', async ({ page }) => {
    test.setTimeout(60_000);
    // Sampled on mutation rather than polled: the whole question is whether one state
    // (step 4, diff rendered) existed BEFORE another (step 5), and a poll can miss it.
    await page.addInitScript(() => {
      const log: string[] = [];
      (window as unknown as { __tourLog: string[] }).__tourLog = log;
      new MutationObserver(() => {
        const step = document.querySelector('[data-testid="demo-tour-step"]')?.textContent ?? '';
        const diff = !!document.querySelector('[data-testid="changes-what-changed"]');
        const s = `${step}|${diff}`;
        if (step && log[log.length - 1] !== s) log.push(s);
      }).observe(document, { subtree: true, childList: true, characterData: true });
    });
    await startTour(page);
    const stepNo = page.getByTestId('demo-tour-step');
    await page.getByTestId('demo-tour-next').click();
    await expect(stepNo).toHaveText('2 / 6');
    await page.getByTestId('demo-tour-skip').click();
    await expect(stepNo).toHaveText('3 / 6', { timeout: 20_000 });
    await page.getByTestId('demo-tour-skip').click();
    await expect(stepNo).toHaveText('4 / 6', { timeout: 20_000 });
    await expect(page).toHaveURL((url) => url.hash.split('?')[0] === '#/fleet/changes/payments-service', { timeout: 20_000 });

    await page.getByTestId('demo-tour-skip').click();
    await expect(stepNo).toHaveText('5 / 6', { timeout: 20_000 });
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/graph', { timeout: 20_000 });
    // The analysis has nothing left to rewrite: the URL stays on step 5's screen well past
    // the point where the orphaned replaceState used to land on it.
    await page.waitForTimeout(2000);
    await expect(page).toHaveURL((url) => url.hash === '#/fleet/graph');
    // And it did not merely finish in time — the comparison really ran while step 4 was
    // still on screen, which is the point of the feature: the reader gets a moment to see
    // what was done for them.
    const log = await page.evaluate(() => (window as unknown as { __tourLog: string[] }).__tourLog);
    expect(log).toContain('4 / 6|true');
  });

  // Waiting must be bounded: a gate that never opens has to move the reader on rather than
  // leave them holding a button that now looks broken.
  test('demo tour: a gate that never opens still lets Skip move on', async ({ page }) => {
    test.setTimeout(60_000);
    // Rename the container the step-2 gate looks for, as it renders, so the gate can never
    // be satisfied. A rename rather than a removal: the node stays where Svelte put it, so
    // nothing else about the page changes.
    await page.addInitScript(() => {
      new MutationObserver(() => {
        const l = document.querySelector('[data-testid="service-list"]');
        if (l) l.setAttribute('data-testid', 'service-list-sabotaged');
      }).observe(document, { subtree: true, childList: true, attributes: true });
    });
    await startTour(page);
    const stepNo = page.getByTestId('demo-tour-step');
    await page.getByTestId('demo-tour-next').click();
    await expect(stepNo).toHaveText('2 / 6');
    const t0 = Date.now();
    await page.getByTestId('demo-tour-skip').click();
    await expect(stepNo).toHaveText('3 / 6', { timeout: 20_000 });
    // It waited for the gate it could never get, then went anyway.
    expect(Date.now() - t0).toBeGreaterThan(2000);
  });

  // The dim is drawn by a spread shadow on the cut-out, so a cut-out bigger than the
  // window is a page with no dim on it at all. At 320px step 1's target is twice the
  // height of the window, which used to render as an undimmed page with a stray outline.
  test('demo tour: at 320x800 the cut-out stays on screen and the page is really dimmed', async ({ page }) => {
    await startTourAt(page, 320, 800);
    const geom = async () => page.evaluate(() => {
      const vw = document.documentElement.clientWidth;
      const vh = document.documentElement.clientHeight;
      const s = document.querySelector('[data-testid="demo-tour-spotlight"]') as HTMLElement | null;
      const b = document.querySelector('[data-testid="demo-tour-bubble"]')!.getBoundingClientRect();
      const r = s && !s.hidden ? s.getBoundingClientRect() : null;
      return {
        lit: !!r,
        onScreen: !r || (r.left >= 0 && r.top >= 0 && r.right <= vw && r.bottom <= vh),
        dimmed: vw * vh - (r ? r.width * r.height : 0),
        bubbleOnScreen: b.left >= 0 && b.top >= 0 && b.right <= vw && b.bottom <= vh,
      };
    });
    await expect.poll(geom, { timeout: 20_000 }).toMatchObject({ lit: true, onScreen: true, bubbleOnScreen: true });
    expect((await geom()).dimmed).toBeGreaterThan(0); // there IS a dim layer, not just an outline
  });

  // The reader has to see the result of what they did before the screen moves under them,
  // and a view that changes with no warning is disorienting to everyone and a WCAG problem
  // for the reader who cannot watch it happen. So the confirmation goes up in the live
  // region while the step that earned it is STILL on screen.
  test('demo tour: the move is confirmed in the live region before the view changes', async ({ page }) => {
    // Sampled on mutation, because the whole question is whether one state (step 2, gate
    // confirmed) existed BEFORE another (step 3), and a poll can miss it entirely.
    await page.addInitScript(() => {
      const log: string[] = [];
      (window as unknown as { __hintLog: string[] }).__hintLog = log;
      new MutationObserver(() => {
        const s = document.querySelector('[data-testid="demo-tour-step"]')?.textContent ?? '';
        const h = document.querySelector('#pacto-tour-hint')?.textContent ?? '';
        const v = `${s}|${h}`;
        if (s && log[log.length - 1] !== v) log.push(v);
      }).observe(document, { subtree: true, childList: true, characterData: true });
    });
    await startTour(page);
    await page.getByTestId('demo-tour-next').click();
    const hint = page.locator('#pacto-tour-hint');
    await expect(hint).toHaveAttribute('aria-live', 'polite');
    await expect(hint).toHaveText('Waiting for a search that narrows the list to payments-service.');
    const search = page.getByTestId('svc-search');
    await search.fill('payments');
    await search.press('Enter');
    await expect(page.getByTestId('demo-tour-step')).toHaveText('3 / 6', { timeout: 20_000 });
    const log = await page.evaluate(() => (window as unknown as { __hintLog: string[] }).__hintLog);
    // Announced while step 2 was still up: the reader connects the search they ran to the
    // result it produced, and is told the tour is about to move rather than discovering it.
    expect(log).toContain('2 / 6|Done — moving on.');
    // The step they land on says what IT is waiting for, so the region is never emptied.
    await expect(hint).toHaveText('Waiting for the payments-service page to open.');
  });

  // House style omits the serial comma, and the sentence has to read as though it never
  // wanted one.
  test('demo tour: step 3 names the three things without a serial comma', async ({ page }) => {
    await startTour(page);
    await page.getByTestId('demo-tour-next').click();
    await page.getByTestId('demo-tour-skip').click();
    await expect(page.getByTestId('demo-tour-step')).toHaveText('3 / 6', { timeout: 20_000 });
    const text = page.getByTestId('demo-tour-text');
    await expect(text).toContainText('every revision it has published and what was observed about the targets running it');
    await expect(text).not.toContainText('every revision of it, and');
  });

  // The strip carrying the fixture disclosure stands down while the tour speaks, so from
  // step 2 on the reader had no way back to the page that says this fleet is invented. The
  // bubble owes them the same link, on every step.
  test('demo tour: the demo disclosure stays one click away on every step', async ({ page }) => {
    test.setTimeout(60_000);
    await startTour(page);
    const about = page.getByTestId('demo-tour-about');
    const stepNo = page.getByTestId('demo-tour-step');
    await expect(about).toBeVisible();
    await expect(about).toHaveAttribute('href', '../examples/dashboard-demo/');
    for (const n of ['2 / 6', '3 / 6', '4 / 6', '5 / 6', '6 / 6']) {
      const skip = page.getByTestId('demo-tour-skip');
      if (await skip.isVisible()) await skip.click();
      else await page.getByTestId('demo-tour-next').click();
      await expect(stepNo).toHaveText(n, { timeout: 20_000 });
      await expect(about).toBeVisible();
      await expect(about).toHaveAttribute('href', '../examples/dashboard-demo/');
    }
    // The last step's hand-off to the CLI tour is a different destination and stays its
    // own link, so the disclosure was never repurposed into it.
    await expect(page.getByTestId('demo-tour-more')).toHaveAttribute('href', '../examples/demo-tour/');
  });

  // "Empty once ready" above would also pass for a counter that never ran at all, so
  // prove the thing actually reports progress. Recording every value it takes, rather
  // than sampling the DOM at a moment, keeps the assertion off the race between the
  // download finishing and the poll: the log holds the whole history either way. The
  // route delay stands in for a slow link — served from localhost the engine lands
  // too fast for a visitor-scale wait to exist at all.
  test('demo strip: the download reports its size and elapsed time while loading', async ({ page }) => {
    await page.addInitScript(() => {
      const log: string[] = [];
      (window as unknown as { __meterLog: string[] }).__meterLog = log;
      new MutationObserver(() => {
        const m = document.getElementById('pacto-demo-meter');
        if (m) log.push(m.textContent ?? '');
        // `document`, not `document.documentElement`: an init script runs before the
        // first byte of the page is parsed, and at that point there is no root element
        // to observe yet.
      }).observe(document, { subtree: true, childList: true, characterData: true });
    });
    await page.route('**/app.wasm*', async (route) => {
      await new Promise((r) => setTimeout(r, 3000));
      await route.continue();
    });
    await waitReady(page);
    await expect(page.getByTestId('demo-strip')).toContainText('fixture fleet', { timeout: 30_000 });
    const log = await page.evaluate(() => (window as unknown as { __meterLog: string[] }).__meterLog);
    // Elapsed seconds while the engine is in flight, and the real transfer size read
    // off the response rather than a number hard-coded in the loader.
    expect(log.some((v) => /^\d+s$/.test(v))).toBe(true);
    expect(log.some((v) => /^\d+ MB · \d+s$/.test(v))).toBe(true);
    // And it stops: the last thing it does is clear itself, not count forever.
    expect(log[log.length - 1]).toBe('');
  });

  test('accessibility: keyboard reaches the primary nav with real, named links', async ({ page }) => {
    await waitReady(page);
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => document.activeElement?.tagName)).toBeTruthy();
    await expect(page.getByRole('link', { name: 'Operational Graph' })).toHaveAttribute('href', /#\/fleet/);
  });
});
