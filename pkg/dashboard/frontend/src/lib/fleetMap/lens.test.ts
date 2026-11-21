import { describe, it, expect } from 'vitest';
import { LENSES, edgeVisible, groupCount, nodeVisibility, ribbonCount, summary, type MapEdge, type MapGroup, type MapNode } from './lens';
import { placeNodes } from './render';
import { mapAnswer, nodesPage } from './testkit';

const node = (over: Partial<MapNode>): MapNode => ({ key: 'k', name: 'k', x: 0, y: 0, ...over });
const edge = (over: Partial<MapEdge>): MapEdge => ({ from: 0, to: 1, ...over });

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

  it('expected lens hides inferred', () => {
    expect(nodeVisibility(node({ inferred: true }), 'expected')).toBe(0);
    expect(nodeVisibility(node({ inferred: true }), 'both')).toBe(1);
    expect(nodeVisibility(node({ match: 'expected-only' }), 'expected')).toBe(1);
    expect(edgeVisible(edge({ inferred: true, difference: 'observed-not-expected' }), 'expected')).toBe(false);
    expect(edgeVisible(edge({ difference: 'observed-not-expected' }), 'expected')).toBe(false);
    expect(edgeVisible(edge({ difference: 'insufficient' }), 'expected')).toBe(true);
    expect(edgeVisible(edge({ difference: 'expected-not-observed' }), 'expected')).toBe(true);
  });

  it('observed lens ghosts insufficient and hides expected-only', () => {
    expect(nodeVisibility(node({ match: 'insufficient' }), 'observed')).toBe(2);
    expect(nodeVisibility(node({ match: 'expected-only' }), 'observed')).toBe(0);
    expect(nodeVisibility(node({ external: true, match: 'insufficient' }), 'observed')).toBe(1);
    expect(nodeVisibility(node({ inferred: true, match: 'observed-only' }), 'observed')).toBe(1);
    expect(edgeVisible(edge({ difference: 'expected-not-observed' }), 'observed')).toBe(false);
    expect(edgeVisible(edge({ difference: 'insufficient' }), 'observed')).toBe(false);
    expect(edgeVisible(edge({ difference: 'matched' }), 'observed')).toBe(true);
    expect(edgeVisible(edge({ inferred: true, difference: 'observed-not-expected' }), 'observed')).toBe(true);
  });

  it('counts ribbons and groups per lens from the answer, so a lens switch never refetches', () => {
    const a = mapAnswer();
    const r = a.ribbons![0];
    expect(LENSES.map((l) => ribbonCount(r, l))).toEqual([4, 3, 3]);
    const g = a.groups![0];
    expect(LENSES.map((l) => groupCount(g, l))).toEqual([5, 4, 5]);
  });

  it('summarises what the lens shows and how it is grouped', () => {
    const a = mapAnswer();
    expect(summary(a, 'both')).toBe('Showing 8 services and 3 dependencies, expected and observed, in 2 domains.');
    expect(summary(a, 'expected')).toBe('Showing 7 services and 3 dependencies, expected (declared in contracts), in 2 domains.');
    expect(summary(a, 'observed')).toBe('Showing 8 services and 2 dependencies, observed and inferred, in 2 domains.');
  });

  it("group by team nests past 40 (from the answer's nested flag)", () => {
    const sub = (key: string) => ({ key, services: 1, x: 0, y: 0, r: 4 });
    const groups = mapAnswer().groups!.map((g, i) => ({ ...g, subs: i ? [sub('t3')] : [sub('t1'), sub('t2')] })) as MapGroup[];
    expect(summary(mapAnswer('s', { groupBy: 'team', nested: true, groups }), 'both')).toContain('in 3 teams across 2 domains.');
    expect(summary(mapAnswer('s', { groupBy: 'team', groups }), 'both')).toContain('in 2 teams.');
  });
});
