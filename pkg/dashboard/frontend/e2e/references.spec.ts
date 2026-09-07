import { test, expect, type Page } from '@playwright/test';

// Every contract relationship the backend can resolve to a canonical Pacto entity is
// navigable from the UI where that relationship is rendered -- and every one it CANNOT
// resolve says so instead of guessing.
//
// The legacy dashboard made remote configuration and policy refs clickable by splitting
// the ref string, which is how a reference to a name two registries both publish reached
// the wrong contract. The product IA resolves refs the only correct way: the fleet engine
// needs immutable content-identity evidence -- a digest pinned in the ref, or a
// pacto.lock entry recording one -- and links only to what it actually resolved.
//
// The demo fixtures deliberately carry NEITHER. Their bundles are not published at the
// registries their refs name, and a synthesized lock digest would be a claim about
// registry content that is not true; so the demo authors no locks, and every remote
// config/policy ref in it is genuinely unresolvable. That makes this file a fail-closed
// suite: it proves the UI keeps the authored ref, states that the destination is unknown
// and fabricates no link. The adversarial row below is the point -- the demo publishes a
// platform-app-config in TWO domains, and neither is allowed to be guessed at.
//
// The positive path -- a ref WITH content-identity evidence resolving to a ServiceKey in
// the referring revision's own domain, partner domain included -- is covered against the
// engine in pkg/fleet/refresolution_test.go. No browser fixture can reach it without
// asserting a registry digest the demo does not have.

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

// Each row is one authored ref in the demo, and `why` is what it would take to link it
// somewhere wrongly. The partner rows are adversarial: `platform-app-config` exists in
// both domains and `platform-http-policy` in only one, so basename inference would send
// the first to the wrong contract and the second to a contract in a domain that never
// published it.
const UNRESOLVABLE = [
  {
    revision: 'payments-service',
    ref: 'oci://ghcr.io/trianalab/pacto/platform-app-config',
    why: 'a configuration ref whose name a service in this same domain does publish',
  },
  {
    revision: 'payments-service',
    ref: 'oci://ghcr.io/trianalab/pacto/platform-http-policy',
    why: 'a policy ref whose name a service in this same domain does publish',
  },
  {
    revision: 'partners/settlement-service',
    ref: 'oci://partners.acme.com/pacto/platform-app-config',
    why: 'a ref to a name TWO domains publish, so a guess has a wrong answer to reach',
  },
  {
    revision: 'partners/settlement-service',
    ref: 'oci://partners.acme.com/pacto/platform-http-policy',
    why: 'a ref to a name only the OTHER domain publishes',
  },
];

test.describe('contract references are navigable', () => {
  for (const { revision, ref, why } of UNRESOLVABLE) {
    test(`${ref} carries no resolved identity, so it links nowhere: ${why}`, async ({ page }) => {
      await boot(page);
      await openLatestRevision(page, revision);

      const row = reference(page, ref.replace('oci://', ''));
      await expect(row).toBeVisible({ timeout: T });
      // The authored ref is contract information: not resolving it never drops it.
      await expect(row).toContainText(ref);
      await expect(row).toContainText('Unresolved');
      await expect(row.locator('a.entity-link')).toHaveCount(0);
    });
  }

  test('a service nothing resolvably references says so rather than listing a guess', async ({ page }) => {
    await boot(page);

    // Both domains publish a platform-app-config, and payments-service authors a ref to
    // the name. Without content-identity evidence the reverse index has nothing to record
    // -- and must record nothing rather than the consumer whose ref merely looks right.
    for (const key of ['platform-app-config', 'partners/platform-app-config']) {
      await openService(page, key);
      const section = page.locator('section', { has: page.getByRole('heading', { name: 'Referenced by' }) });
      await expect(section).toContainText("No service references this service's configuration or policy.", { timeout: T });
      await expect(section.locator('a.entity-link')).toHaveCount(0);
    }
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
