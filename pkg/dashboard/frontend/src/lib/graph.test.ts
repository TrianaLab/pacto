import { describe, it, expect } from 'vitest';
import { extractSubgraph } from './graph.ts';

const sampleGraph = {
  nodes: [
    { id: 'a', serviceName: 'svc-a', status: 'Healthy', edges: [{ targetId: 'b', required: true }] },
    { id: 'b', serviceName: 'svc-b', status: 'Healthy', edges: [{ targetId: 'c', required: false }] },
    { id: 'c', serviceName: 'svc-c', status: 'Degraded', edges: [] },
    { id: 'd', serviceName: 'svc-d', status: 'Healthy', edges: [{ targetId: 'a', required: true }] },
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
    const ids = sub.nodes.map((n) => n.id);
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
    const ids = sub.nodes.map((n) => n.id);
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
        { id: 'x', serviceName: 'x', edges: [{ targetId: 'y' }] },
        { id: 'y', serviceName: 'y', edges: [] },
      ],
    };
    const sub = extractSubgraph(small, 'x');
    expect(sub).not.toBeNull();
    expect(sub.nodes).toHaveLength(2);
  });
});
