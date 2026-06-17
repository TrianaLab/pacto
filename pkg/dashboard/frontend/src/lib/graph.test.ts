import { describe, it, expect } from 'vitest';
import { extractSubgraph, nodeLabel, buildVersionSubgraph } from './graph.ts';

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
