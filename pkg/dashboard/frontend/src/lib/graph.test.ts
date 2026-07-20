import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { extractSubgraph, nodeLabel, buildVersionSubgraph, renderGraph, buildElements, cyLayout, type GraphData } from './graph.ts';

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

  it('emits compound parent nodes and assigns children when groups are given', () => {
    const groups = new Map([['root', 'team-a'], ['a', 'team-a'], ['x', 'team-b']]);
    const els = buildElements(g, undefined, groups);
    const parents = els.filter((e) => e.data.isGroup);
    expect(parents.map((p) => p.data.label).sort()).toEqual(['team-a', 'team-b']);
    const root = els.find((e) => e.data.id === 'root')!;
    // child points at its group parent; parent ids are DOM-safe
    expect(root.data.parent).toBe(parents.find((p) => p.data.label === 'team-a')!.data.id);
    expect(String(root.data.parent).startsWith('group:')).toBe(true);
  });

  it('leaves nodes ungrouped when no groups are given', () => {
    const els = buildElements(g);
    expect(els.some((e) => e.data.isGroup)).toBe(false);
    expect(els.find((e) => e.data.id === 'root')!.data.parent).toBeUndefined();
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
    expect(ctrl.nodes).toHaveLength(2);
    // presentational region for a11y; connections table is the text fallback
    expect(el.getAttribute('role')).toBe('application');
    ctrl.destroy();
  });

  it('applyFilter runs without throwing', () => {
    const ctrl = renderGraph(el, layeredGraph, { layout: 'layered' });
    expect(() => { ctrl.applyFilter((n) => n.status === 'Compliant'); ctrl.applyFilter(null); }).not.toThrow();
    ctrl.destroy();
  });
});
