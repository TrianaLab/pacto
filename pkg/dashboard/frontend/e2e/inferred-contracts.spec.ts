import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// An inferred external contract (517-S3), against the built WASM demo: the registry
// mirror holds stripe's inferred contract, which shows as one collapsed block on the
// stripe external with no signature, so it reads as unsigned and a range is never
// checked against it.

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
      reason: "card-payments pins a compatibility range on stripe's inferred contract, which Pacto published from what it sees; Pacto never checks a range against it",
      nextStep: 'Declare a contract for stripe with pacto declare, then point the ranges at it',
      nextAction: { verb: 'declare', tool: 'pacto_declare' },
    }, ...(doc.items ?? [])];
    doc.total = (doc.total ?? 0) + 1;
    doc.count = (doc.count ?? 0) + 1;
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', { configurable: true, get() { return real ? wrapped : undefined; }, set(v: Serve) { real = v; } });
}

test.describe('WASM demo — inferred external contracts', () => {
  test('stripe shows its inferred contract as unsigned and never range-checked', async ({ page }) => {
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
    // The mark's words are on the page, not only in a tooltip: the demo revision has
    // no provenance, so it reads unsigned.
    await expect(block).toContainText('Unsigned, publisher unknown; a range is never checked against it');

    await block.getByText('What Pacto saw').click();
    await expect(block.locator('.xe-surface li').first()).toContainText('api.stripe.com:443');
    await expect(block.locator('.xe-surface li').first()).toContainText('seen by k8s-production');
  });

  test('the knowledge attention item says to declare a contract with pacto_declare', async ({ page }) => {
    await page.addInitScript(inferredAttention);
    await appReady(page, '/#/fleet/attention?category=knowledge');
    const item = page.getByTestId('attention-item').filter({ hasText: 'Range checked against an inferred contract' });
    await expect(item).toHaveCount(1, { timeout: T });
    await expect(item).toContainText('Fix: declare a contract with pacto_declare');
  });
});
