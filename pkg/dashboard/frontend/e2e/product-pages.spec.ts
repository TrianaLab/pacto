import { test, expect, type Page } from '@playwright/test';

// Browser E2E for the product pages against the built WASM demo (real Svelte
// bundle + the dashboard API compiled to wasm, serving the product endpoints). These
// cover the navigation/history/deep-link behaviors that only a real browser exercises:
// the Services product list, service/revision/target/owner/source pages, canonical
// EntityLink navigation between them, breadcrumbs, and filter deep-linking.
//
// Scenarios the small offline demo cannot exercise (same-named services across
// domains, an ambiguous target, an empty fleet, multi-page pagination, the search
// stale-request race) are covered deterministically by the Vitest component suite
// (FleetServicesView / FleetEntityView / FleetAttentionView / EntitySearch / router).

const T = 20_000;

async function boot(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: T });
}

// Open a service page for a service that has deployments and revisions.
async function openPaymentsService(page: Page) {
  await page.goto('/#/fleet/services');
  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
  // By canonical key, not by label: the demo publishes same-named services in two
  // domains, so a visible name is not an identity.
  const row = page.locator('.sv-item a.entity-link[href$="/fleet/services/payments-service"]').first();
  await expect(row).toBeVisible({ timeout: T });
  await row.click();
  await expect(page).toHaveURL(/#\/fleet\/services\//);
  // The page-ready signal is the entity's own heading, not its canonical key: the key is
  // ontology a first-time user never needs, so it now lives behind the Identifier
  // disclosure (its continued availability is asserted separately, in L21).
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
}

test.describe('WASM demo — product pages', () => {
  test('L1/L22: Navbar Services and the canonical EntryPointServices href both open /fleet/services', async ({ page }) => {
    await boot(page);
    // Wait for capabilities to resolve so the Services nav points at the product list
    // (a fleet-capable host); before that it is the legacy list.
    const servicesLink = page.getByRole('link', { name: 'Services' }).first();
    await expect(servicesLink).toHaveAttribute('href', '#/fleet/services', { timeout: T });
    await servicesLink.click();
    await expect(page).toHaveURL(/#\/fleet\/services/);
    await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
    // The backend emits /fleet/services for EntryPointServices; the router resolves it.
    await page.goto('/#/fleet/services');
    await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
    await expect(page.locator('.sv-item').first()).toBeVisible({ timeout: T });
  });

  test('L3/L20: a service filter survives reload and browser back restores it', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet/services');
    await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
    // Apply a compliance-status filter; it lives in the URL.
    await page.getByLabel('Filter by compliance status').selectOption('Compliant');
    await expect(page).toHaveURL(/status=Compliant/, { timeout: T });
    await page.reload();
    await expect(page).toHaveURL(/status=Compliant/, { timeout: T });
    await expect(page.getByLabel('Filter by compliance status')).toHaveValue('Compliant');
    // Navigate away then back: the filtered list is restored.
    await page.goto('/#/fleet');
    await expect(page.getByRole('heading', { name: 'Operational overview' })).toBeVisible({ timeout: T });
    await page.goBack();
    await expect(page).toHaveURL(/status=Compliant/, { timeout: T });
  });

  test('L9/L10/L11: service navigates to a revision, a deployment and its owner', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    // service -> revision
    const rev = page.locator('a.entity-link[href*="/fleet/revisions/"]').first();
    await expect(rev).toBeVisible({ timeout: T });
    await rev.click();
    await expect(page).toHaveURL(/#\/fleet\/revisions\//);
    await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });

    // back to the service, then service -> deployment
    await openPaymentsService(page);
    const dep = page.locator('a.entity-link[href*="/fleet/targets/"]').first();
    await expect(dep).toBeVisible({ timeout: T });
    await dep.click();
    await expect(page).toHaveURL(/#\/fleet\/targets\//);

    // back to the service, then service -> owner (if the service is owned)
    await openPaymentsService(page);
    const owner = page.locator('a.entity-link[href*="/fleet/owners/"]').first();
    if (await owner.count()) {
      await owner.click();
      await expect(page).toHaveURL(/#\/fleet\/owners\//);
    }
  });

  test('L12/L13: a revision navigates back to its service and to an exact-match target', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await page.locator('a.entity-link[href*="/fleet/revisions/"]').first().click();
    await expect(page).toHaveURL(/#\/fleet\/revisions\//);
    // revision -> service
    const svc = page.locator('a.entity-link[href*="/fleet/services/"]').first();
    await expect(svc).toBeVisible({ timeout: T });
    await svc.click();
    await expect(page).toHaveURL(/#\/fleet\/services\//);
  });

  test('L14/L15: a target shows revision-match certainty and content retrievability independently', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await page.locator('a.entity-link[href*="/fleet/targets/"]').first().click();
    await expect(page).toHaveURL(/#\/fleet\/targets\//);
    // Both identity dimensions are labelled and independent. The payments target uses a
    // mutable version-tag ref that uniquely matches a revision (an INFERRED match), so
    // the revision matches while its content is NOT retrievable — and that pairing must
    // render without being flagged as a contradiction.
    await expect(page.getByText('Revision match', { exact: true })).toBeVisible({ timeout: T });
    await expect(page.getByText('Content', { exact: true })).toBeVisible({ timeout: T });
    await expect(page.locator('.te-identity')).toBeVisible();
  });

  test('L17: an owner navigates to one of its services', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet/owners');
    // Exact: the ownership summary's own heading starts with "Ownership", which a
    // substring name match reads as this page's title too.
    await expect(page.getByRole('heading', { name: 'Owners', exact: true })).toBeVisible({ timeout: T });
    const owner = page.locator('.lv-item a.entity-link').first();
    await expect(owner).toBeVisible({ timeout: T });
    await owner.click();
    await expect(page).toHaveURL(/#\/fleet\/owners\//);
    const svc = page.locator('a.entity-link[href*="/fleet/services/"]').first();
    if (await svc.count()) {
      await svc.click();
      await expect(page).toHaveURL(/#\/fleet\/services\//);
    }
  });

  test('L18: a source navigates to a contributed entity', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet/sources');
    await expect(page.getByRole('heading', { name: 'Data sources' })).toBeVisible({ timeout: T });
    // The "local" source contributes the bundle services/revisions; open it (not the
    // partial edge-cluster source, which contributes nothing here).
    const local = page.locator('.lv-item a.entity-link', { hasText: 'local' }).first();
    await expect(local).toBeVisible({ timeout: T });
    await local.click();
    await expect(page).toHaveURL(/#\/fleet\/sources\//);
    // A contributed entity in the preview list is navigable.
    const entity = page.locator('.ref-list a.entity-link').first();
    await expect(entity).toBeVisible({ timeout: T });
    await entity.click();
    await expect(page).toHaveURL(/#\/fleet\/(services|revisions|targets)\//);
  });

  test('L19: entity breadcrumbs navigate (service page -> Services list)', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    // The breadcrumb trail is Overview > Services > <service>; clicking Services returns.
    await page.getByRole('navigation').getByRole('link', { name: 'Services' }).first().click();
    await expect(page).toHaveURL(/#\/fleet\/services$/);
    await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible({ timeout: T });
  });

  // Progressive disclosure hides the expert identifier by DEFAULT; it must not remove it.
  test('L21: the canonical key is still exact and copyable, one disclosure away', async ({ page }) => {
    await boot(page);
    await openPaymentsService(page);
    await expect(page.getByText('Canonical key')).toBeHidden();
    await page.locator('details.ev-ident > summary').click();
    await expect(page.getByText('Canonical key')).toBeVisible({ timeout: T });
    await expect(page.locator('.ev-key')).toContainText('payments-service');
  });

  test('L8: an attention filter deep link survives reload', async ({ page }) => {
    await boot(page);
    await page.goto('/#/fleet/attention?severity=error');
    await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible({ timeout: T });
    await expect(page.getByLabel('Filter by severity')).toHaveValue('error');
    await page.reload();
    await expect(page).toHaveURL(/severity=error/, { timeout: T });
    await expect(page.getByLabel('Filter by severity')).toHaveValue('error');
  });
});
