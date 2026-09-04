/**
 * Component tests for the product Operational Graph. They
 * prove: /fleet/graph opens a search-first discovery state (no topology, no snapshot,
 * no neighborhood request); a focus consumes the PRODUCT neighborhood API (never the
 * FleetSnapshot) and renders an ACTUAL Cytoscape topology (a graph node for every
 * returned node); mixed kinds and dependency/runs edges are distinguishable; the
 * backend difference/corroboration is rendered without re-inference; service-scoped
 * observation is labelled as service-scoped in fine-grained perspectives; Fit/zoom
 * manipulate only the visual canvas; node/edge selection opens the correct drawer;
 * expand issues a new bounded request; truncation and partial knowledge stay visible;
 * a search transport error is not "no matches"; a stale search cannot overwrite a newer
 * one; keys round-trip; impossible perspective/depth controls are not exposed. The
 * Cytoscape ENGINE (lib/graph.ts renderGraph) is mocked so the render call and the
 * fit/zoom wiring are observable without a real canvas; the adapter's node/edge mapping
 * is proven directly in lib/neighborhoodGraph.test.ts. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { neighborhoodFn, entitiesFn, snapshotFn } = vi.hoisted(() => ({
  neighborhoodFn: vi.fn(), entitiesFn: vi.fn(), snapshotFn: vi.fn(),
}));
const { renderSpy, fitSpy, zoomInSpy, zoomOutSpy, patchDataSpy, applyTopologySpy, legendFilterSpy, focusNodeSpy } = vi.hoisted(() => ({
  renderSpy: vi.fn(), fitSpy: vi.fn(), zoomInSpy: vi.fn(), zoomOutSpy: vi.fn(), patchDataSpy: vi.fn(),
  applyTopologySpy: vi.fn(), legendFilterSpy: vi.fn(), focusNodeSpy: vi.fn(),
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
// Mock the Cytoscape engine (a plain function) so NeighborhoodGraph runs without a real
// canvas; the spy captures the adapted GraphData and the fit/zoom controls it exposes.
vi.mock('../lib/graph.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/graph.ts')>();
  return {
    ...actual,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    renderGraph: (...args: any[]) => {
      renderSpy(...args);
      return {
        nodes: [], destroy: vi.fn(), zoomIn: zoomInSpy, zoomOut: zoomOutSpy, resetView: vi.fn(),
        fit: fitSpy, patchData: patchDataSpy, applyFilter: vi.fn(),
        applyLegendFilter: legendFilterSpy, focusNode: focusNodeSpy,
        applyTopology: applyTopologySpy, restyle: vi.fn(), resetLayout: vi.fn(),
        diagnostics: vi.fn(() => ({})), spatialState: vi.fn(() => ({ positions: {}, pan: { x: 0, y: 0 }, zoom: 1 })),
      };
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import GraphView from './GraphView.svelte';
import { ApiError } from '../lib/api.ts';
import { reactiveProps } from '../testkit.svelte.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const ref = (kind: string, key: string, label?: string, extra: any = {}): any => ({ kind, key, label: label ?? key, href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function neighborhood(extra: any = {}): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', completeness: 'partial', sources: [{ id: 'k8s', status: 'unavailable' }] },
    perspective: 'service', requestedFocus: ref('service', 'domain-a/web', 'web'), focusService: ref('service', 'domain-a/web', 'web'),
    direction: 'both', depth: 1, effectiveDepth: 1, views: ['expected', 'differences'],
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

// A target-perspective neighborhood: a target focus, a runs edge to its revision and a
// dependency edge to a service carrying only SERVICE-scoped corroboration (no
// edge-scope difference). effectiveDepth 1 (target is one-hop).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function targetNeighborhood(extra: any = {}): any {
  return neighborhood({
    perspective: 'target',
    requestedFocus: ref('target', 'prod/k8s/web', 'web'),
    focusService: ref('service', 'domain-a/web', 'web'),
    depth: 1, effectiveDepth: 1,
    nodes: [
      { ref: ref('target', 'prod/k8s/web', 'web'), depth: 0, focus: true, status: 'Compliant', revisionState: 'exact' },
      { ref: ref('revision', 'domain-a/web@sha256:1', 'web@1'), depth: 1, status: 'Compliant' },
      { ref: ref('service', 'domain-a/api', 'api'), depth: 1, status: 'Compliant' },
    ],
    edges: [
      { id: 'prod/k8s/web|domain-a/web@sha256:1', relation: 'runs', from: ref('target', 'prod/k8s/web', 'web'), to: ref('revision', 'domain-a/web@sha256:1', 'web@1'), expected: false, observed: true, provenance: 'observed', observationScope: 'target', declaredClaims: { total: 0, count: 0, truncated: false, items: [] }, observationSources: { total: 0, count: 0, truncated: false, items: [] } },
      { id: 'prod/k8s/web|domain-a/api', relation: 'dependency', from: ref('target', 'prod/k8s/web', 'web'), to: ref('service', 'domain-a/api', 'api'), expected: true, observed: false, provenance: 'declared', observationScope: 'service', serviceCorroboration: 'matched', declaredClaims: { total: 1, count: 1, truncated: false, items: [{ sourceRevision: 'domain-a/web@sha256:1', compatibility: '^1.0.0' }] }, observationSources: { total: 0, count: 0, truncated: false, items: [] } },
    ],
    limitations: { total: 1, count: 1, truncated: false, items: [{ code: 'OBSERVED_NOT_TARGET_SCOPED', message: 'observation is service-scoped' }] },
    ...extra,
  });
}

function mountView(params: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(GraphView, { target, props: { params, refreshTick: 0 } });
  return { target, component };
}
const q = (t: HTMLElement, sel: string) => t.querySelector(sel) as HTMLElement | null;
const qa = (t: HTMLElement, sel: string) => Array.from(t.querySelectorAll(sel)) as HTMLElement[];

describe('GraphView — product Operational Graph', () => {
  beforeEach(() => {
    for (const f of [neighborhoodFn, entitiesFn, snapshotFn, renderSpy, fitSpy, zoomInSpy, zoomOutSpy, patchDataSpy]) f.mockReset();
    neighborhoodFn.mockResolvedValue(neighborhood());
    entitiesFn.mockResolvedValue({ meta: {}, total: 1, count: 1, entities: [ref('service', 'domain-a/web', 'web', { domain: 'domain-a' })] });
    location.hash = '';
  });

  it('discovery: no focus renders no topology and issues no neighborhood/snapshot request', async () => {
    const { target, component } = mountView({});
    await Promise.resolve();
    expect(q(target, '[data-testid="graph-discovery"]')).toBeTruthy();
    expect(q(target, '[data-testid="neighborhood-canvas"]')).toBeNull();
    // The resting discovery state shows an unmistakable "graph renders after you focus"
    // affordance, not an empty page and never the whole fleet.
    expect(q(target, '[data-testid="graph-discovery-placeholder"]')).toBeTruthy();
    expect(neighborhoodFn).not.toHaveBeenCalled();
    expect(snapshotFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });

  it('search results choose the correct default perspective per entity kind', async () => {
    // service -> service (no perspective param, service is the default)
    entitiesFn.mockResolvedValue({ meta: {}, total: 1, count: 1, entities: [ref('service', 'domain-a/web', 'web')] });
    let m = mountView({});
    let input = q(m.target, 'input[type="search"]') as HTMLInputElement;
    input.value = 'web'; input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(q(m.target, '[data-testid="graph-focus-link"]')).toBeTruthy());
    expect((q(m.target, '[data-testid="graph-focus-link"]') as HTMLAnchorElement).getAttribute('href')).toBe('#/fleet/graph/service/domain-a%2Fweb');
    unmount(m.component); document.body.removeChild(m.target);

    // revision -> revision perspective
    entitiesFn.mockResolvedValue({ meta: {}, total: 1, count: 1, entities: [ref('revision', 'domain-a/web@sha256:1', 'web@1')] });
    m = mountView({});
    input = q(m.target, 'input[type="search"]') as HTMLInputElement;
    input.value = 'web'; input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(q(m.target, '[data-testid="graph-focus-link"]')).toBeTruthy());
    expect((q(m.target, '[data-testid="graph-focus-link"]') as HTMLAnchorElement).getAttribute('href')).toContain('perspective=revision');
    unmount(m.component); document.body.removeChild(m.target);

    // target -> target perspective
    entitiesFn.mockResolvedValue({ meta: {}, total: 1, count: 1, entities: [ref('target', 'prod/k8s/web', 'web')] });
    m = mountView({});
    input = q(m.target, 'input[type="search"]') as HTMLInputElement;
    input.value = 'web'; input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(q(m.target, '[data-testid="graph-focus-link"]')).toBeTruthy());
    expect((q(m.target, '[data-testid="graph-focus-link"]') as HTMLAnchorElement).getAttribute('href')).toContain('perspective=target');
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('a focus consumes the PRODUCT neighborhood API (never the snapshot) and renders a Cytoscape node for every returned node', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="neighborhood-canvas"]')).toBeTruthy());
    expect(neighborhoodFn).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'service', key: 'domain-a/web', perspective: 'service', direction: 'both', depth: 1, views: ['expected', 'differences'],
    }));
    expect(snapshotFn).not.toHaveBeenCalled();
    // The engine received a graph node for every returned neighborhood node (3).
    await vi.waitFor(() => expect(renderSpy).toHaveBeenCalled());
    const graphData = renderSpy.mock.calls.at(-1)![1];
    expect(graphData.nodes).toHaveLength(3);
    unmount(component); document.body.removeChild(target);
  });

  it('renders mixed node kinds and distinguishes dependency from runs edges (via the adapter)', async () => {
    neighborhoodFn.mockResolvedValue(targetNeighborhood());
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    await vi.waitFor(() => expect(renderSpy).toHaveBeenCalled());
    const graphData = renderSpy.mock.calls.at(-1)![1];
    const kinds = graphData.nodes.map((n: { kind: string }) => n.kind).sort();
    expect(kinds).toEqual(['revision', 'service', 'target']); // mixed kinds present
    const edgeTypes = graphData.nodes.flatMap((n: { edges?: { type: string }[] }) => (n.edges || []).map((e) => e.type)).sort();
    expect(edgeTypes).toContain('runs');
    expect(edgeTypes).toContain('dependency'); // dependency and runs are distinct edge types
    unmount(component); document.body.removeChild(target);
  });

  it('renders the backend difference verbatim (distinct text); insufficient is not a failure', async () => {
    neighborhoodFn.mockResolvedValue(neighborhood({
      edges: [
        { id: 'a|b', relation: 'dependency', from: ref('service', 'a', 'a'), to: ref('service', 'b', 'b'), expected: true, observed: false, provenance: 'declared', difference: 'insufficient', declaredClaims: { total: 1, count: 1, truncated: false, items: [{ sourceRevision: 'a@1', reconciliation: 'insufficient' }] }, observationSources: { total: 0, count: 0, truncated: false, items: [] } },
        { id: 'c|d', relation: 'dependency', from: ref('service', 'c', 'c'), to: ref('service', 'd', 'd'), expected: false, observed: true, provenance: 'observed', difference: 'observed-not-expected', declaredClaims: { total: 0, count: 0, truncated: false, items: [] }, observationSources: { total: 0, count: 0, truncated: false, items: [] } },
      ],
    }));
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-edges"]')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).toContain('Insufficient evidence');
    expect(text).toContain('Observed, not expected');
    expect(q(target, '.gv-error')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('labels a fine-grained edge as service-scoped corroboration, never as an edge-scope observation', async () => {
    neighborhoodFn.mockResolvedValue(targetNeighborhood());
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-edges"]')).toBeTruthy());
    // Open the dependency edge (the second edge) drawer and assert the service-scoped caveat.
    const edgeBtns = qa(target, '[data-testid="graph-edge"]');
    const depBtn = edgeBtns.find((b) => /Depends on/i.test(b.textContent || ''))!;
    depBtn.click();
    flushSync();
    const drawer = q(target, '[data-testid="graph-drawer"]');
    expect(drawer?.textContent).toContain('Corroboration');
    expect(q(target, '[data-testid="edge-scope-caveat"]')?.textContent).toMatch(/service-scoped corroboration/i);
    unmount(component); document.body.removeChild(target);
  });

  it('keeps an unresolved declared dependency and backend truncation + partial knowledge visible', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="neighborhood-canvas"]')).toBeTruthy());
    expect(q(target, '[data-testid="graph-unresolved"]')).toBeTruthy();
    expect(target.textContent).toContain('oci://x/ghost');
    expect(q(target, '[data-testid="graph-knowledge-caveat"]')).toBeTruthy();
    expect(q(target, '[data-testid="graph-truncated"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  // A truncated graph used to say only "this was truncated", which tells the reader
  // neither how much is missing nor what to do about it. TotalNodes is the denominator
  // the service projection can honestly supply, and raising the node budget is the way out.
  it('reports how much of the neighborhood is missing and offers the next node budget', async () => {
    neighborhoodFn.mockResolvedValue(neighborhood({ totalNodes: 210 }));
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-truncated"]')).toBeTruthy());
    expect(q(target, '[data-testid="graph-truncated"]')?.textContent).toContain('Showing 3 of 210');

    (q(target, '[data-testid="graph-show-more"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('maxNodes=150');
    unmount(component); document.body.removeChild(target);
  });

  it('climbs to the ceiling and then tells the reader to narrow instead of offering a dead button', async () => {
    neighborhoodFn.mockResolvedValue(neighborhood({ totalNodes: 210 }));
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web', maxNodes: '500' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-truncated"]')).toBeTruthy());
    expect(q(target, '[data-testid="graph-show-more"]')).toBeNull();
    expect(q(target, '[data-testid="graph-truncated"]')?.textContent).toMatch(/Narrow the direction or depth/i);
    unmount(component); document.body.removeChild(target);
  });

  // The revision and target projections cannot count what they never expanded, so they
  // report zero. Zero has to read as "no denominator", never as "you have all of it" --
  // "Showing 3 of 0" would be worse than saying nothing.
  it('falls back to the bare truncation notice when the projection supplies no denominator', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-truncated"]')).toBeTruthy());
    const note = q(target, '[data-testid="graph-truncated"]')?.textContent ?? '';
    expect(note).toContain('bounded and was truncated');
    expect(note).not.toMatch(/Showing \d+ of/);
    unmount(component); document.body.removeChild(target);
  });

  it('asks the backend for the node budget in the URL', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web', maxNodes: '150' });
    await vi.waitFor(() => expect(neighborhoodFn).toHaveBeenCalled());
    expect(neighborhoodFn.mock.calls[0][0]).toMatchObject({ maxNodes: 150 });
    unmount(component); document.body.removeChild(target);
  });

  it('Fit and Zoom in/out manipulate only the visual canvas; direction and depth persist in the URL', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(renderSpy).toHaveBeenCalled()); // canvas mounted + controls wired
    flushSync();
    (q(target, '[data-testid="graph-fit"]') as HTMLButtonElement).click();
    expect(fitSpy).toHaveBeenCalled();
    (q(target, '[data-testid="graph-zoom-in"]') as HTMLButtonElement).click();
    expect(zoomInSpy).toHaveBeenCalled();
    (q(target, '[data-testid="graph-zoom-out"]') as HTMLButtonElement).click();
    expect(zoomOutSpy).toHaveBeenCalled();
    expect(location.hash).toBe(''); // fit/zoom are ephemeral: no URL change
    (q(target, '[data-testid="dir-dependencies"]') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/graph/service/domain-a%2Fweb?direction=dependencies');
    unmount(component); document.body.removeChild(target);
  });

  it('Expand issues a new bounded neighborhood request at a larger depth, preserving views', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web', views: 'observed' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-expand"]')).toBeTruthy());
    (q(target, '[data-testid="graph-expand"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('depth=2');
    expect(location.hash).toContain('views=observed');
    unmount(component); document.body.removeChild(target);
  });

  it('a depth change carries the previous answer instead of blanking the canvas', async () => {
    // The canvas used to be unmounted the instant the question changed, which destroyed
    // the Cytoscape instance before it could reconcile -- so Depth+1 threw away the
    // arrangement and re-ran the layout however well NeighborhoodGraph kept its own key.
    const target = document.createElement('div');
    document.body.appendChild(target);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = reactiveProps({ params: { kind: 'service', sel: 'domain-a/web' }, refreshTick: 0 });
    const component = mount(GraphView, { target, props });
    await vi.waitFor(() => expect(q(target, '[data-testid="neighborhood-canvas"]')).toBeTruthy());

    neighborhoodFn.mockReturnValue(new Promise(() => {})); // the deeper request never settles
    props.params = { kind: 'service', sel: 'domain-a/web', depth: '2' };
    flushSync();

    // Same subject, different question: what the user arranged is still on screen, and the
    // page says a request is in flight rather than presenting the old answer as the new one.
    expect(q(target, '[data-testid="neighborhood-canvas"]')).toBeTruthy();
    expect(q(target, '[data-testid="graph-refreshing"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('exposes only perspectives valid for the focus (a service cannot become a revision/target)', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="perspective-service"]')).toBeTruthy());
    expect(q(target, '[data-testid="perspective-revision"]')).toBeNull();
    expect(q(target, '[data-testid="perspective-target"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('offers the revision perspective for a target only when its revision link is authoritative', async () => {
    // authoritative (exact) -> revision offered
    neighborhoodFn.mockResolvedValue(targetNeighborhood());
    let m = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    await vi.waitFor(() => expect(q(m.target, '[data-testid="neighborhood-canvas"]')).toBeTruthy());
    flushSync();
    await vi.waitFor(() => expect(q(m.target, '[data-testid="perspective-revision"]')).toBeTruthy());
    unmount(m.component); document.body.removeChild(m.target);

    // unresolved -> revision NOT offered
    neighborhoodFn.mockResolvedValue(targetNeighborhood({
      nodes: [{ ref: ref('target', 'prod/k8s/web', 'web'), depth: 0, focus: true, status: 'Unknown', revisionState: 'unresolved' }],
      edges: [],
    }));
    m = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    await vi.waitFor(() => expect(q(m.target, '[data-testid="neighborhood-canvas"]')).toBeTruthy());
    flushSync();
    expect(q(m.target, '[data-testid="perspective-target"]')).toBeTruthy();
    expect(q(m.target, '[data-testid="perspective-revision"]')).toBeNull();
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('labels the perspective control in product language, not the wire enum', async () => {
    neighborhoodFn.mockResolvedValue(targetNeighborhood());
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    await vi.waitFor(() => expect(q(target, '[data-testid="perspective-target"]')).toBeTruthy());
    // "Operational targets", never a lowercase "target" -- the noun the rest of the
    // product uses, because Pacto observes where a revision runs and never deploys it.
    expect(q(target, '[data-testid="perspective-target"]')?.textContent?.trim()).toBe('Operational targets');
    unmount(component); document.body.removeChild(target);
  });

  it('disables depth and expand for the one-hop target perspective and shows the effective-depth note', async () => {
    neighborhoodFn.mockResolvedValue(targetNeighborhood({ depth: 3, effectiveDepth: 1 }));
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target', depth: '3' });
    await vi.waitFor(() => expect(q(target, '[data-testid="neighborhood-canvas"]')).toBeTruthy());
    expect(q(target, '[data-testid="graph-depth"]')).toBeNull();  // no inert depth control
    expect(q(target, '[data-testid="graph-expand"]')).toBeNull(); // no inert expand
    expect(q(target, '[data-testid="graph-effective-depth"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('canonicalizes the URL to the service identity when switching a revision focus to the service perspective', async () => {
    // Part 4: switching perspective must not silently reinterpret identity. A revision
    // focus switching to the service perspective canonicalizes the URL to the service
    // (from the backend focusService), so the URL and the visible graph agree.
    const { target, component } = mountView({ kind: 'revision', sel: 'domain-a/web@sha256:1', perspective: 'revision' });
    await vi.waitFor(() => expect(q(target, '[data-testid="perspective-service"]')).toBeTruthy());
    (q(target, '[data-testid="perspective-service"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('/fleet/graph/service/');
    expect(location.hash).not.toContain('/fleet/graph/revision/');
    unmount(component); document.body.removeChild(target);
  });

  it('canonicalizes a target->revision switch to the linked revision identity (Part 4)', async () => {
    neighborhoodFn.mockResolvedValue(targetNeighborhood());
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    // The target link is authoritative (revisionState exact), so the revision perspective
    // is offered; clicking it canonicalizes the URL to the LINKED revision (from the runs
    // edge), never keeping the target key with a revision perspective.
    await vi.waitFor(() => expect(q(target, '[data-testid="perspective-revision"]')).toBeTruthy());
    (q(target, '[data-testid="perspective-revision"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('/fleet/graph/revision/');
    expect(location.hash).toContain(encodeURIComponent('domain-a/web@sha256:1'));
    expect(location.hash).not.toContain('/fleet/graph/target/');
    unmount(component); document.body.removeChild(target);
  });

  it('canonicalizes a target->service switch to the service identity (Part 4)', async () => {
    neighborhoodFn.mockResolvedValue(targetNeighborhood());
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'target' });
    await vi.waitFor(() => expect(q(target, '[data-testid="perspective-service"]')).toBeTruthy());
    (q(target, '[data-testid="perspective-service"]') as HTMLButtonElement).click();
    expect(location.hash).toContain('/fleet/graph/service/');
    expect(location.hash).toContain(encodeURIComponent('domain-a/web'));
    unmount(component); document.body.removeChild(target);
  });

  it('canonicalizes a bookmarked reinterpreted URL to the projection focus on load (Part 4)', async () => {
    // A stale deep link: kind=target but perspective=revision. The backend keeps
    // requestedFocus the target and supplies projectionFocus (the resolved revision); the
    // URL must canonicalize (replace, not push) to the revision identity so a reload
    // stays on the Product URL and the active perspective never contradicts the graph.
    neighborhoodFn.mockResolvedValue(neighborhood({
      perspective: 'revision',
      requestedFocus: ref('target', 'prod/k8s/web', 'web'),
      projectionFocus: ref('revision', 'domain-a/web@sha256:1', 'web@1'),
      focusService: ref('service', 'domain-a/web', 'web'),
      nodes: [{ ref: ref('revision', 'domain-a/web@sha256:1', 'web@1'), depth: 0, focus: true, status: 'Compliant' }],
      edges: [],
    }));
    const { target, component } = mountView({ kind: 'target', sel: 'prod/k8s/web', perspective: 'revision' });
    await vi.waitFor(() => expect(location.hash).toContain('/fleet/graph/revision/'));
    expect(location.hash).toContain(encodeURIComponent('domain-a/web@sha256:1'));
    unmount(component); document.body.removeChild(target);
  });

  it('closes the quick-inspect drawer on Escape and returns focus to the opener (Part 5, 8.3)', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-node-item"]')).toBeTruthy());
    const nodeBtn = qa(target, '[data-testid="graph-node-item"]')[0] as HTMLButtonElement;
    nodeBtn.focus();
    nodeBtn.click();
    flushSync();
    const drawer = q(target, '[data-testid="graph-drawer"]');
    expect(drawer).toBeTruthy();
    (drawer as HTMLElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-drawer"]')).toBeNull());
    await vi.waitFor(() => expect(document.activeElement).toBe(nodeBtn)); // focus returned to the opener
    unmount(component); document.body.removeChild(target);
  });

  // The legend named every distinction the canvas draws and did nothing with any of
  // them. A dense neighborhood is read by taking things out of it, and this was already
  // the correct list of things to take out.
  it('the legend filters the canvas, and the summary says what it is hiding', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-legend-toggle"]')).toBeTruthy());
    const runs = qa(target, '[data-testid="graph-legend-toggle"]')
      .find((b) => b.getAttribute('data-legend-key') === 'rel:runs') as HTMLButtonElement;
    expect(runs.getAttribute('aria-pressed')).toBe('true'); // everything is shown to begin with
    legendFilterSpy.mockClear();
    runs.click();
    flushSync();
    expect(runs.getAttribute('aria-pressed')).toBe('false');
    expect(legendFilterSpy).toHaveBeenCalledWith(new Set(['rel:runs']));
    // ...and a reader who cannot see the canvas is told, rather than left with a text
    // list that silently disagrees with the picture beside it.
    expect(q(target, '[data-testid="graph-summary"]')?.textContent).toContain('Dimmed on the canvas: Runs.');
    runs.click();
    flushSync();
    expect(legendFilterSpy).toHaveBeenLastCalledWith(new Set());
    expect(q(target, '[data-testid="graph-summary"]')?.textContent).not.toContain('Dimmed');
    unmount(component); document.body.removeChild(target);
  });

  // Two halves of one screen: the text list must move the canvas, or a keyboard reader
  // can never move it at all and a mouse reader watches the drawer describe one entity
  // while the topology emphasises another.
  it('picking from the text list points the canvas at the same node', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-node-item"]')).toBeTruthy());
    focusNodeSpy.mockClear();
    const btn = qa(target, '[data-testid="graph-node-item"]')[0] as HTMLButtonElement;
    btn.click();
    flushSync();
    expect(focusNodeSpy).toHaveBeenCalledWith('domain-a/web');
    expect(btn.getAttribute('aria-current')).toBe('true');
    expect(q(target, '[data-testid="graph-summary"]')?.textContent).toContain('Selected web.');
    unmount(component); document.body.removeChild(target);
  });

  it('selecting a node opens a bounded quick-inspect drawer with a full-detail link (no navigation)', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-node-item"]')).toBeTruthy());
    (qa(target, '[data-testid="graph-node-item"]')[0] as HTMLButtonElement).click();
    flushSync();
    const drawer = q(target, '[data-testid="graph-drawer"]');
    expect(drawer).toBeTruthy();
    expect(location.hash).toBe(''); // selecting does not navigate away
    const fullDetail = Array.from(drawer!.querySelectorAll('a')).find((a) => /full detail/i.test(a.textContent || '')) as HTMLAnchorElement;
    expect(fullDetail.getAttribute('href')).toBe('#/fleet/services/domain-a%2Fweb');
    unmount(component); document.body.removeChild(target);
  });

  it('selecting an edge opens the edge drawer with declared claims and observed provenance', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-edge"]')).toBeTruthy());
    (qa(target, '[data-testid="graph-edge"]')[0] as HTMLButtonElement).click();
    flushSync();
    const drawer = q(target, '[data-testid="graph-drawer"]');
    expect(drawer?.textContent).toContain('Declared by');
    expect(drawer?.textContent).toContain('^1.0.0');
    unmount(component); document.body.removeChild(target);
  });

  it('Reset focus returns to the discovery state', async () => {
    const { target, component } = mountView({ kind: 'service', sel: 'domain-a/web' });
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-reset"]')).toBeTruthy());
    (q(target, '[data-testid="graph-reset"]') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/graph');
    unmount(component); document.body.removeChild(target);
  });

  it('a search transport error is shown as an error, never as "No matches"', async () => {
    entitiesFn.mockRejectedValue(new ApiError(503, 'backend unavailable'));
    const { target, component } = mountView({});
    const input = q(target, 'input[type="search"]') as HTMLInputElement;
    input.value = 'web'; input.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-search-error"]')).toBeTruthy());
    expect(q(target, '[data-testid="graph-search-empty"]')).toBeNull(); // NOT "no matches" after a failure
    expect(target.textContent).toContain('503');
    unmount(component); document.body.removeChild(target);
  });

  it('a stale search response cannot overwrite a newer query', async () => {
    let resolveFirst: (v: unknown) => void = () => {};
    const first = new Promise((res) => { resolveFirst = res; });
    entitiesFn.mockReturnValueOnce(first);
    entitiesFn.mockResolvedValueOnce({ meta: {}, total: 1, count: 1, entities: [ref('service', 'domain-b/new', 'new')] });
    const { target, component } = mountView({});
    const input = q(target, 'input[type="search"]') as HTMLInputElement;
    input.value = 'ol'; input.dispatchEvent(new Event('input', { bubbles: true }));   // query 1 (pending)
    input.value = 'new'; input.dispatchEvent(new Event('input', { bubbles: true }));  // query 2 (resolves)
    await vi.waitFor(() => expect(q(target, '[data-testid="graph-focus-link"]')).toBeTruthy());
    expect(target.textContent).toContain('new');
    // Now resolve the STALE first query; it must not overwrite the newer result.
    resolveFirst({ meta: {}, total: 1, count: 1, entities: [ref('service', 'domain-a/old', 'old')] });
    await Promise.resolve(); await Promise.resolve();
    flushSync();
    expect(target.textContent).toContain('new');
    expect(target.textContent).not.toContain('old');
    unmount(component); document.body.removeChild(target);
  });

  it('slash/percent/domain-qualified keys round-trip in the focus route', async () => {
    const key = 'oci://ghcr.io/acme/pay@sha256:ab';
    const { target, component } = mountView({ kind: 'revision', sel: key, perspective: 'revision' });
    await vi.waitFor(() => expect(neighborhoodFn).toHaveBeenCalled());
    expect(neighborhoodFn).toHaveBeenCalledWith(expect.objectContaining({ kind: 'revision', key }));
    unmount(component); document.body.removeChild(target);
  });
});
