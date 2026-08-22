import { test, expect, type Page } from '@playwright/test';

// The first-time-user acceptance suite for the product-coherence migration, run in a
// real browser against the built WASM demo (real Svelte bundle + the real dashboard API
// compiled to wasm). The persona is a platform engineer who understands services and
// Kubernetes but has never heard of Pacto: every journey below is a question that
// persona actually has, and the assertions are about ROUTES, CONCEPTUAL LABELS,
// CANONICAL IDENTITY, ABSENCE OF LEGACY SCREENS and WORKFLOW CONTINUITY -- never exact
// marketing copy, which would make the suite brittle without making it stricter.
//
// The other specs cover mechanics (deep links, history, a11y, responsiveness). This one
// covers whether the product reads as ONE product.

const T = 20_000;

// Root markers of the four legacy screens. Each is unique to a legacy view: .page-header
// (ReadinessView, OwnersView), .list-header (ServiceListView), .graph-header
// (GraphPageView) and nav.breadcrumb (DiffView, whose product counterpart is
// nav.breadcrumbs). If any is in the DOM on a Fleet host, a legacy island survived.
const LEGACY_MARKERS = '.page-header, .list-header, .graph-header, nav.breadcrumb';

async function expectNoLegacyScreen(page: Page) {
  await expect(page.locator(LEGACY_MARKERS)).toHaveCount(0);
}

async function boot(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: T });
}

async function openPaymentsService(page: Page) {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { level: 1, name: 'Services' })).toBeVisible({ timeout: T });
  // By canonical key, not by label: the demo publishes same-named services in two
  // domains, so a visible name is not an identity.
  await page.locator('.sv-item a.entity-link[href$="/fleet/services/payments-service"]').first().click();
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
}

test.describe('novice journey — a first-time user reads ONE product', () => {
  // J1. "Where am I, and what can I do here?" The app opens on state, and the primary
  // nav teaches one order: state -> inventory -> relationships -> change.
  test('J1: the app opens on operational state with exactly four primary workflows', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveURL(/#\/fleet$/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: 'Operational overview' })).toBeVisible({ timeout: T });
    const nav = page.getByRole('navigation', { name: 'Primary' }).first();
    await expect(nav.getByRole('link')).toHaveText(['Overview', 'Services', 'Operational graph', 'Change analysis']);
    await expectNoLegacyScreen(page);
  });

  // J2. "What is wrong right now?" Triage is reachable in one click and its reasons are
  // written in words, not in the backend's enum slugs.
  test('J2: attention triage is one click away and its categories are plain language', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet');
    await page.getByRole('link', { name: /need attention/ }).click();
    await expect(page).toHaveURL(/#\/fleet\/attention/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: T });
    // The category control offers readable names; the raw wire values stay in the URL.
    const options = await page.getByLabel('Filter by category').locator('option').allTextContents();
    expect(options).toContain('Not compliant');
    expect(options).toContain('Stale evidence');
    expect(options.join(' ')).not.toContain('non-compliant');
    await expectNoLegacyScreen(page);
  });

  // J3. "Is anything not ready?" Readiness is a DIMENSION of triage, not a fifth
  // destination, and the legacy Readiness screen is unreachable on a Fleet host.
  test('J3: readiness is a triage dimension, reachable from the overview, never a legacy screen', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet');
    await page.getByRole('navigation', { name: 'Filter attention by category' })
      .getByRole('link', { name: 'Readiness gate' }).click();
    await expect(page).toHaveURL(/#\/fleet\/attention\?category=readiness/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: 'Needs attention' })).toBeVisible({ timeout: T });
    // The old bookmark lands on the revision inventory rather than mounting a second
    // definition. Readiness is declared BY a contract revision and assessed against the
    // threshold that revision set for itself, so the population it describes is the
    // revisions -- the attention list is the queue of things to do about them, which is
    // a different question and was the wrong home for the word.
    await page.goto('/#/readiness');
    await expect(page).toHaveURL(/#\/fleet\/revisions$/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: 'Contract revisions' })).toBeVisible({ timeout: T });
    await expectNoLegacyScreen(page);
  });

  // J4. "What is out there?" Inventory from the primary nav, navigable to an entity.
  test('J4: the inventory is reachable from the nav and a row opens a real entity', async ({ page }) => {
    await boot(page);
    await page.getByRole('navigation', { name: 'Primary' }).first().getByRole('link', { name: 'Services' }).click();
    await expect(page).toHaveURL(/#\/fleet\/services/, { timeout: T });
    await page.locator('.sv-item a.entity-link').first().click();
    await expect(page).toHaveURL(/#\/fleet\/services\//, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
    await expectNoLegacyScreen(page);
  });

  // J5. "What is this service, and where does it run?" Pacto observes where a revision
  // runs; it does not deploy. The page must never NAME that a Deployment.
  //
  // The vocabulary contract (section 0b) draws the line precisely: "Deployment" means the
  // Kubernetes kind and nothing else, and the Kubernetes kind is part of a canonical
  // TargetKey ("prod/Deployment/payments-service"). So the word is allowed to appear
  // inside an identity and nowhere else -- never as a heading, a label, a nav item or a
  // column header. Scanning for the bare word would forbid the identity too, and would
  // only pass while the demo fixture happened to use a placeholder kind.
  test('J5: a service page says "Operational targets", and calls nothing a Deployment', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await expect(page.getByRole('heading', { name: 'Operational targets' })).toBeVisible({ timeout: T });

    const misuses = await page.evaluate(() => {
      const WORD = /\bDeployments?\b/;
      // A canonical TargetKey is "<scope>/<Kind>/<name>"; the kind sits between slashes.
      const INSIDE_A_KEY = /\/Deployment\//;
      const out: string[] = [];
      const walker = document.createTreeWalker(document.querySelector('main')!, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        const text = (n.textContent || '').trim();
        if (WORD.test(text) && !INSIDE_A_KEY.test(text)) out.push(text);
      }
      return out;
    });
    expect(misuses, `"Deployment" used as product wording, not as a Kubernetes kind: ${misuses.join(' | ')}`).toEqual([]);
    await expectNoLegacyScreen(page);
  });

  // J6. "Do you actually know what is running there?" The two identity dimensions are
  // labelled separately, so "we matched the revision" is never confused with "we can
  // fetch its contract".
  test('J6: an operational target separates revision match from content retrievability', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await page.locator('a.entity-link[href*="/fleet/targets/"]').first().click();
    await expect(page).toHaveURL(/#\/fleet\/targets\//, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: /^Operational target: / })).toBeVisible({ timeout: T });
    await expect(page.getByText('Revision match', { exact: true })).toBeVisible({ timeout: T });
    await expect(page.getByText('Content', { exact: true })).toBeVisible({ timeout: T });
    await expectNoLegacyScreen(page);
  });

  // J7. "This says the revision is 'ready' -- ready by whose measurement?" Readiness is
  // an authored self-assessment; the page must say so rather than let a novice read it
  // as a runtime health check.
  test('J7: a revision frames readiness as declared, not measured', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await page.locator('a.entity-link[href*="/fleet/revisions/"]').first().click();
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
    const body = await page.locator('main').innerText();
    if (/readiness/i.test(body)) expect(body).toMatch(/declared/i);
    await expectNoLegacyScreen(page);
  });

  // J8. "What else does this touch?" The entity page continues into the graph focused on
  // that exact entity, and the focus is in the URL so it can be shared.
  test('J8: an entity continues into a graph focused on itself, and the focus is shareable', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await page.getByRole('link', { name: 'Open in graph' }).click();
    await expect(page).toHaveURL(/#\/fleet\/graph\/service\//, { timeout: T });
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: T });
    const url = page.url();
    await page.reload();
    await expect(page).toHaveURL(url);
    await expect(page.getByTestId('neighborhood-canvas')).toBeVisible({ timeout: T });
    await expectNoLegacyScreen(page);
  });

  // J9. "Will this dump the whole estate on me?" The graph tab opens search-first and
  // says so, so an empty canvas is never mistaken for a broken page.
  test('J9: the graph opens search-first, never a whole-fleet hairball', async ({ page }) => {
    await boot(page);
    await page.getByRole('navigation', { name: 'Primary' }).first().getByRole('link', { name: 'Operational Graph' }).click();
    await expect(page).toHaveURL(/#\/fleet(\/graph)?$/, { timeout: T });
    await expect(page.getByTestId('graph-discovery')).toBeVisible({ timeout: T });
    await expect(page.getByTestId('graph-discovery-placeholder')).toBeVisible({ timeout: T });
    await expect(page.getByTestId('neighborhood-canvas')).toHaveCount(0);
    await expectNoLegacyScreen(page);
  });

  // J10. "What changed, and what does that change break?" ONE workspace answers both,
  // entered from the service the user is already looking at, and its answer is a
  // shareable URL rather than an un-linkable screen state.
  test('J10: comparing revisions answers both halves in one workspace, shareably', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await page.getByRole('link', { name: 'Compare revisions' }).click();
    await expect(page).toHaveURL(/#\/fleet\/changes\//, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: 'Change analysis' })).toBeVisible({ timeout: T });
    await page.locator('#impact-old-rev').selectOption({ label: 'payments-service 1.0.0' });
    await page.locator('#impact-new-rev').selectOption({ label: 'payments-service 2.0.0' });
    await page.getByRole('button', { name: /Compare revisions/ }).click();
    await expect(page.getByTestId('changes-what-changed')).toBeVisible({ timeout: T });
    await expect(page.getByTestId('changes-what-it-affects')).toBeVisible({ timeout: T });
    // The analyzed pair is in the URL, so reloading restores the same answer.
    await expect(page).toHaveURL(/old=.+&new=/, { timeout: T });
    const url = page.url();
    await page.reload();
    await expect(page).toHaveURL(url);
    await expect(page.getByTestId('changes-what-changed')).toBeVisible({ timeout: T });
    await expectNoLegacyScreen(page);
  });

  // J11. "Where did these numbers come from, and can I trust them?" Sources are the
  // ingestion seam, not collectors, and a degraded one must read as INCOMPLETE rather
  // than as a healthy zero.
  test('J11: data sources are named as such and explain that degraded is not zero', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet');
    await page.getByRole('link', { name: /View all data sources/ }).click();
    await expect(page).toHaveURL(/#\/fleet\/sources$/, { timeout: T });
    await expect(page.getByRole('heading', { level: 1, name: 'Data sources' })).toBeVisible({ timeout: T });
    const body = await page.locator('main').innerText();
    expect(body).not.toMatch(/collector/i);
    expect(body).toMatch(/incomplete/i);
    await page.locator('.lv-item a.entity-link').first().click();
    await expect(page.getByRole('heading', { level: 1, name: /^Data source: / })).toBeVisible({ timeout: T });
    await expectNoLegacyScreen(page);
  });

  // J12. "My teammate sent me an old link." Every legacy route with a product equivalent
  // canonicalizes into the product IA; none of them mounts a second, older-looking UI.
  test('J12: every legacy bookmark canonicalizes into the product IA, with no legacy screen', async ({ page }) => {
    await boot(page);
    for (const [legacy, expected] of [
      ['/#/', /#\/fleet$/],
      ['/#/services', /#\/fleet\/services$/],
      ['/#/graph', /#\/fleet\/graph$/],
      ['/#/owners', /#\/fleet\/owners$/],
      // Readiness is a property of a contract revision, so the legacy screen's URL
      // canonicalizes onto the revision inventory that carries the readiness figure.
      ['/#/readiness', /#\/fleet\/revisions$/],
      ['/#/diff', /#\/fleet\/changes/],
      ['/#/fleet/impact/payments-service', /#\/fleet\/changes\//],
    ] as [string, RegExp][]) {
      await page.goto(legacy);
      await expect(page, `legacy ${legacy} should canonicalize`).toHaveURL(expected, { timeout: T });
      await expectNoLegacyScreen(page);
    }
  });
});
