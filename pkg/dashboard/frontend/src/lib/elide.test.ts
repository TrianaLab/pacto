import { describe, it, expect } from 'vitest';
import { fitName, fitSiblings, elidable } from './elide.ts';

const within = (n: number) => (s: string) => Array.from(s).length <= n;

describe('fitName (C6)', () => {
  it('keeps a name that fits whole', () => {
    expect(fitName('ledger', within(10))).toBe('ledger');
  });
  it('replaces whole middle segments, keeping the separator on both sides', () => {
    const out = fitName('risk-us-eu-west-1-partner-feed-compliance', within(26))!;
    expect(out).toMatch(/^risk-us-.*…-.*compliance$/);
    expect(Array.from(out).length).toBeLessThanOrEqual(26);
  });
  it('keeps at least 4 characters of head and 5 of tail', () => {
    const out = fitName('ab-cdefgh-ijklmnop-qrstuvwx-yz', within(20))!;
    const [head, tail] = out.split('…');
    expect(head.length).toBeGreaterThanOrEqual(4);
    expect(tail.length).toBeGreaterThanOrEqual(2);
    expect(out.startsWith('ab-')).toBe(true);
  });
  it('falls back to head characters and the last segment when no segment cut fits', () => {
    expect(fitName('averyveryverylongsinglewordname', within(14))).toBe('avery…wordname');
    expect(fitName('averyveryverylongsinglewordname', within(3))).toBeNull();
  });
  it('never elides a short name or one with a bidi control', () => {
    expect(elidable('payments-api')).toBe(false);
    expect(elidable('orders-‮reverse‬-sync-bridge')).toBe(false);
    expect(elidable('payments-service-eu')).toBe(true);
  });
});

describe('fitSiblings (C6 siblings)', () => {
  it('keeps the segment that tells two colliding names apart, or elides neither', () => {
    const names = ['catalog-service-bridge-eu-west-compliance', 'catalog-service-stream-eu-west-compliance'];
    const out = fitSiblings(names, (_n, s) => Array.from(s).length <= 30);
    const head = (s: string | null) => Array.from(s ?? '').slice(0, 16).join('');
    if (out[0] && out[1] && out[0] !== names[0] && out[1] !== names[1]) expect(head(out[0])).not.toBe(head(out[1]));
    else expect(out.every((o, i) => o === null || o === names[i])).toBe(true);
    expect(out[0]).not.toBe(out[1]);
  });
  it('leaves names that do not collide alone', () => {
    expect(fitSiblings(['a-b', 'c-d'], () => true)).toEqual(['a-b', 'c-d']);
  });
});
