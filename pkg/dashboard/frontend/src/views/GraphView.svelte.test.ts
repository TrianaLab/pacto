/**
 * Component tests for the search-first Operational Graph (Phase 4, requirement S,
 * deterministic subset). They prove: /fleet/graph opens a search-first discovery state
 * (no fleet hairball, no snapshot fetch); a focus consumes the PRODUCT neighborhood
 * API (never the FleetSnapshot); differences are rendered from the backend value with
 * distinct text (never color-only); insufficient observation is not a failure;
 * unresolved deps and truncation are visible; the controls persist state in the URL;
 * and node/edge selection opens a bounded quick-inspection drawer. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { neighborhoodFn, entitiesFn, snapshotFn } = vi.hoisted(() => ({
  neighborhoodFn: vi.fn(), entitiesFn: vi.fn(), snapshotFn: vi.fn(),
}));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      fleetNeighborhood: (...a: unknown[]) => neighborhoodFn(...a),
      fleetEntities: (...a: unknown[]) => entitiesFn(...a),
      fleetSnapshot: (...a: unknown[]) => snapshotFn(...a), // must NEVER be called
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import GraphView from './GraphView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const ref = (kind: string, key: string, label?: string, extra: any = {}): any => ({ kind, key, label: label ?? key, href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function neighborhood(extra: any = {}): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', completeness: 'partial', sources: [{ id: 'k8s', status: 'unavailable' }] },
    perspective: 'service', requestedFocus: ref('service', 'domain-a/web', 'web'), focusService: ref('service', 'domain-a/web', 'web'),
    direction: 'both', depth: 1, views: ['expected', 'differences'],
    nodes: [
      { ref: ref('service', 'domain-a/web', 'web'), depth: 0, focus: true, status: 'Compliant', owner: 'team-a', expansions: ['dependencies'] },
      { ref: ref('service', 'domain-a/api', 'api'), depth: 1, status: 'Compliant', expansions: [] },
      { ref: ref('service', 'domain-a/obs', 'obs'), depth: 1, status: 'Unknown', expansions: [] },
    ],
    edges: [
      { id: 'domain-a/web|domain-a/api', relation: 'dependency', from: ref('service', 'domain-a/web', 'web'), to: ref('service', 'domain-a/api', 'api'), expected: true, observed: false, provenance: 'declared', difference: 'expected-not-observed', declaredClaims: { total: 1, count: 1, truncated: false, items: [{ sourceRevision: 'domain-a/web@sha256:1', compatibility: '^1.0.0', reconciliation: 'declared-not-observed' }] }, observationSources: { total: 0, count: 0, truncated: false, items: [] }, count: 0 },
      { id: 'domain-a/obs|domain-a/web', relation: 'dependency', from: ref('service', 'domain-a/obs', 'obs'), to: ref('service', 'domain-a/web', 'web'), expected: false, observed: true, provenance: 'observed', difference: 'observed-not-expected', declaredClaims: { total: 0, count: 0, truncated: false, items: [] }, observationSources: { total: 1, count: 1, truncated: false, items: [{ source: 'k8s', count: 5 }] }, count: 5 },
    ],
    unresolvedDependencies: { total: 1, count: 1, truncated: false, items: [{ from: ref('service', 'domain-a/web', 'web'), ref: 'ghost', requestedRef: 'oci://x/ghost', reason: 'no provider' }] },
    limitations: { total: 1, count: 1, truncated: false, items: [{ code: 'OBSERVED_NOT_REVISION_SCOPED', message: 'observation is service-scoped' }] },
    truncated: true, maxNodes: 60, maxEdges: 120,
    ...extra,
  };
}

function mountView(params: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(GraphView, { target, props: { params, refreshTick: 0 } });
  return { target, component };
}
const q = (t: HTMLElement, sel: string) => t.querySelector(sel) as HTMLElement | null;

describe('GraphView — search-first Operational Graph (Phase 4)', () => {
  beforeEach(() => {
    for (const f of [neighborhoodFn, entitiesFn, snapshotFn]) f.mockReset();
    neighborhoodFn.mockResolvedValue(neighborhood());
    entitiesFn.mockResolvedValue({ meta: {}, entities: [ref('service', 'domain-a/web', 'web', { domain: 'domain-a' })] });
    location.hash = '';
  });

  it('scenario 1/24: no focus opens the search-first discovery state, NO fleet render or snapshot fetch', async () => {
    const { target, component } = mountView({});
    await Promise.resolve();
    expect(q(target, '[data-testid="graph-discovery"]')).toBeTruthy();
    expect(q(target, '[data-testid="graph-canvas"]')).toBeNull(); // no topology nodes rendered
    expect(neighborhoodFn).not.toHaveBeenCalled(); // no neighborhood loaded without a focus
    expect(snapshotFn).not.toHaveBeenCalled();     // the FleetSnapshot is never the graph contract
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 2: search focuses an entity via a canonical graph route', async () => {
    const { target, component } = mountView({});
    const input = q(target, 'input[type="search"]') as HTMLInputElement;
    input.value = 'web';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-focus-link"]')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ text: 'web' }));
    const link = q(target, '[data-testid="graph-focus-link"]') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('#/fleet/graph/service/domain-a%2Fweb');
    unmount(component); document.body.removeChild(target);
  });

  it('a focus consumes the PRODUCT neighborhood API (never the snapshot) with the focused defaults', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-canvas"]')).toBeTruthy());
    expect(neighborhoodFn).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'service', key: 'domain-a/web', perspective: 'service', direction: 'both', depth: 1, views: ['expected', 'differences'],
    }));
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 6/7: renders the backend difference verbatim (distinct text), insufficient is not a failure', async () => {
    neighborhoodFn.mockResolvedValue(neighborhood({
      edges: [
        { id: 'a|b', relation: 'dependency', from: ref('service', 'a', 'a'), to: ref('service', 'b', 'b'), expected: true, observed: false, provenance: 'declared', difference: 'insufficient', declaredClaims: { total: 1, count: 1, truncated: false, items: [{ sourceRevision: 'a@1', reconciliation: 'insufficient' }] }, observationSources: { total: 0, count: 0, truncated: false, items: [] } },
        { id: 'c|d', relation: 'dependency', from: ref('service', 'c', 'c'), to: ref('service', 'd', 'd'), expected: false, observed: true, provenance: 'observed', difference: 'observed-not-expected', declaredClaims: { total: 0, count: 0, truncated: false, items: [] }, observationSources: { total: 0, count: 0, truncated: false, items: [] } },
      ],
    }));
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-edges"]')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).toContain('Insufficient evidence');   // distinct textual difference, not color-only
    expect(text).toContain('Observed, not expected');   // observed-not-expected rendered explicitly
    expect(q(target, '.gv-error')).toBeNull();          // insufficient/observation gaps are not failures
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 8: an unresolved declared dependency stays visible', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-unresolved"]')).toBeTruthy());
    expect(target.textContent).toContain('oci://x/ghost');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 16/17: partial knowledge and backend truncation are both surfaced', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-canvas"]')).toBeTruthy());
    expect(q(target, '[data-testid="graph-knowledge-caveat"]')).toBeTruthy();
    expect(q(target, '[data-testid="graph-truncated"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 9/10/11: direction and depth controls persist in the URL', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="dir-dependencies"]')).toBeTruthy());
    (q(target, '[data-testid="dir-dependencies"]') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/graph/service/domain-a%2Fweb?direction=dependencies');
    location.hash = '';
    // re-mount to reset control state, then bump depth
    unmount(component); document.body.removeChild(target);
    const m = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(m.target, '[data-testid="graph-depth"]')).toBeTruthy());
    (Array.from(m.target.querySelectorAll('.gv-depth button')).find((b) => b.textContent === '+') as HTMLButtonElement).click();
    expect(location.hash).toContain('depth=2');
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('scenario 12: Expand increases depth while preserving the current knowledge views', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web', views: 'observed' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-expand"]')).toBeTruthy());
    (q(target, '[data-testid="graph-expand"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('depth=2');
    expect(location.hash).toContain('views=observed'); // views preserved through expansion
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 20: the perspective control switches projection via the URL', async () => {
    const { target, component } = mountView({ kind: 'revision', sel: 'domain-a/web@sha256:1' });
    await vi.waitFor(() => expect(q(target, '[data-testid="perspective-revision"]')).toBeTruthy());
    (q(target, '[data-testid="perspective-revision"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('perspective=revision');
    expect(neighborhoodFn).toHaveBeenCalledWith(expect.objectContaining({ kind: 'revision' }));
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 13/14: selecting a node opens a bounded quick-inspect drawer with a full-detail link', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-focus-node"]')).toBeTruthy());
    (q(target, '[data-testid="graph-focus-node"]') as HTMLButtonElement).click();
    flushSync();
    const drawer = q(target, '[data-testid="graph-drawer"]');
    expect(drawer).toBeTruthy();
    const fullDetail = Array.from(drawer!.querySelectorAll('a')).find((a) => /full detail/i.test(a.textContent || '')) as HTMLAnchorElement;
    expect(fullDetail.getAttribute('href')).toBe('#/fleet/services/domain-a%2Fweb');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 15: selecting an edge explains its declared claims and observed provenance', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-edge"]')).toBeTruthy());
    (target.querySelectorAll('[data-testid="graph-edge"]')[0] as HTMLButtonElement).click();
    flushSync();
    const drawer = q(target, '[data-testid="graph-drawer"]');
    expect(drawer?.textContent).toContain('Declared by');
    expect(drawer?.textContent).toContain('^1.0.0'); // the declared compatibility claim
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 18: Reset focus returns to the discovery state', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-reset"]')).toBeTruthy());
    (q(target, '[data-testid="graph-reset"]') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/graph');
    unmount(component); document.body.removeChild(target);
  });
});
