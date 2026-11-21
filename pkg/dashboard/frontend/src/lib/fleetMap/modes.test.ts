import { describe, it, expect } from 'vitest';
import {
  GAP_KINDS, edgeEmphasis, environmentChoices, groupEmphasis, groupModeLine, modeFrom, modeParams, nodeEmphasis, ribbonEmphasis, toggleGap, type Mode,
} from './modes';
import { mapAnswer } from './testkit';
import type { MapEdge, MapGroup, MapNode } from './lens';

const node = (over: Partial<MapNode>): MapNode => ({ key: 'k', name: 'k', x: 0, y: 0, ...over });
const edge = (over: Partial<MapEdge>): MapEdge => ({ from: 0, to: 1, ...over });

describe('fleet map modes', () => {
  it('reads and writes the mode from the hash query, one mode at a time', () => {
    expect(modeFrom({})).toBeNull();
    expect(modeFrom({ only: 'problems' })).toEqual({ kind: 'problems' });
    expect(modeFrom({ gaps: 'all' })).toEqual({ kind: 'gaps', gaps: new Set(GAP_KINDS) });
    expect(modeFrom({ gaps: 'observed-only,bogus' })).toEqual({ kind: 'gaps', gaps: new Set(['observed-only']) });
    expect(modeFrom({ missing: 'readiness' })).toEqual({ kind: 'missing', section: 'readiness' });
    expect(modeParams({ kind: 'gaps', gaps: new Set(['insufficient', 'expected-only']) })).toEqual({ only: null, gaps: 'expected-only,insufficient', missing: null });
    expect(modeParams({ kind: 'gaps', gaps: new Set(GAP_KINDS) }).gaps).toBe('all');
    expect(modeParams(null)).toEqual({ only: null, gaps: null, missing: null });
  });

  it('only problems keeps healthy as grey context', () => {
    const m: Mode = { kind: 'problems' };
    expect(nodeEmphasis(node({ severity: 'error' }), m)).toBe(true);
    expect(nodeEmphasis(node({ severity: 'warning' }), m)).toBe(true);
    expect(nodeEmphasis(node({ severity: 'info' }), m)).toBe(false);
    expect(nodeEmphasis(node({}), m)).toBe(false);
    expect(edgeEmphasis(edge({ problem: { code: 'BREAKING', severity: 'error' } }), m)).toBe(true);
    expect(edgeEmphasis(edge({ difference: 'observed-not-expected' }), m)).toBe(false);
    const [payments, checkout] = mapAnswer().groups!;
    expect(groupEmphasis(payments, m)).toBe(true);
    expect(groupEmphasis(checkout, m)).toBe(false);
    expect(groupModeLine(payments, m)).toBe('5 problems');
    expect(ribbonEmphasis({ from: 'a', to: 'b', total: 4, problems: 2 }, m)).toBe(2);
  });

  it('gaps chips toggle each kind', () => {
    let gaps = new Set(GAP_KINDS);
    gaps = toggleGap(gaps, 'observed-only');
    expect([...gaps].sort()).toEqual(['expected-only', 'insufficient']);
    gaps = toggleGap(gaps, 'observed-only');
    expect(gaps.has('observed-only')).toBe(true);
    // The last kind never leaves: gaps mode always shows something.
    expect([...toggleGap(new Set(['insufficient'] as const), 'insufficient')]).toEqual(['insufficient']);
    const m: Mode = { kind: 'gaps', gaps: new Set(['expected-only']) };
    expect(nodeEmphasis(node({ match: 'expected-only' }), m)).toBe(true);
    expect(nodeEmphasis(node({ match: 'observed-only' }), m)).toBe(false);
    expect(nodeEmphasis(node({ match: 'matched' }), m)).toBe(false);
    expect(edgeEmphasis(edge({ difference: 'expected-not-observed' }), m)).toBe(true);
    expect(edgeEmphasis(edge({ difference: 'matched' }), m)).toBe(false);
    expect(ribbonEmphasis({ from: 'a', to: 'b', total: 4, matched: 2, expectedOnly: 1, observedOnly: 1 }, m)).toBe(1);
    // payments: services expected-only 0 + dependencies expected-only 1.
    expect(groupModeLine(mapAnswer().groups![0], m)).toBe('1 gap');
  });

  it('missing filter dims complete services', () => {
    const any: Mode = { kind: 'missing', section: 'any' };
    const readiness: Mode = { kind: 'missing', section: 'readiness' };
    const complete = node({ percentComplete: 100, missing: [] });
    const partial = node({ percentComplete: 60, missing: ['configuration', 'readiness'] });
    const unjudged = node({ inferred: true });
    expect([complete, partial, unjudged].map((n) => nodeEmphasis(n, any))).toEqual([false, true, false]);
    expect(nodeEmphasis(partial, readiness)).toBe(true);
    expect(nodeEmphasis(node({ percentComplete: 80, missing: ['owner'] }), readiness)).toBe(false);
    const g = { ...mapAnswer().groups![0], percentComplete: 72 } as MapGroup;
    expect(groupModeLine(g, any)).toBe('72% complete');
    expect(groupModeLine({ ...g, percentComplete: undefined }, any)).toBe('not judged');
    expect(groupEmphasis({ ...g, percentComplete: 100 }, any)).toBe(false);
    // A group is emphasised for the chosen section only, and never when nothing in it is judged.
    const missesOwner = { ...g, missing: [{ section: 'owner' as const, count: 2 }] };
    expect(groupEmphasis(missesOwner, any)).toBe(true);
    expect(groupEmphasis(missesOwner, { kind: 'missing', section: 'owner' })).toBe(true);
    expect(groupEmphasis(missesOwner, readiness)).toBe(false);
    expect(groupEmphasis({ ...g, percentComplete: undefined, missing: undefined }, any)).toBe(false);
    expect(groupEmphasis({ ...g, percentComplete: 72, missing: undefined }, any)).toBe(false);
  });

  it('gaps emphasise a runtime-inferred call by its match state', () => {
    const observedOnly: Mode = { kind: 'gaps', gaps: new Set(['observed-only']) };
    expect(edgeEmphasis({ from: 0, to: 1, inferred: true, match: 'observed-only' }, observedOnly)).toBe(true);
    expect(edgeEmphasis({ from: 0, to: 1, difference: 'observed-not-expected' }, observedOnly)).toBe(true);
    expect(edgeEmphasis({ from: 0, to: 1, difference: 'matched', match: 'matched' }, observedOnly)).toBe(false);
  });

  it("environment columns in the answer's order with Previews (N)", () => {
    const env = mapAnswer('snap-1', {
      groupBy: 'environment',
      groups: [
        { key: 'prod', label: 'prod', kind: 'environment', depth: 2 },
        { key: '(previews)', label: 'Previews (2)', kind: 'previews', preview: true, depth: -1, subs: [{ key: 'pr-1', services: 1, x: 0, y: 0, r: 5 }, { key: 'pr-2', label: 'PR 2', services: 1, x: 0, y: 0, r: 5 }] },
        { key: 'dev', label: 'dev', kind: 'environment', depth: 0 },
        { key: 'staging', label: 'staging', kind: 'environment', depth: 1 },
        { key: '(external)', label: 'External systems', kind: 'external' },
      ] as unknown as MapGroup[],
    });
    expect(environmentChoices(env)).toEqual([
      { value: 'dev', label: 'dev' },
      { value: 'staging', label: 'staging' },
      { value: 'prod', label: 'prod' },
      { value: 'pr-1', label: 'pr-1', preview: true },
      { value: 'pr-2', label: 'PR 2', preview: true },
    ]);
    expect(environmentChoices(null)).toEqual([]);
  });
});
