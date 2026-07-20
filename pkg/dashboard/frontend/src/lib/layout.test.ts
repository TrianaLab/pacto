import { describe, it, expect } from 'vitest';
import { computeVisible } from './layout';
import type { GraphData } from './graph';

// root -> a,b ; a -> a1 ; b -> b1
const graph: GraphData = {
  nodes: [
    { id: 'root', serviceName: 'root', status: 'Compliant', edges: [
      { targetId: 'a' }, { targetId: 'b' } ] },
    { id: 'a', serviceName: 'a', status: 'Compliant', edges: [{ targetId: 'a1' }] },
    { id: 'b', serviceName: 'b', status: 'Compliant', edges: [{ targetId: 'b1' }] },
    { id: 'a1', serviceName: 'a1', status: 'Compliant', edges: [] },
    { id: 'b1', serviceName: 'b1', status: 'Compliant', edges: [] },
  ],
};

const opts = (o: Partial<Parameters<typeof computeVisible>[1]> = {}) => ({
  rootId: 'root', direction: 'down' as const, depth: 1, expanded: new Set<string>(), childCap: 12, ...o,
});

describe('computeVisible', () => {
  it('limits to the given depth', () => {
    const r = computeVisible(graph, opts({ depth: 1 }));
    const ids = r.nodes.map((x) => x.id).sort();
    expect(ids).toEqual(['a', 'b', 'root']);
    expect(r.hidden.get('a')).toBe(1); // a1 hidden
    expect(r.hidden.get('b')).toBe(1); // b1 hidden
  });

  it('expands a specific node past the depth limit', () => {
    const r = computeVisible(graph, opts({ depth: 1, expanded: new Set(['a']) }));
    const ids = r.nodes.map((x) => x.id).sort();
    expect(ids).toEqual(['a', 'a1', 'b', 'root']);
    expect(r.hidden.has('a')).toBe(false);
    expect(r.hidden.get('b')).toBe(1);
  });

  it('resolves the root by serviceName and follows reverse edges upstream', () => {
    const r = computeVisible(graph, opts({ rootId: 'a1', direction: 'up', depth: 2 }));
    const ids = r.nodes.map((x) => x.id).sort();
    expect(ids).toEqual(['a', 'a1', 'root']); // a1 <- a <- root
  });

  it('caps children and reports the overflow as hidden', () => {
    const wide: GraphData = { nodes: [
      { id: 'r', serviceName: 'r', status: 'Compliant',
        edges: Array.from({ length: 5 }, (_, i) => ({ targetId: `c${i}` })) },
      ...Array.from({ length: 5 }, (_, i) => ({ id: `c${i}`, serviceName: `c${i}`, status: 'Compliant', edges: [] })),
    ] };
    const r = computeVisible(wide, opts({ rootId: 'r', depth: 1, childCap: 2 }));
    expect(r.nodes.filter((x) => x.id.startsWith('c')).length).toBe(2);
    expect(r.hidden.get('r')).toBe(3);
  });

  it('reveals all children (uncapped) for an expanded node past the cap', () => {
    const wide: GraphData = { nodes: [
      { id: 'r', serviceName: 'r', status: 'Compliant',
        edges: Array.from({ length: 15 }, (_, i) => ({ targetId: `c${i}` })) },
      ...Array.from({ length: 15 }, (_, i) => ({ id: `c${i}`, serviceName: `c${i}`, status: 'Compliant', edges: [] })),
    ] };
    const r = computeVisible(wide, opts({ rootId: 'r', depth: 1, childCap: 12, expanded: new Set(['r']) }));
    expect(r.nodes.filter((x) => x.id.startsWith('c')).length).toBe(15);
    expect(r.hidden.has('r')).toBe(false);
  });

  it('dedupes duplicate edges to the same target in the hidden count', () => {
    // c has both a dependency and a reference edge to the same node d
    const dup: GraphData = { nodes: [
      { id: 'root', serviceName: 'root', status: 'Compliant', edges: [{ targetId: 'c' }] },
      { id: 'c', serviceName: 'c', status: 'Compliant', edges: [
        { targetId: 'd', type: 'dependency' }, { targetId: 'd', type: 'reference' } ] },
      { id: 'd', serviceName: 'd', status: 'Compliant', edges: [] },
    ] };
    const r = computeVisible(dup, opts({ rootId: 'root', depth: 1 }));
    // c is a frontier node (depth 1, no descent); d is its one distinct hidden child
    expect(r.hidden.get('c')).toBe(1);
  });

  it('is cycle-safe', () => {
    const cyc: GraphData = { nodes: [
      { id: 'x', serviceName: 'x', status: 'Compliant', edges: [{ targetId: 'y' }] },
      { id: 'y', serviceName: 'y', status: 'Compliant', edges: [{ targetId: 'x' }] },
    ] };
    const r = computeVisible(cyc, opts({ rootId: 'x', depth: 10 }));
    expect(r.nodes.map((x) => x.id).sort()).toEqual(['x', 'y']);
  });
});
