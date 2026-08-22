import { test, expect, type Page } from '@playwright/test';

// Every contract relationship the backend can resolve to a canonical Pacto entity is
// navigable from the UI where that relationship is rendered.
//
// The legacy dashboard made remote configuration and policy refs clickable by splitting
// the ref string. The product IA lost that, then got it back the only correct way: the
// fleet engine resolves the reference to a canonical ServiceKey inside the referring
// revision's own DOMAIN, and the UI links to what the backend resolved. Nothing here is
// inferred from a label, so the adversarial case below is the point of the whole design:
// the demo publishes a platform-app-config in TWO domains, and a reference to that name
// must reach the one in the referring revision's domain and never the other.

const T = 20_000;

async function boot(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Operational Graph' })).toBeVisible({ timeout: T });
}

// Open a service by its canonical key -- never by visible name, which two services share.
async function openService(page: Page, key: string) {
  await page.goto(`/#/fleet/services/${encodeURIComponent(key)}`);
  await expect(page.getByRole('heading', { level: 1, name: /^Service: / })).toBeVisible({ timeout: T });
}

async function openLatestRevision(page: Page, key: string) {
  await openService(page, key);
  await page.locator('a.entity-link[href*="/fleet/revisions/"]').first().click();
  await expect(page.getByRole('heading', { level: 1, name: /^Revision: / })).toBeVisible({ timeout: T });
}

// The one reference row whose authored ref contains `refFragment`.
function reference(page: Page, refFragment: string) {
  return page.locator('.cr').filter({ hasText: refFragment }).first();
}

test.describe('contract references are navigable', () => {
  test('a configuration reference keeps its authored ref and links to the resolved service', async ({ page }) => {
    await boot(page);
    await openLatestRevision(page, 'payments-service');

    const cfg = reference(page, 'ghcr.io/trianalab/pacto/platform-app-config');
    await expect(cfg).toBeVisible({ timeout: T });
    // The authored ref is contract information: resolution never replaces it.
    await expect(cfg).toContainText('oci://ghcr.io/trianalab/pacto/platform-app-config');

    const link = cfg.locator('a.entity-link');
    await expect(link).toHaveAttribute('href', '#/fleet/services/platform-app-config');
    await link.click();
    await expect(page.getByRole('heading', { level: 1, name: 'Service: platform-app-config' })).toBeVisible({ timeout: T });
  });

  test('a policy reference links to the resolved service too', async ({ page }) => {
    await boot(page);
    await openLatestRevision(page, 'payments-service');

    const pol = reference(page, 'ghcr.io/trianalab/pacto/platform-http-policy');
    await expect(pol).toBeVisible({ timeout: T });
    await expect(pol).toContainText('oci://ghcr.io/trianalab/pacto/platform-http-policy');

    const link = pol.locator('a.entity-link');
    await expect(link).toHaveAttribute('href', '#/fleet/services/platform-http-policy');
    await link.click();
    await expect(page.getByRole('heading', { level: 1, name: 'Service: platform-http-policy' })).toBeVisible({ timeout: T });
  });

  test('a reference to a name TWO domains use resolves inside its own domain', async ({ page }) => {
    await boot(page);
    await openLatestRevision(page, 'partners/settlement-service');

    const cfg = reference(page, 'partners.acme.com/pacto/platform-app-config');
    await expect(cfg).toBeVisible({ timeout: T });

    // Both domains publish a platform-app-config. This revision must reach the PARTNER
    // one; linking to the default-domain one would be a cross-domain identity failure.
    const link = cfg.locator('a.entity-link');
    await expect(link).toHaveAttribute('href', '#/fleet/services/partners%2Fplatform-app-config');
    await link.click();
    await expect(page.getByRole('heading', { level: 1, name: 'Service: platform-app-config' })).toBeVisible({ timeout: T });
    await expect(page).toHaveURL(/partners%2Fplatform-app-config$/);
    // The page identifies itself as the partner one, not merely by its shared name.
    await expect(page.locator('main')).toContainText('partners');
  });

  test('a reference that resolves to nothing says so and fabricates no service', async ({ page }) => {
    await boot(page);
    await openLatestRevision(page, 'partners/settlement-service');

    // The partners domain publishes no http policy bundle, so this ref leads nowhere --
    // and must NOT fall through to the default domain's platform-http-policy.
    const pol = reference(page, 'partners.acme.com/pacto/platform-http-policy');
    await expect(pol).toBeVisible({ timeout: T });
    await expect(pol).toContainText('oci://partners.acme.com/pacto/platform-http-policy');
    await expect(pol).toContainText('Unresolved');
    await expect(pol.locator('a.entity-link')).toHaveCount(0);
  });

  test('the referenced service lists who references it, per domain', async ({ page }) => {
    await boot(page);

    await openService(page, 'platform-app-config');
    const core = page.locator('section', { has: page.getByRole('heading', { name: 'Referenced by' }) });
    await expect(core).toContainText('payments-service', { timeout: T });
    // The reverse direction is domain-scoped in both directions: the partner consumer
    // must not appear here.
    await expect(core.locator('a.entity-link[href*="partners%2F"]')).toHaveCount(0);

    await openService(page, 'partners/platform-app-config');
    const partner = page.locator('section', { has: page.getByRole('heading', { name: 'Referenced by' }) });
    const links = partner.locator('a.entity-link');
    await expect(links).toHaveCount(1, { timeout: T });
    await expect(links.first()).toHaveAttribute('href', '#/fleet/services/partners%2Fsettlement-service');
    await links.first().click();
    await expect(page).toHaveURL(/partners%2Fsettlement-service$/);
  });

  test('a declared dependency is navigable from the revision that declares it', async ({ page }) => {
    await boot(page);
    await openLatestRevision(page, 'payments-service');

    const dep = page.locator('a.entity-link[href$="/fleet/services/postgresql"]').first();
    await expect(dep).toBeVisible({ timeout: T });
    await dep.click();
    await expect(page.getByRole('heading', { level: 1, name: 'Service: postgresql' })).toBeVisible({ timeout: T });
  });
});
