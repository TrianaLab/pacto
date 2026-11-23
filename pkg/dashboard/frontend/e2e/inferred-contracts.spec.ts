import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// An inferred external contract (517-S3), against the built WASM demo: the registry
// mirror holds stripe's inferred contract, which shows as one collapsed block on the
// stripe external. No source checked its signature, so it says so (never "unsigned"),
// and a range is never checked against it.

const T = 20_000;

/**
 * Adds one INFERRED_PROVIDER item to every attention answer: no demo consumer pins a
 * pacto-inferred ref, because the published demo bundles never change.
 */
function inferredAttention() {
  type Res = { status: number; body: string; contentType: string };
  type Serve = (method: string, path: string, body: string | null) => Promise<Res>;
  let real: Serve | undefined;
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    if (path.split('?')[0] !== '/api/fleet/attention' || res.status !== 200) return res;
    const doc = JSON.parse(res.body);
    doc.items = [{
      entity: { kind: 'external', key: 'stripe', label: 'Stripe', secondary: 'vendor-api', status: 'Unknown', href: '/fleet/externals/stripe' },
      label: "1 service checks a range against stripe's inferred contract", severity: 'info', code: 'INFERRED_PROVIDER', category: 'knowledge',
      summary: 'Range checked against an inferred contract',
      reason: "card-payments pins a compatibility range on stripe's inferred contract, which lives under pacto-inferred; Pacto never checks a range against it",
      nextStep: 'Write a contract with service.external: stripe, publish it outside pacto-inferred, then point the ranges at it',
      nextAction: { verb: 'edit-contract', cli: 'pacto ci' },
    }, ...(doc.items ?? [])];
    doc.total = (doc.total ?? 0) + 1;
    doc.count = (doc.count ?? 0) + 1;
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
}

test.describe('WASM demo — inferred external contracts', () => {
  test('stripe shows its inferred contract as not checked and never range-checked', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/externals/stripe');

    const block = page.getByTestId('external-inferred-contract');
    await expect(block).toBeVisible({ timeout: T });
    // Collapsed by default: essentials first.
    await expect(block).not.toHaveAttribute('open', '');
    await expect(block.locator('summary').first()).toContainText('Range checks: unverified');
    await block.locator('summary').first().click();
    await expect(block).toContainText('registry.pacto.demo/demo/pacto-inferred/stripe');
    await expect(block).toContainText('version 0.0.1');
    // The mark's words are on the page, not only in a tooltip: no source checked the
    // demo revision's signature, so it says so.
    await expect(block).toContainText('Signature not checked, publisher unknown; a range is never checked against it');

    await block.getByText('What the record lists').click();
    await expect(block.getByTestId('inferred-surface-meta')).toContainText(/^Computed .+; (complete|partial)/);
    await expect(block.locator('.xe-surface li').first()).toContainText('api.stripe.com:443');
    await expect(block.locator('.xe-surface li').first()).toContainText('seen by k8s-production');
  });

  test('the knowledge attention item says to edit the contract and run pacto ci', async ({ page }) => {
    await page.addInitScript(inferredAttention);
    await appReady(page, '/#/fleet/attention?category=knowledge');
    // The list beside the Overview's map, opened and filtered by the attention link.
    const item = page.getByTestId('fleet-map-rail-item').filter({ hasText: 'Range checked against an inferred contract' });
    await expect(item).toHaveCount(1, { timeout: T });
    await expect(item).toContainText('Fix: edit the contract, then run pacto ci');
  });
});
