import { describe, it, expect } from 'vitest';
import { LENSES, groupCount, nodeVisibility, ribbonCount, type MapNode } from './lens';
import { placeNodes } from './render';
import { mapAnswer, nodesPage } from './testkit';

const node = (over: Partial<MapNode>): MapNode => ({ key: 'k', name: 'k', x: 0, y: 0, ...over });

describe('fleet map lens', () => {
  it('lens never moves a node', () => {
    const a = mapAnswer();
    const g = a.groups![0];
    const page = { group: g.key, total: 4, complete: true, edges: [],
      nodes: nodesPage(g.key, [{ match: 'matched' }, { inferred: true, match: 'observed-only' }, { match: 'insufficient' }, { match: 'expected-only' }]).nodes! };
    const at = LENSES.map((l) => placeNodes(g, page, l).map((p) => [p.node.key, p.x, p.y]));
    expect(at[1]).toEqual(at[0]);
    expect(at[2]).toEqual(at[0]);
    // Only visibility differs.
    expect(LENSES.map((l) => placeNodes(g, page, l).map((p) => p.vis))).toEqual([[1, 1, 1, 1], [1, 0, 1, 1], [1, 1, 2, 0]]);
  });

  it('expected hides inferred; observed ghosts insufficient and hides expected-only', () => {
    expect(nodeVisibility(node({ inferred: true }), 'expected')).toBe(0);
    expect(nodeVisibility(node({ match: 'insufficient' }), 'observed')).toBe(2);
    expect(nodeVisibility(node({ match: 'expected-only' }), 'observed')).toBe(0);
    expect(nodeVisibility(node({ external: true, match: 'insufficient' }), 'observed')).toBe(1);
    expect(nodeVisibility(node({ inferred: true }), 'both')).toBe(1);
  });

  it('counts ribbons and groups per lens from the answer, so a lens switch never refetches', () => {
    const a = mapAnswer();
    const r = a.ribbons![0];
    expect(LENSES.map((l) => ribbonCount(r, l))).toEqual([4, 3, 3]);
    const g = a.groups![0];
    expect(LENSES.map((l) => groupCount(g, l))).toEqual([5, 4, 5]);
  });
});
