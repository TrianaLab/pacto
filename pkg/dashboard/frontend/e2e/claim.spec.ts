import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';

// Claim an external system from its page (517-S5), against the built WASM demo. The
// demo's declarer plans the claim, signs it with the tab's simulated sign-in and folds
// the claimed revision into the tab's fleet, so the Owner block reads claimed after
// Publish. Nothing reaches a registry and no published demo bundle changes.

const T = 30_000;

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

type Item = { code: string; label: string; entity: { key: string } };

test.describe('claim an external system', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('the unowned list ranks unclaimed external systems by how many services use them', async ({ page }) => {
    await appReady(page);
    const items = await page.evaluate(async () => {
      const serve = (window as unknown as { __pactoServe: (m: string, p: string, b: null, h: object) => Promise<{ body: string }> }).__pactoServe;
      const r = await serve('GET', '/api/fleet/attention?category=unowned&limit=500', null, {});
      return (JSON.parse(r.body).items as Item[]).filter((i) => i.code === 'UNOWNED_EXTERNAL');
    });
    const keys = items.map((i) => i.entity.key);
    for (const k of ['stripe', 'twilio', 'parcel-carrier', 'llm-provider']) expect(keys, `${k} is routed`).toContain(k);
    // Highest consumer count first, ties by label: the label carries the count.
    const count = (i: Item) => Number(/: (\d+) services? uses? it$/.exec(i.label)?.[1] ?? NaN);
    for (let i = 1; i < items.length; i++) {
      const [a, b] = [items[i - 1], items[i]];
      expect(count(a) > count(b) || (count(a) === count(b) && a.label <= b.label), `${a.label} before ${b.label}`).toBe(true);
    }
    expect(items.find((i) => i.entity.key === 'twilio')?.label).toBe('Twilio: 1 service uses it');

    // The list renders them in the same order.
    await page.goto('/#/fleet/attention?category=unowned');
    const rows = page.getByTestId('fleet-map-rail-item').filter({ hasText: 'External system has no owner' });
    await expect(rows).toHaveCount(items.length, { timeout: T });
    // Each row starts with the system's name, the label before its count.
    const names = items.map((i) => i.label.replace(/: \d+ services? uses? it$/, ''));
    const texts = (await rows.allTextContents()).map((t) => t.trim());
    expect(texts.map((t) => names.findIndex((n) => t.startsWith(n)))).toEqual(names.map((_, i) => i));
  });

  test('a person claims stripe: the owner is required, the plan links external:stripe and signing claims it', async ({ page }) => {
    const errors = watchConsole(page);
    await appReady(page);
    // A person signs a claim; the agent the demo starts as only drafts.
    const err = await page.evaluate(() => (window as unknown as { __pactoTours: { setPersona: (n: string, f: boolean) => Promise<string> } }).__pactoTours.setPersona('alice', false));
    expect(err).toBe('');
    await page.goto('/#/fleet/externals/stripe');
    const owner = page.getByTestId('external-owner');
    await expect(owner.getByTestId('external-owner-unclaimed')).toContainText('Unclaimed', { timeout: T });
    await owner.getByTestId('external-claim').click();

    const panel = page.getByTestId('declare');
    await panel.getByTestId('claim-plan').click();
    await expect(panel.getByRole('alert')).toHaveText('Name the team that owns it');
    await panel.getByTestId('claim-owner').fill('team-platform');
    await panel.getByTestId('claim-plan').click();
    await expect(panel.getByTestId('declare-signer')).toContainText('alice@example.com', { timeout: T });
    await expect(panel.getByTestId('declare-command')).toContainText('pacto claim stripe --owner team-platform --registry demo.pacto.local --intent sha256:');
    await panel.getByTestId('declare-record').locator('> summary').click();
    await expect(panel.getByTestId('declare-record')).toContainText('inferredFrom: external:stripe');
    await expect(panel.getByTestId('declare-save-draft')).toHaveCount(0);

    // The demo's simulated sign-in keeps the tab and comes back to the panel.
    await panel.getByTestId('declare-sign').click();
    await page.getByTestId('demo-signin-submit').click();
    await expect(page).toHaveURL(/signed=sha256/);
    const back = page.getByTestId('declare');
    await expect(back.getByTestId('declare-signed-return')).toContainText('alice@example.com', { timeout: T });
    await back.getByRole('button', { name: 'Publish 0.1.0' }).click();
    await expect(owner.getByTestId('external-owner-claimed')).toContainText('team-platform', { timeout: T });
    await expect(owner.getByTestId('external-owner-claimed')).toContainText('0.1.0');

    // The claimed revision links back to the external system it claims.
    await owner.getByTestId('external-owner-claimed').getByRole('link', { name: 'stripe', exact: true }).click();
    await page.locator('a[href*="/fleet/revisions/"]').first().click();
    await expect(page.getByTestId('inferred-declaration').getByRole('link')).toHaveAttribute('href', '#/fleet/externals/stripe', { timeout: T });

    // A claimed stripe is no longer routed.
    await page.goto('/#/fleet/attention?category=unowned');
    const routed = page.getByTestId('fleet-map-rail-item').filter({ hasText: 'External system has no owner' });
    await expect(routed.filter({ hasText: /^\s*Twilio/ })).toHaveCount(1, { timeout: T });
    await expect(routed.filter({ hasText: /^\s*Stripe/ })).toHaveCount(0);
    expect(errors).toEqual([]);
  });
});
