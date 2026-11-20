import { describe, expect, it } from 'vitest';
import { advisoryText, breakText, rangesLine, unknownRanges } from './acceptance.ts';

describe('acceptance words', () => {
  it('prints the Ranges line rounded down, or no edges', () => {
    expect(rangesLine({ edges: 58, declared: 41, accepted: 6 })).toBe('Ranges: 41 declared and 6 accepted of 58 dependency edges (81%)');
    expect(rangesLine({ edges: 0, declared: 0, accepted: 0 })).toBe('Ranges: no dependency edges');
  });
  it('says hidden consumers are not counted, so a partial view never reads complete', () => {
    expect(rangesLine({ edges: 3, declared: 3, accepted: 0 }, 5)).toBe('Ranges: 3 declared and 0 accepted of 3 dependency edges (100%); 5 consumers hidden by your access are not counted');
    expect(rangesLine({ edges: 0, declared: 0, accepted: 0 }, 1)).toBe('Ranges: no dependency edges; 1 consumer hidden by your access is not counted');
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
    expect(breakText({ state: 'announced', expires: '2026-12-01', route: 'waiting for the owner of reports: ana@acme.com', reason: 'x' }))
      .toBe('Break announced: nothing accepts it yet. Expires 2026-12-01. Waiting for the owner of reports: ana@acme.com. x');
    // The route is said once, as the server words it.
    expect(breakText({ state: 'announced', expires: 'd', route: 'reports declares no owner: declare service.owner.' }))
      .toBe('Break announced: nothing accepts it yet. Expires d. Reports declares no owner: declare service.owner.');
    expect(breakText({ state: 'other', expires: 'd' })).toBe('Break other. Expires d.');
  });
});

describe('advisoryText', () => {
  it('names the interface, the source, the first findings, the consumers and the declare hint', () => {
    const changes = [1, 2, 3, 4].map((i) => ({ path: `p${i}`, reason: `r${i}` }));
    expect(advisoryText({ interface: 'api', source: 'a.yaml:1', changes, consumers: [{ service: 'web' }] })).toBe(
      'Possible break on inferred interface api (a.yaml:1). Findings: r1; r2; r3 and 1 more. Consumers (inferred, compatibility unknown): web. ' +
      'It never blocks or clears: declare the interface to classify the change.');
    expect(advisoryText({ interface: 'api', source: 'a.yaml:1' })).toBe(
      'Possible break on inferred interface api (a.yaml:1). No consumer is known. It never blocks or clears: declare the interface to classify the change.');
  });
});
