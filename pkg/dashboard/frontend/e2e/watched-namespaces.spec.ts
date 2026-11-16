import { test, expect, type Page } from '@playwright/test';
import { appReady } from './ready';

/**
 * A fleet read from a namespace list (controller.watchNamespaces, `pacto dashboard
 * --namespace a,b`) is partial on purpose. The Overview says which namespaces Pacto
 * watches, names any it could not read, and never reads as an all-clear.
 *
 * The WASM demo has no Kubernetes source, so the overview answer gets one in the page.
 */

const T = 30_000;

/** Adds a scoped Kubernetes source to the overview meta. Runs in the page. */
function installScope(scope: { namespaces: string[]; unreadable: string[] }): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => Promise<ServeResult>;
  let real: Serve | null = null;
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    if (path.split('?')[0] !== '/api/fleet/overview' || res.status !== 200) return res;
    const doc = JSON.parse(res.body);
    const meta = doc.meta;
    meta.completeness = 'partial';
    meta.sources = [{ id: 'k8s', kind: 'kubernetes', status: 'partial', revisionCount: 0, targetCount: 0, namespaces: scope.namespaces }, ...(meta.sources ?? [])];
    meta.sourceCounts = { ...meta.sourceCounts, total: (meta.sourceCounts?.total ?? 0) + 1, partial: (meta.sourceCounts?.partial ?? 0) + 1 };
    meta.limitations = [
      { code: 'K8S_NAMESPACE_SCOPED', source: 'k8s', message: `source k8s watches ${scope.namespaces.length} namespaces; services in any other namespace are unknown` },
      ...scope.unreadable.map((ns) => ({ code: 'K8S_NAMESPACE_UNREADABLE', source: 'k8s', message: `source k8s could not read namespace ${ns}: forbidden` })),
      ...(meta.limitations ?? []),
    ];
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

async function openOverview(page: Page, namespaces: string[], unreadable: string[] = []) {
  await page.addInitScript(installScope, { namespaces, unreadable });
  await appReady(page, '/#/fleet');
  await expect(page.getByTestId('overview-answer')).toBeVisible({ timeout: T });
}

const watched = (page: Page) => page.getByTestId('watched-scope');
const scopedChip = (page: Page) => page.locator('#sec-sources a.sh-chip', { hasText: 'k8s' }).first();
const flat = (s: string | null) => (s ?? '').replace(/\s+/g, ' ').trim();

test.describe('WASM demo — the Overview names the watched namespaces', () => {
  test('two namespaces: the banner opens with the scope and Data sources names it', async ({ page }) => {
    await openOverview(page, ['payments', 'ledger']);
    await expect(watched(page)).toHaveText('Watching 2 namespaces (ledger and payments).', { timeout: T });
    // The banner's first sentence is the scope.
    expect(flat(await page.locator('.knowledge').first().textContent())).toMatch(/^Watching 2 namespaces \(ledger and payments\)\. /);
    await expect(scopedChip(page)).toContainText('Watches ledger and payments');
    // A partial fleet is never an all-clear.
    await expect(page.locator('.all-clear')).toHaveCount(0);
  });

  test('one namespace reads in the singular', async ({ page }) => {
    await openOverview(page, ['payments']);
    await expect(watched(page)).toHaveText('Watching 1 namespace (payments).', { timeout: T });
  });

  test('a long scope shows the first 3 and keeps every name in the title', async ({ page }) => {
    const names = ['alpha', 'bravo', 'charlie', 'delta', 'echo', 'foxtrot'];
    await openOverview(page, names);
    await expect(watched(page)).toHaveText('Watching 6 namespaces (alpha, bravo, charlie and 3 more).', { timeout: T });
    await expect(watched(page)).toHaveAttribute('title', 'alpha, bravo, charlie, delta, echo and foxtrot');
  });

  test('an unreadable namespace is named as unreadable, never as watched', async ({ page }) => {
    await openOverview(page, ['payments'], ['ledger']);
    await expect(watched(page)).toHaveText('Watching 1 namespace (payments).', { timeout: T });
    await expect(scopedChip(page)).toContainText('Watches payments');
    await expect(scopedChip(page)).toContainText('Could not read ledger');
    await expect(page.locator('.all-clear')).toHaveCount(0);
  });
});
