import { describe, expect, it } from 'vitest';
import { breakText, rangesLine, unknownRanges } from './acceptance.ts';

describe('acceptance words', () => {
  it('prints the Ranges line rounded down, or no edges', () => {
    expect(rangesLine({ edges: 58, declared: 41, accepted: 6 })).toBe('Ranges: 41 declared and 6 accepted of 58 dependency edges (81%)');
    expect(rangesLine({ edges: 0, declared: 0, accepted: 0 })).toBe('Ranges: no dependency edges');
  });
  it('lists unknown ranges and how many more', () => {
    expect(unknownRanges({ edges: 3, declared: 0, accepted: 0, unknown: [{ from: 'reports', to: 'ledger', proposed: '^2.3.0' }, { from: 'web', to: 'ledger' }], unknownTotal: 3 }))
      .toEqual(['unknown: reports -> ledger (proposed ^2.3.0)', 'unknown: web -> ledger', 'and 1 more']);
    expect(unknownRanges({ edges: 1, declared: 1, accepted: 0 })).toEqual([]);
  });
  it('names state, signer, record, expiry and route', () => {
    expect(breakText({ state: 'accepted', by: 'ana@acme.com', record: 'r@sha256:1', expires: '2026-12-01' }))
      .toBe('Break accepted: it never blocks. Signed by ana@acme.com. Record: r@sha256:1. Expires 2026-12-01.');
    expect(breakText({ state: 'expired' })).toBe('Break expired or records no expiry: it keeps its verdict. Expiry unknown.');
    expect(breakText({ state: 'announced', expires: '2026-12-01', route: 'the owner of reports', reason: 'x' }))
      .toBe('Break announced: nothing accepts it yet. Expires 2026-12-01. Waiting for the owner of reports. x');
    expect(breakText({ state: 'other', expires: 'd' })).toBe('Break other. Expires d.');
  });
});
