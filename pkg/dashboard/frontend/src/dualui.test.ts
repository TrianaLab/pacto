/**
 * Architecture regression guard (Part 1.7): the dashboard must expose ONE user-facing
 * implementation per already-migrated concept. On a Fleet-capable host, legacy routes
 * canonicalize to the product IA (they never mount the old Services/Owners/Graph/entity
 * screens); a legacy name-bearing URL is migrated through the Product API rather than the
 * legacy detail view. On a non-Fleet host, the legacy UI is the only UI and stays. If a
 * future change re-mounts a superseded legacy view on a Fleet host, these fail.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { capabilitiesFn, entitiesFn, overviewFn, neighborhoodFn } = vi.hoisted(() => ({
  capabilitiesFn: vi.fn(), entitiesFn: vi.fn(), overviewFn: vi.fn(), neighborhoodFn: vi.fn(),
}));
const PSV = 'pacto.dev/fleet-product/v1';
const meta = { schemaVersion: PSV, snapshotId: 's', asOf: '', completeness: 'complete', sources: [], sourcesTruncated: false, limitations: { items: [], total: 0, count: 0, truncated: false }, limitationsTruncated: false };

vi.mock('./lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/api.ts')>();
  return {
    ...actual,
    api: {
      capabilities: (...a: unknown[]) => capabilitiesFn(...a),
      services: vi.fn().mockResolvedValue([]),
      sources: vi.fn().mockResolvedValue({ sources: [], discovering: false }),
      health: vi.fn().mockResolvedValue({ version: 'x' }),
      refresh: vi.fn().mockResolvedValue({}),
      fleetEntities: (...a: unknown[]) => entitiesFn(...a),
      fleetOverview: (...a: unknown[]) => overviewFn(...a),
      fleetNeighborhood: (...a: unknown[]) => neighborhoodFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import App from './App.svelte';

async function mountAt(hash: string) {
  location.hash = hash;
  const target = document.createElement('div');
  document.body.appendChild(target);
  const app = mount(App, { target });
  // Let capabilities resolve and the redirect effect settle.
  for (let i = 0; i < 6; i++) { await Promise.resolve(); }
  return { target, app };
}
const q = (t: HTMLElement, s: string) => t.querySelector(s);

describe('dual-UI guard — Fleet host canonicalizes legacy routes to the product IA', () => {
  beforeEach(() => {
    capabilitiesFn.mockResolvedValue({ fleet: true, impact: true, observed: false });
    entitiesFn.mockResolvedValue({ meta, total: 0, count: 0, entities: [] });
    overviewFn.mockResolvedValue({ meta, summary: {}, attention: { items: [], total: 0, count: 0, truncated: false }, recentEvidence: { items: [], total: 0, count: 0, truncated: false }, entryPoints: [] });
    neighborhoodFn.mockResolvedValue({ meta, perspective: 'service', requestedFocus: {}, focusService: {}, direction: 'both', depth: 1, effectiveDepth: 1, views: ['expected'], nodes: [], edges: [], unresolvedDependencies: { items: [], total: 0, count: 0, truncated: false }, limitations: { items: [], total: 0, count: 0, truncated: false }, truncated: false, maxNodes: 60, maxEdges: 120 });
  });
  afterEach(() => { location.hash = ''; });

  it('redirects the legacy Services list to the product Services list', async () => {
    const { target, app } = await mountAt('#/services');
    expect(location.hash).toBe('#/fleet/services');
    expect(q(target, '.app-loading')).toBeNull(); // committed to the product view, not stuck loading
    unmount(app); document.body.removeChild(target);
  });

  it('redirects the legacy standalone Graph to the Operational Graph', async () => {
    const { target, app } = await mountAt('#/graph');
    expect(location.hash).toBe('#/fleet/graph');
    unmount(app); document.body.removeChild(target);
  });

  it('redirects the legacy Owners list to the product Owners list', async () => {
    const { target, app } = await mountAt('#/owners');
    expect(location.hash).toBe('#/fleet/owners');
    unmount(app); document.body.removeChild(target);
  });

  it('redirects the legacy root (and unknown hashes) to the operational overview', async () => {
    const { target, app } = await mountAt('#/');
    expect(location.hash).toBe('#/fleet');
    unmount(app); document.body.removeChild(target);
    const r = await mountAt('#/nonsense-old-bookmark');
    expect(location.hash).toBe('#/fleet');
    unmount(r.app); document.body.removeChild(r.target);
  });

  it('migrates a legacy service-detail URL through the Product API, not the legacy detail view', async () => {
    // Two same-named services -> explicit disambiguation, not the legacy ServiceDetailView.
    entitiesFn.mockResolvedValue({ meta, total: 2, count: 2, entities: [
      { kind: 'service', key: 'a/pay', label: 'pay', href: '/fleet/services/a%2Fpay' },
      { kind: 'service', key: 'b/pay', label: 'pay', href: '/fleet/services/b%2Fpay' },
    ] });
    const { target, app } = await mountAt('#/services/pay');
    expect(q(target, '[data-testid="legacy-migration"]')).toBeTruthy();
    expect(location.hash).toBe('#/services/pay'); // no redirect; the user disambiguates
    unmount(app); document.body.removeChild(target);
  });
});

describe('dual-UI guard — non-Fleet host keeps the legacy UI as its only UI', () => {
  beforeEach(() => { capabilitiesFn.mockResolvedValue({ fleet: false, impact: false, observed: false }); });
  afterEach(() => { location.hash = ''; });

  it('renders the legacy Services list at the root and does not redirect', async () => {
    const { target, app } = await mountAt('#/');
    expect(location.hash).toBe('#/'); // no product IA to redirect to
    expect(q(target, '.app-loading')).toBeNull();
    expect(target.querySelector('h1')?.textContent).toContain('Services');
    unmount(app); document.body.removeChild(target);
  });

  // The product migration canonicalizes #/readiness and #/diff into the product IA on a
  // Fleet host. On the offline `pacto doc` export there IS no product IA, so the same
  // routes must still mount their legacy screens -- canonicalizing there would strand
  // the user on a route nothing serves.
  it.each([
    ['#/readiness', 'Service Readiness'],
    ['#/diff', 'Compare Versions'],
  ])('keeps %s on its legacy screen (there is no product route to send it to)', async (hash, heading) => {
    const { target, app } = await mountAt(hash);
    expect(location.hash).toBe(hash);
    expect(target.querySelector('h1')?.textContent).toContain(heading);
    unmount(app); document.body.removeChild(target);
  });
});
