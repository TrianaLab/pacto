/**
 * Unit tests for the graph spatial-state identity and its bounded browser-local
 * store. The properties that matter are: two different QUESTIONS never
 * share an arrangement; the store is bounded in both directions; and anything it cannot
 * vouch for reads as absent, so a stale or corrupt entry can only cost a fresh layout,
 * never a broken graph.
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { graphQueryKey, loadSpatial, saveSpatial, clearSpatial, SPATIAL_VERSION } from './graphSpatial.ts';

const q = (over: Partial<Parameters<typeof graphQueryKey>[0]> = {}) => ({
  kind: 'service', key: 'domain-a/web', perspective: 'service',
  views: ['expected', 'differences'], direction: 'both', depth: 1, ...over,
});

const state = (positions: Record<string, { x: number; y: number }>, zoom = 1.25) => ({
  positions, pan: { x: 12, y: 34 }, zoom,
});

describe('graphQueryKey — canonical graph-query identity', () => {
  it('is stable for the same question regardless of view order', () => {
    expect(graphQueryKey(q({ views: ['expected', 'differences'] })))
      .toBe(graphQueryKey(q({ views: ['differences', 'expected'] })));
  });

  it('distinguishes every dimension of the question', () => {
    const base = graphQueryKey(q());
    for (const over of [
      { kind: 'revision' }, { key: 'domain-a/api' }, { perspective: 'revision' },
      { views: ['observed'] }, { direction: 'dependents' }, { depth: 2 },
    ]) {
      expect(graphQueryKey(q(over))).not.toBe(base);
    }
  });

  it('tolerates a partially-formed query without throwing', () => {
    // @ts-expect-error — deliberately malformed, as an unparsed route can be
    expect(typeof graphQueryKey({})).toBe('string');
  });
});

describe('spatial store — save, load, clear', () => {
  beforeEach(() => sessionStorage.clear());

  it('round-trips an arrangement for a query', () => {
    const key = graphQueryKey(q());
    saveSpatial(key, state({ a: { x: 1.4, y: 2.6 } }));
    const got = loadSpatial(key);
    expect(got?.v).toBe(SPATIAL_VERSION);
    expect(got?.positions).toEqual({ a: { x: 1, y: 3 } }); // rounded: sub-pixel precision is noise
    expect(got?.pan).toEqual({ x: 12, y: 34 });
    expect(got?.zoom).toBe(1.25);
  });

  it('never returns one question`s arrangement for another', () => {
    saveSpatial(graphQueryKey(q()), state({ a: { x: 1, y: 2 } }));
    expect(loadSpatial(graphQueryKey(q({ depth: 2 })))).toBeNull();
    expect(loadSpatial(graphQueryKey(q({ key: 'domain-a/api' })))).toBeNull();
  });

  it('clearSpatial forgets exactly one question', () => {
    const a = graphQueryKey(q());
    const b = graphQueryKey(q({ depth: 2 }));
    saveSpatial(a, state({ n: { x: 0, y: 0 } }));
    saveSpatial(b, state({ n: { x: 5, y: 5 } }));
    clearSpatial(a);
    expect(loadSpatial(a)).toBeNull();
    expect(loadSpatial(b)).not.toBeNull();
  });

  it('evicts the least-recently-saved entries beyond the cap', () => {
    const keys = Array.from({ length: 10 }, (_, i) => graphQueryKey(q({ key: `svc-${i}` })));
    keys.forEach((k) => saveSpatial(k, state({ n: { x: 1, y: 1 } })));
    // 8 kept, the two oldest gone -- the store stays bounded however long a session runs.
    expect(keys.filter((k) => loadSpatial(k) !== null)).toHaveLength(8);
    expect(loadSpatial(keys[0])).toBeNull();
    expect(loadSpatial(keys[9])).not.toBeNull();
  });

  it('caps the node positions in a single entry', () => {
    const positions: Record<string, { x: number; y: number }> = {};
    for (let i = 0; i < 500; i++) positions[`n${String(i).padStart(4, '0')}`] = { x: i, y: i };
    const key = graphQueryKey(q());
    saveSpatial(key, state(positions));
    expect(Object.keys(loadSpatial(key)!.positions)).toHaveLength(400);
  });

  it('re-saving a question keeps one entry, not a new one each time', () => {
    const key = graphQueryKey(q());
    saveSpatial(key, state({ a: { x: 1, y: 1 } }));
    saveSpatial(key, state({ a: { x: 2, y: 2 } }));
    expect(loadSpatial(key)?.positions).toEqual({ a: { x: 2, y: 2 } });
    expect(Object.keys(sessionStorage).filter((k) => k.startsWith('pacto.graph.spatial.v1:'))).toHaveLength(1);
  });
});

describe('spatial store — a stale or malformed entry can never break the graph', () => {
  const key = graphQueryKey(q());
  const raw = `pacto.graph.spatial.v1:${key}`;
  beforeEach(() => sessionStorage.clear());

  it.each([
    ['not JSON at all', '{nope'],
    ['a JSON scalar', '42'],
    ['a future version', JSON.stringify({ v: 99, positions: { a: { x: 1, y: 1 } }, pan: { x: 0, y: 0 }, zoom: 1 })],
    ['a missing viewport', JSON.stringify({ v: 1, positions: { a: { x: 1, y: 1 } }, zoom: 1 })],
    ['a non-finite zoom', JSON.stringify({ v: 1, positions: { a: { x: 1, y: 1 } }, pan: { x: 0, y: 0 }, zoom: null })],
    ['a zero zoom', JSON.stringify({ v: 1, positions: { a: { x: 1, y: 1 } }, pan: { x: 0, y: 0 }, zoom: 0 })],
    ['no usable positions', JSON.stringify({ v: 1, positions: { a: { x: 'left' } }, pan: { x: 0, y: 0 }, zoom: 1 })],
    ['an empty positions map', JSON.stringify({ v: 1, positions: {}, pan: { x: 0, y: 0 }, zoom: 1 })],
  ])('reads %s as absent', (_label, stored) => {
    sessionStorage.setItem(raw, stored);
    expect(loadSpatial(key)).toBeNull();
  });

  it('keeps the coordinates it can vouch for and drops the ones it cannot', () => {
    sessionStorage.setItem(raw, JSON.stringify({
      v: 1, positions: { good: { x: 3, y: 4 }, bad: { x: NaN, y: 0 }, worse: null },
      pan: { x: 0, y: 0 }, zoom: 1,
    }));
    // NaN does not survive JSON, so `bad.x` arrives as null -- still not a coordinate.
    expect(loadSpatial(key)?.positions).toEqual({ good: { x: 3, y: 4 } });
  });

  it('refuses to persist a state with no finite geometry', () => {
    saveSpatial(key, { positions: { a: { x: 1, y: 1 } }, pan: { x: NaN, y: 0 }, zoom: 1 });
    saveSpatial(key, { positions: {}, pan: { x: 0, y: 0 }, zoom: 1 });
    saveSpatial(key, { positions: { a: { x: 1, y: 1 } }, pan: { x: 0, y: 0 }, zoom: -1 });
    expect(loadSpatial(key)).toBeNull();
  });
});
