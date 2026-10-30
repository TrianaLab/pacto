import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import cytoscape from 'cytoscape';
import { extractSubgraph, nodeLabel, fitLines, graphLabels, buildVersionSubgraph, renderGraph, buildElements, cyLayout, LEGEND_SELECTORS, fullName, spread, seedPositions, laneLayout, type GraphData } from './graph.ts';
import { laneGraph } from './neighborhoodGraph.ts';

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

  it('cuts a long name at the end, over two lines without a version and one with', () => {
    expect(nodeLabel({ id: 'x', serviceName: 'a-really-long-service-name', version: '1.0.0' }).name).toBe('a-really-long-se…');
    expect(nodeLabel({ id: 'inventory-order-gateway-risk-thumbnail' }).name).toBe('inventory-order-\ngateway-risk-thu…');
    expect(nodeLabel({ id: 'payments' }).name).toBe('payments');
  });

  it('never cuts the focus: it wraps at word breaks in a wider box', () => {
    const raw = 'inventory-order-gateway-risk-thumbnail-refund-service-search-notification';
    const { name } = nodeLabel({ id: raw }, true);
    expect(name.replace(/\n/g, '')).toBe(raw);
    expect(name.split('\n').every((l) => l.length <= 25)).toBe(true);
    const focus = buildElements({ nodes: [{ id: raw, serviceName: raw, status: 'Compliant' }] }, raw)[0].data;
    expect(focus).toMatchObject({ w: 240, isFocus: 1 });
    expect(focus.h).toBeGreaterThan(42);
  });
});

describe('fitLines', () => {
  it('breaks at word breaks, hard-breaks a word longer than a line and cuts only past the limit', () => {
    expect(fitLines('aaaa-bbbb-cccc', 10)).toEqual(['aaaa-bbbb-', 'cccc']);
    expect(fitLines('abcdefghijkl', 5)).toEqual(['abcde', 'fghij', 'kl']);
    expect(fitLines('abcdefghijkl', 5, 2)).toEqual(['abcde', 'fghi…']);
    expect(fitLines('short', 5, 1)).toEqual(['short']);
  });
});

describe('graphLabels', () => {
  const n = (id: string, version?: string) => ({ id, serviceName: id, status: 'Compliant', ...(version ? { version } : {}) });
  it('tells apart names cut to the same words by keeping the part where they differ', () => {
    const ids = Array.from({ length: 12 }, (_, i) => `payments-ledger-replica-zone-eu-west-${String(i).padStart(2, '0')}`);
    const labels = graphLabels(ids.map((id) => n(id)));
    expect(new Set(labels.values()).size).toBe(12);
    expect(labels.get(ids[7])).toBe('payments-ledger-\nreplica-zone-…07');
    // With a version line the name has one line; the version is never cut.
    const v = graphLabels([n('checkout-orchestrator-a', '1.0.0'), n('checkout-orchestrator-b', '1.0.0')]);
    expect([...v.values()]).toEqual(['checkout-…a\n1.0.0', 'checkout-…b\n1.0.0']);
    // The same name twice (two revisions of one service) is not a clash to resolve.
    expect([...graphLabels([{ ...n('svc@1', '1'), serviceName: 'svc' }, { ...n('svc@2', '2'), serviceName: 'svc' }]).values()]).toEqual(['svc\n1', 'svc\n2']);
    // One long word: the kept end starts just before the difference.
    expect(graphLabels([n('abcdefghijklmnopqrstuvwxyz0123456789A'), n('abcdefghijklmnopqrstuvwxyz0123456789B')]).get('abcdefghijklmnopqrstuvwxyz0123456789A')).toBe('abcdefghijklmnopq\nrstuvwxyz0123…89A');
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
  it('settles a big graph at once instead of animating it', () => {
    expect((cyLayout('force', 10) as { animate: unknown }).animate).not.toBe(false);
    expect((cyLayout('force', 200) as { animate: unknown }).animate).toBe(false);
  });

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
        { targetId: 'ins', edgeState: 'insufficient', refused: true },
      ] },
      { id: 'rev', serviceName: 'svc', status: 'Compliant', kind: 'revision', edges: [] },
      { id: 'tgt', serviceName: 'svc', status: 'Compliant', kind: 'target', edges: [{ targetId: 'rev', type: 'runs' }] },
      { id: 'dep', serviceName: 'dep', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'eno', serviceName: 'eno', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'dri', serviceName: 'dri', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'ins', serviceName: 'ins', status: 'Compliant', kind: 'service', edges: [] },
      { id: 'group:team', serviceName: 'team', status: 'group', kind: 'service', group: 3, edges: [] },
      { id: 'lane:used-by', serviceName: 'Used by (1)', status: 'lane', kind: 'service', lane: true, side: 1, edges: [] },
    ] };
    const cy = cytoscape({ elements: buildElements(all) });
    for (const [key, selector] of Object.entries(LEGEND_SELECTORS)) {
      expect(cy.$(selector).length, `${key} → ${selector}`).toBeGreaterThan(0);
      // A lane box is scaffolding: no legend entry switches it off.
      expect(cy.$(selector).filter('[id = "lane:used-by"]').length, `${key} spares the lane`).toBe(0);
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

describe('large neighborhoods', () => {
  it('names a node in full for its hover title, and says what a team node holds', () => {
    expect(fullName({ id: 'x', serviceName: 'a-very-long-service-name-indeed' })).toBe('a-very-long-service-name-indeed');
    expect(fullName({ id: 'x', serviceName: 'payments', version: '1.2.0' })).toBe('payments 1.2.0');
    expect(fullName({ id: 'group:t', serviceName: 'team-a', group: 7 })).toBe('team-a: 7 entities. Select to show them.');
    // The "Other teams" box is short on the canvas; its hover title says which side it is.
    expect(fullName({ id: 'group:used-by:*', serviceName: 'Other teams', owner: 'Other teams that depend on it', group: 3 })).toBe('Other teams that depend on it: 3 entities. Select to show them.');
    const els = buildElements({ nodes: [{ id: 'a', serviceName: 'a-very-long-service-name-indeed', status: 'Compliant', depth: 1, owner: 'o' }] });
    expect(els[0].data).toMatchObject({ fullName: 'a-very-long-service-name-indeed', depth: 1, owner: 'o', group: 0 });
  });

  it('spreads a large graph out with the square root of its size', () => {
    expect(spread(10)).toBe(1);
    expect(spread(160)).toBe(2);
    const small = cyLayout('force', 10) as unknown as { nodeRepulsion: number };
    const big = cyLayout('force', 160) as unknown as { nodeRepulsion: number; numIter: number };
    expect(big.nodeRepulsion).toBe(small.nodeRepulsion * 4);
    expect(big.numIter).toBe(1500);
  });

  it('seeds a focused neighborhood in rings by hop, ordered by owner, and anything else on a grid', () => {
    const at = seedPositions([
      { id: 'focus', depth: 0 }, { id: 'b', depth: 1, owner: 'z' }, { id: 'a', depth: 1, owner: 'y' }, { id: 'c', depth: 2 }, { id: 'u' },
    ]);
    expect(at.get('focus')).toEqual({ x: 0, y: 0 });
    const r = (id: string) => Math.hypot(at.get(id)!.x, at.get(id)!.y);
    // An unknown hop sits with the direct neighbors; no owner sorts first, then y, then z.
    expect(Math.round(r('a'))).toBe(260);
    expect(Math.round(r('u'))).toBe(260);
    expect(at.get('u')).toEqual({ x: 260, y: 0 });
    expect(at.get('a')!.y).toBeGreaterThan(0);
    expect(r('c')).toBeGreaterThan(r('a'));
    expect([...seedPositions([{ id: 'b' }, { id: 'a' }]).entries()]).toEqual([['a', { x: 0, y: 0 }], ['b', { x: 180, y: 0 }]]);
    // A ring of two focuses still goes round, not on top of each other.
    expect(seedPositions([{ id: 'p', depth: 0 }, { id: 'q', depth: 0 }]).get('q')!.x).toBeLessThan(0);
  });

  it('lays a focused neighborhood out in lanes: depends-on left, focus centre, used-by right', () => {
    const nodes = [
      { id: 'focus', depth: 0, side: 0 },
      { id: 'lane:depends-on', lane: 1, side: -1 },
      ...Array.from({ length: 30 }, (_, i) => ({ id: `d${i}`, depth: 1, side: -1, owner: `t${i % 3}` })),
      { id: 'u0', depth: 1, side: 1 }, { id: 'u1', depth: 1, side: 1 },
      { id: 'group:depends-on:z', depth: 2, side: -1, group: 4, owner: 'a' }, { id: 'f0', depth: 2, side: -1, owner: 'z' },
      { id: 'loose' },
    ];
    const pos = laneLayout(nodes, { width: 1100, height: 520 });
    expect(pos.has('lane:depends-on')).toBe(false); // a lane wraps what it holds; it is not placed
    expect(pos.get('focus')).toEqual({ x: 0, y: 0 });
    const xs = (pred: (id: string) => boolean) => [...pos].filter(([id]) => pred(id)).map(([, p]) => p.x);
    expect(Math.max(...xs((id) => /^d\d/.test(id)))).toBeLessThan(0);
    expect(Math.min(...xs((id) => /^u\d|loose/.test(id)))).toBeGreaterThan(0);
    // Hop 2 is outside the lane: the groups next to it, an opened group's entities beyond.
    expect(pos.get('group:depends-on:z')!.x).toBeLessThan(Math.min(...xs((id) => /^d\d/.test(id))));
    expect(pos.get('f0')!.x).toBeLessThan(pos.get('group:depends-on:z')!.x);
    // Thirty direct dependencies wrap into staggered sub-columns rather than one tall one.
    const cols = new Set(xs((id) => /^d\d/.test(id)));
    expect(cols.size).toBeGreaterThan(1);
    const keys = new Set([...pos.values()].map((p) => `${p.x},${p.y}`));
    expect(keys.size).toBe(pos.size);
    // A tall canvas affords taller columns.
    const tall = laneLayout(nodes, { width: 600, height: 2400 });
    expect(new Set(nodes.filter((n) => /^d\d/.test(n.id)).map((n) => tall.get(n.id)!.x)).size).toBeLessThan(cols.size);
    // A lane up to its cap stays one column even on a wide, short canvas.
    const capped = [{ id: 'focus', depth: 0, side: 0 },
      ...Array.from({ length: 8 }, (_, i) => ({ id: `c${i}`, depth: 1, side: -1, owner: 'z' }))];
    const one = laneLayout(capped, { width: 2400, height: 300 });
    expect(new Set(capped.slice(1).map((n) => one.get(n.id)!.x)).size).toBe(1);
  });
});

describe('renderGraph in lanes (headless)', () => {
  // jsdom's ResizeObserver never fires, so nothing lays itself out on mount; Reset layout
  // is the same layout run on demand.
  let el: HTMLElement;
  beforeEach(() => { el = document.createElement('div'); document.body.appendChild(el); });
  afterEach(() => { el.remove(); });
  type N = GraphData['nodes'][number];
  const focused = (deps: string[], users: string[], far: string[] = []): GraphData => laneGraph({ nodes: [
    { id: 'focus', serviceName: 'focus', status: 'Compliant', depth: 0, edges: deps.map((d) => ({ targetId: d })) },
    ...deps.map((d, i): N => ({ id: d, serviceName: d, status: 'Compliant', depth: 1, edges: i ? [] : far.map((f) => ({ targetId: f, edgeState: 'drift' })) })),
    ...users.map((u): N => ({ id: u, serviceName: u, status: 'Compliant', depth: 1, edges: [{ targetId: 'focus' }] })),
    ...far.map((f): N => ({ id: f, serviceName: f, status: 'Compliant', depth: 2, owner: 'team-f', edges: [] })),
  ] }, { threshold: 8 }).graph;

  it('places the sides around the focus and never counts, saves or restores a lane box', () => {
    const seen: number[] = [];
    const ctrl = renderGraph(el, focused(['a', 'b'], ['u']), { edgeStyle: 'visible', autoSpotlightFocus: false, onReady: (d) => seen.push(d.nodeCount),
      savedPositions: { 'lane:depends-on': { x: 999, y: 999 } } });
    ctrl.resetLayout();
    const p = ctrl.spatialState().positions;
    expect(Object.keys(p).sort()).toEqual(['a', 'b', 'focus', 'u']);
    expect(p.a.x).toBeLessThan(p.focus.x);
    expect(p.u.x).toBeGreaterThan(p.focus.x);
    expect(seen.at(-1)).toBe(4);
    expect(ctrl.diagnostics().nodeCount).toBe(4);
    ctrl.destroy();
  });

  it('runs a group edge straight across from its lane, and opens a group by laying the lanes out again', () => {
    const far = ['f0', 'f1', 'f2', 'f3', 'f4', 'f5'];
    const ctrl = renderGraph(el, focused(['a', 'b'], ['u'], far), { edgeStyle: 'visible', autoSpotlightFocus: false });
    ctrl.resetLayout();
    expect(Object.keys(ctrl.spatialState().positions)).toContain('group:depends-on:team-f');
    // The reader opens the group: the lanes are laid out again with its entities in a column.
    ctrl.applyTopology(laneGraph({ nodes: [
      { id: 'focus', serviceName: 'focus', status: 'Compliant', depth: 0, edges: [{ targetId: 'a' }, { targetId: 'b' }] },
      { id: 'a', serviceName: 'a', status: 'Compliant', depth: 1, edges: far.map((f) => ({ targetId: f })) },
      { id: 'b', serviceName: 'b', status: 'Compliant', depth: 1, edges: [] },
      { id: 'u', serviceName: 'u', status: 'Compliant', depth: 1, edges: [{ targetId: 'focus' }] },
      ...far.map((f): N => ({ id: f, serviceName: f, status: 'Compliant', depth: 2, owner: 'team-f', edges: [] })),
    ] }, { all: true }).graph);
    const p = ctrl.spatialState().positions;
    expect(p['group:depends-on:team-f']).toBeUndefined();
    expect(new Set(far.map((f) => p[f].x)).size).toBe(1);
    expect(p.f0.x).toBeLessThan(p.a.x);
    ctrl.destroy();
  });

  it('keeps an arranged graph put and drops an arrival into its own lane column', () => {
    const mm = vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
    vi.useFakeTimers();
    try {
      const ctrl = renderGraph(el, focused(['a', 'b'], ['u']), { edgeStyle: 'visible', autoSpotlightFocus: false });
      ctrl.resetLayout();
      vi.advanceTimersByTime(1000);
      ctrl.zoomIn(); // the reader framed it: the viewport is theirs now
      const before = ctrl.spatialState().positions;
      expect(before.a.x).toBeLessThan(0);
      // A new dependency, a new hop-2 entity past it, and u now also depends on by the focus.
      ctrl.applyTopology(laneGraph({ nodes: [
        { id: 'focus', serviceName: 'focus', status: 'Compliant', depth: 0, edges: [{ targetId: 'a' }, { targetId: 'b' }, { targetId: 'c' }, { targetId: 'u' }] },
        { id: 'a', serviceName: 'a', status: 'Compliant', depth: 1, edges: [{ targetId: 'x' }] },
        { id: 'b', serviceName: 'b', status: 'Compliant', depth: 1, edges: [] },
        { id: 'c', serviceName: 'c', status: 'Compliant', depth: 1, edges: [] },
        { id: 'u', serviceName: 'u', status: 'Compliant', depth: 1, edges: [] },
        { id: 'x', serviceName: 'x', status: 'Compliant', depth: 2, edges: [] },
        { id: 'y', serviceName: 'y', status: 'Compliant', depth: 2, edges: [] },
      ] }).graph);
      const p = ctrl.spatialState().positions;
      for (const id of ['focus', 'a', 'b', 'u']) expect(p[id]).toEqual(before[id]);
      const low = Math.max(before.a.y, before.b.y);
      expect(p.c).toEqual({ x: before.a.y > before.b.y ? before.a.x : before.b.x, y: low + 62 });
      expect(p.x.x).toBeLessThan(Math.min(before.a.x, before.b.x));
      // y sits alone on the used-by side, a hop further out than anything there.
      expect(p.y.x).toBeGreaterThan(before.focus.x);
      ctrl.destroy();
    } finally {
      vi.useRealTimers();
      mm.mockRestore();
    }
  });
});
