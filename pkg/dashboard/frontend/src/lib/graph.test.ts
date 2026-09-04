import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import cytoscape from 'cytoscape';
import { extractSubgraph, nodeLabel, buildVersionSubgraph, renderGraph, buildElements, cyLayout, LEGEND_SELECTORS, type GraphData } from './graph.ts';

const sampleGraph = {
  nodes: [
    { id: 'a', serviceName: 'svc-a', status: 'Compliant', edges: [{ targetId: 'b', required: true }] },
    { id: 'b', serviceName: 'svc-b', status: 'Compliant', edges: [{ targetId: 'c', required: false }] },
    { id: 'c', serviceName: 'svc-c', status: 'Warning', edges: [] },
    { id: 'd', serviceName: 'svc-d', status: 'Compliant', edges: [{ targetId: 'a', required: true }] },
    { id: 'e', serviceName: 'svc-e', status: 'Unknown', edges: [] },
  ],
};

describe('extractSubgraph', () => {
  it('returns null for null graphData', () => {
    expect(extractSubgraph(null, 'a')).toBeNull();
  });

  it('returns null for empty nodes', () => {
    expect(extractSubgraph({ nodes: [] }, 'a')).toBeNull();
  });

  it('returns null for null focusId', () => {
    expect(extractSubgraph(sampleGraph, null)).toBeNull();
  });

  it('returns null for non-existent focusId', () => {
    expect(extractSubgraph(sampleGraph, 'nonexistent')).toBeNull();
  });

  it('returns subgraph centered on focus node', () => {
    const sub = extractSubgraph(sampleGraph, 'a');
    expect(sub).not.toBeNull();
    const ids = sub!.nodes.map((n) => n.id);
    // 'a' depends on 'b', 'b' depends on 'c', 'd' depends on 'a'
    expect(ids).toContain('a');
    expect(ids).toContain('b');
    expect(ids).toContain('c');
    expect(ids).toContain('d');
    // 'e' is disconnected — should NOT be in the subgraph
    expect(ids).not.toContain('e');
  });

  it('includes nodes that point TO visited nodes', () => {
    const sub = extractSubgraph(sampleGraph, 'b');
    const ids = sub!.nodes.map((n) => n.id);
    // b -> c (downstream), a -> b (upstream)
    expect(ids).toContain('b');
    expect(ids).toContain('c');
    expect(ids).toContain('a');
  });

  it('returns null when focus node has no connections (single node)', () => {
    const result = extractSubgraph(sampleGraph, 'e');
    // 'e' has no edges, and no one points to 'e' — subgraph is just 1 node
    expect(result).toBeNull();
  });

  it('handles graph with single connected pair', () => {
    const small = {
      nodes: [
        { id: 'x', serviceName: 'x', status: 'Unknown', edges: [{ targetId: 'y' }] },
        { id: 'y', serviceName: 'y', status: 'Unknown', edges: [] },
      ],
    };
    const sub = extractSubgraph(small, 'x');
    expect(sub).not.toBeNull();
    expect(sub!.nodes).toHaveLength(2);
  });
});

describe('nodeLabel', () => {
  it('returns name and version when present', () => {
    expect(nodeLabel({ id: 'a', serviceName: 'payments', version: '1.2.3' })).toEqual({
      name: 'payments',
      version: '1.2.3',
    });
  });

  it('returns empty version when absent', () => {
    expect(nodeLabel({ id: 'a', serviceName: 'payments' })).toEqual({
      name: 'payments',
      version: '',
    });
  });

  it('falls back to id when serviceName missing', () => {
    expect(nodeLabel({ id: 'ext-svc' })).toEqual({ name: 'ext-svc', version: '' });
  });

  it('truncates long names to 18 chars with ellipsis', () => {
    const { name } = nodeLabel({ id: 'x', serviceName: 'a-really-long-service-name', version: '1.0.0' });
    expect(name).toBe('a-really-long-ser…');
    expect(name.length).toBe(18);
  });
});

describe('extractSubgraph — version passthrough', () => {
  it('preserves the version field on returned nodes', () => {
    const g = {
      nodes: [
        { id: 'a', serviceName: 'svc-a', status: 'Compliant', version: '1.0.0', edges: [{ targetId: 'b' }] },
        { id: 'b', serviceName: 'svc-b', status: 'Compliant', version: '2.0.0', edges: [] },
      ],
    };
    const sub = extractSubgraph(g, 'a');
    expect(sub!.nodes.find((n) => n.id === 'a')!.version).toBe('1.0.0');
    expect(sub!.nodes.find((n) => n.id === 'b')!.version).toBe('2.0.0');
  });
});

describe('buildVersionSubgraph', () => {
  const detail = {
    name: 'payments',
    contractStatus: 'Compliant',
    version: '2.0.0',
    dependencies: [
      { name: 'postgresql', ref: 'oci://reg/postgresql', required: true },
      { name: 'fraud-service', ref: 'oci://reg/fraud-service', required: false },
    ],
  };
  const services = [
    { name: 'payments', contractStatus: 'Compliant', version: '2.0.0' },
    { name: 'fraud-service', contractStatus: 'Warning', version: '1.3.0' },
    // postgresql intentionally absent from the fleet -> external
  ];

  it('roots the graph at the selected version with edges to its deps', () => {
    const g = buildVersionSubgraph(detail, services, '2.0.0');
    const root = g.nodes.find((n) => n.id === 'payments')!;
    expect(root.version).toBe('2.0.0');
    expect(root.edges!.map((e) => e.targetId).sort()).toEqual(['fraud-service', 'postgresql']);
    expect(g.nodes).toHaveLength(3);
  });

  it('labels known deps from the fleet and unknown deps as external', () => {
    const g = buildVersionSubgraph(detail, services, '2.0.0');
    const fraud = g.nodes.find((n) => n.id === 'fraud-service')!;
    expect(fraud.status).toBe('Warning');
    expect(fraud.version).toBe('1.3.0');
    const pg = g.nodes.find((n) => n.id === 'postgresql')!;
    expect(pg.status).toBe('external');
    expect(pg.version).toBe('');
  });

  it('skips self-references and dedupes repeated deps', () => {
    const d = {
      name: 'svc',
      dependencies: [
        { name: 'svc' }, // self -> skipped
        { name: 'dep' },
        { name: 'dep' }, // duplicate node, but edge added once more
      ],
    };
    const g = buildVersionSubgraph(d, [], '1.0.0');
    expect(g.nodes.map((n) => n.id)).toEqual(['svc', 'dep']);
    const root = g.nodes[0];
    expect(root.edges!.filter((e) => e.targetId === 'svc')).toHaveLength(0);
  });

  it('handles a service with no dependencies', () => {
    const g = buildVersionSubgraph({ name: 'lonely', dependencies: [] }, [], '1.0.0');
    expect(g.nodes).toHaveLength(1);
    expect(g.nodes[0].edges).toEqual([]);
  });
});

describe('buildElements', () => {
  const g: GraphData = { nodes: [
    { id: 'root', serviceName: 'root', status: 'Compliant', version: '1.2.0',
      edges: [{ targetId: 'a', required: true }, { targetId: 'a', type: 'reference' }] },
    { id: 'a', serviceName: 'a', status: 'Warning', edges: [] },
    { id: 'x', serviceName: 'x', status: 'external', reason: 'auth_failed', edges: [{ targetId: 'ghost' }] },
  ] };

  it('emits one element per node and per in-graph edge (drops edges to unknown targets)', () => {
    const els = buildElements(g);
    const nodes = els.filter((e) => !e.data.source);
    const edges = els.filter((e) => e.data.source);
    expect(nodes).toHaveLength(3);
    // root→a dependency + root→a reference; the x→ghost edge is dropped (no ghost node)
    expect(edges).toHaveLength(2);
  });

  it('keeps a dependency and a reference to the same target as distinct edges', () => {
    const ids = buildElements(g).filter((e) => e.data.source).map((e) => String(e.data.id));
    expect(new Set(ids).size).toBe(2);
    expect(ids.some((id) => id.endsWith(':dependency'))).toBe(true);
    expect(ids.some((id) => id.endsWith(':reference'))).toBe(true);
  });

  it('carries label with version, status, external and focus flags', () => {
    const els = buildElements(g, 'root');
    const root = els.find((e) => e.data.id === 'root')!;
    expect(root.data.label).toBe('root\n1.2.0');
    expect(root.data.isFocus).toBe(1);
    const x = els.find((e) => e.data.id === 'x')!;
    expect(x.data.external).toBe(1);
    expect(x.data.reason).toBe('auth_failed');
  });

  it('emits the reconciliation state on edge data so the canvas can render it (Part 6)', () => {
    const gd: GraphData = { nodes: [
      { id: 'a', serviceName: 'a', status: 'Compliant', edges: [
        { targetId: 'b', edgeState: 'matched' },
        { targetId: 'c', edgeState: 'drift' },
        { targetId: 'd', driftStatus: 'drift' }, // legacy driftStatus folds into state
        { targetId: 'e' }, // no state
      ] },
      { id: 'b', serviceName: 'b', status: 'Compliant', edges: [] },
      { id: 'c', serviceName: 'c', status: 'Compliant', edges: [] },
      { id: 'd', serviceName: 'd', status: 'Compliant', edges: [] },
      { id: 'e', serviceName: 'e', status: 'Compliant', edges: [] },
    ] };
    const edges = buildElements(gd).filter((el) => el.data.source);
    const stateOf = (t: string) => edges.find((el) => el.data.target === t)!.data.state;
    expect(stateOf('b')).toBe('matched');
    expect(stateOf('c')).toBe('drift');
    expect(stateOf('d')).toBe('drift'); // legacy driftStatus -> state 'drift'
    expect(stateOf('e')).toBe('');
    // drift flag stays consistent with the unified state.
    expect(edges.find((el) => el.data.target === 'd')!.data.drift).toBe(1);
    expect(edges.find((el) => el.data.target === 'b')!.data.drift).toBe(0);
  });
});

describe('cyLayout', () => {
  it('uses dagre top-down for the dependency tree', () => {
    const l = cyLayout('layered') as { name: string; rankDir: string };
    expect(l.name).toBe('dagre');
    expect(l.rankDir).toBe('TB');
  });
  it('uses fcose (compact) for the force view', () => {
    expect((cyLayout('force') as { name: string }).name).toBe('fcose');
  });
});

describe('renderGraph (Cytoscape)', () => {
  const layeredGraph: GraphData = { nodes: [
    { id: 'root', serviceName: 'root', status: 'Compliant', edges: [{ targetId: 'a' }] },
    { id: 'a', serviceName: 'a', status: 'Compliant', edges: [] },
  ] };
  let el: HTMLElement;
  beforeEach(() => { el = document.createElement('div'); document.body.appendChild(el); });
  afterEach(() => { el.remove(); });

  it('mounts without throwing and returns the controls API', () => {
    const ctrl = renderGraph(el, layeredGraph, { layout: 'layered', focusId: 'root' });
    expect(typeof ctrl.zoomIn).toBe('function');
    expect(typeof ctrl.zoomOut).toBe('function');
    expect(typeof ctrl.resetView).toBe('function');
    expect(typeof ctrl.applyFilter).toBe('function');
    expect(typeof ctrl.patchData).toBe('function');
    expect(ctrl.nodes).toHaveLength(2);
    // The canvas is described as an image, not an incomplete role="application"; the
    // connections table / relationships list is the keyboard model.
    expect(el.getAttribute('role')).toBe('img');
    ctrl.destroy();
  });

  it('applyFilter runs without throwing', () => {
    const ctrl = renderGraph(el, layeredGraph, { layout: 'layered' });
    expect(() => { ctrl.applyFilter((n) => n.status === 'Compliant'); ctrl.applyFilter(null); }).not.toThrow();
    ctrl.destroy();
  });

  // A legend entry that matches nothing is the worst kind of broken control: it reports
  // itself as off, the canvas does not change, and there is no error anywhere. The way
  // that happens is a selector written from the wire vocabulary instead of the element
  // data -- an edge's relation arrives as `type` and is stored as `etype`. So the
  // assertion is against what buildElements actually emits, for a graph carrying one of
  // every distinction the legend offers.
  it('every legend filter selector matches something buildElements emits', () => {
    const all: GraphData = { nodes: [
      { id: 'svc', serviceName: 'svc', status: 'Compliant', kind: 'service', edges: [
        { targetId: 'dep', edgeState: 'matched' },
        { targetId: 'eno', edgeState: 'expected-not-observed' },
        { targetId: 'dri', edgeState: 'drift' },
        { targetId: 'ins', edgeState: 'insufficient' },
      ] },
      { id: 'rev', serviceName: 'svc', status: 'Compliant', kind: 'revision', edges: [] },
      { id: 'tgt', serviceName: 'svc', status: 'Compliant', kind: 'target', edges: [{ targetId: 'rev', type: 'runs' }] },
      { id: 'dep', serviceName: 'dep', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'eno', serviceName: 'eno', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'dri', serviceName: 'dri', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'ins', serviceName: 'ins', status: 'Compliant', kind: 'service', edges: [] },
    ] };
    const cy = cytoscape({ elements: buildElements(all) });
    for (const [key, selector] of Object.entries(LEGEND_SELECTORS)) {
      expect(cy.$(selector).length, `${key} → ${selector}`).toBeGreaterThan(0);
    }
    cy.destroy();
  });

  it('the legend filter and an out-of-graph focus are both survivable', () => {
    const ctrl = renderGraph(el, layeredGraph, { layout: 'layered' });
    expect(() => {
      ctrl.applyLegendFilter(new Set(['kind:service', 'state:drift']));
      ctrl.focusNode('root');
      ctrl.applyLegendFilter(null); // clearing while pinned must not un-pin or throw
      ctrl.focusNode('nobody-here'); // a stale id from a text list the canvas has moved past
      ctrl.focusNode(null);
    }).not.toThrow();
    ctrl.destroy();
  });

  it('a fit never leaves the camera below the legibility floor', () => {
    // A fit is allowed to zoom out until everything is in frame, which on a wide fleet
    // (or a phone) means node labels stop being text. Reduced motion is the branch that
    // fits synchronously, so it is the one a non-painting environment can observe; the
    // animated branch applies the same floor when its camera move completes.
    const mm = vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
    try {
      const ctrl = renderGraph(el, layeredGraph, { layout: 'layered' });
      for (let i = 0; i < 8; i++) ctrl.zoomOut(); // down to minZoom
      expect(ctrl.spatialState().zoom).toBeLessThan(0.6);
      ctrl.fit();
      expect(ctrl.spatialState().zoom).toBe(0.6);
      ctrl.destroy();
    } finally {
      mm.mockRestore();
    }
  });

  // A floored fit no longer means "the whole graph is on screen", and a cropped canvas
  // looks exactly like a complete one. So the clamp has to be reported, not just applied.
  it('reports a floored fit through the diagnostics, and only when it changes', () => {
    const mm = vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
    try {
      const seen: boolean[] = [];
      // jsdom lays nothing out, so the container is zero-sized and every fit here lands
      // under the floor -- which is exactly the case the flag exists for.
      const ctrl = renderGraph(el, layeredGraph, { layout: 'layered', onReady: (d) => seen.push(d.fitFloored) });
      ctrl.fit();
      expect(ctrl.spatialState().zoom).toBe(0.6);
      expect(seen.at(-1)).toBe(true);

      // Fitting again from the floor changes nothing, so it must not re-publish: a
      // diagnostics callback that fires on every fit is a re-render on every fit.
      const before = seen.length;
      ctrl.fit();
      expect(seen).toHaveLength(before);
      ctrl.destroy();
    } finally {
      mm.mockRestore();
    }
  });

  it('restyle repaints in place: new palette, untouched geometry', () => {
    // Cytoscape draws to a canvas and cannot read CSS custom properties, so every colour
    // is resolved by getComputedStyle ONCE at init -- which is why a theme toggle used to
    // leave the graph painted in the previous theme until the next data change.
    // The guarantee worth pinning is the other half: restyle is STYLE ONLY. Implemented as
    // a re-render or a relayout it would still recolour, and would silently throw away the
    // arrangement the user built and the viewport they were reading.
    const ctrl = renderGraph(el, layeredGraph, { layout: 'layered' });
    const before = ctrl.spatialState();
    el.style.setProperty('--c-ok', 'rgb(9, 8, 7)');
    expect(() => ctrl.restyle()).not.toThrow();
    expect(ctrl.spatialState()).toEqual(before);
    expect(ctrl.nodes).toHaveLength(2);
    ctrl.destroy();
  });
});
