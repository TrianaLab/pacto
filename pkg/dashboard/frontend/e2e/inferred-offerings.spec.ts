import { test, expect, type Page } from '@playwright/test';
import { boot } from './typographyChecks';
import { XBUCKET } from './layoutGate';

/**
 * Inferred offerings (515-S4), in the demo: the production cluster's operator maps two
 * XRDs to inferred offerings, xbucket and objectstore (examples/demo/source_offering.go).
 * The Offerings list keeps them behind one checkbox, muted with one mark each; xbucket's
 * page plans its acceptance as a declared offering and signs nothing; objectstore was
 * accepted at generation 3 and its XRD has changed since, so its provider drifted.
 */
const T = 30_000;
const PRODUCER = 'crossplane-5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11';

test('the inferred checkbox lists the inferred offerings muted, each with one mark, and survives a reload', async ({ page }) => {
  await boot(page, '#/fleet/offerings');
  const filter = page.getByTestId('offering-inferred-filter');
  await expect(filter).not.toBeChecked({ timeout: T });
  // The declared list counts them apart and links the inferred list.
  await expect(page.getByTestId('offering-inferred-total')).toHaveText('+2 inferred');
  await filter.check();
  await expect(page).toHaveURL(/[?&]inferred=1/);
  await expect(page.getByText('Shows: inferred')).toBeVisible();
  const rows = page.getByTestId('offering-list').locator('tbody tr.ot-inferred');
  await expect(rows).toHaveCount(2, { timeout: T });
  await expect(page.getByTestId('offering-list').locator('tbody tr:not(.ot-inferred)')).toHaveCount(0);
  const xbucket = rows.filter({ hasText: 'xbucket' });
  await expect(xbucket).toContainText('Inferred from CompositeResourceDefinition xbuckets.storage.acme.io, classes large, small; accept to make it consumable');
  // objectstore was accepted already: its row names the declared offering, never the accept hint.
  const objectstore = rows.filter({ hasText: 'xobjectstores' });
  await expect(objectstore).toContainText('; accepted as');
  await expect(objectstore).not.toContainText('accept to make it consumable');
  await expect(objectstore.getByTestId('offering-inferred-accepted').locator('a.entity-link')).toHaveText(/objectstore/);
  // Muted through the text token, never a fade.
  const style = await xbucket.evaluate((el) => { const s = getComputedStyle(el); return { opacity: s.opacity, filter: s.filter }; });
  expect(style).toEqual({ opacity: '1', filter: 'none' });
  const mark = xbucket.getByTestId('offering-inferred');
  await expect(mark).toHaveCount(1);
  await mark.getByRole('button', { name: `Inferred offering: from ${PRODUCER}` }).click();
  const tip = mark.locator('.prov-tip');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('from CompositeResourceDefinition xbuckets.storage.acme.io generation 2');
  await expect(tip).toContainText('never counts as declared');
  // The deep link is the state.
  await page.reload();
  await expect(page.getByTestId('offering-inferred-filter')).toBeChecked({ timeout: T });
  await expect(page.getByTestId('offering-list').locator('tbody tr.ot-inferred')).toHaveCount(2, { timeout: T });
});

test('an inferred offering opens its page, whose Accept panel plans a declared offering and signs nothing', async ({ page }) => {
  await boot(page, '#/fleet/offerings?inferred=1');
  const row = page.getByTestId('offering-list').locator('tbody tr.ot-inferred').filter({ hasText: 'xbucket' });
  await row.locator('a.entity-link').click();
  await expect(page).toHaveURL(/xbucket/);
  // The page's Accept action opens its Accept fold, planned only once open.
  await page.getByRole('link', { name: 'Accept', exact: true }).click();
  await expect(page).toHaveURL(/[?&]declare=1/);
  const fold = page.getByTestId('offering-accept');
  await expect(fold).toHaveAttribute('open', '', { timeout: T });
  const panel = fold.getByTestId('declare');
  await panel.getByTestId('offering-version').fill('1.0.0');
  await panel.getByTestId('offering-plan').click();
  // A first acceptance names its owner: refused with the command.
  await expect(panel.getByTestId('refusal')).toContainText('name the team that owns xbucket', { timeout: T });
  await expect(panel.getByTestId('declare-refusal-command')).toContainText('pacto declare offering:xbucket');
  await panel.getByTestId('offering-owner').fill('team-storage');
  await panel.getByTestId('offering-plan').click();
  await expect(panel.getByTestId('declare-signer')).toContainText('You do not sign here', { timeout: T });
  await expect(panel.getByTestId('declare-command')).toContainText(`pacto declare offering:xbucket --from oci://ghcr.io/acme/pacto-inferred/${PRODUCER}/services@sha256:`);
  await expect(panel.getByTestId('declare-command')).toContainText('--version 1.0.0 --owner team-storage --intent sha256:');
  await panel.getByTestId('declare-record').locator('> summary').click();
  await expect(panel.getByTestId('declare-record')).toContainText(`inferredFrom: offering:${PRODUCER}/xbucket@sha256:`);
  await expect(panel.getByTestId('declare-no-draft')).toBeVisible();
  await expect(panel.getByTestId('declare-save-draft')).toHaveCount(0);
});

/** Answers every plan with the refusal of an offering whose parameters are open. */
async function openSchema(page: Page) {
  await page.addInitScript(() => {
    type Serve = (m: string, p: string, b: string | null, h?: object) => Promise<{ status: number; body: string; contentType: string }>;
    let real: Serve | null = null;
    const wrapped: Serve = async (m, p, b, h) => (m === 'POST' && p.split('?')[0] === '/api/fleet/declare'
      ? { status: 200, contentType: 'application/json', body: JSON.stringify({ signing: { mode: 'none', reason: 'the demo has no sign-in' }, write: { available: true },
        refusal: { code: 'OfferingSchemaOpen', message: "the offering's parameters are not a closed space, open at provisioning: schema.properties.extra, provisioning: schema.properties.tags: close each path in a folder (additionalProperties: false, a type, a class enum) and accept it with --dir",
          command: 'pacto declare offering:xbucket --dir ./xbucket --registry demo.pacto.local --version 1.0.0' } }) }
      : (real as Serve)(m, p, b, h));
    Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
  });
}

test('an open schema is refused naming each open path with the next command', async ({ page }) => {
  await openSchema(page);
  await boot(page, `${XBUCKET}?declare=1`);
  const panel = page.getByTestId('declare');
  await panel.getByTestId('offering-version').fill('1.0.0');
  await panel.getByTestId('offering-plan').click();
  const paths = panel.getByTestId('offering-open-paths').locator('li');
  await expect(paths).toHaveText(['provisioning: schema.properties.extra', 'provisioning: schema.properties.tags'], { timeout: T });
  await expect(panel.getByTestId('declare-refusal-command')).toContainText('pacto declare offering:xbucket --dir ./xbucket');
});

test('the accepted objectstore offering says its source object changed since', async ({ page }) => {
  await boot(page, '#/fleet/offerings/objectstore');
  await expect(page.getByTestId('provider-drift')).toContainText('source object changed since accepted (now generation 4)', { timeout: T });
});
